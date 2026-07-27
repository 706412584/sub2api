package service

import (
	"encoding/json"
	"regexp"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

// GroupPromptPolicyResult 是提示词策略处理后的结果。
type GroupPromptPolicyResult struct {
	Body     []byte
	Modified bool
	Blocked  bool
}

// ApplyGroupPromptPolicy 按协议和规则顺序处理允许修改的文本字段。
// 未启用、无规则或无命中时直接返回原始 body，避免改变普通分组请求。
func ApplyGroupPromptPolicy(body []byte, endpoint domain.GroupPromptPolicyEndpoint, policy GroupPromptPolicy) (GroupPromptPolicyResult, error) {
	if !policy.Enabled || len(policy.Rules) == 0 {
		return GroupPromptPolicyResult{Body: body}, nil
	}

	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return GroupPromptPolicyResult{}, err
	}

	processor := groupPromptPolicyProcessor{endpoint: endpoint, policy: policy}
	switch endpoint {
	case domain.GroupPromptPolicyEndpointChatCompletions:
		processor.processChatCompletions(request)
	case domain.GroupPromptPolicyEndpointMessages:
		processor.processMessages(request)
	case domain.GroupPromptPolicyEndpointResponses:
		processor.processResponses(request)
	}
	if processor.blocked {
		return GroupPromptPolicyResult{Body: body, Blocked: true}, nil
	}
	if !processor.modified {
		return GroupPromptPolicyResult{Body: body}, nil
	}
	updated, err := json.Marshal(request)
	if err != nil {
		return GroupPromptPolicyResult{}, err
	}
	return GroupPromptPolicyResult{Body: updated, Modified: true}, nil
}

type groupPromptPolicyProcessor struct {
	endpoint domain.GroupPromptPolicyEndpoint
	policy   GroupPromptPolicy
	modified bool
	blocked  bool
}

// processChatCompletions 仅处理 Chat Completions 的 system 与消息文本内容。
func (p *groupPromptPolicyProcessor) processChatCompletions(request map[string]any) {
	messages, _ := request["messages"].([]any)
	for _, entry := range messages {
		message, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		target := domain.GroupPromptPolicyTargetMessageText
		if message["role"] == "system" {
			target = domain.GroupPromptPolicyTargetSystem
		}
		p.processContent(message, "content", target)
		if p.blocked {
			return
		}
	}
}

// processMessages 仅处理 Anthropic Messages 的 system 与 messages 文本块。
func (p *groupPromptPolicyProcessor) processMessages(request map[string]any) {
	p.processContent(request, "system", domain.GroupPromptPolicyTargetSystem)
	if p.blocked {
		return
	}
	messages, _ := request["messages"].([]any)
	for _, entry := range messages {
		message, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		p.processContent(message, "content", domain.GroupPromptPolicyTargetMessageText)
		if p.blocked {
			return
		}
	}
}

// processResponses 仅处理 OpenAI Responses 的 instructions 与 input 文本内容。
func (p *groupPromptPolicyProcessor) processResponses(request map[string]any) {
	p.processContent(request, "instructions", domain.GroupPromptPolicyTargetInstructions)
	if p.blocked {
		return
	}
	p.processContent(request, "input", domain.GroupPromptPolicyTargetMessageText)
}

// processContent 处理字符串或 content part 数组中的 text 字段，不触及工具、图片与推理块。
func (p *groupPromptPolicyProcessor) processContent(container map[string]any, key string, target domain.GroupPromptPolicyTarget) {
	value, exists := container[key]
	if !exists {
		return
	}
	switch content := value.(type) {
	case string:
		updated, changed, blocked := p.applyRules(content, target)
		if blocked {
			p.blocked = true
			return
		}
		if changed {
			container[key] = updated
			p.modified = true
		}
	case []any:
		for _, item := range content {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text, ok := part["text"].(string)
			if !ok {
				continue
			}
			updated, changed, blocked := p.applyRules(text, target)
			if blocked {
				p.blocked = true
				return
			}
			if changed {
				part["text"] = updated
				p.modified = true
			}
		}
	}
}

// applyRules 在单个文本槽位内顺序应用适配的启用规则。
func (p *groupPromptPolicyProcessor) applyRules(text string, target domain.GroupPromptPolicyTarget) (string, bool, bool) {
	changed := false
	for _, rule := range p.policy.Rules {
		if !rule.Enabled || !ruleMatchesEndpoint(rule, p.endpoint) || !ruleMatchesTarget(rule, target) {
			continue
		}
		matcher, err := compileGroupPromptPolicyMatcher(rule.Match)
		if err != nil || !matcher.MatchString(text) {
			continue
		}
		switch rule.Mode {
		case domain.GroupPromptPolicyModeBlock:
			return text, changed, true
		case domain.GroupPromptPolicyModeReplace:
			updated := matcher.ReplaceAllString(text, rule.Value)
			if updated != text {
				text = updated
				changed = true
			}
		case domain.GroupPromptPolicyModePrepend:
			text = rule.Value + text
			changed = true
		case domain.GroupPromptPolicyModeAppend:
			text += rule.Value
			changed = true
		}
	}
	return text, changed, false
}

func ruleMatchesEndpoint(rule domain.GroupPromptPolicyRule, endpoint domain.GroupPromptPolicyEndpoint) bool {
	for _, candidate := range rule.Endpoints {
		if candidate == endpoint {
			return true
		}
	}
	return false
}

func ruleMatchesTarget(rule domain.GroupPromptPolicyRule, target domain.GroupPromptPolicyTarget) bool {
	for _, candidate := range rule.Targets {
		if candidate == target {
			return true
		}
	}
	return false
}

// compileGroupPromptPolicyMatcher 使用 Go RE2 引擎生成文本匹配器。
func compileGroupPromptPolicyMatcher(match domain.GroupPromptPolicyMatch) (*regexp.Regexp, error) {
	pattern := match.Value
	if match.Kind == domain.GroupPromptPolicyMatchKindLiteral {
		pattern = regexp.QuoteMeta(pattern)
	}
	if !match.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}
