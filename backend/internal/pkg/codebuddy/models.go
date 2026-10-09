package codebuddy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ModelInfo 上游动态模型接口返回的单个模型。
type ModelInfo struct {
	ID            string
	Name          string
	ContextWindow int64    // = maxInputTokens
	MaxTokens     int64    // = maxOutputTokens
	Efforts       []string // reasoning.supportedEfforts（空 = 未知/固定档）
}

// modelsResponse 动态模型接口响应（GET {chatBase}/console/enterprises/personal/models）。
//
// 关键结构：data.models 是全量模型超集，data.agents[name=="cli"].models 才是 CLI 可用子集。
// 必须取交集——超集里的 glm-5.0 / glm-4.7 / minimax-m2.5 / kimi-k2-thinking 等
// 实测请求返回 400 code=11102（模型不可用）。
type modelsResponse struct {
	Code int `json:"code"`
	Data struct {
		Models []struct {
			ID              string `json:"id"`
			Name            string `json:"name"`
			MaxInputTokens  int64  `json:"maxInputTokens"`
			MaxOutputTokens int64  `json:"maxOutputTokens"`
			Disabled        bool   `json:"disabled"`
			Reasoning       struct {
				Effort           string   `json:"effort"`
				SupportedEfforts []string `json:"supportedEfforts"`
			} `json:"reasoning"`
		} `json:"models"`
		Agents []struct {
			Name   string   `json:"name"`
			Models []string `json:"models"`
		} `json:"agents"`
	} `json:"data"`
}

// ParseModelsResponse 解析动态模型接口响应，返回 cli agent ∩ models 的可用模型。
// 空列表或解析失败返回错误，由调用方决定是否回落静态表。
func ParseModelsResponse(raw []byte) ([]ModelInfo, error) {
	var env modelsResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("codebuddy: parse models response: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("codebuddy: models api returned code=%d", env.Code)
	}
	var cliIDs []string
	for _, ag := range env.Data.Agents {
		if ag.Name == "cli" {
			cliIDs = ag.Models
			break
		}
	}
	if len(cliIDs) == 0 {
		return nil, fmt.Errorf("codebuddy: models response has no cli agent models")
	}
	byID := make(map[string]ModelInfo, len(env.Data.Models))
	for _, m := range env.Data.Models {
		if m.Disabled {
			continue
		}
		byID[m.ID] = ModelInfo{
			ID:            m.ID,
			Name:          m.Name,
			ContextWindow: m.MaxInputTokens,
			MaxTokens:     m.MaxOutputTokens,
			Efforts:       m.Reasoning.SupportedEfforts,
		}
	}
	out := make([]ModelInfo, 0, len(cliIDs))
	for _, id := range cliIDs {
		if m, ok := byID[id]; ok {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("codebuddy: models response yielded no usable models")
	}
	return out, nil
}

// ModelIDs 提取模型 ID 列表（保序）。
func ModelIDs(models []ModelInfo) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		if strings.TrimSpace(m.ID) != "" {
			out = append(out, m.ID)
		}
	}
	return out
}

// EffortsMap 构造 reasoning 档位能力表（仅收录 supportedEfforts 非空的模型），
// 供 PrepareBody 做档位降级。无该字段的模型不入表 = 不降级（透传）。
func EffortsMap(models []ModelInfo) map[string][]string {
	out := make(map[string][]string, len(models))
	for _, m := range models {
		if len(m.Efforts) > 0 {
			out[m.ID] = m.Efforts
		}
	}
	return out
}

// 静态兜底模型表。仅在动态接口不可用时使用。
//
// Global 区必然走这里：其 /console/enterprises/personal/models 实测恒 500
// （服务端 APISIX 故障，已排除 host/path/header 变体），因此 global 表必须准确。
// 内容为 2026-09 用真实 global 凭证逐个模型实测可用的结果。
var (
	staticIDsCN = []string{
		"glm-5.2",
		"glm-5.1",
		"glm-5v-turbo",
		"kimi-k2.7",
		"minimax-m3",
		"hy3",
		"hy3-preview",
		"hy3-preview-agent",
		"deepseek-v4-pro",
		"deepseek-v4-flash",
	}
	// 注意 global 不含 deepseek-v4-pro / deepseek-v4-flash：
	// 调用报 code=11102（service info not found）。
	staticIDsGlobal = []string{
		"glm-5.3",
		"glm-5.2",
		"glm-5.1",
		"glm-5v-turbo",
		"kimi-k2.7",
		"kimi-k2.6",
		"kimi-k2.5",
		"minimax-m3",
		"hy3",
		"hy4-preview",
		"hy4-preview-x",
		"deepseek-v4.1-flash",
		"auto",
	}
)

// StaticModelIDs 返回区域的静态兜底模型 ID 表（副本，调用方可自由修改）。
func StaticModelIDs(r Region) []string {
	src := staticIDsCN
	if r == RegionGlobal {
		src = staticIDsGlobal
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// StaticModelsAll 返回两区静态表的并集（去重升序），用于平台默认模型列表展示。
func StaticModelsAll() []string {
	set := make(map[string]struct{}, len(staticIDsCN)+len(staticIDsGlobal))
	for _, id := range staticIDsCN {
		set[id] = struct{}{}
	}
	for _, id := range staticIDsGlobal {
		set[id] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
