# 第 1 阶段：崩溃后能恢复

## 目标

Agent 修一个测试没通过的包，跑到一半被 `kill -9`。再执行一次 `agentkeel resume <id>`，它从断的地方接着跑：

- 已经跑完的命令不再跑
- 模型看到的上下文和没崩过一样
- 最后事件日志里每个工具调用都恰好有一条结果

做到这一点，`./scripts/crash-demo.sh` 能跑通，`go test ./...` 全过。

## 代码在哪

- `internal/agent/replay.go`：`Replay(events)` 从事件算出当前状态（消息列表、还没出结果的工具调用、调模型次数、下一个 seq）。核心是 `State.apply`，一次处理一条事件。
- `internal/agent/run.go`：`Run(ctx, sessionID)` 读事件、Replay、接着跑。新会话和崩溃后恢复走同一个函数。
- `internal/agent/agent.go`：`emit` 先把事件写进库，写成功后再调用同一个 `apply` 更新内存里的状态。所以跑着跑着的状态和重放出来的状态一定一样。

验收用 `internal/agent/agent_test.go`：

```sh
go test -race ./...
./scripts/crash-demo.sh
```

## 事件

一个会话就是 `events` 表里同一个 `session_id` 下的一串记录，`seq` 从 1 开始连续递增。只追加，不修改。

| type | 什么时候写 | 内容 |
|---|---|---|
| `session_started` | `Start` 时，seq 1 | 任务、模型名、系统提示词、工作目录 |
| `user_message` | 用户追加消息（这一阶段还用不到） | 文本 |
| `assistant_message` | 模型返回后 | 文本、要调用的工具 |
| `tool_started` | **执行工具之前** | call id、工具名、参数 |
| `tool_finished` | 拿到结果之后 | call id、输出、是否出错 |
| `session_finished` | 结束时 | `done` 或 `max_turns` |

修 fixtest 的一次正常运行大概是：

```text
1  session_started
2  assistant_message   -> bash "go test ./..."
3  tool_started
4  tool_finished       FAIL ...
5  assistant_message   -> write_file median.go
6  tool_started
7  tool_finished
8  assistant_message   -> bash "go test ./..."
9  tool_started
10 tool_finished       ok
11 assistant_message   修好了
12 session_finished    done
```

进程可能停在任意两条之间。`TestResumeAtEveryPoint` 会让写第 1～N 条之后的写入全部失败，模拟停在每个位置，再恢复，检查最后的日志。

## 为什么要先写 tool_started

如果只在拿到结果后写一条记录，进程停在"命令执行了、结果还没写"之间时，恢复后看不出这条命令有没有执行过，只能再执行一次。

先写 `tool_started`，恢复时就分得清三种情况：

- 只有 `assistant_message`，没有 `tool_started`：肯定没执行，直接执行
- 有 `tool_started`，没有 `tool_finished`：可能执行了一部分，也可能执行完了
- 有 `tool_finished`：执行完了，不能再执行

第二种情况怎么处理见下一节。

## 执行到一半的命令怎么办

上一个进程停在某条命令执行中间（有 `tool_started`、没有 `tool_finished`）时，恢复后有两种做法：

- **A. 重新执行**：简单；但 `git push`、发请求、写数据库这类操作会做两遍
- **B. 写一条出错的 `tool_finished`，说明命令被中断、不确定是否执行完**，让模型自己决定要不要再跑；多一轮模型调用，但不会悄悄重复副作用

现在用的是 B，结果内容见 `run.go` 里的 `InterruptedOutput`。理由：

- Agent 调的命令大多不是幂等的，而模型能先 `git status`、`ls` 看一眼现状再决定，比运行时盲目重跑稳妥
- OpenHands agent-server 重启时也是这样：正在跑的会话标成出错，给模型一条 "A restart occurred while this tool was in progress"，不自动重跑（`openhands-agent-server/openhands/agent_server/event_service.py`）
- Temporal 的 Activity 默认会重试，代价是要求每个 Activity 自己做到幂等。Agent 的工具做不到这一点

两种做法都能通过测试，想试 A，把 `runPending` 里 `if pc.Started` 那段删掉就行。

Ctrl-C 也按这个处理：工具被取消时 `Run` 直接返回，不写 `tool_finished`，下次恢复就当它执行到一半。

## 接下来

1. 接真实模型跑一次 fixtest，中途 Ctrl-C 或 `kill -9`，再 resume
2. 对照阅读：
   - Anthropic《Scaling Managed Agents》里的 `wake(sessionId)` 和 `getEvents()`
   - OpenHands 的 `ConversationState`：`openhands-sdk/openhands/sdk/conversation/state.py`，每个事件一个 JSON 文件
   - Pi durable 的 `docs/spec.md`
3. 还没做的：两个进程同时 `resume` 同一个会话。`Store.Append` 在 seq 冲突时返回 `ErrConflict`，能保证只有一个进程写成功，但另一个进程可能已经执行了工具。要彻底解决需要租约（lease）
4. 已知问题：resume 用的是当前环境变量里的模型，不是 `session_started` 里记下的模型
