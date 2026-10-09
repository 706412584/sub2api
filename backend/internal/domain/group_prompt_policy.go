package domain

// GroupPromptPolicy 是分组级请求提示词处理策略。
// 仅 Enabled 为 true 时在网关入口生效。
type GroupPromptPolicy struct {
	Enabled bool                    `json:"enabled"`
	Rules   []GroupPromptPolicyRule `json:"rules"`
}

// GroupPromptPolicyRule 定义一条按顺序执行的提示词处理规则。
type GroupPromptPolicyRule struct {
	Enabled   bool                        `json:"enabled"`
	Endpoints []GroupPromptPolicyEndpoint `json:"endpoints"`
	Targets   []GroupPromptPolicyTarget   `json:"targets"`
	Mode      GroupPromptPolicyMode       `json:"mode"`
	Match     GroupPromptPolicyMatch      `json:"match"`
	Value     string                      `json:"value"`
}

// GroupPromptPolicyEndpoint 是策略适用的入站协议类型。
type GroupPromptPolicyEndpoint string

const (
	GroupPromptPolicyEndpointChatCompletions GroupPromptPolicyEndpoint = "chat_completions"
	GroupPromptPolicyEndpointMessages        GroupPromptPolicyEndpoint = "messages"
	GroupPromptPolicyEndpointResponses       GroupPromptPolicyEndpoint = "responses"
)

// GroupPromptPolicyTarget 是 JSON 请求体中允许修改的文本位置。
type GroupPromptPolicyTarget string

const (
	GroupPromptPolicyTargetSystem       GroupPromptPolicyTarget = "system"
	GroupPromptPolicyTargetInstructions GroupPromptPolicyTarget = "instructions"
	GroupPromptPolicyTargetMessageText  GroupPromptPolicyTarget = "message_text"
)

// GroupPromptPolicyMode 是规则命中后的处理动作。
type GroupPromptPolicyMode string

const (
	GroupPromptPolicyModeReplace GroupPromptPolicyMode = "replace"
	GroupPromptPolicyModeBlock   GroupPromptPolicyMode = "block"
	GroupPromptPolicyModePrepend GroupPromptPolicyMode = "prepend"
	GroupPromptPolicyModeAppend  GroupPromptPolicyMode = "append"
)

// GroupPromptPolicyMatch 定义规则文本的匹配方式。
type GroupPromptPolicyMatch struct {
	Kind          GroupPromptPolicyMatchKind `json:"kind"`
	Value         string                     `json:"value"`
	CaseSensitive bool                       `json:"case_sensitive"`
}

// GroupPromptPolicyMatchKind 支持字面量与 RE2 正则匹配。
type GroupPromptPolicyMatchKind string

const (
	GroupPromptPolicyMatchKindLiteral GroupPromptPolicyMatchKind = "literal"
	GroupPromptPolicyMatchKindRegex   GroupPromptPolicyMatchKind = "regex"
)
