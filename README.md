# AgentKeel

用 Go 从零写一个 Agent 运行时，用来学 Agent Infra：Agent 怎么在进程崩溃后接着跑、怎么放进沙箱、怎么在 K8s 上调度、工具和凭证怎么管、改动怎么评测。

Agent 的主循环本身很短，难的是它外面那一层。这个项目按阶段往外加，每一步先做出来，再去对照现成的项目。

## 阶段

1. **崩溃后能恢复**（已完成，租约还没做）：所有状态都记在一张只追加的事件表里，进程被 `kill -9` 之后，从事件重放出状态接着跑，已经跑完的命令不重跑。见 [docs/stage1.md](docs/stage1.md)。
2. **Docker 沙箱**：每个会话一个容器，限制 CPU、内存、时间，默认不能联网。
3. **K8s 上的沙箱**：用 kubebuilder 写 Sandbox CRD 和 controller，加预热池。
4. **工具网关和凭证**：沙箱里看不到 token，由网关代为调用；高风险操作要审批。
5. **Trace 和评测**：20～30 个"修好失败测试"的任务，改了模型或提示词后跑一遍，变差就拦下。

## 目录

- `cmd/agentkeel`：命令行，`run` / `resume` / `log` / `sessions`
- `cmd/fakemodel`：按固定剧本回复的假模型，演示和测试用
- `internal/event`：事件类型
- `internal/store`：事件存储（SQLite）
- `internal/llm`：模型接口和 OpenAI 兼容实现
- `internal/tools`：`read_file`、`write_file`、`bash`
- `internal/agent`：主循环，`Replay` 和 `Run`
- `testdata/fixtest`：一个测试没通过的小 Go 包，给 Agent 修
- `scripts/crash-demo.sh`：跑到一半 `kill -9`，再恢复

## 运行

需要 Go 1.27。

```sh
go test ./...

export AGENTKEEL_BASE_URL=https://api.deepseek.com/v1   # 任意 OpenAI 兼容接口，不填就用 OpenAI
export AGENTKEEL_API_KEY=...
export AGENTKEEL_MODEL=deepseek-chat
cp -R testdata/fixtest /tmp/work
go run ./cmd/agentkeel run -workdir /tmp/work "让 go test 通过"
```

`bash` 工具直接在本机执行命令，第 2 阶段之前没有任何隔离，只在不要紧的目录里跑。
