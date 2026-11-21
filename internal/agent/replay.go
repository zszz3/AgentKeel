package agent

import (
	"errors"

	"github.com/zszz3/agentkeel/internal/event"
)

// Replay 从一个会话的全部事件算出当前状态。
//
// 它只看传进来的事件：不读库、不调模型、不执行工具。
// 同一串事件无论 Replay 多少次，结果都一样。
//
// 要求（docs/stage1.md 里有完整说明，agent_test.go 里的 TestReplay* 会检查）：
//   - 第一条必须是 session_started，seq 从 1 开始连续，否则返回错误
//   - Messages 以 system（Config.SystemPrompt）和 user（Config.Task）开头，
//     之后按事件顺序追加 user / assistant / tool 消息
//   - Pending 是最后一条 assistant 消息里还没有 tool_finished 的调用；
//     有 tool_started 的那个，Started 为 true
//   - Turns 是 assistant_message 的条数；NextSeq 是最后一条的 seq + 1
//
// 提示：Run 每写一条事件，也要让内存里的 State 跟着变。
// 可以把“处理一条事件”单独写成一个函数，Replay 和 Run 都调它，两边就不会算得不一样。
func Replay(events []event.Event) (*State, error) {
	// TODO(stage1): 你来写。
	return nil, errors.New("agent: Replay not implemented")
}
