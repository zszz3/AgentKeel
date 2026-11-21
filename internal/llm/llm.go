// Package llm 定义 agent 和模型之间的最小接口。
// 第一步只接 OpenAI Chat Completions 这一种接口，见 openai.go。
package llm

import "context"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall 是模型发起的一次工具调用。Args 是模型给出的原始 JSON 字符串。
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

// Message 是发给模型或模型返回的一条消息。
//   - assistant 消息可以同时有 Text 和 ToolCalls
//   - tool 消息用 ToolCallID 说明回复的是哪次调用
type Message struct {
	Role       Role       `json:"role"`
	Text       string     `json:"text,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolSpec 是告诉模型“有这个工具”的描述。Parameters 是 JSON Schema。
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Model 调一次模型，返回一条 assistant 消息。
type Model interface {
	Complete(ctx context.Context, msgs []Message, tools []ToolSpec) (Message, error)
}
