// Package agent 是 agent 循环本身：调模型、执行工具、把每一步写进事件日志，
// 进程被杀掉后能从日志接着跑。
//
// replay.go：从事件算出状态；run.go：主循环和恢复。设计说明在 docs/stage1.md。
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/zszz3/agentkeel/internal/event"
	"github.com/zszz3/agentkeel/internal/llm"
	"github.com/zszz3/agentkeel/internal/store"
	"github.com/zszz3/agentkeel/internal/tools"
)

const DefaultSystemPrompt = `You are a coding agent working in a local directory.
Use the tools to inspect files, edit them and run commands.
When the task is done, reply with a short summary and do not call any tool.`

type Agent struct {
	Store store.Store
	Model llm.Model

	// Tools 按会话的工作目录生成工具。为空时用 tools.Default。
	Tools func(workdir string) *tools.Registry

	// MaxTurns 是一个会话最多调用模型的次数，0 表示 30。
	MaxTurns int

	// OnEvent 在每条事件写入成功后调用，命令行用它实时打印进度。可以为空。
	OnEvent func(event.Event)
}

// State 是从事件日志算出来的会话状态。Run 每次启动都从日志重新算一遍，
// 不依赖上一个进程留下的任何内存。
type State struct {
	SessionID string
	Config    event.SessionStarted

	// Messages 是下一次调模型时要发的完整消息列表，第一条是 system，第二条是任务。
	Messages []llm.Message

	// Pending 是最后一条 assistant 消息里还没有结果的工具调用，按模型给出的顺序排列。
	Pending []PendingCall

	// Turns 是已经调用过模型的次数，也就是日志里 assistant_message 的条数。
	Turns int

	Finished bool

	// NextSeq 是下一条事件的 seq。
	NextSeq int64
}

type PendingCall struct {
	Call llm.ToolCall

	// Started 为 true 表示日志里已经有这次调用的 tool_started：
	// 上一个进程很可能是在执行这个工具的过程中停掉的。
	Started bool
}

// Start 新建一个会话，只写入第一条 session_started 事件，返回会话 ID。
// 之后调用 Run 开始执行。
func (a *Agent) Start(ctx context.Context, cfg event.SessionStarted) (string, error) {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	id := newSessionID()
	e, err := event.New(id, 1, cfg)
	if err != nil {
		return "", err
	}
	if err := a.Store.Append(ctx, e); err != nil {
		return "", fmt.Errorf("agent: start session: %w", err)
	}
	if a.OnEvent != nil {
		a.OnEvent(e)
	}
	return id, nil
}

// emit 先把一条事件写进日志，写成功后再用同一条事件更新 st（和 Replay 走同一个 apply），
// 最后调用 OnEvent。
//
// 写失败时 st 不变，调用方应当立刻返回：日志里没记下的事，就当没有发生过。
func (a *Agent) emit(ctx context.Context, st *State, p event.Payload) error {
	e, err := event.New(st.SessionID, st.NextSeq, p)
	if err != nil {
		return err
	}
	if err := a.Store.Append(ctx, e); err != nil {
		return fmt.Errorf("agent: append %s (seq %d): %w", p.Type(), st.NextSeq, err)
	}
	if err := st.apply(e); err != nil {
		return err
	}
	if a.OnEvent != nil {
		a.OnEvent(e)
	}
	return nil
}

func (a *Agent) tools(workdir string) *tools.Registry {
	if a.Tools != nil {
		return a.Tools(workdir)
	}
	return tools.Default(workdir)
}

func (a *Agent) maxTurns() int {
	if a.MaxTurns > 0 {
		return a.MaxTurns
	}
	return 30
}

func newSessionID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}
