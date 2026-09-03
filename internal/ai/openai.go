package ai

import "context"

// OpenAIProvider OpenAI 适配器。
type OpenAIProvider struct {
	lib     *Library
	apiKey  string
	model   string
	baseURL string
}

// NewOpenAI 创建 OpenAI provider。
func NewOpenAI(lib *Library, o Options) *OpenAIProvider {
	base := o.BaseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{lib: lib, apiKey: o.APIKey, model: o.Model, baseURL: base}
}

// Name 实现 Provider。
func (p *OpenAIProvider) Name() string { return ProviderOpenAI }

// Chat 实现 Provider。
func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (string, error) {
	req.Model = p.model
	if req.Model == "" {
		req.Model = "gpt-4o-mini"
	}
	return p.lib.postChat(ctx, p.baseURL, p.apiKey, req)
}