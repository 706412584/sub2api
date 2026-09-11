package kiro

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const CLITarget = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"

type EndpointOptions struct {
	Region        string
	MachineID     string
	KiroVersion   string
	SystemVersion string
	NodeVersion   string
	InvocationID  string
}

// BuildDataPlaneRequest builds an authenticated data-plane request. API keys
// default to CLI, send tokentype=API_KEY, and never inject profileArn.
func BuildDataPlaneRequest(creds Credentials, payload Request, opts EndpointOptions) (*http.Request, error) {
	if err := creds.Validate(); err != nil {
		return nil, err
	}
	token, err := creds.BearerToken()
	if err != nil {
		return nil, err
	}
	region := creds.EffectiveRegion(opts.Region)
	endpoint := creds.EffectiveEndpoint()
	payload.ConversationState.History = cloneHistory(payload.ConversationState.History)

	if endpoint == EndpointCLI {
		transformCLIRequest(&payload)
	} else {
		transformIDERequest(&payload, creds)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("kiro: marshal request: %w", err)
	}

	url := fmt.Sprintf("https://q.%s.amazonaws.com/", region)
	contentType := "application/x-amz-json-1.0"
	if endpoint == EndpointIDE {
		url += "generateAssistantResponse"
		contentType = "application/json"
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/vnd.amazon.eventstream")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Host = req.URL.Host
	req.Header.Set("amz-sdk-invocation-id", invocationID(opts.InvocationID))
	req.Header.Set("amz-sdk-request", "attempt=1; max=3")
	if typ := creds.TokenType(); typ != "" {
		req.Header.Set("tokentype", typ)
	}

	if endpoint == EndpointCLI {
		req.Header.Set("x-amz-target", CLITarget)
		req.Header.Set("x-amzn-codewhisperer-optout", "false")
		req.Header.Set("User-Agent", fmt.Sprintf("aws-sdk-rust/1.3.15 ua/2.1 api/codewhispererstreaming/0.1.14474 os/%s lang/rust/1.92.0 md/appVersion-%s app/AmazonQ-For-CLI", opts.SystemVersion, opts.KiroVersion))
		req.Header.Set("x-amz-user-agent", fmt.Sprintf("aws-sdk-rust/1.3.15 ua/2.1 api/codewhispererstreaming/0.1.14474 os/%s lang/rust/1.92.0 m/F app/AmazonQ-For-CLI", opts.SystemVersion))
	} else {
		req.Header.Set("x-amzn-codewhisperer-optout", "true")
		req.Header.Set("x-amzn-kiro-agent-mode", "vibe")
		req.Header.Set("User-Agent", fmt.Sprintf("aws-sdk-js/1.0.34 ua/2.1 os/%s lang/js md/nodejs#%s api/codewhispererstreaming#1.0.34 m/E KiroIDE-%s-%s", opts.SystemVersion, opts.NodeVersion, opts.KiroVersion, opts.MachineID))
		req.Header.Set("x-amz-user-agent", fmt.Sprintf("aws-sdk-js/1.0.34 KiroIDE-%s-%s", opts.KiroVersion, opts.MachineID))
	}
	return req, nil
}

// BuildAvailableModelsRequest builds the v0.7.1 REST model discovery request.
func BuildAvailableModelsRequest(creds Credentials, opts EndpointOptions) (*http.Request, error) {
	return buildRESTRequest(creds, opts, "/ListAvailableModels?origin=AI_EDITOR")
}

// BuildUsageLimitsRequest builds the v0.7.1 REST quota request. Profile ARN is
// intentionally absent for this legacy endpoint, including OAuth credentials.
func BuildUsageLimitsRequest(creds Credentials, opts EndpointOptions) (*http.Request, error) {
	return buildRESTRequest(creds, opts, "/getUsageLimits?origin=AI_EDITOR&resourceType=AGENTIC_REQUEST&isEmailRequired=true")
}

// RESTRegionCandidates mirrors Kiro's regional fallback order for model/quota
// endpoints, which are available only in us-east-1 and eu-central-1.
func RESTRegionCandidates(ssoRegion string) [2]string {
	if ssoRegion == "eu-central-1" || strings.HasPrefix(ssoRegion, "eu-") {
		return [2]string{"eu-central-1", "us-east-1"}
	}
	return [2]string{"us-east-1", "eu-central-1"}
}

func buildRESTRequest(creds Credentials, opts EndpointOptions, path string) (*http.Request, error) {
	if err := creds.Validate(); err != nil {
		return nil, err
	}
	token, err := creds.BearerToken()
	if err != nil {
		return nil, err
	}
	region := creds.EffectiveRegion(opts.Region)
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://q.%s.amazonaws.com%s", region, path), nil)
	if err != nil {
		return nil, err
	}
	req.Host = req.URL.Host
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Connection", "close")
	req.Header.Set("amz-sdk-invocation-id", invocationID(opts.InvocationID))
	req.Header.Set("amz-sdk-request", "attempt=1; max=1")
	req.Header.Set("User-Agent", fmt.Sprintf("aws-sdk-js/1.0.0 ua/2.1 os/%s lang/js md/nodejs#%s api/codewhispererruntime#1.0.0 m/N,E KiroIDE-%s-%s", opts.SystemVersion, opts.NodeVersion, opts.KiroVersion, opts.MachineID))
	req.Header.Set("x-amz-user-agent", fmt.Sprintf("aws-sdk-js/1.0.0 KiroIDE-%s-%s", opts.KiroVersion, opts.MachineID))
	if typ := creds.TokenType(); typ != "" {
		req.Header.Set("tokentype", typ)
	}
	return req, nil
}

func cloneHistory(history []Message) []Message {
	if len(history) == 0 {
		return nil
	}
	clone := append([]Message(nil), history...)
	for i := range clone {
		if clone[i].UserInputMessage != nil {
			user := *clone[i].UserInputMessage
			clone[i].UserInputMessage = &user
		}
	}
	return clone
}

func invocationID(configured string) string {
	if configured != "" {
		return configured
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

func transformIDERequest(payload *Request, creds Credentials) {
	setOrigin(&payload.ConversationState, OriginIDE)
	payload.ProfileARN = creds.StreamingProfileARN()
}

func transformCLIRequest(payload *Request) {
	payload.ProfileARN = ""
	payload.ConversationState.AgentContinuationID = ""
	setOrigin(&payload.ConversationState, OriginCLI)
	for i := range payload.ConversationState.History {
		if user := payload.ConversationState.History[i].UserInputMessage; user != nil {
			user.ModelID = ""
		}
	}
}

func setOrigin(state *ConversationState, origin string) {
	state.CurrentMessage.UserInputMessage.Origin = origin
	for i := range state.History {
		if user := state.History[i].UserInputMessage; user != nil {
			user.Origin = origin
		}
	}
}
