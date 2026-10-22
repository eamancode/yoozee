package app

// 文本生成、Agent 工具循环和文本流解析。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
)

func runTextTask(ctx context.Context, input canvasGenerationInput) (map[string]interface{}, error) {
	if _, ok := declarativeProtocolAdapterForContext(ctx, input.Config.InterfaceType); ok {
		return runDeclarativeProtocolTask(ctx, input)
	}
	switch input.Config.InterfaceType {
	case "chat-completion":
		return runChatCompletionsTextTask(ctx, input)
	case "openai-response":
		return runResponsesTextTask(ctx, input)
	case string(model.ChannelInterfaceClaudeAPI):
		return runClaudeTextTask(ctx, input)
	}
	return runLegacyTextTask(ctx, input)
}

// Known text protocols keep the plugin's request mapping and host transport,
// while sharing the SSE parser used by text generation and Agent requests.
func executeProtocolCreateRequest(ctx context.Context, input canvasGenerationInput, spec protocol.RequestSpec) ([]byte, *protocol.Result, error) {
	wire := input.Config.InterfaceType
	if wire == string(model.ChannelInterfaceOpenAIResponse) {
		wire = "responses"
	}
	if input.Mode != "text" || !input.StreamText || (wire != "chat-completion" && wire != "responses" && wire != "claude-api") {
		data, err := executeProtocolRequest(ctx, input.Config, spec)
		if err == nil && input.Mode == "text" {
			err = checkTextFinish(protocolTextFinishReason(wire, data))
		}
		return data, nil, err
	}
	body := protocolBodyObject(spec.Body)
	if body == nil {
		return nil, nil, errors.New("声明式流式文本请求体必须是 JSON 对象")
	}
	body["stream"] = true
	if wire == "chat-completion" {
		if err := ensureChatCompletionStreamUsage(body); err != nil {
			return nil, nil, err
		}
	}
	spec.Body = body
	parser := newStreamingAgentParser(wire, input.OnTextDelta)
	parser.emitReasoning = input.OnReasoningDelta
	data, mimeType, err := executeProtocolBinaryRequestWithConsumer(ctx, input.Config, spec, parser.consume)
	if err != nil {
		return data, nil, err
	}
	if !strings.Contains(strings.ToLower(mimeType), "event-stream") {
		return data, nil, checkTextFinish(protocolTextFinishReason(wire, data))
	}
	parser.flush()
	parsed, err := parser.result()
	if err != nil {
		return nil, nil, err
	}
	if err := checkTextFinish(stringField(parsed, "finishReason")); err != nil {
		return nil, nil, err
	}
	text := stringField(parsed, "text")
	if text == "" {
		return nil, nil, errors.New("流式文本接口没有返回内容")
	}
	return data, &protocol.Result{Text: text, Reasoning: stringField(parsed, "reasoning")}, nil
}

func runLegacyTextTask(ctx context.Context, input canvasGenerationInput) (map[string]interface{}, error) {
	responseInput, err := textResponseInput(input)
	if err != nil {
		return nil, err
	}
	body := map[string]interface{}{"model": input.Config.Model, "input": responseInput}
	applyTextThinking(body, input, "responses")
	applyTextOutputLimit(body, input.MaxOutputTokens, "max_output_tokens")
	result, err := requestTextProvider(ctx, input.Config, "/responses", body, "responses", input.StreamText, input.OnTextDelta)
	if err != nil {
		if !shouldFallbackTextToChat(err) {
			return nil, err
		}
		result, chatErr := runChatCompletionsTextTask(ctx, input)
		if chatErr == nil {
			return result, nil
		}
		return nil, fmt.Errorf("文本接口请求失败：Responses API %v；Chat Completions %v", err, chatErr)
	}
	return providerTextTaskResult(result), nil
}

func runResponsesTextTask(ctx context.Context, input canvasGenerationInput) (map[string]interface{}, error) {
	responseInput, err := textResponseInput(input)
	if err != nil {
		return nil, err
	}
	body := map[string]interface{}{"model": input.Config.Model, "input": responseInput}
	applyTextThinking(body, input, "responses")
	applyTextOutputLimit(body, input.MaxOutputTokens, "max_output_tokens")
	result, err := requestTextProvider(ctx, input.Config, "/responses", body, "responses", input.StreamText, input.OnTextDelta)
	if err != nil {
		return nil, err
	}
	return providerTextTaskResult(result), nil
}

func runChatCompletionsTextTask(ctx context.Context, input canvasGenerationInput) (map[string]interface{}, error) {
	messages := []map[string]interface{}{}
	if systemPrompt := strings.TrimSpace(input.Config.SystemPrompt); systemPrompt != "" {
		messages = append(messages, map[string]interface{}{"role": "system", "content": systemPrompt})
	}
	messages = append(messages, validatedTextHistory(input.TextHistory)...)
	userContent, err := textChatContent(input)
	if err != nil {
		return nil, err
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": userContent})
	body := map[string]interface{}{"model": input.Config.Model, "messages": messages}
	applyTextThinking(body, input, "chat-completion")
	applyTextOutputLimit(body, input.MaxOutputTokens, "max_tokens")
	result, err := requestTextProvider(ctx, input.Config, "/chat/completions", body, "chat-completion", input.StreamText, input.OnTextDelta)
	if err != nil {
		return nil, err
	}
	return providerTextTaskResult(result), nil
}

func runClaudeTextTask(ctx context.Context, input canvasGenerationInput) (map[string]interface{}, error) {
	if len(input.ReferenceVideos) > 0 {
		return nil, errors.New("Claude API 当前不支持视频参考输入")
	}
	messages := make([]map[string]interface{}, 0, len(input.TextHistory)+1)
	for _, message := range validatedTextHistory(input.TextHistory) {
		messages = append(messages, message)
	}
	content, err := claudeTextContent(input)
	if err != nil {
		return nil, err
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": content})
	maxTokens := 4096
	if input.MaxOutputTokens > 0 {
		maxTokens = input.MaxOutputTokens
	}
	body := map[string]interface{}{"model": input.Config.Model, "max_tokens": maxTokens, "messages": messages}
	applyTextThinking(body, input, "claude-api")
	if systemPrompt := strings.TrimSpace(input.Config.SystemPrompt); systemPrompt != "" {
		body["system"] = systemPrompt
	}
	result, err := requestTextProvider(ctx, input.Config, "/messages", body, "claude-api", input.StreamText, input.OnTextDelta)
	if err != nil {
		return nil, err
	}
	return providerTextTaskResult(result), nil
}

func applyTextThinking(body map[string]interface{}, input canvasGenerationInput, protocol string) {
	if !input.TextOptions.Thinking {
		return
	}
	switch protocol {
	case "responses":
		body["reasoning"] = map[string]interface{}{"effort": "medium", "summary": "auto"}
	case "chat-completion":
		body["reasoning_effort"] = "medium"
	case "claude-api":
		body["thinking"] = map[string]interface{}{"type": "enabled", "budget_tokens": 1024}
	}
}

// Chat Completion defaults to automatic tool selection when tools are present,
// so an explicit "auto" only reduces compatibility. Reasoning endpoints also
// disagree on forced choices. Normalize before the first network request while
// preserving required/named choices for non-reasoning structured tasks.
func normalizeAgentToolChoice(body map[string]interface{}, input canvasGenerationInput, protocol string) {
	if protocol == "chat-completion" && (input.TextOptions.Thinking || isAutoAgentToolChoice(body["tool_choice"])) {
		delete(body, "tool_choice")
	}
}

type providerTextResult struct {
	Text      string
	Reasoning string
}

// textPayloadFinishReason 取非流式文本响应的停笔原因：Chat Completions 的 choices[].finish_reason、
// Claude 的 stop_reason、Responses 的 incomplete_details.reason（status=incomplete 时），
// 以及 Gemini 兼容响应的 candidates[].finishReason。多个 choice 时只要有一个被截断就返回它。
func textPayloadFinishReason(payload map[string]interface{}, protocol string) string {
	if payload == nil {
		return ""
	}
	if data, ok := payload["data"].(map[string]interface{}); ok && payload["choices"] == nil {
		payload = data
	}
	switch protocol {
	case "claude-api":
		return stringField(payload, "stop_reason")
	case "responses":
		if stringField(payload, "status") != "incomplete" {
			return ""
		}
		details, _ := payload["incomplete_details"].(map[string]interface{})
		return defaultString(stringField(details, "reason"), "incomplete")
	}
	reason := ""
	for _, key := range []string{"choices", "candidates"} {
		for _, value := range interfaceSlice(payload[key]) {
			choice, _ := value.(map[string]interface{})
			current := firstNonEmptyString(stringField(choice, "finish_reason"), stringField(choice, "finishReason"))
			if textFinishTruncated(current) {
				return current
			}
			reason = firstNonEmptyString(reason, current)
		}
	}
	return reason
}

// protocolTextFinishReason 读声明式协议的非流式文本响应。插件协议的 wire 不一定是三种内置协议，
// 认不出时按 OpenAI 兼容（choices / candidates）读，再看顶层 stop_reason / stopReason（Anthropic、Bedrock 形状）。
// 响应不是 JSON 对象时返回空，交给协议自己的解析报错。
func protocolTextFinishReason(wire string, data []byte) string {
	var payload map[string]interface{}
	if json.Unmarshal(data, &payload) != nil {
		return ""
	}
	switch wire {
	case "claude-api", "anthropic-messages", "claude-messages":
		return textPayloadFinishReason(payload, "claude-api")
	case "responses":
		return textPayloadFinishReason(payload, "responses")
	}
	if reason := textPayloadFinishReason(payload, "chat-completion"); reason != "" {
		return reason
	}
	return firstNonEmptyString(stringField(payload, "stop_reason"), stringField(payload, "stopReason"))
}

// textFinishTruncated 判断停笔原因是不是输出上限：OpenAI 兼容 length、Claude max_tokens、
// Responses max_output_tokens、Gemini MAX_TOKENS。
func textFinishTruncated(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens", "max_output_tokens":
		return true
	}
	return false
}

// textOutputTruncatedError 让截断的文本任务失败：分镜表、提取结果这类结构化输出被截断后
// 解析不全，按成功保存只会表现成"没有返回镜头行"，所以带稳定 reason 交给前端提示。
func textOutputTruncatedError(reason string) error {
	return &AppError{
		Status:  CodeBadGateway,
		Code:    CodeBadGateway,
		Reason:  ReasonTextOutputTruncated,
		Message: fmt.Sprintf("模型输出达到上限被截断（%s），结果不完整：请换输出上限更大的模型，或按场分开生成", strings.TrimSpace(reason)),
	}
}

// checkTextFinish 在文本任务成功前核对停笔原因；截断就失败，其余原因放行。
func checkTextFinish(reason string) error {
	if textFinishTruncated(reason) {
		return textOutputTruncatedError(reason)
	}
	return nil
}

func providerTextTaskResult(result providerTextResult) map[string]interface{} {
	payload := map[string]interface{}{"mode": "text", "text": result.Text}
	if strings.TrimSpace(result.Reasoning) != "" {
		payload["reasoning"] = result.Reasoning
	}
	return payload
}

func applyTextOutputLimit(body map[string]interface{}, limit int, field string) {
	if limit > 0 {
		body[field] = limit
	}
}

func claudeTextContent(input canvasGenerationInput) (interface{}, error) {
	if len(input.ReferenceImages) == 0 {
		return input.Prompt, nil
	}
	content := []map[string]interface{}{{"type": "text", "text": input.Prompt}}
	for _, image := range input.ReferenceImages {
		value, err := openAIImageInputURL(image)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(value, "data:") {
			mimeType, data, ok := splitDataURL(value)
			if !ok {
				return nil, errors.New("Claude 参考图片 data URL 无效")
			}
			content = append(content, map[string]interface{}{"type": "image", "source": map[string]interface{}{"type": "base64", "media_type": mimeType, "data": data}})
		} else {
			content = append(content, map[string]interface{}{"type": "image", "source": map[string]interface{}{"type": "url", "url": value}})
		}
	}
	return content, nil
}

func splitDataURL(value string) (string, string, bool) {
	if !strings.HasPrefix(value, "data:") {
		return "", "", false
	}
	separator := strings.Index(value, ",")
	if separator <= len("data:") {
		return "", "", false
	}
	header := strings.TrimPrefix(value[:separator], "data:")
	if !strings.HasSuffix(header, ";base64") {
		return "", "", false
	}
	return strings.TrimSuffix(header, ";base64"), value[separator+1:], value[separator+1:] != ""
}

func textResponseInput(input canvasGenerationInput) (interface{}, error) {
	systemPrompt := strings.TrimSpace(input.Config.SystemPrompt)
	if len(input.TextHistory) == 0 && len(input.ReferenceImages) == 0 && len(input.ReferenceVideos) == 0 {
		return withSystemPrompt(input.Config, input.Prompt), nil
	}
	messages := make([]map[string]interface{}, 0, len(input.TextHistory)+2)
	if systemPrompt != "" {
		messages = append(messages, map[string]interface{}{"role": "system", "content": systemPrompt})
	}
	messages = append(messages, validatedTextHistory(input.TextHistory)...)
	content, err := textResponseContent(input)
	if err != nil {
		return nil, err
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": content})
	return messages, nil
}

func validatedTextHistory(history []providerTextMessage) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(history))
	for _, message := range history {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		content := strings.TrimSpace(message.Content)
		if (role != "user" && role != "assistant") || content == "" {
			continue
		}
		result = append(result, map[string]interface{}{"role": role, "content": content})
	}
	return result
}

func textResponseContent(input canvasGenerationInput) ([]map[string]interface{}, error) {
	content := []map[string]interface{}{{"type": "input_text", "text": input.Prompt}}
	for _, image := range input.ReferenceImages {
		url, err := openAIImageInputURL(image)
		if err != nil {
			return nil, err
		}
		content = append(content, map[string]interface{}{"type": "input_image", "image_url": url})
	}
	for _, video := range input.ReferenceVideos {
		url, err := openAIVideoInputURL(video)
		if err != nil {
			return nil, err
		}
		content = append(content, map[string]interface{}{"type": "input_video", "video_url": url})
	}
	return content, nil
}

func textChatContent(input canvasGenerationInput) (interface{}, error) {
	if len(input.ReferenceImages) == 0 && len(input.ReferenceVideos) == 0 {
		return input.Prompt, nil
	}
	content := []map[string]interface{}{{"type": "text", "text": input.Prompt}}
	for _, image := range input.ReferenceImages {
		url, err := openAIImageInputURL(image)
		if err != nil {
			return nil, err
		}
		content = append(content, map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": url}})
	}
	for _, video := range input.ReferenceVideos {
		url, err := openAIVideoInputURL(video)
		if err != nil {
			return nil, err
		}
		content = append(content, map[string]interface{}{"type": "video_url", "video_url": map[string]interface{}{"url": url}})
	}
	return content, nil
}

func shouldFallbackTextToChat(err error) bool {
	var httpErr providerHTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	// 只在上游明确说这个路径不存在/不允许时，才把 Responses 换成 Chat Completions。
	// 502/503/504 是瞬时故障，换协议不会修好，还会把真正的上游故障伪装成“能力缺失后的第二次失败”。
	switch httpErr.StatusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	default:
		return false
	}
}
