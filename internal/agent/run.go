package agent

import (
	"context"
	"fmt"

	"github.com/zszz3/agentkeel/internal/event"
	"github.com/zszz3/agentkeel/internal/llm"
)

// InterruptedOutput 是恢复时补给“执行到一半就停了”的工具调用的结果。
// 不自动重跑，是因为不知道它已经做了多少；让模型先看一眼现状再决定。
const InterruptedOutput = "interrupted: the agent process stopped while this tool was running, " +
	"so it may have partly or fully completed. Check the current state before running it again."

// Run 把一个会话跑到结束，或者跑到 ctx 被取消、出错为止。
//
// 新会话和被中断过的会话走同一条路：从库里读出全部事件，Replay 得到 State，然后接着跑。
// 所以 kill -9 之后再调一次 Run，就是恢复。
//
// 每一轮：
//  1. 会话已经结束：直接返回 nil，不调模型
//  2. 有 Pending 的工具调用：按顺序处理完
//  3. 最后一条消息是不带工具调用的 assistant 消息（上次写完它、还没写 session_finished 就停了）：
//     补写 session_finished，不再调模型
//  4. 调模型的次数到了 MaxTurns：写 session_finished（max_turns）
//  5. 调模型，写 assistant_message
//  6. 模型没有要调工具：写 session_finished（done）；否则回到第 2 步
//
// 规则：先写事件再做事。执行工具前写 tool_started，拿到结果后写 tool_finished；
// 任何一次写失败都立刻返回，日志里没记下的事就当没有发生过。
func (a *Agent) Run(ctx context.Context, sessionID string) error {
	events, err := a.Store.Load(ctx, sessionID)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return fmt.Errorf("agent: no session %s", sessionID)
	}
	st, err := Replay(events)
	if err != nil {
		return err
	}
	reg := a.tools(st.Config.Workdir)

	for !st.Finished {
		for len(st.Pending) > 0 {
			if err := a.runPending(ctx, st, reg); err != nil {
				return err
			}
		}

		if last := st.Messages[len(st.Messages)-1]; last.Role == llm.RoleAssistant && len(last.ToolCalls) == 0 {
			return a.emit(ctx, st, event.SessionFinished{Reason: event.ReasonDone})
		}
		if st.Turns >= a.maxTurns() {
			return a.emit(ctx, st, event.SessionFinished{Reason: event.ReasonMaxTurns})
		}

		msg, err := a.Model.Complete(ctx, st.Messages, reg.Specs())
		if err != nil {
			return fmt.Errorf("agent: model: %w", err)
		}
		if err := a.emit(ctx, st, event.AssistantMessage{Text: msg.Text, ToolCalls: msg.ToolCalls}); err != nil {
			return err
		}
		// 没有工具调用时，下一轮会走到第 3 步写 session_finished。
	}
	return nil
}

// toolCaller 是 Run 对工具的唯一需求，方便以后换成沙箱里的实现。
type toolCaller interface {
	Call(ctx context.Context, name string, args string) (output string, isError bool)
}

// runPending 处理 Pending 里的第一个调用，结束后它会从 Pending 里移除。
func (a *Agent) runPending(ctx context.Context, st *State, reg toolCaller) error {
	pc := st.Pending[0]

	// 上一个进程在执行它的时候停了：不重跑，告诉模型发生了什么。
	if pc.Started {
		return a.emit(ctx, st, event.ToolFinished{CallID: pc.Call.ID, Output: InterruptedOutput, IsError: true})
	}

	if err := a.emit(ctx, st, event.ToolStarted{CallID: pc.Call.ID, Name: pc.Call.Name, Args: pc.Call.Args}); err != nil {
		return err
	}
	out, isErr := reg.Call(ctx, pc.Call.Name, pc.Call.Args)
	// 被 Ctrl-C 或上层取消时，工具多半是被杀掉的，结果不完整。
	// 不写 tool_finished，下次恢复会把它当成“执行到一半”处理。
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.emit(ctx, st, event.ToolFinished{CallID: pc.Call.ID, Output: out, IsError: isErr})
}
