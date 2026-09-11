package kiro

import "encoding/json"

const (
	OriginIDE = "AI_EDITOR"
	OriginCLI = "KIRO_CLI"
)

type Request struct {
	ConversationState            ConversationState             `json:"conversationState"`
	ProfileARN                   string                        `json:"profileArn,omitempty"`
	AdditionalModelRequestFields *AdditionalModelRequestFields `json:"additionalModelRequestFields,omitempty"`
}

type AdditionalModelRequestFields struct {
	OutputConfig *OutputConfig `json:"output_config,omitempty"`
}

type OutputConfig struct {
	Effort string `json:"effort"`
}

type ConversationState struct {
	AgentContinuationID string         `json:"agentContinuationId,omitempty"`
	AgentTaskType       string         `json:"agentTaskType,omitempty"`
	ChatTriggerType     string         `json:"chatTriggerType,omitempty"`
	CurrentMessage      CurrentMessage `json:"currentMessage"`
	ConversationID      string         `json:"conversationId"`
	History             []Message      `json:"history,omitempty"`
}

type CurrentMessage struct {
	UserInputMessage UserInputMessage `json:"userInputMessage"`
}

type UserInputMessage struct {
	UserInputMessageContext UserInputMessageContext `json:"userInputMessageContext"`
	Content                 string                  `json:"content"`
	ModelID                 string                  `json:"modelId"`
	Images                  []Image                 `json:"images,omitempty"`
	Origin                  string                  `json:"origin,omitempty"`
}

type EnvState struct {
	OperatingSystem         string `json:"operatingSystem"`
	CurrentWorkingDirectory string `json:"currentWorkingDirectory"`
}

type UserInputMessageContext struct {
	EnvState    EnvState     `json:"envState"`
	ToolResults []ToolResult `json:"toolResults,omitempty"`
	Tools       []Tool       `json:"tools,omitempty"`
}

type Image struct {
	Format string      `json:"format"`
	Source ImageSource `json:"source"`
}
type ImageSource struct {
	Bytes string `json:"bytes"`
}

// Message is the wire union used by history. Exactly one field should be set.
type Message struct {
	UserInputMessage         *HistoryUserInputMessage `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *AssistantMessage        `json:"assistantResponseMessage,omitempty"`
}

type HistoryUserInputMessage struct {
	Content                 string                   `json:"content"`
	ModelID                 string                   `json:"modelId,omitempty"`
	Origin                  string                   `json:"origin,omitempty"`
	Images                  []Image                  `json:"images,omitempty"`
	UserInputMessageContext *UserInputMessageContext `json:"userInputMessageContext,omitempty"`
}

type AssistantMessage struct {
	Content  string    `json:"content"`
	ToolUses []ToolUse `json:"toolUses,omitempty"`
}

type Tool struct {
	ToolSpecification ToolSpecification `json:"toolSpecification"`
}
type ToolSpecification struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}
type InputSchema struct {
	JSON json.RawMessage `json:"json"`
}

type ToolResult struct {
	ToolUseID string           `json:"toolUseId"`
	Content   []map[string]any `json:"content"`
	Status    string           `json:"status,omitempty"`
	IsError   bool             `json:"isError,omitempty"`
}

type ToolUse struct {
	ToolUseID string `json:"toolUseId"`
	Name      string `json:"name"`
	Input     any    `json:"input"`
}

func NewRequest(conversationID, modelID, content string) Request {
	return Request{ConversationState: ConversationState{
		AgentTaskType: "vibe", ChatTriggerType: "MANUAL", ConversationID: conversationID,
		CurrentMessage: CurrentMessage{UserInputMessage: UserInputMessage{
			Content: content, ModelID: modelID, Origin: OriginIDE,
			UserInputMessageContext: UserInputMessageContext{EnvState: EnvState{OperatingSystem: "macos", CurrentWorkingDirectory: "/"}},
		}},
	}}
}

func SuccessToolResult(id, content string) ToolResult {
	return ToolResult{ToolUseID: id, Content: []map[string]any{{"text": content}}, Status: "success"}
}
func ErrorToolResult(id, content string) ToolResult {
	return ToolResult{ToolUseID: id, Content: []map[string]any{{"text": content}}, Status: "error", IsError: true}
}
