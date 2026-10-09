package codebuddy

import "strings"

// modelRegions 模型 → 支持它的区域集合（区域归属表）。
// 来源：2026-09 实测。CN 取自动态接口的 cli agent 列表；Global 为逐个模型实调结果。
//
// 本表只用于一件事：拦截「已知属于另一区域」的模型。两区模型阵容不同，
// 把请求发给不支持的域名会拿到 code=11102。**未收录的模型一律放行**——
// 上游随时会新增模型，本表必然滞后，宁可让请求按普通轮换试一次，
// 也不要因表未收录而拒绝用户的自定义模型。
var modelRegions = func() map[string]map[Region]bool {
	m := map[string]map[Region]bool{}
	add := func(r Region, ids ...string) {
		for _, id := range ids {
			if m[id] == nil {
				m[id] = map[Region]bool{}
			}
			m[id][r] = true
		}
	}
	add(RegionCN,
		"auto", "default",
		"deepseek-v3-2-volc", "deepseek-v4-flash", "deepseek-v4-pro", "deepseek-v4.1-flash",
		"glm-4.6", "glm-4.6v", "glm-4.7", "glm-5.0", "glm-5.1", "glm-5.2", "glm-5.3", "glm-5.3-flash", "glm-5v-turbo",
		"hunyuan-2.0-thinking", "hunyuan-chat", "hunyuan-image-v3.0",
		"hy3", "hy3-x", "hy4-preview", "hy4-preview-x",
		"kimi-k2-thinking", "kimi-k2.5", "kimi-k2.6", "kimi-k2.7", "kimi-k3-1",
		"minimax-m2.5", "minimax-m3",
	)
	add(RegionGlobal,
		"auto", "deepseek-v4.1-flash",
		"glm-5.1", "glm-5.2", "glm-5.3", "glm-5v-turbo",
		"hy3", "hy4-preview", "hy4-preview-x",
		"kimi-k2.5", "kimi-k2.6", "kimi-k2.7",
		"minimax-m3",
	)
	return m
}()

// ModelAllowedInRegion 报告 model 是否允许在 accountType 对应的区域上使用。
//
// 三种结果：
//   - 本区域已知模型 → true（放行）
//   - 另一区域已知模型 → false（拒绝：两区模型不互通，唯一实现点）
//   - 两区均未收录（自定义 / 上游新增）→ true（放行，交由上游裁决 code=11102）
//
// 空模型返回 true（不干预，由上游报错）。
func ModelAllowedInRegion(accountType, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return true
	}
	regions := modelRegions[model]
	if regions == nil {
		// 精确未命中：再试小写归一（客户端可能发 GLM-5.2）。
		regions = modelRegions[strings.ToLower(model)]
	}
	if len(regions) == 0 {
		// 未知模型：不作限制，透传上游。
		return true
	}
	return regions[RegionForAccountType(accountType)]
}

// ModelRegions 返回收录该模型的区域集合（用于诊断/测试）；未知模型返回 nil。
func ModelRegions(model string) []Region {
	regions := modelRegions[strings.TrimSpace(model)]
	if len(regions) == 0 {
		return nil
	}
	out := make([]Region, 0, len(regions))
	if regions[RegionCN] {
		out = append(out, RegionCN)
	}
	if regions[RegionGlobal] {
		out = append(out, RegionGlobal)
	}
	return out
}
