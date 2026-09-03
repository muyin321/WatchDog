// Package ai：统一 LLM 适配层
//
// 目标：屏蔽各家大模型差异，对外只暴露 Provider 接口，通过配置切换厂商。
// 第一版骨架同时声明三家 provider（deepseek / openai / qwen），并提供默认
// HTTP 客户端框架。真正的 prompt 组装与请求发送，由二次开发者在以下方法内补齐。
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// ProviderName 支持的厂商标识
const (
	ProviderDeepSeek  = "deepseek"  // DeepSeek
	ProviderOpenAI    = "openai"    // OpenAI
	ProviderQwen      = "qwen"      // 阿里通义千问
	ProviderZhipu     = "zhipu"     // 智谱 GLM
	ProviderDoubao    = "doubao"    // 字节豆包（火山方舟）
	ProviderMiniMax   = "minimax"   // MiniMax
	ProviderAnthropic = "anthropic" // Anthropic Claude
	ProviderCustom    = "custom"    // 自定义 OpenAI 兼容端点
)

// defaultModels 各厂商未显式配置 model 时的默认值。
var defaultModels = map[string]string{
	ProviderDeepSeek:  "deepseek-chat",
	ProviderOpenAI:    "gpt-4o-mini",
	ProviderQwen:      "qwen-plus",
	ProviderZhipu:     "glm-4-flash",
	ProviderDoubao:    "doubao-1.5-pro-32k",
	ProviderMiniMax:   "MiniMax-Text-01",
	ProviderAnthropic: "claude-sonnet-4-20250514",
	ProviderCustom:    "",
}

// DefaultModel 返回厂商默认模型；custom 无默认（必须用户填写）。
func DefaultModel(provider string) string { return defaultModels[provider] }

// defaultBaseURLs 各厂商官方默认端点（均可被配置 ai.base_url 覆盖）。
var defaultBaseURLs = map[string]string{
	ProviderDeepSeek:  "https://api.deepseek.com/v1",
	ProviderOpenAI:    "https://api.openai.com/v1",
	ProviderQwen:      "https://dashscope.aliyuncs.com/compatible-mode/v1",
	ProviderZhipu:     "https://open.bigmodel.cn/api/paas/v4",
	ProviderDoubao:    "https://ark.cn-beijing.volces.com/api/v3",
	ProviderMiniMax:   "https://api.minimaxi.com/v1",
	ProviderAnthropic: "https://api.anthropic.com",
	ProviderCustom:    "",
}

// Message 一次对话消息
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest 通用请求（与各家 OpenAI 兼容接口保持大体一致）
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// ChatResponse 通用响应
type ChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Options 构造 Provider 所需的凭据与端点
type Options struct {
	APIKey  string
	Model   string
	BaseURL string
}

// Provider 统一接口：任何接入的新模型只需实现 Chat。
type Provider interface {
	// Name 返回厂商名
	Name() string
	// Chat 发送对话，返回回复文本
	Chat(ctx context.Context, req ChatRequest) (string, error)
}

// Library 适配器管理器：持有当前激活的 Provider，可热切换。
type Library struct {
	active      Provider
	defaultHTTP *http.Client
}

// NewLibrary 创建适配层库。
func NewLibrary() *Library {
	return &Library{
		defaultHTTP: &http.Client{Timeout: 60 * time.Second},
	}
}

// SetProvider 切换当前厂商。
func (l *Library) SetProvider(p Provider) { l.active = p }

// Active 返回当前厂商，nil 表示未配置。
func (l *Library) Active() Provider { return l.active }

// Complete 便捷入口：给定消息列表，走当前 Provider 返回结果。
func (l *Library) Complete(ctx context.Context, sys, user string) (string, error) {
	if l.active == nil {
		return "", errors.New("未配置 AI 厂商，请先在配置中心填写 API Key")
	}
	return l.active.Chat(ctx, ChatRequest{
		Messages: []Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: user},
		},
	})
}

// postChat 通用 HTTP 调用：向 OpenAI 兼容的 /chat/completions 发起 JSON 请求。
func (l *Library) postChat(ctx context.Context, baseURL, apiKey string, req ChatRequest) (string, error) {
	url := baseURL
	if url == "" {
		url = "https://api.openai.com/v1"
	}
	full := url + "/chat/completions"

	body, _ := json.Marshal(req)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, full, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := l.defaultHTTP.Do(hreq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", errors.New(out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("AI 返回为空")
	}
	return out.Choices[0].Message.Content, nil
}

// postMessages Anthropic Claude 专用：调用 /v1/messages 接口（协议与 OpenAI 不同）。
// 差异点：鉴权用 x-api-key 头、system 为独立字段、响应结构为 content[]。
func (l *Library) postMessages(ctx context.Context, baseURL, apiKey string, req ChatRequest) (string, error) {
	url := baseURL
	if url == "" {
		url = "https://api.anthropic.com"
	}
	full := strings.TrimRight(url, "/") + "/v1/messages"

	// 分离 system 与对话消息
	system := ""
	chat := make([]Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			system += m.Content + "\n"
			continue
		}
		chat = append(chat, m)
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}

	body, _ := json.Marshal(struct {
		Model     string    `json:"model"`
		System    string    `json:"system,omitempty"`
		Messages  []Message `json:"messages"`
		MaxTokens int       `json:"max_tokens"`
	}{Model: req.Model, System: system, Messages: chat, MaxTokens: maxTokens})

	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, full, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("x-api-key", apiKey)
	hreq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := l.defaultHTTP.Do(hreq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", errors.New(out.Error.Message)
	}
	for _, seg := range out.Content {
		if seg.Type == "text" && seg.Text != "" {
			return seg.Text, nil
		}
	}
	return "", errors.New("AI 返回为空")
}

// sendWithRetry 带简单重试的包装（网络抖动兜底）。
func (l *Library) sendWithRetry(fn func() (string, error), tries int) (string, error) {
	var last error
	for i := 0; i < tries; i++ {
		if s, err := fn(); err == nil {
			return s, nil
		} else {
			last = err
		}
	}
	return "", last
}