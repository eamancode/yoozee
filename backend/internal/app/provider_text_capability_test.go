package app

import (
	"encoding/json"
	"testing"

	"infinite-canvas/backend/internal/model"
)

// 回归：系统渠道的文本任务必须带上模型声明的 maxOutputTokens。
// 少了这一步，声明式协议适配器只能退回写死的 max_tokens；思考与正文共用输出预算，
// 思考型模型会把预算全花在思考上、正文一个字都不输出，
// 画布 Agent 于是报「接口没有返回内容」。
func TestEnsureTextCapabilityConfigHydratesDeclaredOutputLimit(t *testing.T) {
	svc, db := newChannelModelTestService(t)
	svc.dataDir = t.TempDir()
	channel := model.ModelChannel{
		ID: "channel-text", Scope: model.ChannelScopeSystem, Enabled: true, Name: "Text",
		BaseURL: "https://api.example.com", APIKey: "test-key", APIFormat: "claude",
		ModelsJSON: `["deepseek-v4.1-flash"]`,
	}
	if err := svc.encryptSystemChannelSecrets(&channel); err != nil {
		t.Fatal(err)
	}
	profile := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceClaudeAPI), "deepseek-v4.1-flash")
	profile.Text.MaxOutputTokens = 16384
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	item := model.ChannelModel{
		ID: "model-text", ChannelID: channel.ID, ModelKey: "deepseek-v4.1-flash",
		Capability: "text", Protocol: model.ChannelInterfaceClaudeAPI, Enabled: true,
		CapabilityConfigJSON: string(encoded),
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}

	input := canvasGenerationInput{
		Mode:   "text",
		Config: providerConfig{ChannelID: channel.ID, ChannelModelKey: item.ModelKey, Model: item.ModelKey},
	}
	svc.ensureTextCapabilityConfig(&input)
	if input.Config.CapabilityConfig == nil || input.Config.CapabilityConfig.Text == nil {
		t.Fatal("文本能力配置没有补齐")
	}
	if got := input.Config.CapabilityConfig.Text.MaxOutputTokens; got != 16384 {
		t.Fatalf("maxOutputTokens = %d, want 16384", got)
	}

	// 已经带能力配置的输入（例如用户自定义渠道）保持原样。
	existing := &ModelCapabilityConfig{Version: 1, Text: &TextCapabilityConfig{MaxOutputTokens: 2048}}
	input.Config.CapabilityConfig = existing
	svc.ensureTextCapabilityConfig(&input)
	if input.Config.CapabilityConfig != existing {
		t.Fatal("已有能力配置被覆盖")
	}

	// 没有渠道信息的输入（自定义渠道直连）不查库、不补齐。
	detached := canvasGenerationInput{Mode: "text", Config: providerConfig{Model: item.ModelKey}}
	svc.ensureTextCapabilityConfig(&detached)
	if detached.Config.CapabilityConfig != nil {
		t.Fatal("无渠道信息时不应补齐能力配置")
	}
}
