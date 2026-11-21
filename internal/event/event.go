// Package event 定义会话事件。会话的全部状态都记在一串只追加的事件里，
// 进程内存里的状态只是从这串事件算出来的结果。
package event

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/zszz3/agentkeel/internal/llm"
)

type Type string

const (
	TypeSessionStarted   Type = "session_started"
	TypeUserMessage      Type = "user_message"
	TypeAssistantMessage Type = "assistant_message"
	TypeToolStarted      Type = "tool_started"
	TypeToolFinished     Type = "tool_finished"
	TypeSessionFinished  Type = "session_finished"
)

// Event 是事件日志里的一行。Seq 在同一个会话内从 1 开始连续递增。
type Event struct {
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	Type      Type            `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// Payload 是各类事件的内容。每种内容自己知道自己是哪类事件。
type Payload interface {
	Type() Type
}

// SessionStarted 是会话的第一条事件。恢复时要用的配置都记在这里，
// 这样换一个进程也能从日志里拿到同样的配置。
type SessionStarted struct {
	Task         string `json:"task"`
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
	Workdir      string `json:"workdir"`
}

// UserMessage 是会话中途用户追加的消息。第一条任务记在 SessionStarted.Task 里。
type UserMessage struct {
	Text string `json:"text"`
}

// AssistantMessage 是模型的一次回复，可能带若干工具调用。
type AssistantMessage struct {
	Text      string         `json:"text,omitempty"`
	ToolCalls []llm.ToolCall `json:"tool_calls,omitempty"`
}

// ToolStarted 在执行工具之前写入。只有 ToolStarted、没有 ToolFinished，
// 说明进程在执行这个工具时停掉了。
type ToolStarted struct {
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	Args   string `json:"args"`
}

// ToolFinished 记录工具的结果，会作为 tool 消息发回给模型。
type ToolFinished struct {
	CallID  string `json:"call_id"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error,omitempty"`
}

// SessionFinished 表示会话结束，之后不应再有事件。Reason 取下面两个值之一。
type SessionFinished struct {
	Reason string `json:"reason"`
}

const (
	ReasonDone     = "done"      // 模型回复里没有工具调用，任务结束
	ReasonMaxTurns = "max_turns" // 调用模型的次数到了上限
)

func (SessionStarted) Type() Type   { return TypeSessionStarted }
func (UserMessage) Type() Type      { return TypeUserMessage }
func (AssistantMessage) Type() Type { return TypeAssistantMessage }
func (ToolStarted) Type() Type      { return TypeToolStarted }
func (ToolFinished) Type() Type     { return TypeToolFinished }
func (SessionFinished) Type() Type  { return TypeSessionFinished }

// New 把内容编码成一条事件。Seq 由调用方给出。
func New(sessionID string, seq int64, p Payload) (Event, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return Event{}, fmt.Errorf("event: encode %s: %w", p.Type(), err)
	}
	return Event{
		SessionID: sessionID,
		Seq:       seq,
		Type:      p.Type(),
		Payload:   raw,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// Decode 把事件内容解码成具体类型，类型对不上时报错。
//
//	started, err := event.Decode[event.SessionStarted](e)
func Decode[T Payload](e Event) (T, error) {
	var p T
	if e.Type != p.Type() {
		return p, fmt.Errorf("event: seq %d is %s, not %s", e.Seq, e.Type, p.Type())
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return p, fmt.Errorf("event: decode seq %d (%s): %w", e.Seq, e.Type, err)
	}
	return p, nil
}
