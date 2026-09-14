package codebuddy

import (
	"encoding/json"
	"testing"
)

// 动态接口真实响应形状（截取自 CN 区实测，含超集模型与 cli 子集）。
const modelsFixture = `{
  "code": 0,
  "msg": "ok",
  "data": {
    "endpoint": "https://copilot.tencent.com",
    "mergeStrategy": "merge",
    "models": [
      {"id":"glm-5.2","name":"GLM-5.2","maxInputTokens":131072,"maxOutputTokens":32768,"supportsReasoning":true,
       "reasoning":{"supportedEfforts":["off","low","high"],"defaultEffort":"high","canDisableThinking":true}},
      {"id":"glm-5.0","name":"GLM-5.0","maxInputTokens":131072,"maxOutputTokens":32768},
      {"id":"glm-4.7","name":"GLM-4.7","maxInputTokens":131072,"maxOutputTokens":32768},
      {"id":"minimax-m2.5","name":"MiniMax-M2.5","maxInputTokens":131072,"maxOutputTokens":32768},
      {"id":"kimi-k2-thinking","name":"Kimi-K2-Thinking","maxInputTokens":131072,"maxOutputTokens":32768},
      {"id":"deepseek-v4-pro","name":"DeepSeek-V4-Pro","maxInputTokens":131072,"maxOutputTokens":65536},
      {"id":"disabled-model","name":"Disabled","maxInputTokens":1024,"maxOutputTokens":1024,"disabled":true},
      {"id":"glm-5.3-flash","name":"GLM-5.3-Flash","maxInputTokens":131072,"maxOutputTokens":32768,
       "reasoning":{"effort":"high","summary":"auto"}}
    ],
    "agents": [
      {"name":"other","models":["glm-5.0"]},
      {"name":"cli","models":["glm-5.2","glm-5.0","glm-4.7","minimax-m2.5","kimi-k2-thinking","deepseek-v4-pro","disabled-model","glm-5.3-flash","not-in-models"]}
    ]
  }
}`

// TestParseModelsResponseIntersection 核心：必须取 cli agent ∩ models 的交集。
// data.models 是超集（含 CLI 不可用模型），只取交集才与实际可用性一致。
func TestParseModelsResponseIntersection(t *testing.T) {
	models, err := ParseModelsResponse([]byte(modelsFixture))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ModelInfo{}
	for _, m := range models {
		got[m.ID] = m
	}

	// 交集内：cli 与 models 都有的模型保留
	for _, id := range []string{"glm-5.2", "glm-5.0", "glm-4.7", "minimax-m2.5", "kimi-k2-thinking", "deepseek-v4-pro", "glm-5.3-flash"} {
		if _, ok := got[id]; !ok {
			t.Errorf("intersection should contain %q", id)
		}
	}
	// 超集独有（不在 cli 列表）→ 剔除
	// 注：fixture 里所有 models 都在 cli 列表中，故用 cli 独有项反证
	if _, ok := got["not-in-models"]; ok {
		t.Error("model listed only in cli agent (absent from models) must be dropped")
	}
	// disabled 模型剔除
	if _, ok := got["disabled-model"]; ok {
		t.Error("disabled model must be dropped")
	}

	// 顺序与 cli 列表一致
	if models[0].ID != "glm-5.2" {
		t.Errorf("first model=%q want glm-5.2 (cli 顺序)", models[0].ID)
	}

	// 元数据解析
	glm := got["glm-5.2"]
	if glm.ContextWindow != 131072 || glm.MaxTokens != 32768 {
		t.Errorf("glm-5.2 window=%d/%d want 131072/32768", glm.ContextWindow, glm.MaxTokens)
	}
	if len(glm.Efforts) != 3 {
		t.Errorf("glm-5.2 efforts=%v want 3 档", glm.Efforts)
	}
	// 固定档形态（无 supportedEfforts）→ 空
	if len(got["glm-5.3-flash"].Efforts) != 0 {
		t.Errorf("glm-5.3-flash efforts=%v want empty（固定档不入降级表）", got["glm-5.3-flash"].Efforts)
	}
}

func TestParseModelsResponseErrors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"非法 JSON", `{broken`},
		{"业务 code 非 0", `{"code":10001,"data":{}}`},
		{"无 cli agent", `{"code":0,"data":{"models":[{"id":"a"}],"agents":[{"name":"other","models":["a"]}]}}`},
		{"交集为空", `{"code":0,"data":{"models":[{"id":"a"}],"agents":[{"name":"cli","models":["zzz"]}]}}`},
		{"cli 列表为空", `{"code":0,"data":{"models":[{"id":"a"}],"agents":[{"name":"cli","models":[]}]}}`},
		{"models 为空", `{"code":0,"data":{"models":[],"agents":[{"name":"cli","models":["a"]}]}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseModelsResponse([]byte(c.raw)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestModelIDsAndEffortsMap(t *testing.T) {
	models, err := ParseModelsResponse([]byte(modelsFixture))
	if err != nil {
		t.Fatal(err)
	}
	ids := ModelIDs(models)
	if len(ids) != len(models) {
		t.Fatalf("ids=%d models=%d", len(ids), len(models))
	}
	if ids[0] != "glm-5.2" {
		t.Errorf("ids[0]=%q", ids[0])
	}

	efforts := EffortsMap(models)
	if _, ok := efforts["glm-5.2"]; !ok {
		t.Error("glm-5.2 should be in efforts map")
	}
	if _, ok := efforts["glm-5.3-flash"]; ok {
		t.Error("model without supportedEfforts must not enter efforts map")
	}
	if _, ok := efforts["deepseek-v4-pro"]; ok {
		t.Error("model without reasoning metadata must not enter efforts map")
	}

	// EffortsMap 输出可直接驱动 PrepareBody 降级。
	body := PrepareBody([]byte(`{"model":"glm-5.2","reasoning_effort":"max"}`), efforts)
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m["reasoning_effort"] != "high" {
		t.Errorf("effort=%v want high（max 不在支持档内 → 降级到最高支持档）", m["reasoning_effort"])
	}
}

func TestParseModelsResponseEmptyInput(t *testing.T) {
	if _, err := ParseModelsResponse(nil); err == nil {
		t.Fatal("expected error for nil input")
	}
}
