# 第 1 阶段：崩溃后能恢复

## 目标

Agent 修一个测试没通过的包，跑到一半被 `kill -9`。再执行一次 `agentkeel resume <id>`，它从断的地方接着跑：

- 已经跑完的命令不再跑
- 模型看到的上下文和没崩过一样
- 最后事件日志里每个工具调用都恰好有一条结果

做到这一点，`./scripts/crash-demo.sh` 能跑通，`go test ./...` 全过。

## 你要写的

两个函数，其他的都写好了：

- `internal/agent/replay.go` 里的 `Replay(events) (*State, error)`：从事件算出当前状态
- `internal/agent/run.go` 里的 `(*Agent).Run(ctx, sessionID) error`：读事件、Replay、接着跑

函数上面的注释写了具体要求。验收用 `internal/agent/agent_test.go`，现在这些测试都会失败，写完应该全过：

```sh
go test ./internal/agent/ -run 'TestReplay'   # 先写 Replay
go test ./internal/agent/                     # 再写 Run
go test -race ./...
```

可以用的东西：

- `a.Store.Load` / `a.Store.Append`：读写事件
- `emit(ctx, st, payload)`（`agent.go`）：写一条事件，同时把 `st.NextSeq` 加一，并调用 `OnEvent`
- `a.Model.Complete`：调模型
- `a.tools(st.Config.Workdir)`：拿到这个会话的工具；`Registry.Call` 返回输出和是否出错，工具出错不算 Go 的 error，要原样交给模型

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

第二种情况要你决定怎么处理，见最后一节。

## 要你决定的事

上一个进程停在某条命令执行中间（有 `tool_started`、没有 `tool_finished`）时，恢复后：

- **A. 重新执行**：简单；但 `git push`、发请求、写数据库这类操作会做两遍
- **B. 写一条出错的 `tool_finished`，内容类似"这条命令被中断了，不确定是否执行完"**，让模型自己决定要不要再跑；多一轮模型调用，但不会悄悄重复副作用

两种都能通过测试。可以先看看别人怎么做的再定：

- OpenHands agent-server 重启时，正在跑的会话标成出错，并给模型一条 "A restart occurred while this tool was in progress"，不会自动重跑（`openhands-agent-server/openhands/agent_server/event_service.py`）
- Pi 的 durable 包：`pi/packages/durable/docs/spec.md`
- Temporal 的 Activity 默认会重试，所以要求 Activity 自己做到幂等

## 写完之后

1. 跑 `./scripts/crash-demo.sh`，看事件日志和模型调用次数对不对
2. 接真实模型跑一次 fixtest，中途 Ctrl-C 或 `kill -9`，再 resume
3. 对照阅读：
   - Anthropic《Scaling Managed Agents》里的 `wake(sessionId)` 和 `getEvents()`
   - OpenHands 的 `ConversationState`：`openhands-sdk/openhands/sdk/conversation/state.py`，每个事件一个 JSON 文件
   - Pi durable 的 `docs/spec.md`
4. 可选：两个进程同时 `resume` 同一个会话会怎样？`Store.Append` 在 seq 冲突时返回 `ErrConflict`，靠这个能保证只有一个进程写成功，但另一个进程可能已经执行了工具。要彻底解决需要租约（lease），可以作为这一阶段的加分项。

## 我的选择

（写完 Run 后补上：选了 A 还是 B，为什么。）
