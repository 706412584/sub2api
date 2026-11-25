package kiro

import (
	"encoding/json"
	"fmt"
	"io"
)

type CompletedToolUse struct {
	ToolUseID string `json:"toolUseId"`
	Name      string `json:"name"`
	Input     any    `json:"input"`
}

type Usage struct {
	ContextUsagePercentage float64 `json:"contextUsagePercentage"`
	InputTokens            int     `json:"inputTokens,omitempty"`
	Credits                float64 `json:"credits"`
}

type Response struct {
	Content           string             `json:"content"`
	Thinking          string             `json:"thinking,omitempty"`
	ThinkingSignature string             `json:"thinkingSignature,omitempty"`
	RedactedThinking  []string           `json:"redactedThinking,omitempty"`
	ToolUses          []CompletedToolUse `json:"toolUses,omitempty"`
	Usage             Usage              `json:"usage"`
	StopReason        string             `json:"stopReason"`
}

type toolBuffer struct{ name, input string }

// ResponseState is the single state machine used by streaming adapters and by
// CollectResponse. Apply returns a completed structured tool call when one is
// ready; Snapshot exposes the same accumulated state at any point in a stream.
type ResponseState struct {
	response      Response
	tools         map[string]*toolBuffer
	err           error
	contextWindow int
}

func NewResponseState(contextWindow int) *ResponseState {
	return &ResponseState{response: Response{StopReason: "end_turn"}, tools: make(map[string]*toolBuffer), contextWindow: contextWindow}
}

func (s *ResponseState) Apply(event *Event) (*CompletedToolUse, error) {
	if s.err != nil {
		return nil, s.err
	}
	if event == nil {
		return nil, nil
	}
	if event.UpstreamError != nil {
		if event.UpstreamError.ContentLengthExceeded() {
			s.response.StopReason = "max_tokens"
			return nil, nil
		}
		s.err = event.UpstreamError
		return nil, s.err
	}
	switch event.Type {
	case EventAssistantResponse:
		s.response.Content += event.AssistantResponse.Content
	case EventReasoningContent:
		r := event.ReasoningContent
		s.response.Thinking += r.Text
		if r.Signature != "" {
			s.response.ThinkingSignature = r.Signature
		}
		if r.RedactedContent != "" {
			s.response.RedactedThinking = append(s.response.RedactedThinking, r.RedactedContent)
		}
	case EventContextUsage:
		percentage := event.ContextUsage.ContextUsagePercentage
		s.response.Usage.ContextUsagePercentage = percentage
		if s.contextWindow > 0 {
			s.response.Usage.InputTokens = int(percentage * float64(s.contextWindow) / 100)
		}
		if percentage >= 100 {
			s.response.StopReason = "model_context_window_exceeded"
		}
	case EventMetering:
		s.response.Usage.Credits += event.Metering.Usage
	case EventToolUse:
		tool := event.ToolUse
		buffer := s.tools[tool.ToolUseID]
		if buffer == nil {
			buffer = &toolBuffer{name: tool.Name}
			s.tools[tool.ToolUseID] = buffer
		}
		if buffer.name == "" {
			buffer.name = tool.Name
		}
		buffer.input += tool.Input
		if !tool.Stop {
			return nil, nil
		}
		delete(s.tools, tool.ToolUseID)
		var input any = map[string]any{}
		if buffer.input != "" {
			if err := json.Unmarshal([]byte(buffer.input), &input); err != nil {
				s.err = fmt.Errorf("kiro: invalid toolUseEvent input for %s (%s): %w", tool.ToolUseID, buffer.name, err)
				return nil, s.err
			}
		}
		completed := CompletedToolUse{ToolUseID: tool.ToolUseID, Name: buffer.name, Input: input}
		s.response.ToolUses = append(s.response.ToolUses, completed)
		if s.response.StopReason == "end_turn" {
			s.response.StopReason = "tool_use"
		}
		return &completed, nil
	}
	return nil, nil
}

func (s *ResponseState) Finish() error {
	if s.err != nil {
		return s.err
	}
	for id, tool := range s.tools {
		s.err = fmt.Errorf("kiro: stream ended before completing toolUseEvent %s (%s), buffered %d bytes", id, tool.name, len(tool.input))
		return s.err
	}
	return nil
}

func (s *ResponseState) Snapshot() Response {
	response := s.response
	response.RedactedThinking = append([]string(nil), response.RedactedThinking...)
	response.ToolUses = append([]CompletedToolUse(nil), response.ToolUses...)
	return response
}

func CollectResponse(r io.Reader, contextWindow int) (Response, error) {
	decoder := NewEventDecoder(r)
	state := NewResponseState(contextWindow)
	for {
		event, err := decoder.ReadEvent()
		if err != nil {
			if err == io.EOF {
				break
			}
			return state.Snapshot(), err
		}
		if _, err := state.Apply(event); err != nil {
			return state.Snapshot(), err
		}
	}
	if err := state.Finish(); err != nil {
		return state.Snapshot(), err
	}
	return state.Snapshot(), nil
}
