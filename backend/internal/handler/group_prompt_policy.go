package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// applyGroupPromptPolicy 在安全审计与路由前执行当前 API Key 所属分组的提示词策略。
func applyGroupPromptPolicy(apiKey *service.APIKey, body []byte, endpoint domain.GroupPromptPolicyEndpoint) ([]byte, bool, error) {
	if apiKey == nil || apiKey.Group == nil {
		return body, false, nil
	}
	result, err := service.ApplyGroupPromptPolicy(body, endpoint, apiKey.Group.PromptPolicy)
	if err != nil {
		return nil, false, err
	}
	return result.Body, result.Blocked, nil
}
