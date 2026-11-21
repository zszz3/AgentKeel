// agentkeel 是第一步的命令行：开一个会话、恢复会话、查看事件日志。
//
//	agentkeel run -workdir ./work "让 go test 通过"
//	agentkeel resume <session-id>
//	agentkeel log <session-id>
//	agentkeel sessions
//
// 模型配置从环境变量读取：
//
//	AGENTKEEL_BASE_URL  OpenAI 兼容接口地址，例如 https://api.deepseek.com/v1，不填就用 OpenAI 官方地址
//	AGENTKEEL_API_KEY   API key
//	AGENTKEEL_MODEL     模型名，例如 deepseek-chat
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/zszz3/agentkeel/internal/agent"
	"github.com/zszz3/agentkeel/internal/event"
	"github.com/zszz3/agentkeel/internal/llm"
	"github.com/zszz3/agentkeel/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "agentkeel:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  agentkeel [-db FILE] run [-workdir DIR] [-max-turns N] TASK
  agentkeel [-db FILE] resume SESSION_ID
  agentkeel [-db FILE] log SESSION_ID
  agentkeel [-db FILE] sessions
`)
}

func run(args []string) error {
	global := flag.NewFlagSet("agentkeel", flag.ContinueOnError)
	global.Usage = usage
	dbPath := global.String("db", "agentkeel.db", "SQLite file for the event log")
	if err := global.Parse(args); err != nil {
		return err
	}
	if global.NArg() == 0 {
		usage()
		return errors.New("missing command")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.OpenSQLite(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	cmd, rest := global.Arg(0), global.Args()[1:]
	switch cmd {
	case "run":
		return cmdRun(ctx, st, rest)
	case "resume":
		if len(rest) != 1 {
			return errors.New("usage: agentkeel resume SESSION_ID")
		}
		a, err := newAgent(st, 0)
		if err != nil {
			return err
		}
		return a.Run(ctx, rest[0])
	case "log":
		if len(rest) != 1 {
			return errors.New("usage: agentkeel log SESSION_ID")
		}
		events, err := st.Load(ctx, rest[0])
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return fmt.Errorf("no session %s", rest[0])
		}
		for _, e := range events {
			printEvent(e)
		}
		return nil
	case "sessions":
		ids, err := st.Sessions(ctx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			fmt.Println(id)
		}
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func cmdRun(ctx context.Context, st store.Store, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	workdir := fs.String("workdir", ".", "directory the agent works in")
	maxTurns := fs.Int("max-turns", 0, "max model calls (0 = default 30)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	task := strings.Join(fs.Args(), " ")
	if task == "" {
		return errors.New("usage: agentkeel run [-workdir DIR] TASK")
	}
	dir, err := filepath.Abs(*workdir)
	if err != nil {
		return err
	}

	a, err := newAgent(st, *maxTurns)
	if err != nil {
		return err
	}
	id, err := a.Start(ctx, event.SessionStarted{
		Task:    task,
		Model:   os.Getenv("AGENTKEEL_MODEL"),
		Workdir: dir,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "session %s (resume with: agentkeel resume %s)\n", id, id)
	return a.Run(ctx, id)
}

// newAgent 用环境变量里的配置连模型。
//
// 注意：恢复时用的是这次进程的环境变量，不是 session_started 里记下的 Model。
// 两次用不同的模型，日志里看不出来。要不要管这件事，你可以自己决定。
func newAgent(st store.Store, maxTurns int) (*agent.Agent, error) {
	key, model := os.Getenv("AGENTKEEL_API_KEY"), os.Getenv("AGENTKEEL_MODEL")
	if key == "" || model == "" {
		return nil, errors.New("set AGENTKEEL_API_KEY and AGENTKEEL_MODEL (and AGENTKEEL_BASE_URL for non-OpenAI providers)")
	}
	return &agent.Agent{
		Store:    st,
		Model:    llm.NewOpenAI(os.Getenv("AGENTKEEL_BASE_URL"), key, model),
		MaxTurns: maxTurns,
		OnEvent:  printEvent,
	}, nil
}

func printEvent(e event.Event) {
	var line string
	switch e.Type {
	case event.TypeSessionStarted:
		p, _ := event.Decode[event.SessionStarted](e)
		line = fmt.Sprintf("task: %s (workdir %s)", p.Task, p.Workdir)
	case event.TypeUserMessage:
		p, _ := event.Decode[event.UserMessage](e)
		line = "user: " + p.Text
	case event.TypeAssistantMessage:
		p, _ := event.Decode[event.AssistantMessage](e)
		parts := []string{}
		if p.Text != "" {
			parts = append(parts, oneLine(p.Text, 200))
		}
		for _, c := range p.ToolCalls {
			parts = append(parts, fmt.Sprintf("-> %s %s", c.Name, oneLine(c.Args, 120)))
		}
		line = "assistant: " + strings.Join(parts, " | ")
	case event.TypeToolStarted:
		p, _ := event.Decode[event.ToolStarted](e)
		line = fmt.Sprintf("tool %s started (%s)", p.Name, p.CallID)
	case event.TypeToolFinished:
		p, _ := event.Decode[event.ToolFinished](e)
		status := "ok"
		if p.IsError {
			status = "error"
		}
		line = fmt.Sprintf("tool %s %s: %s", p.CallID, status, oneLine(p.Output, 160))
	case event.TypeSessionFinished:
		p, _ := event.Decode[event.SessionFinished](e)
		line = "finished: " + p.Reason
	default:
		raw, _ := json.Marshal(e.Payload)
		line = string(raw)
	}
	fmt.Printf("%4d  %-17s  %s\n", e.Seq, e.Type, line)
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
