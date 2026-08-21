package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ConsoleModelCatalog 管理 Console 模型目录的缓存
type ConsoleModelCatalog struct {
	dpopProvider *GrokConsoleDPoPProvider
	mu           sync.RWMutex
	models       []ConsoleModel
	capabilities ConsoleCapabilities
	lastFetch    time.Time
	cacheTTL     time.Duration
}

// ConsoleModel 表示 Console API 返回的模型
type ConsoleModel struct {
	ID          string   `json:"id"`
	Object      string   `json:"object"`
	Created     int64    `json:"created"`
	OwnedBy     string   `json:"owned_by"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// ConsoleCapabilities 表示 Console 支持的能力集合
type ConsoleCapabilities struct {
	ChatCompletions bool     `json:"chat_completions"`
	ImageGeneration bool     `json:"image_generation"`
	VideoGeneration bool     `json:"video_generation"`
	TextToSpeech    bool     `json:"text_to_speech"`
	SpeechToText    bool     `json:"speech_to_text"`
	RealtimeVoice   bool     `json:"realtime_voice"`
	Models          []string `json:"models"`
}

// consoleModelsResponse 是 /v1/models 的响应结构
type consoleModelsResponse struct {
	Object string         `json:"object"`
	Data   []ConsoleModel `json:"data"`
}

// NewConsoleModelCatalog 创建 Console 模型目录
func NewConsoleModelCatalog(dpopProvider *GrokConsoleDPoPProvider) *ConsoleModelCatalog {
	return &ConsoleModelCatalog{
		dpopProvider: dpopProvider,
		cacheTTL:     15 * time.Minute,
	}
}

// FetchModels 从 Console API 获取真实的模型列表
func (c *ConsoleModelCatalog) FetchModels(ctx context.Context, accountID int64) ([]ConsoleModel, error) {
	c.mu.RLock()
	if time.Since(c.lastFetch) < c.cacheTTL && len(c.models) > 0 {
		models := make([]ConsoleModel, len(c.models))
		copy(models, c.models)
		c.mu.RUnlock()
		return models, nil
	}
	c.mu.RUnlock()

	// 1. 获取或创建 DPoP 会话（包含 DPoP-bound access token）
	session, err := c.dpopProvider.GetOrCreateSession(ctx, accountID, nil)
	if err != nil {
		return nil, fmt.Errorf("get DPoP session: %w", err)
	}

	// 2. 生成 DPoP proof（绑定 access token）
	dpopProof, err := c.dpopProvider.BuildDPoPProof(session, "GET", "https://console.x.ai/v1/models")
	if err != nil {
		return nil, fmt.Errorf("generate DPoP proof: %w", err)
	}

	// 3. 构建请求（使用 Authorization: DPoP <access_token> + DPoP: <proof>）
	req, err := http.NewRequestWithContext(ctx, "GET", "https://console.x.ai/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// 设置 DPoP 认证头
	req.Header.Set("Authorization", "DPoP "+session.AccessToken)
	req.Header.Set("DPoP", dpopProof)
	req.Header.Set("Origin", "https://console.x.ai")
	req.Header.Set("Referer", "https://console.x.ai/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	// 4. 发送请求
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}

	// 5. 解析响应
	var modelsResp consoleModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// 6. 更新缓存
	c.mu.Lock()
	c.models = modelsResp.Data
	c.lastFetch = time.Now()
	c.buildCapabilities()
	c.mu.Unlock()

	models := make([]ConsoleModel, len(modelsResp.Data))
	copy(models, modelsResp.Data)
	return models, nil
}

// buildCapabilities 根据模型列表构建能力集合（需要持有写锁）
func (c *ConsoleModelCatalog) buildCapabilities() {
	c.capabilities = ConsoleCapabilities{
		Models: make([]string, 0, len(c.models)),
	}

	for _, model := range c.models {
		c.capabilities.Models = append(c.capabilities.Models, model.ID)

		// 根据模型 ID 推断能力
		id := strings.ToLower(model.ID)
		switch {
		case strings.Contains(id, "grok-4") || strings.Contains(id, "grok-3") || strings.Contains(id, "build"):
			c.capabilities.ChatCompletions = true
		case strings.Contains(id, "imagine-image"):
			c.capabilities.ImageGeneration = true
		case strings.Contains(id, "imagine-video"):
			c.capabilities.VideoGeneration = true
		case strings.Contains(id, "voice") && !strings.Contains(id, "stt"):
			c.capabilities.TextToSpeech = true
			c.capabilities.RealtimeVoice = true
		case strings.Contains(id, "stt"):
			c.capabilities.SpeechToText = true
		}
	}
}

// GetCapabilities 返回当前的能力集合
func (c *ConsoleModelCatalog) GetCapabilities() ConsoleCapabilities {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.capabilities
}

// IsGrokConsoleModel 判断模型是否为 Console 模型
func (c *ConsoleModelCatalog) IsGrokConsoleModel(model string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, m := range c.models {
		if m.ID == model {
			return true
		}
	}
	return false
}

// SupportsGrokConsoleCapability 判断 Console 账号是否支持指定能力
func (c *ConsoleModelCatalog) SupportsGrokConsoleCapability(capability string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	switch capability {
	case "responses", "chat_completions", "messages":
		return c.capabilities.ChatCompletions
	case "image", "image_generation":
		return c.capabilities.ImageGeneration
	case "video", "video_generation":
		return c.capabilities.VideoGeneration
	case "tts", "text_to_speech":
		return c.capabilities.TextToSpeech
	case "stt", "speech_to_text":
		return c.capabilities.SpeechToText
	case "realtime", "realtime_voice":
		return c.capabilities.RealtimeVoice
	default:
		return false
	}
}
