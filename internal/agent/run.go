package agent

import (
	"context"
	"errors"
)

// Run 把一个会话跑到结束，或者跑到 ctx 被取消、出错为止。
//
// 新会话和被中断过的会话走同一条路：从库里读出全部事件，Replay 得到 State，然后接着跑。
// 所以 kill -9 之后再调一次 Run，就是恢复。
//
// 每一轮大致是：
//  1. 会话已经结束：直接返回 nil，不调模型
//  2. 有 Pending 的工具调用：按顺序处理完
//  3. 最后一条消息是不带工具调用的 assistant 消息（上次写完它、还没写 session_finished 就停了）：
//     补写 session_finished（ReasonDone），返回，不要再调模型
//  4. 调模型的次数到了 MaxTurns：写 session_finished（ReasonMaxTurns），返回
//  5. 调模型，写 assistant_message
//  6. 模型没有要调工具：写 session_finished（ReasonDone），返回；否则回到第 2 步
//
// 要求（agent_test.go 里的 TestRun* / TestResume* 会检查）：
//   - 先写事件再做事：执行工具前写 tool_started，拿到结果后写 tool_finished
//   - 已经有 tool_finished 的调用，恢复时不能再执行
//   - 每个工具调用最后都恰好有一条 tool_finished
//   - 会话不存在时返回错误
//   - 写事件失败时立刻返回，错误原样返回或用 %w 包一层（测试用 errors.Is 判断）
//
// 要你自己决定的一点：Pending 里 Started 为 true 的调用（上个进程在执行它时停掉了），
// 是重新执行，还是写一条出错的 tool_finished 告诉模型“这条命令被打断了”？
// 两种都能通过测试。把你的选择和理由写在 docs/stage1.md 末尾。
func (a *Agent) Run(ctx context.Context, sessionID string) error {
	// TODO(stage1): 你来写。
	return errors.New("agent: Run not implemented")
}
