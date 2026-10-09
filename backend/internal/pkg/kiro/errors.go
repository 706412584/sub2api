package kiro

import (
	"encoding/json"
	"fmt"
	"strings"
)

var quotaReasons = map[string]struct{}{
	"MONTHLY_REQUEST_COUNT":          {},
	"OVERAGE_REQUEST_LIMIT_EXCEEDED": {},
}

var clientValidationReasons = map[string]struct{}{
	"TOOL_USE_RESULT_MISMATCH": {},
	"TOOL_SCHEMA_INVALID":      {},
}

type UpstreamError struct {
	Kind    EventType
	Code    string
	Message string
	Reason  string
	Payload []byte
}

func NewUpstreamError(kind EventType, code, payload string) *UpstreamError {
	e := &UpstreamError{Kind: kind, Code: code, Message: payload, Payload: []byte(payload)}
	var v struct {
		Message string `json:"message"`
		Reason  string `json:"reason"`
		Error   *struct {
			Message string `json:"message"`
			Reason  string `json:"reason"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(payload), &v) == nil {
		if v.Message != "" {
			e.Message = v.Message
		}
		e.Reason = v.Reason
		if v.Error != nil {
			if e.Message == "" {
				e.Message = v.Error.Message
			}
			if e.Reason == "" {
				e.Reason = v.Error.Reason
			}
		}
	}
	return e
}

func (e *UpstreamError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("kiro upstream %s: %s", e.Code, e.Message)
	}
	return "kiro upstream error: " + e.Message
}
func (e *UpstreamError) QuotaExhausted() bool { _, ok := quotaReasons[e.Reason]; return ok }
func (e *UpstreamError) ClientValidation() bool {
	if _, ok := clientValidationReasons[e.Reason]; ok {
		return true
	}
	return strings.Contains(e.Message, "Expected toolResult blocks")
}
func (e *UpstreamError) BearerTokenInvalid() bool {
	return strings.Contains(e.Message, "The bearer token included in the request is invalid")
}
func (e *UpstreamError) AccountThrottled() bool {
	return strings.Contains(e.Message, "suspicious activity") && strings.Contains(e.Message, "temporary limits")
}
func (e *UpstreamError) ContentLengthExceeded() bool {
	return e.Code == "ContentLengthExceededException"
}

func IsQuotaExhausted(body []byte) bool {
	var v struct {
		Reason string `json:"reason"`
		Error  *struct {
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &v) == nil {
		if _, ok := quotaReasons[v.Reason]; ok {
			return true
		}
		if v.Error != nil {
			_, ok := quotaReasons[v.Error.Reason]
			return ok
		}
		return false
	}
	text := string(body)
	for reason := range quotaReasons {
		if strings.Contains(text, reason) {
			return true
		}
	}
	return false
}
