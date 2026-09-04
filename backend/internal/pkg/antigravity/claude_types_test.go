package antigravity

import "testing"

func TestDefaultModels_ContainsNewAndLegacyImageModels(t *testing.T) {
	t.Parallel()

	models := DefaultModels()
	byID := make(map[string]ClaudeModel, len(models))
	for _, m := range models {
		byID[m.ID] = m
	}

	requiredIDs := []string{
		"claude-fable-5-1",
		"claude-fable-5",
		"claude-opus-4-8",
		"claude-opus-4-6-thinking",
		"gemini-2.5-flash-image",
		"gemini-2.5-flash-image-preview",
		"gemini-3.1-flash-image",
		"gemini-3.1-flash-image-preview",
		"gemini-3-pro-image", // legacy compatibility
		"gemini-3.6-flash",
		"gemini-3.6-flash-high",
		"gemini-3.6-flash-low",
		"gemini-3.6-flash-medium",
		"gemini-3.6-flash-tiered",
		"gemini-3.8-flash",
		"gemini-3.8-flash-high",
		"gemini-3.8-flash-low",
		"gemini-3.8-flash-medium",
		"gemini-3.8-flash-tiered",
	}

	for _, id := range requiredIDs {
		if _, ok := byID[id]; !ok {
			t.Fatalf("expected model %q to be exposed in DefaultModels", id)
		}
	}
}

// 分档模型的裸名不是 reasoning 模型（不触发 ToolConfig 过滤），
// 带档位后缀的才是——与 3.6 的既有语义保持一致。
func TestIsGeminiReasoningModel_TieredFlashVariants(t *testing.T) {
	t.Parallel()

	for _, base := range []string{"gemini-3.6-flash", "gemini-3.8-flash"} {
		if IsGeminiReasoningModel(base) {
			t.Fatalf("expected %q not to be treated as a reasoning model", base)
		}
		for _, tier := range []string{"low", "medium", "high", "tiered"} {
			id := base + "-" + tier
			if !IsGeminiReasoningModel(id) {
				t.Fatalf("expected %q to be treated as a reasoning model", id)
			}
		}
	}
}
