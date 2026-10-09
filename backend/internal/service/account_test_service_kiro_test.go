//go:build unit

package service

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/awseventstream"
	"github.com/stretchr/testify/require"
)

func kiroTestEventHeader(name, value string) []byte {
	var body bytes.Buffer
	body.WriteByte(byte(len(name)))
	body.WriteString(name)
	body.WriteByte(byte(awseventstream.HeaderString))
	_ = binary.Write(&body, binary.BigEndian, uint16(len(value)))
	body.WriteString(value)
	return body.Bytes()
}

func kiroTestEventFrame(eventType string, payload []byte) []byte {
	headers := append(kiroTestEventHeader(":message-type", "event"), kiroTestEventHeader(":event-type", eventType)...)
	total := awseventstream.PreludeSize + len(headers) + len(payload) + 4
	frame := make([]byte, total)
	binary.BigEndian.PutUint32(frame[:4], uint32(total))
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(headers)))
	binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(frame[:8]))
	copy(frame[12:], headers)
	copy(frame[12+len(headers):], payload)
	binary.BigEndian.PutUint32(frame[total-4:], crc32.ChecksumIEEE(frame[:total-4]))
	return frame
}

func TestAccountTestServiceKiroAPIKeyUsesKiroCLI(t *testing.T) {
	account := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"kiro_api_key": "ksk_test-value",
			"auth_method":  "api_key",
			"endpoint":     "cli",
			"api_region":   "us-west-2",
		},
	}
	stream := kiroTestEventFrame("assistantResponseEvent", []byte(`{"content":"ok"}`))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(stream)),
	}}}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}
	svc := NewAccountTestService(repo, nil, nil, nil, nil, upstream, nil, nil)
	ctx, recorder := newTestContext()

	err := svc.TestAccountConnection(ctx, account.ID, "claude-sonnet-4.6", "", AccountTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	request := upstream.requests[0]
	require.Equal(t, "q.us-west-2.amazonaws.com", request.URL.Host)
	require.Equal(t, "/", request.URL.Path)
	require.Equal(t, "Bearer ksk_test-value", request.Header.Get("Authorization"))
	require.Equal(t, "API_KEY", request.Header.Get("tokentype"))
	require.NotContains(t, recorder.Body.String(), "ksk_test-value")
	require.Contains(t, recorder.Body.String(), `"text":"ok"`)
	require.Contains(t, recorder.Body.String(), `"success":true`)
}
