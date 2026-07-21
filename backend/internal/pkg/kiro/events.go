package kiro

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/awseventstream"
)

type EventType string

const (
	EventAssistantResponse EventType = "assistantResponseEvent"
	EventToolUse           EventType = "toolUseEvent"
	EventMetering          EventType = "meteringEvent"
	EventContextUsage      EventType = "contextUsageEvent"
	EventReasoningContent  EventType = "reasoningContentEvent"
	EventUnknown           EventType = "unknown"
	EventError             EventType = "error"
	EventException         EventType = "exception"
)

type AssistantResponseEvent struct {
	Content string `json:"content"`
}
type ToolUseEvent struct {
	Name      string `json:"name"`
	ToolUseID string `json:"toolUseId"`
	Input     string `json:"input"`
	Stop      bool   `json:"stop"`
}
type MeteringEvent struct {
	Usage float64 `json:"usage"`
}
type ContextUsageEvent struct {
	ContextUsagePercentage float64 `json:"contextUsagePercentage"`
}
type ReasoningContentEvent struct {
	Text            string `json:"text"`
	Signature       string `json:"signature"`
	RedactedContent string `json:"redactedContent"`
}

type Event struct {
	Type              EventType
	AssistantResponse *AssistantResponseEvent
	ToolUse           *ToolUseEvent
	Metering          *MeteringEvent
	ContextUsage      *ContextUsageEvent
	ReasoningContent  *ReasoningContentEvent
	UpstreamError     *UpstreamError
	RawPayload        []byte
}

type EventDecoder struct{ reader *awseventstream.Reader }

func NewEventDecoder(r io.Reader) *EventDecoder {
	return &EventDecoder{reader: awseventstream.NewReader(r)}
}
func NewEventDecoderSize(r io.Reader, max uint32) *EventDecoder {
	return &EventDecoder{reader: awseventstream.NewReaderSize(r, max)}
}

func (d *EventDecoder) ReadEvent() (*Event, error) {
	message, err := d.reader.ReadMessage()
	if err != nil {
		return nil, err
	}
	if streamErr := message.Error(); streamErr != nil {
		var me *awseventstream.MessageError
		if ok := asMessageError(streamErr, &me); ok {
			kind := EventError
			if me.MessageType == "exception" {
				kind = EventException
			}
			return &Event{Type: kind, UpstreamError: NewUpstreamError(kind, me.Code, string(me.Payload)), RawPayload: me.Payload}, nil
		}
		return nil, streamErr
	}

	eventType := EventType(message.Headers.EventType())
	event := &Event{Type: eventType, RawPayload: append([]byte(nil), message.Payload...)}
	var target any
	switch eventType {
	case EventAssistantResponse:
		event.AssistantResponse = &AssistantResponseEvent{}
		target = event.AssistantResponse
	case EventToolUse:
		event.ToolUse = &ToolUseEvent{}
		target = event.ToolUse
	case EventMetering:
		event.Metering = &MeteringEvent{}
		target = event.Metering
	case EventContextUsage:
		event.ContextUsage = &ContextUsageEvent{}
		target = event.ContextUsage
	case EventReasoningContent:
		event.ReasoningContent = &ReasoningContentEvent{}
		target = event.ReasoningContent
	default:
		event.Type = EventUnknown
	}
	if target != nil {
		if err := json.Unmarshal(message.Payload, target); err != nil {
			return nil, fmt.Errorf("kiro: decode %s: %w", eventType, err)
		}
	}
	if embedded := parseEmbeddedError(message.Payload); embedded != nil {
		event.UpstreamError = embedded
	}
	return event, nil
}

func asMessageError(err error, target **awseventstream.MessageError) bool {
	me, ok := err.(*awseventstream.MessageError)
	if ok {
		*target = me
	}
	return ok
}

func parseEmbeddedError(payload []byte) *UpstreamError {
	var envelope struct {
		Type         string `json:"__type"`
		Code         string `json:"code"`
		ErrorCode    string `json:"errorCode"`
		Message      string `json:"message"`
		ErrorMessage string `json:"errorMessage"`
		Reason       string `json:"reason"`
		Error        *struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
			Reason  string `json:"reason"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return nil
	}
	code, message, reason := envelope.ErrorCode, envelope.ErrorMessage, envelope.Reason
	if code == "" {
		code = envelope.Code
	}
	if code == "" {
		code = envelope.Type
	}
	if message == "" {
		message = envelope.Message
	}
	if envelope.Error != nil {
		if code == "" {
			code = envelope.Error.Code
		}
		if code == "" {
			code = envelope.Error.Type
		}
		if message == "" {
			message = envelope.Error.Message
		}
		if reason == "" {
			reason = envelope.Error.Reason
		}
	}
	// Normal event payloads may carry incidental message fields. Require a code,
	// reason, nested error, or an explicit error-like type.
	if code == "" && reason == "" && envelope.Error == nil {
		return nil
	}
	if code != "" && !strings.Contains(strings.ToLower(code), "error") && !strings.Contains(strings.ToLower(code), "exception") && reason == "" && envelope.Error == nil {
		return nil
	}
	return &UpstreamError{Kind: EventError, Code: code, Message: message, Reason: reason, Payload: append([]byte(nil), payload...)}
}
