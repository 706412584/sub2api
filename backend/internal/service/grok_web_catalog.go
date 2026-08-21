package service

import "github.com/Wei-Shaw/sub2api/internal/pkg/xai"

// Grok Web 模型目录（tier 分级，搬自参考项目 catalog.go）。
// entitlement 由上游按订阅等级强制执行；免费账号只能用 basic tier。
type grokWebTier int

const (
	grokWebTierBasic grokWebTier = 1
	grokWebTierSuper grokWebTier = 2
	grokWebTierHeavy grokWebTier = 3
)

type grokWebModelSpec struct {
	Mode          string      // 上游 mgw session.model
	MinimumTier   grokWebTier // 需要的最低订阅档
	WebSearchCapable bool
	XSearchCapable   bool
}

var grokWebModels = map[string]grokWebModelSpec{
	"grok-3":     {Mode: "grok-3", MinimumTier: grokWebTierBasic, WebSearchCapable: true, XSearchCapable: true},
	"grok-3-mini": {Mode: "grok-3-mini", MinimumTier: grokWebTierBasic},
	"grok-chat-fast":  {Mode: "grok-chat-fast", MinimumTier: grokWebTierBasic, WebSearchCapable: true, XSearchCapable: true},
	"grok-chat-auto":  {Mode: "grok-chat-auto", MinimumTier: grokWebTierSuper, WebSearchCapable: true, XSearchCapable: true},
	"grok-chat-expert": {Mode: "grok-chat-expert", MinimumTier: grokWebTierSuper, WebSearchCapable: true, XSearchCapable: true},
	"grok-chat-heavy":  {Mode: "grok-chat-heavy", MinimumTier: grokWebTierHeavy, WebSearchCapable: true, XSearchCapable: true},
}

// grokWebModelMode 返回上游 mgw 模型名；未知模型回落 grok-3（免费可用）。
func grokWebModelMode(model string) string {
	if spec, ok := grokWebModels[model]; ok {
		return spec.Mode
	}
	return "grok-3"
}

// GrokWebDefaultModelMapping 返回 Web 账号的默认模型白名单（identity 映射）。
func GrokWebDefaultModelMapping() map[string]string {
	mapping := make(map[string]string, len(grokWebModels)+2)
	for id := range grokWebModels {
		mapping[id] = id
	}
	mapping["grok"] = "grok-chat-fast"
	mapping["grok-latest"] = "grok-chat-fast"
	return mapping
}

// GrokConsoleDefaultModelMapping 返回 Console 账号的默认模型白名单。
// Console 上游与 Build OAuth 共享 api.x.ai 模型集（含别名），直接复用。
func GrokConsoleDefaultModelMapping() map[string]string {
	return xai.DefaultModelMapping()
}
