// CodeBuddy 网关的小型辅助函数：efforts 能力表、模型名替换、
// 聚合响应 usage 提取、透传头写出。
package service

import (
	"encoding/json"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
)

// codebuddyEffortsExtraKey 存放 EffortsMap 序列化结果的 extra 键
// （模型同步写入；网关转发读取）。缺省时 PrepareBody 不做档位降级。
const codebuddyEffortsExtraKey = "codebuddy_model_efforts"

// modelEfforts 返回该账号模型的 reasoning 档位能力表。来源优先级：
// 1. extra[codebuddyEffortsExtraKey]（模型同步任务写入）
// 2. UpstreamModelMetadataSnapshot 的 supported_reasoning_levels
// 3. nil（未知，不降级）
func (s *CodebuddyGatewayService) modelEfforts(account *Account) map[string][]string {
	if account == nil {
		return nil
	}
	if raw, ok := account.Extra[codebuddyEffortsExtraKey]; ok && raw != nil {
		if body, err := json.Marshal(raw); err == nil {
			var efforts map[string][]string
			if json.Unmarshal(body, &efforts) == nil && len(efforts) > 0 {
				return efforts
			}
		}
	}
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	if snapshot == nil {
		return nil
	}
	efforts := make(map[string][]string, len(snapshot.Models))
	for id, meta := range snapshot.Models {
		if len(meta.SupportedReasoningLevels) > 0 {
			efforts[id] = meta.SupportedReasoningLevels
		}
	}
	if len(efforts) == 0 {
		return nil
	}
	return efforts
}

// replaceChatCompletionsBodyModel 就地替换 CC 请求体的 model 字段。
func replaceChatCompletionsBodyModel(body []byte, model string) []byte {
	return ReplaceModelInBody(body, model)
}

// apicompatNormalizeChunk 输出标准化 CC chunk（透传路径仅保留白名单字段）。
// 复用 codebuddy.NormalizeFrame 的白名单语义，但输入是 apicompat 结构体：
// 重新 marshal 后经 NormalizeFrame 重建。
func apicompatNormalizeChunk(chunk *apicompat.ChatCompletionsChunk) map[string]any {
	raw, err := json.Marshal(chunk)
	if err != nil {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	return codebuddy.NormalizeFrame(obj)
}

// extractCodebuddyAggregatedUsage 从 codebuddy.Aggregate 产出的
// chat.completion map 中提取 usage（OpenAIUsage 桶语义）。
func extractCodebuddyAggregatedUsage(agg map[string]any) OpenAIUsage {
	body, err := json.Marshal(agg)
	if err != nil {
		return OpenAIUsage{}
	}
	if usage, ok := extractOpenAIUsageFromJSONBytes(body); ok {
		return usage
	}
	return OpenAIUsage{}
}

// writeFilteredResponseHeaders 透传过滤后的上游诊断头到客户端响应。
func writeFilteredResponseHeaders(dst http.Header, src http.Header, filter *responseheaders.CompiledHeaderFilter) {
	if filter == nil {
		return
	}
	responseheaders.WriteFilteredHeaders(dst, src, filter)
}
