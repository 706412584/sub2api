package im

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Forwarder self-calls the local gateway's /v1/messages with the bot-bound
// API key. Auth, group allowlist, sticky scheduling and billing all ride the
// normal gateway pipeline — the forwarder only builds the request and parses
// the SSE reply.
type Forwarder struct {
	client  *http.Client
	baseURL string
}

// ForwardRequest is one chat turn through the gateway.
type ForwardRequest struct {
	// APIKey is the bound key's plaintext (memory-only, never logged).
	APIKey string
	// Model: chat override > bot override > "" (group default).
	Model string
	// System is the bot's system prompt ("" = none).
	System string
	// Messages is the rebuilt history window (must be Anthropic-valid).
	Messages []HistoryMessage
	// SessionUUID identifies the chat for sticky scheduling + usage logs.
	SessionUUID string
	// SessionLogKey is the full X-Session-Id header value (usage-log
	// correlation, e.g. "im:3:uuid"). Empty skips the header.
	SessionLogKey string
	// DeviceHex64 is the bot's stable device component of metadata.user_id.
	DeviceHex64 string
	// MaxTokens caps the turn.
	MaxTokens int
	// Tools is reserved for the tool phase; nil keeps today's behavior.
	Tools json.RawMessage
}

// ForwardResult summarizes a completed turn.
type ForwardResult struct {
	FullText     string
	RequestID    string
	StopReason   string
	InputTokens  int
	OutputTokens int
}

const forwardIdleTimeout = 120 * time.Second

// NewForwarder builds a forwarder against the gateway base URL
// (e.g. http://127.0.0.1:18080, no trailing slash).
func NewForwarder(baseURL string) *Forwarder {
	return &Forwarder{
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:          4,
				MaxIdleConnsPerHost:   4,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: forwardIdleTimeout,
			},
		},
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type forwardBody struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Metadata  *forwardMetadata   `json:"metadata,omitempty"`
	Tools     json.RawMessage    `json:"tools,omitempty"`
}

type forwardMetadata struct {
	UserID string `json:"user_id"`
}

// Stream posts the request and pipes SSE text deltas into stream. stream is
// always finished exactly once. On success the full text is returned; err is
// non-nil on transport/upstream failure (already surfaced to the stream).
func (f *Forwarder) Stream(ctx context.Context, req *ForwardRequest, stream OutboundStream) (*ForwardResult, error) {
	result, err := f.stream(ctx, req, stream)
	stream.Finish(err)
	return result, err
}

func (f *Forwarder) stream(ctx context.Context, req *ForwardRequest, stream OutboundStream) (*ForwardResult, error) {
	body := forwardBody{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Stream:    true,
		Messages:  make([]anthropicMessage, 0, len(req.Messages)),
	}
	if req.System != "" {
		body.System = req.System
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, anthropicMessage{Role: m.Role, Content: m.Content})
	}
	if req.SessionUUID != "" {
		body.Metadata = &forwardMetadata{
			UserID: BuildMetadataUserID(req.DeviceHex64, req.SessionUUID),
		}
	}
	if len(req.Tools) > 0 {
		body.Tools = req.Tools
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, f.baseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	if req.SessionLogKey != "" {
		httpReq.Header.Set("X-Session-Id", req.SessionLogKey)
	}

	resp, err := f.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	result := &ForwardResult{RequestID: resp.Header.Get("X-Client-Request-Id")}

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return result, fmt.Errorf("gateway /v1/messages status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var full strings.Builder
	reader := bufio.NewReaderSize(resp.Body, 32*1024)
	var eventName string

	for {
		if ctx.Err() != nil {
			result.FullText = full.String()
			return result, ctx.Err()
		}
		line, readErr := reader.ReadString('\n')
		if trimmed := strings.TrimRight(line, "\r\n"); trimmed != "" {
			if strings.HasPrefix(trimmed, "event:") {
				eventName = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
			} else if strings.HasPrefix(trimmed, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if data != "" {
					if stop, err := applySSEEvent(data, eventName, stream, &full, result); stop || err != nil {
						result.FullText = full.String()
						return result, err
					}
				}
			}
		}
		if readErr != nil {
			result.FullText = full.String()
			if readErr != io.EOF {
				return result, readErr
			}
			return result, nil
		}
	}
}

// applySSEEvent folds one SSE data payload into the stream/result.
// stop=true means the turn is over (message_stop). err is non-nil on an
// upstream-declared error event.
func applySSEEvent(data, eventName string, stream OutboundStream, full *strings.Builder, result *ForwardResult) (bool, error) {
	switch eventName {
	case "content_block_delta":
		var ev struct {
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err == nil && ev.Delta.Text != "" {
			full.WriteString(ev.Delta.Text)
			stream.Append(ev.Delta.Text)
		}
	case "message_start":
		var ev struct {
			Message struct {
				Usage struct {
					InputTokens int `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err == nil && ev.Message.Usage.InputTokens > 0 {
			result.InputTokens = ev.Message.Usage.InputTokens
		}
	case "message_delta":
		var ev struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage *struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err == nil {
			if ev.Delta.StopReason != "" {
				result.StopReason = ev.Delta.StopReason
			}
			if ev.Usage != nil && ev.Usage.OutputTokens > 0 {
				result.OutputTokens = ev.Usage.OutputTokens
			}
		}
	case "error":
		var ev struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err == nil && ev.Error.Message != "" {
			return false, fmt.Errorf("upstream error: %s", ev.Error.Message)
		}
		return false, fmt.Errorf("upstream error event")
	case "message_stop":
		return true, nil
	}
	return false, nil
}
