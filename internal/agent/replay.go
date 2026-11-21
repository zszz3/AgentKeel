package agent

import (
	"errors"
	"fmt"

	"github.com/zszz3/agentkeel/internal/event"
	"github.com/zszz3/agentkeel/internal/llm"
)

// Replay 从一个会话的全部事件算出当前状态。
//
// 它只看传进来的事件：不读库、不调模型、不执行工具。
// 同一串事件无论 Replay 多少次，结果都一样。
//
// 第一条必须是 session_started，seq 从 1 开始连续，否则返回错误。
func Replay(events []event.Event) (*State, error) {
	if len(events) == 0 {
		return nil, errors.New("agent: replay: empty log")
	}
	st := &State{SessionID: events[0].SessionID, NextSeq: 1}
	for _, e := range events {
		if err := st.apply(e); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// apply 用一条事件更新状态。
//
// Replay 用它把历史事件一条条叠起来；Run 每写成功一条新事件，emit 也调用它。
// 两边走同一个函数，所以“跑着跑着的内存状态”和“崩溃后重放出来的状态”不会不一样。
func (st *State) apply(e event.Event) error {
	if e.SessionID != st.SessionID {
		return fmt.Errorf("agent: event for session %s in session %s", e.SessionID, st.SessionID)
	}
	if e.Seq != st.NextSeq {
		return fmt.Errorf("agent: seq %d, want %d", e.Seq, st.NextSeq)
	}
	if (e.Seq == 1) != (e.Type == event.TypeSessionStarted) {
		return fmt.Errorf("agent: seq %d has type %s; session_started must be first and only first", e.Seq, e.Type)
	}
	if st.Finished {
		return fmt.Errorf("agent: seq %d (%s) after session_finished", e.Seq, e.Type)
	}
	// 工具调用和它的结果之间不能插进新消息，模型 API 也不接受这样的消息列表。
	if len(st.Pending) > 0 && (e.Type == event.TypeUserMessage || e.Type == event.TypeAssistantMessage) {
		return fmt.Errorf("agent: seq %d (%s) while %d tool call(s) have no result", e.Seq, e.Type, len(st.Pending))
	}

	switch e.Type {
	case event.TypeSessionStarted:
		p, err := event.Decode[event.SessionStarted](e)
		if err != nil {
			return err
		}
		st.Config = p
		st.Messages = []llm.Message{
			{Role: llm.RoleSystem, Text: p.SystemPrompt},
			{Role: llm.RoleUser, Text: p.Task},
		}

	case event.TypeUserMessage:
		p, err := event.Decode[event.UserMessage](e)
		if err != nil {
			return err
		}
		st.Messages = append(st.Messages, llm.Message{Role: llm.RoleUser, Text: p.Text})

	case event.TypeAssistantMessage:
		p, err := event.Decode[event.AssistantMessage](e)
		if err != nil {
			return err
		}
		st.Messages = append(st.Messages, llm.Message{Role: llm.RoleAssistant, Text: p.Text, ToolCalls: p.ToolCalls})
		// 这条消息里的每个工具调用都还没有结果。
		for _, c := range p.ToolCalls {
			st.Pending = append(st.Pending, PendingCall{Call: c})
		}
		st.Turns++

	case event.TypeToolStarted:
		p, err := event.Decode[event.ToolStarted](e)
		if err != nil {
			return err
		}
		i := st.pendingIndex(p.CallID)
		if i < 0 {
			return fmt.Errorf("agent: seq %d: tool_started for unknown call %s", e.Seq, p.CallID)
		}
		st.Pending[i].Started = true

	case event.TypeToolFinished:
		p, err := event.Decode[event.ToolFinished](e)
		if err != nil {
			return err
		}
		i := st.pendingIndex(p.CallID)
		if i < 0 {
			return fmt.Errorf("agent: seq %d: tool_finished for unknown or finished call %s", e.Seq, p.CallID)
		}
		st.Messages = append(st.Messages, llm.Message{Role: llm.RoleTool, Text: p.Output, ToolCallID: p.CallID})
		st.Pending = append(st.Pending[:i], st.Pending[i+1:]...)

	case event.TypeSessionFinished:
		st.Finished = true

	default:
		return fmt.Errorf("agent: seq %d: unknown event type %q", e.Seq, e.Type)
	}

	st.NextSeq++
	return nil
}

func (st *State) pendingIndex(callID string) int {
	for i, pc := range st.Pending {
		if pc.Call.ID == callID {
			return i
		}
	}
	return -1
}
