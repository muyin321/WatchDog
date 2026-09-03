package ai

import "context"

// compatProvider 通用 OpenAI 兼容适配器。
//
// 智谱 GLM、字节豆包（火山方舟）、MiniMax 以及任意自定义网关均提供
// OpenAI 兼容的 /chat/completions 接口，因此共用同一实现，仅端点/默认模型不同。
type compatProvider struct {
	lib          *Library
	name         string
	apiKey       string
	model        string
	baseURL      string
	defaultModel string
}

// Name 实现 Provider。
func (p *compatProvider) Name() string { return p.name }

// Chat 实现 Provider：填充默认模型后走通用 OpenAI 兼容调用（带 1 次重试）。
func (p *compatProvider) Chat(ctx context.Context, req ChatRequest) (string, error) {
	req.Model = p.model
	if req.Model == "" {
		req.Model = p.defaultModel
	}
	if req.Model == "" {
		return "", errNoModel
	}
	return p.lib.sendWithRetry(
		func() (string, error) { return p.lib.postChat(ctx, p.baseURL, p.apiKey, req) },
		2,
	)
}

// errNoModel custom 厂商未配置模型名时的错误。
var errNoModel = &providerError{"自定义厂商必须在配置中心填写模型名"}

type providerError struct{ msg string }

func (e *providerError) Error() string { return e.msg }

// newCompat 构造一个 OpenAI 兼容 provider；baseURL 为空时取厂商官方默认端点。
func newCompat(lib *Library, name string, o Options) *compatProvider {
	base := o.BaseURL
	if base == "" {
		base = defaultBaseURLs[name]
	}
	return &compatProvider{
		lib:          lib,
		name:         name,
		apiKey:       o.APIKey,
		model:        o.Model,
		baseURL:      base,
		defaultModel: defaultModels[name],
	}
}

// NewZhipu 智谱 GLM（https://open.bigmodel.cn，OpenAI 兼容）。
func NewZhipu(lib *Library, o Options) Provider { return newCompat(lib, ProviderZhipu, o) }

// NewDoubao 字节豆包 / 火山方舟（https://www.volcengine.com/product/doubao）。
// 说明：方舟支持两种接入方式——模型名直连（如 doubao-1.5-pro-32k）或
// 推理接入点 ID（ep-xxxx）；后者请把接入点 ID 填在“模型名”里即可。
func NewDoubao(lib *Library, o Options) Provider { return newCompat(lib, ProviderDoubao, o) }

// NewMiniMax MiniMax（https://www.minimax.io，OpenAI 兼容）。
func NewMiniMax(lib *Library, o Options) Provider { return newCompat(lib, ProviderMiniMax, o) }

// NewCustom 自定义 OpenAI 兼容端点（中转网关、私有化部署、Ollama 等）。
// 要求配置 ai.base_url；模型名必须填写。
func NewCustom(lib *Library, o Options) Provider { return newCompat(lib, ProviderCustom, o) }

// AnthropicProvider Anthropic Claude 适配器（独立 Messages 协议，非 OpenAI 兼容）。
type AnthropicProvider struct {
	lib     *Library
	apiKey  string
	model   string
	baseURL string
}

// NewAnthropic 创建 Anthropic provider。
func NewAnthropic(lib *Library, o Options) *AnthropicProvider {
	base := o.BaseURL
	if base == "" {
		base = defaultBaseURLs[ProviderAnthropic]
	}
	return &AnthropicProvider{lib: lib, apiKey: o.APIKey, model: o.Model, baseURL: base}
}

// Name 实现 Provider。
func (p *AnthropicProvider) Name() string { return ProviderAnthropic }

// Chat 实现 Provider：走 /v1/messages 协议。
func (p *AnthropicProvider) Chat(ctx context.Context, req ChatRequest) (string, error) {
	req.Model = p.model
	if req.Model == "" {
		req.Model = defaultModels[ProviderAnthropic]
	}
	return p.lib.sendWithRetry(
		func() (string, error) { return p.lib.postMessages(ctx, p.baseURL, p.apiKey, req) },
		2,
	)
}
