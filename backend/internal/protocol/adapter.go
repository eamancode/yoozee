package protocol

import "context"

// Adapter owns only protocol translation. The host owns credentials, outbound
// security, polling leases, billing, result downloads and task recovery.
type Adapter interface {
	Metadata() Metadata
	BuildCreate(context.Context, RequestContext) (RequestSpec, error)
	ParseCreate(context.Context, []byte) (CreateResult, error)
	BuildPoll(context.Context, PollContext) (RequestSpec, error)
	ParsePoll(context.Context, PollContext, []byte) (PollResult, error)
	BuildCancel(context.Context, PollContext) (RequestSpec, error)
}

type RequestAwareCreateParser interface {
	ParseCreateWithRequest(context.Context, GenerationRequest, []byte) (CreateResult, error)
}

// MediaUploadPlan 是协议声明的图床能力：哪些媒体需要先上传，以及响应里地址的位置。
type MediaUploadPlan struct {
	Kinds   []string
	URLPath string
}

// MediaUploader 是可选协议能力。供应商只接受自己域名下的参考素材地址时，宿主会在创建
// 任务前把内联媒体上传到供应商图床，再用返回的 URL 替换内联数据。插件只描述请求与响应
// 字段；凭证注入、出站安全策略、超时、大小限制和审计仍由宿主统一执行。
type MediaUploader interface {
	MediaUploadPlan() (MediaUploadPlan, bool)
	BuildMediaUpload(RequestContext, MediaReference) (RequestSpec, error)
}

// AgentAdapter is the optional protocol surface for tool-capable text calls.
// The host still owns credentials, outbound policy and billing; a plugin only
// maps the platform's agent request into the provider payload and parses the
// provider response back into the platform tool-call contract.
type AgentAdapter interface {
	BuildAgent(context.Context, AgentRequestContext) (RequestSpec, error)
	ParseAgent(context.Context, []byte) (AgentResult, error)
}

// AgentCapability lets the host distinguish a declarative provider that
// actually declares an agent operation from one that only satisfies the Go
// method set through the shared manifest adapter implementation.
type AgentCapability interface {
	AgentAvailable() bool
}

// ResultAdapter optionally resolves an authenticated binary result after an
// asynchronous task succeeds without returning a public media URL.
type ResultAdapter interface {
	BuildResult(context.Context, PollContext) (RequestSpec, error)
}

type ResultCapability interface {
	ResultAvailable() bool
}

type AgentRequestContext struct {
	BaseURL string
	Model   string
	Request map[string]any
}

type AgentToolCall struct {
	ID               string
	Name             string
	Arguments        string
	ThoughtSignature string
}

type AgentResult struct {
	Text      string
	Reasoning string
	ToolCalls []AgentToolCall
}

type UnavailableAdapter struct {
	Info Metadata
}

func (a UnavailableAdapter) Metadata() Metadata { return a.Info }

func (a UnavailableAdapter) BuildCreate(context.Context, RequestContext) (RequestSpec, error) {
	return RequestSpec{}, unavailable(a.Info)
}

func (a UnavailableAdapter) ParseCreate(context.Context, []byte) (CreateResult, error) {
	return CreateResult{}, unavailable(a.Info)
}

func (a UnavailableAdapter) BuildPoll(context.Context, PollContext) (RequestSpec, error) {
	return RequestSpec{}, unavailable(a.Info)
}

func (a UnavailableAdapter) ParsePoll(context.Context, PollContext, []byte) (PollResult, error) {
	return PollResult{}, unavailable(a.Info)
}

func (a UnavailableAdapter) BuildCancel(context.Context, PollContext) (RequestSpec, error) {
	return RequestSpec{}, unavailable(a.Info)
}
