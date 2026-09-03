package ai

import (
	"log"

	"github.com/watchdog-ai/watchdog/internal/model"
)

// BuildFromConfig 依据全局配置（model.Config 表）构建并激活对应 Provider。
// 这是“配置写活”的关键：切换厂商只需改库表配置，无需改代码。
//
// 支持：deepseek / openai / qwen / zhipu(智谱) / doubao(字节豆包) /
//       minimax / anthropic / custom(自定义 OpenAI 兼容端点)。
func (l *Library) BuildFromConfig(kv func(key string) string) {
	provider := kv(model.CfgAIProvider)
	opts := Options{
		APIKey:  kv(model.CfgAIAPIKey),
		Model:   kv(model.CfgAIModel),
		BaseURL: kv(model.CfgAIBaseURL),
	}
	if opts.APIKey == "" {
		l.active = nil
		return
	}

	var p Provider
	switch provider {
	case ProviderDeepSeek:
		p = NewDeepSeek(l, opts)
	case ProviderQwen:
		p = NewQwen(l, opts)
	case ProviderZhipu:
		p = NewZhipu(l, opts)
	case ProviderDoubao:
		p = NewDoubao(l, opts)
	case ProviderMiniMax:
		p = NewMiniMax(l, opts)
	case ProviderAnthropic:
		p = NewAnthropic(l, opts)
	case ProviderCustom:
		// 自定义端点必须提供 base_url，否则无法调用
		if opts.BaseURL == "" {
			log.Printf("[ai] custom 厂商未配置 ai.base_url，AI 分析保持禁用")
			l.active = nil
			return
		}
		p = NewCustom(l, opts)
	case ProviderOpenAI:
		fallthrough
	default:
		p = NewOpenAI(l, opts)
	}
	l.SetProvider(p)
	log.Printf("[ai] 当前 AI 厂商: %s (model=%s)", p.Name(), opts.Model)
}
