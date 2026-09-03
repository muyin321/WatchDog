package ai

import "context"

// DeepSeekProvider DeepSeek 适配器。
type DeepSeekProvider struct {
	lib     *Library
	apiKey  string
	model   string
	baseURL string
}

// NewDeepSeek 创建 DeepSeek provider。
func NewDeepSeek(lib *Library, o Options) *DeepSeekProvider {
	base := o.BaseURL
	if base == "" {
		base = "https://api.deepseek.com/v1"
	}
	return &DeepSeekProvider{lib: lib, apiKey: o.APIKey, model: o.Model, baseURL: base}
}

// Name 实现 Provider。
func (p *DeepSeekProvider) Name() string { return ProviderDeepSeek }

// Chat 实现 Provider。
func (p *DeepSeekProvider) Chat(ctx context.Context, req ChatRequest) (string, error) {
	req.Model = p.model
	return p.lib.sendWithRetry(
		func() (string, error) { return p.lib.postChat(ctx, p.baseURL, p.apiKey, req) },
		2,
	)
}