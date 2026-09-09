package im

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// fakeStream records Append/Finish calls.
type fakeStream struct {
	mu        sync.Mutex
	text      strings.Builder
	finished  bool
	finishErr error
}

func (f *fakeStream) Append(d string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, _ = f.text.WriteString(d)
}

func (f *fakeStream) Finish(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished = true
	f.finishErr = err
}

// sseServer streams a scripted Anthropic SSE sequence.
func sseServer(t *testing.T, events []string, status int, checkReq func(r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if checkReq != nil {
			checkReq(r)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		for _, ev := range events {
			_, _ = fmt.Fprintln(w, ev)
			flusher.Flush()
		}
	}))
}

func anthropicStreamEvents(text string) []string {
	return []string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"usage":{"input_tokens":12}}}`,
		"",
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0}`,
		"",
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"你好"}}`,
		"",
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"，世界"}}`,
		"",
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		"",
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":34}}`,
		"",
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
		"",
	}
}

func TestForwarderStream_HappyPath(t *testing.T) {
	srv := sseServer(t, anthropicStreamEvents("你好，世界"), 200, func(r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		require.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		require.Equal(t, "im:3:abc", r.Header.Get("X-Session-Id"))
	})
	defer srv.Close()

	f := NewForwarder(srv.URL)
	st := &fakeStream{}
	res, err := f.Stream(context.Background(), &ForwardRequest{
		APIKey:        "sk-test",
		Model:         "claude-sonnet-5",
		System:        "you are a bot",
		Messages:      []HistoryMessage{{Role: "user", Content: "hi"}},
		SessionUUID:   "abc",
		SessionLogKey: "im:3:abc",
		DeviceHex64:   strings.Repeat("a", 64),
		MaxTokens:     100,
	}, st)

	require.NoError(t, err)
	require.True(t, st.finished)
	require.Nil(t, st.finishErr)
	require.Equal(t, "你好，世界", st.text.String())
	require.Equal(t, "你好，世界", res.FullText)
	require.Equal(t, "end_turn", res.StopReason)
	require.Equal(t, 12, res.InputTokens)
	require.Equal(t, 34, res.OutputTokens)
}

func TestForwarderStream_MetadataUserIDRoundTrip(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()

	f := NewForwarder(srv.URL)
	st := &fakeStream{}
	const sessionUUID = "12345678-1234-1234-1234-123456789abc"
	_, err := f.Stream(context.Background(), &ForwardRequest{
		APIKey:      "k",
		Messages:    []HistoryMessage{{Role: "user", Content: "x"}},
		SessionUUID: sessionUUID,
		DeviceHex64: strings.Repeat("a", 64),
	}, st)
	require.NoError(t, err)

	meta, _ := body["metadata"].(map[string]any)
	userID, _ := meta["user_id"].(string)
	require.True(t, strings.HasPrefix(userID, "user_"))
	require.Contains(t, userID, "_session_"+sessionUUID)
	parsed := service.ParseMetadataUserID(userID)
	require.NotNil(t, parsed)
	require.Equal(t, strings.Repeat("a", 64), parsed.DeviceID)
	require.Equal(t, sessionUUID, parsed.SessionID)
}

func TestForwarderStream_UpstreamErrorEvent(t *testing.T) {
	events := []string{
		`event: error` + "\n" + `data: {"type":"error","error":{"message":"overloaded"}}`,
		"",
	}
	srv := sseServer(t, events, 200, nil)
	defer srv.Close()

	f := NewForwarder(srv.URL)
	st := &fakeStream{}
	_, err := f.Stream(context.Background(), &ForwardRequest{
		APIKey: "sk", Model: "m", Messages: []HistoryMessage{{Role: "user", Content: "x"}},
	}, st)
	require.Error(t, err)
	require.Contains(t, err.Error(), "overloaded")
	require.True(t, st.finished)
}

func TestForwarderStream_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key"}}`))
	}))
	defer srv.Close()

	f := NewForwarder(srv.URL)
	st := &fakeStream{}
	_, err := f.Stream(context.Background(), &ForwardRequest{APIKey: "bad"}, st)
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
	require.True(t, st.finished)
}

func TestForwarderStream_HalfLineSSE(t *testing.T) {
	// SSE lines split across writes (TCP half-packets) must still parse.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			"event: content_block_del",
			"ta\ndata: {\"type\":\"content_block_del",
			"ta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
		for _, c := range chunks {
			_, _ = w.Write([]byte(c))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	f := NewForwarder(srv.URL)
	st := &fakeStream{}
	res, err := f.Stream(context.Background(), &ForwardRequest{APIKey: "k"}, st)
	require.NoError(t, err)
	require.Equal(t, "ok", res.FullText)
}

func TestForwarderStream_Cancel(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer srv.Close()
	defer close(block)

	f := NewForwarder(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	st := &fakeStream{}
	_, err := f.Stream(ctx, &ForwardRequest{APIKey: "k"}, st)
	require.Error(t, err)
	require.True(t, st.finished)
}

// ---- history ----

func msgs(roles ...string) []service.IMBotMessage {
	out := make([]service.IMBotMessage, 0, len(roles))
	for i, role := range roles {
		out = append(out, service.IMBotMessage{
			ID: int64(i + 1), Role: role, Content: fmt.Sprintf("m%d", i),
		})
	}
	return out
}

func TestBuildHistoryWindow_Alternation(t *testing.T) {
	window := BuildHistoryWindow(msgs("user", "assistant", "user", "assistant", "user"), 40, 24576)
	require.True(t, ValidateHistoryWindow(window))
	require.Len(t, window, 5)
}

func TestBuildHistoryWindow_TrimsLeadingAssistant(t *testing.T) {
	// /clear-ish history where assistant leads
	src := msgs("assistant", "assistant", "user", "assistant")
	window := BuildHistoryWindow(src, 40, 24576)
	require.True(t, ValidateHistoryWindow(window))
	require.Equal(t, service.IMMessageRoleUser, window[0].Role)
}

func TestBuildHistoryWindow_MergesSameRole(t *testing.T) {
	src := msgs("user", "user", "assistant", "assistant", "user")
	window := BuildHistoryWindow(src, 40, 24576)
	require.True(t, ValidateHistoryWindow(window))
	require.Len(t, window, 3)
	require.Equal(t, "m0\n\nm1", window[0].Content)
	require.Equal(t, "m2\n\nm3", window[1].Content)
}

func TestBuildHistoryWindow_MessageCap(t *testing.T) {
	src := make([]service.IMBotMessage, 0, 50)
	for i := 0; i < 50; i++ {
		role := service.IMMessageRoleUser
		if i%2 == 1 {
			role = service.IMMessageRoleAssistant
		}
		src = append(src, service.IMBotMessage{ID: int64(i + 1), Role: role, Content: "x"})
	}
	window := BuildHistoryWindow(src, 10, 1<<20)
	// cap 10 + leading-assistant trimming may drop one more; validate shape
	require.LessOrEqual(t, len(window), 10)
	require.True(t, ValidateHistoryWindow(window))
}

func TestBuildHistoryWindow_ByteCap(t *testing.T) {
	src := []service.IMBotMessage{
		{Role: "user", Content: strings.Repeat("a", 100)},
		{Role: "assistant", Content: strings.Repeat("b", 100)},
		{Role: "user", Content: strings.Repeat("c", 100)},
	}
	window := BuildHistoryWindow(src, 40, 150)
	require.True(t, ValidateHistoryWindow(window))
	var total int
	for _, m := range window {
		total += len(m.Content)
	}
	require.LessOrEqual(t, total, 150)
}
