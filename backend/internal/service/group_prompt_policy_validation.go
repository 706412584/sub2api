package service

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

const (
	maxGroupPromptPolicyRules      = 50
	maxGroupPromptPolicyTextLength = 10000
)

// ValidateGroupPromptPolicy 校验分组提示词策略，避免无效配置进入请求热路径。
func ValidateGroupPromptPolicy(policy GroupPromptPolicy) error {
	if len(policy.Rules) > maxGroupPromptPolicyRules {
		return fmt.Errorf("prompt_policy rules must not exceed %d", maxGroupPromptPolicyRules)
	}
	for index, rule := range policy.Rules {
		if err := validateGroupPromptPolicyRule(rule); err != nil {
			return fmt.Errorf("prompt_policy rule %d: %w", index+1, err)
		}
	}
	return nil
}

// validateGroupPromptPolicyRule 校验单条提示词规则的枚举和文本约束。
func validateGroupPromptPolicyRule(rule domain.GroupPromptPolicyRule) error {
	if len(rule.Endpoints) == 0 {
		return fmt.Errorf("endpoints is required")
	}
	if len(rule.Targets) == 0 {
		return fmt.Errorf("targets is required")
	}
	if len(rule.Match.Value) == 0 || len(rule.Match.Value) > maxGroupPromptPolicyTextLength {
		return fmt.Errorf("match.value length must be between 1 and %d", maxGroupPromptPolicyTextLength)
	}
	if len(rule.Value) > maxGroupPromptPolicyTextLength {
		return fmt.Errorf("value length must not exceed %d", maxGroupPromptPolicyTextLength)
	}
	for _, endpoint := range rule.Endpoints {
		if endpoint != domain.GroupPromptPolicyEndpointChatCompletions && endpoint != domain.GroupPromptPolicyEndpointMessages && endpoint != domain.GroupPromptPolicyEndpointResponses {
			return fmt.Errorf("invalid endpoint %q", endpoint)
		}
	}
	for _, target := range rule.Targets {
		if target != domain.GroupPromptPolicyTargetSystem && target != domain.GroupPromptPolicyTargetInstructions && target != domain.GroupPromptPolicyTargetMessageText {
			return fmt.Errorf("invalid target %q", target)
		}
	}
	if rule.Mode != domain.GroupPromptPolicyModeReplace && rule.Mode != domain.GroupPromptPolicyModeBlock && rule.Mode != domain.GroupPromptPolicyModePrepend && rule.Mode != domain.GroupPromptPolicyModeAppend {
		return fmt.Errorf("invalid mode %q", rule.Mode)
	}
	if rule.Mode != domain.GroupPromptPolicyModeBlock && strings.TrimSpace(rule.Value) == "" {
		return fmt.Errorf("value is required for %s", rule.Mode)
	}
	if rule.Match.Kind != domain.GroupPromptPolicyMatchKindLiteral && rule.Match.Kind != domain.GroupPromptPolicyMatchKindRegex {
		return fmt.Errorf("invalid match.kind %q", rule.Match.Kind)
	}
	if rule.Match.Kind == domain.GroupPromptPolicyMatchKindRegex {
		if _, err := regexp.Compile(rule.Match.Value); err != nil {
			return fmt.Errorf("invalid regex: %w", err)
		}
	}
	return nil
}
