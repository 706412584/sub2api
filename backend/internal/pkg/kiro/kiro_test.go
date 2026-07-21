package kiro

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/awseventstream"
)

func eventHeader(name, value string) []byte {
	var b bytes.Buffer
	b.WriteByte(byte(len(name)))
	b.WriteString(name)
	b.WriteByte(byte(awseventstream.HeaderString))
	_ = binary.Write(&b, binary.BigEndian, uint16(len(value)))
	b.WriteString(value)
	return b.Bytes()
}

func eventFrame(messageType, eventType, code string, payload []byte) []byte {
	headers := eventHeader(":message-type", messageType)
	if eventType != "" {
		headers = append(headers, eventHeader(":event-type", eventType)...)
	}
	if messageType == "exception" {
		headers = append(headers, eventHeader(":exception-type", code)...)
	}
	if messageType == "error" {
		headers = append(headers, eventHeader(":error-code", code)...)
	}
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

func TestCredentialsAndEndpointRequests(t *testing.T) {
	base := NewRequest("conv", "claude-sonnet-4.5", "hello")
	base.ProfileARN = "must-not-leak"
	base.ConversationState.AgentContinuationID = "continuation"
	base.ConversationState.History = []Message{{UserInputMessage: &HistoryUserInputMessage{Content: "old", ModelID: "old-model", Origin: OriginIDE}}}
	opts := EndpointOptions{Region: "us-west-2", KiroVersion: "0.7.1", SystemVersion: "windows", NodeVersion: "22", MachineID: "machine", InvocationID: "invocation"}

	t.Run("API key defaults CLI and omits profile", func(t *testing.T) {
		creds := Credentials{APIKey: "ksk_test"}
		if creds.ShouldRefresh() {
			t.Fatal("API key must not refresh")
		}
		req, err := BuildDataPlaneRequest(creds, base, opts)
		if err != nil {
			t.Fatal(err)
		}
		if req.URL.String() != "https://q.us-west-2.amazonaws.com/" {
			t.Fatalf("URL = %s", req.URL)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer ksk_test" {
			t.Fatalf("authorization = %q", got)
		}
		if got := req.Header.Get("tokentype"); got != "API_KEY" {
			t.Fatalf("tokentype = %q", got)
		}
		if got := req.Header.Get("x-amz-target"); got != CLITarget {
			t.Fatalf("target = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["profileArn"]; ok {
			t.Fatal("API key request injected profileArn")
		}
		state := body["conversationState"].(map[string]any)
		if _, ok := state["agentContinuationId"]; ok {
			t.Fatal("CLI retained agentContinuationId")
		}
		current := state["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
		if current["origin"] != OriginCLI {
			t.Fatalf("origin = %v", current["origin"])
		}
		historyUser := state["history"].([]any)[0].(map[string]any)["userInputMessage"].(map[string]any)
		if _, ok := historyUser["modelId"]; ok {
			t.Fatal("CLI history retained modelId")
		}
		if base.ConversationState.History[0].UserInputMessage.ModelID != "old-model" || base.ProfileARN != "must-not-leak" {
			t.Fatal("request builder mutated caller-owned payload")
		}
	})

	t.Run("OAuth IDE endpoint", func(t *testing.T) {
		creds := Credentials{AccessToken: "oauth", RefreshToken: "refresh", ProfileARN: "arn:real", Endpoint: EndpointIDE}
		if !creds.ShouldRefresh() {
			t.Fatal("OAuth refresh capability lost")
		}
		req, err := BuildDataPlaneRequest(creds, base, opts)
		if err != nil {
			t.Fatal(err)
		}
		if req.URL.String() != "https://q.us-west-2.amazonaws.com/generateAssistantResponse" {
			t.Fatalf("URL = %s", req.URL)
		}
		if req.Header.Get("tokentype") != "" || req.Header.Get("x-amz-target") != "" {
			t.Fatal("IDE got CLI headers")
		}
		if req.Header.Get("x-amzn-kiro-agent-mode") != "vibe" {
			t.Fatal("IDE agent mode missing")
		}
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		if body["profileArn"] != "arn:real" {
			t.Fatalf("profileArn = %v", body["profileArn"])
		}
		current := body["conversationState"].(map[string]any)["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
		if current["origin"] != OriginIDE {
			t.Fatalf("origin = %v", current["origin"])
		}
	})

	if _, err := BuildDataPlaneRequest(Credentials{APIKey: "bad"}, base, opts); err == nil {
		t.Fatal("invalid API key accepted")
	}

	restCreds := Credentials{APIKey: "ksk_rest", ProfileARN: "must-not-send"}
	models, err := BuildAvailableModelsRequest(restCreds, opts)
	if err != nil {
		t.Fatal(err)
	}
	if models.URL.String() != "https://q.us-west-2.amazonaws.com/ListAvailableModels?origin=AI_EDITOR" || models.Header.Get("tokentype") != "API_KEY" {
		t.Fatalf("models request = %s %#v", models.URL, models.Header)
	}
	quota, err := BuildUsageLimitsRequest(restCreds, opts)
	if err != nil {
		t.Fatal(err)
	}
	if quota.URL.Query().Get("resourceType") != "AGENTIC_REQUEST" || quota.URL.Query().Get("isEmailRequired") != "true" {
		t.Fatalf("quota URL = %s", quota.URL)
	}
	if got := RESTRegionCandidates("eu-west-1"); got != [2]string{"eu-central-1", "us-east-1"} {
		t.Fatalf("EU candidates = %v", got)
	}
}

func TestEventDecoderAndSharedResponseState(t *testing.T) {
	frames := [][]byte{
		eventFrame("event", string(EventAssistantResponse), "", []byte(`{"content":"Hello "}`)),
		eventFrame("event", string(EventReasoningContent), "", []byte(`{"text":"think","signature":"sig"}`)),
		eventFrame("event", string(EventToolUse), "", []byte(`{"name":"shell","toolUseId":"t1","input":"{\"com"}`)),
		eventFrame("event", string(EventToolUse), "", []byte(`{"name":"shell","toolUseId":"t1","input":"mand\":\"ls\"}","stop":true}`)),
		eventFrame("event", string(EventAssistantResponse), "", []byte(`{"content":"world"}`)),
		eventFrame("event", string(EventContextUsage), "", []byte(`{"contextUsagePercentage":25}`)),
		eventFrame("event", string(EventMetering), "", []byte(`{"unit":"credit","usage":0.25}`)),
		eventFrame("event", string(EventMetering), "", []byte(`{"usage":0.5}`)),
	}
	stream := bytes.Join(frames, nil)

	// Streaming use: callers consume each event and the completed tool signal.
	decoder := NewEventDecoder(&oneByteReader{data: stream})
	state := NewResponseState(200000)
	completedCount := 0
	for {
		event, err := decoder.ReadEvent()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		tool, err := state.Apply(event)
		if err != nil {
			t.Fatal(err)
		}
		if tool != nil {
			completedCount++
		}
	}
	if err := state.Finish(); err != nil {
		t.Fatal(err)
	}
	got := state.Snapshot()
	if got.Content != "Hello world" || got.Thinking != "think" || got.ThinkingSignature != "sig" {
		t.Fatalf("response = %#v", got)
	}
	if completedCount != 1 || len(got.ToolUses) != 1 || got.StopReason != "tool_use" {
		t.Fatalf("tools = %#v, stop=%s", got.ToolUses, got.StopReason)
	}
	input := got.ToolUses[0].Input.(map[string]any)
	if input["command"] != "ls" {
		t.Fatalf("tool input = %#v", input)
	}
	if got.Usage.InputTokens != 50000 || got.Usage.Credits != 0.75 {
		t.Fatalf("usage = %#v", got.Usage)
	}

	// Non-streaming use is the same state machine.
	collected, err := CollectResponse(bytes.NewReader(stream), 200000)
	if err != nil {
		t.Fatal(err)
	}
	if collected.Content != got.Content || len(collected.ToolUses) != 1 || collected.Usage != got.Usage {
		t.Fatalf("collected = %#v", collected)
	}
}

type oneByteReader struct{ data []byte }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

func TestEventErrorsAndIncompleteTools(t *testing.T) {
	t.Run("exception", func(t *testing.T) {
		decoder := NewEventDecoder(bytes.NewReader(eventFrame("exception", "", "ContentLengthExceededException", []byte(`{"message":"too long"}`))))
		event, err := decoder.ReadEvent()
		if err != nil {
			t.Fatal(err)
		}
		state := NewResponseState(0)
		if _, err := state.Apply(event); err != nil {
			t.Fatal(err)
		}
		if state.Snapshot().StopReason != "max_tokens" {
			t.Fatalf("state = %#v", state.Snapshot())
		}
	})

	t.Run("error", func(t *testing.T) {
		decoder := NewEventDecoder(bytes.NewReader(eventFrame("error", "", "BadRequest", []byte(`{"message":"bad","reason":"TOOL_SCHEMA_INVALID"}`))))
		event, err := decoder.ReadEvent()
		if err != nil {
			t.Fatal(err)
		}
		state := NewResponseState(0)
		_, err = state.Apply(event)
		var upstream *UpstreamError
		if !errors.As(err, &upstream) || !upstream.ClientValidation() {
			t.Fatalf("error = %#v", err)
		}
	})

	t.Run("embedded error", func(t *testing.T) {
		frame := eventFrame("event", string(EventAssistantResponse), "", []byte(`{"error":{"code":"InternalServerException","message":"broken"}}`))
		_, err := CollectResponse(bytes.NewReader(frame), 0)
		var upstream *UpstreamError
		if !errors.As(err, &upstream) || upstream.Code != "InternalServerException" {
			t.Fatalf("error = %#v", err)
		}
	})

	t.Run("incomplete tool", func(t *testing.T) {
		frame := eventFrame("event", string(EventToolUse), "", []byte(`{"name":"shell","toolUseId":"t1","input":"{\"x\":"}`))
		_, err := CollectResponse(bytes.NewReader(frame), 0)
		if err == nil || !strings.Contains(err.Error(), "before completing") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("invalid completed tool JSON", func(t *testing.T) {
		frame := eventFrame("event", string(EventToolUse), "", []byte(`{"name":"shell","toolUseId":"t1","input":"{","stop":true}`))
		_, err := CollectResponse(bytes.NewReader(frame), 0)
		if err == nil || !strings.Contains(err.Error(), "invalid toolUseEvent input") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestModelsQuotaAndErrorClassification(t *testing.T) {
	var models ListAvailableModelsResponse
	if err := json.Unmarshal([]byte(`{"models":[{"modelId":"claude-sonnet-4.5","tokenLimits":{"maxInputTokens":200000}}]}`), &models); err != nil {
		t.Fatal(err)
	}
	if len(models.Models) != 1 || *models.Models[0].TokenLimits.MaxInputTokens != 200000 {
		t.Fatalf("models = %#v", models)
	}

	var quota UsageLimitsResponse
	payload := `{"subscriptionInfo":{"subscriptionTitle":"KIRO PRO+","overageCapability":"OVERAGE_CAPABLE"},"userInfo":{"email":"a@example.com"},"usageBreakdownList":[{"currentUsageWithPrecision":2,"usageLimitWithPrecision":10,"freeTrialInfo":{"freeTrialStatus":"ACTIVE","currentUsageWithPrecision":1,"usageLimitWithPrecision":5},"bonuses":[{"status":"ACTIVE","currentUsage":0.5,"usageLimit":2}]}]}`
	if err := json.Unmarshal([]byte(payload), &quota); err != nil {
		t.Fatal(err)
	}
	if quota.SubscriptionTitle() != "KIRO PRO+" || quota.Email() != "a@example.com" || quota.UsageLimit() != 17 || quota.CurrentUsage() != 3.5 {
		t.Fatalf("quota = %#v", quota)
	}
	if capable, known := quota.OverageCapable(); !known || !capable {
		t.Fatalf("overage capable = %v,%v", capable, known)
	}
	if !IsQuotaExhausted([]byte(`{"error":{"reason":"OVERAGE_REQUEST_LIMIT_EXCEEDED"}}`)) {
		t.Fatal("quota reason not detected")
	}
	if IsQuotaExhausted([]byte(`{"message":"MONTHLY_REQUEST_COUNT","reason":"OTHER"}`)) {
		t.Fatal("incidental quota text matched structured body")
	}
}
