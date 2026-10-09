package im

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// HistoryMessage is one turn in the rebuilt context window.
type HistoryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// BuildHistoryWindow turns the newest-first chronologically-ordered stored
// messages into a valid Anthropic messages array:
//   - keeps at most maxMessages turns and maxBytes total content bytes;
//   - merges adjacent same-role turns ("\n\n" join) — Anthropic requires
//     alternating roles;
//   - trims leading assistant turns so the window starts with a user turn.
//
// Input must be in chronological order (oldest first); output is too.
func BuildHistoryWindow(msgs []service.IMBotMessage, maxMessages, maxBytes int) []HistoryMessage {
	if maxMessages <= 0 {
		maxMessages = 40
	}
	if maxBytes <= 0 {
		maxBytes = 24576
	}

	// Walk from the newest message backwards, collecting until limits hit.
	start := len(msgs)
	total := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		turns := len(msgs) - i
		if turns > maxMessages {
			break
		}
		if total+len(m.Content) > maxBytes && total > 0 {
			break
		}
		total += len(m.Content)
		start = i
	}
	window := msgs[start:]

	// Trim leading assistant turns: window must open with a user turn.
	for len(window) > 0 && window[0].Role == service.IMMessageRoleAssistant {
		window = window[1:]
	}

	if len(window) == 0 {
		return nil
	}

	// Merge adjacent same-role turns.
	out := make([]HistoryMessage, 0, len(window))
	for _, m := range window {
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			out[n-1].Content = out[n-1].Content + "\n\n" + m.Content
			continue
		}
		out = append(out, HistoryMessage{Role: m.Role, Content: m.Content})
	}
	return out
}

// ValidateHistoryWindow asserts the final window is Anthropic-safe:
// non-empty, starts with user, strictly alternating roles. Test helper.
func ValidateHistoryWindow(window []HistoryMessage) bool {
	if len(window) == 0 {
		return false
	}
	if window[0].Role != service.IMMessageRoleUser {
		return false
	}
	for i := 1; i < len(window); i++ {
		if window[i].Role == window[i-1].Role {
			return false
		}
	}
	for _, m := range window {
		if strings.TrimSpace(m.Content) == "" {
			return false
		}
	}
	return true
}
