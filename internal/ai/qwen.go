package ai

import "context"

// QwenProvider 通义千问适配器。
type QwenProvider struct {
	lib     *Library
	apiKey  string
	model   string
	baseURL string
}

// NewQwen 创建通义千问 provider。
func NewQwen(lib *Library, o Options) *QwenProvider {
	base := o.BaseURL
	if base == "" {
		base = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	}
	return &QwenProvider{lib: lib, apiKey: o.APIKey, model: o.Model, baseURL: base}
}

// Name 实现 Provider。
func (p *QwenProvider) Name() string { return ProviderQwen }

// Chat 实现 Provider（通义提供了 OpenAI 兼容端点）。
func (p *QwenProvider) Chat(ctx context.Context, req ChatRequest) (string, error) {
	req.Model = p.model
	if req.Model == "" {
		req.Model = "qwen-plus"
	}
	return p.lib.postChat(ctx, p.baseURL, p.apiKey, req)
}