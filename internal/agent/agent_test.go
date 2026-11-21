package agent

// 第一步的验收测试。Replay 和 Run 写好之后，下面的测试应该全部通过：
//
//	go test ./internal/agent/
//
// 测试里没有真模型：llm.Scripted 和 turnModel 按事先写好的回复返回。
// “进程被杀”用 crashStore 模拟：从某一次 Append 开始，所有写入都失败，
// 就像进程在那一刻死了，日志停在最后一条写成功的事件上。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zszz3/agentkeel/internal/event"
	"github.com/zszz3/agentkeel/internal/llm"
	"github.com/zszz3/agentkeel/internal/store"
	"github.com/zszz3/agentkeel/internal/tools"
)

// ---------- Replay ----------

func TestReplayBuildsMessagesAndPending(t *testing.T) {
	c1, c2 := call("c1", "a"), call("c2", "b")
	events := build(t, "s",
		event.SessionStarted{Task: "fix it", SystemPrompt: "sys", Workdir: "/w"},
		event.AssistantMessage{Text: "looking", ToolCalls: []llm.ToolCall{c1, c2}},
		event.ToolStarted{CallID: "c1", Name: c1.Name, Args: c1.Args},
		event.ToolFinished{CallID: "c1", Output: "a=1"},
		event.ToolStarted{CallID: "c2", Name: c2.Name, Args: c2.Args},
	)

	st, err := Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	wantMessages(t, st.Messages, []llm.Message{
		{Role: llm.RoleSystem, Text: "sys"},
		{Role: llm.RoleUser, Text: "fix it"},
		{Role: llm.RoleAssistant, Text: "looking", ToolCalls: []llm.ToolCall{c1, c2}},
		{Role: llm.RoleTool, Text: "a=1", ToolCallID: "c1"},
	})
	if len(st.Pending) != 1 || st.Pending[0].Call != c2 || !st.Pending[0].Started {
		t.Errorf("Pending = %+v, want [c2 started]", st.Pending)
	}
	if st.SessionID != "s" || st.Config.Workdir != "/w" {
		t.Errorf("SessionID/Config = %q / %+v", st.SessionID, st.Config)
	}
	if st.Turns != 1 || st.NextSeq != 6 || st.Finished {
		t.Errorf("Turns=%d NextSeq=%d Finished=%v, want 1 6 false", st.Turns, st.NextSeq, st.Finished)
	}
}

func TestReplayPendingNotStarted(t *testing.T) {
	c1, c2 := call("c1", "a"), call("c2", "b")
	st, err := Replay(build(t, "s",
		event.SessionStarted{Task: "t", SystemPrompt: "sys"},
		event.AssistantMessage{ToolCalls: []llm.ToolCall{c1, c2}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Pending) != 2 || st.Pending[0].Call != c1 || st.Pending[1].Call != c2 ||
		st.Pending[0].Started || st.Pending[1].Started {
		t.Errorf("Pending = %+v, want [c1, c2] both not started", st.Pending)
	}
}

func TestReplayUserMessageAndFinish(t *testing.T) {
	st, err := Replay(build(t, "s",
		event.SessionStarted{Task: "t", SystemPrompt: "sys"},
		event.UserMessage{Text: "also check README"},
		event.AssistantMessage{Text: "done"},
		event.SessionFinished{Reason: event.ReasonDone},
	))
	if err != nil {
		t.Fatal(err)
	}
	wantMessages(t, st.Messages, []llm.Message{
		{Role: llm.RoleSystem, Text: "sys"},
		{Role: llm.RoleUser, Text: "t"},
		{Role: llm.RoleUser, Text: "also check README"},
		{Role: llm.RoleAssistant, Text: "done"},
	})
	if !st.Finished || len(st.Pending) != 0 || st.NextSeq != 5 {
		t.Errorf("Finished=%v Pending=%v NextSeq=%d", st.Finished, st.Pending, st.NextSeq)
	}
}

func TestReplayRejectsBrokenLog(t *testing.T) {
	ok := build(t, "s", event.SessionStarted{Task: "t"}, event.UserMessage{Text: "x"})

	gap := build(t, "s", event.SessionStarted{Task: "t"}, event.UserMessage{Text: "x"})
	gap[1].Seq = 3

	cases := map[string][]event.Event{
		"empty":           nil,
		"no start":        ok[1:],
		"seq gap":         gap,
		"start not first": {ok[1], ok[0]},
	}
	for name, events := range cases {
		if _, err := Replay(events); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

// ---------- Run ----------

func TestRunCompletes(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	counter := newCountTool()
	model := &llm.Scripted{Replies: []llm.Message{
		{ToolCalls: []llm.ToolCall{call("c1", "a")}},
		{ToolCalls: []llm.ToolCall{call("c2", "b"), call("c3", "a")}},
		{Text: "all done"},
	}}
	a := newAgent(st, model, counter)

	id := mustStart(t, a)
	if err := a.Run(ctx, id); err != nil {
		t.Fatal(err)
	}

	events := load(t, st, id)
	wantTypes(t, events,
		event.TypeSessionStarted,
		event.TypeAssistantMessage, event.TypeToolStarted, event.TypeToolFinished,
		event.TypeAssistantMessage, event.TypeToolStarted, event.TypeToolFinished, event.TypeToolStarted, event.TypeToolFinished,
		event.TypeAssistantMessage, event.TypeSessionFinished,
	)
	checkLog(t, events, event.ReasonDone)
	counter.want(t, map[string]int{"a": 2, "b": 1})

	calls := model.Calls()
	if len(calls) != 3 {
		t.Fatalf("model called %d times, want 3", len(calls))
	}
	// 每次调模型，消息都是 system、任务，再加上之前的全部回复和工具结果。
	wantMessages(t, calls[1][2:], []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call("c1", "a")}},
		{Role: llm.RoleTool, Text: "a=1", ToolCallID: "c1"},
	})
	wantMessages(t, calls[2][4:], []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call("c2", "b"), call("c3", "a")}},
		{Role: llm.RoleTool, Text: "b=1", ToolCallID: "c2"},
		{Role: llm.RoleTool, Text: "a=2", ToolCallID: "c3"},
	})
}

func TestRunMaxTurns(t *testing.T) {
	st := openStore(t)
	model := &llm.Scripted{Replies: []llm.Message{
		{ToolCalls: []llm.ToolCall{call("c1", "a")}},
		{ToolCalls: []llm.ToolCall{call("c2", "a")}},
	}}
	a := newAgent(st, model, newCountTool())
	a.MaxTurns = 2

	id := mustStart(t, a)
	if err := a.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	checkLog(t, load(t, st, id), event.ReasonMaxTurns)
	if n := len(model.Calls()); n != 2 {
		t.Errorf("model called %d times, want 2", n)
	}
}

func TestRunFinishedSessionIsNoop(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	a := newAgent(st, &llm.Scripted{Replies: []llm.Message{{Text: "done"}}}, newCountTool())
	id := mustStart(t, a)
	if err := a.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	before := len(load(t, st, id))

	idle := &llm.Scripted{}
	if err := newAgent(st, idle, newCountTool()).Run(ctx, id); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(idle.Calls()) != 0 || len(load(t, st, id)) != before {
		t.Error("Run on a finished session must not call the model or write events")
	}
}

func TestRunUnknownSession(t *testing.T) {
	a := newAgent(openStore(t), &llm.Scripted{}, newCountTool())
	if err := a.Run(context.Background(), "no-such-session"); err == nil {
		t.Error("want error for unknown session")
	}
}

// ---------- 恢复 ----------

// 进程在工具执行完、tool_finished 还没写进去时死掉。
// 日志里 c1 只有 tool_started。恢复后 c1 要有且只有一条 tool_finished，会话要正常结束。
// c1 被执行了 1 次还是 2 次，取决于你的选择，两种都可以。
func TestResumeAfterCrashDuringTool(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	counter := newCountTool()

	first := newAgent(crashWhen(st, func(e event.Event) bool { return e.Type == event.TypeToolFinished }),
		&llm.Scripted{Replies: []llm.Message{{ToolCalls: []llm.ToolCall{call("c1", "a")}}}}, counter)
	id := mustStart(t, first)
	if err := first.Run(ctx, id); !errors.Is(err, errCrash) {
		t.Fatalf("first Run: want crash, got %v", err)
	}
	wantTypes(t, load(t, st, id), event.TypeSessionStarted, event.TypeAssistantMessage, event.TypeToolStarted)

	model := &llm.Scripted{Replies: []llm.Message{{Text: "done"}}}
	if err := newAgent(st, model, counter).Run(ctx, id); err != nil {
		t.Fatalf("resume: %v", err)
	}
	checkLog(t, load(t, st, id), event.ReasonDone)
	if n := counter.get("a"); n < 1 || n > 2 {
		t.Errorf("c1 executed %d times, want 1 or 2", n)
	}
	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("model called %d times after resume, want 1", len(calls))
	}
	last := calls[0][len(calls[0])-1]
	if last.Role != llm.RoleTool || last.ToolCallID != "c1" {
		t.Errorf("after resume the model should see c1's result last, got %+v", last)
	}
}

// 一条 assistant 消息里有两个工具调用。c1 做完了，c2 的 tool_started 还没写进去进程就死了。
// 恢复后：c1 不能再执行；c2 从来没开始过，要执行一次。
func TestResumeSkipsFinishedTools(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	counter := newCountTool()

	crash := func(e event.Event) bool {
		if e.Type != event.TypeToolStarted {
			return false
		}
		p, _ := event.Decode[event.ToolStarted](e)
		return p.CallID == "c2"
	}
	first := newAgent(crashWhen(st, crash),
		&llm.Scripted{Replies: []llm.Message{{ToolCalls: []llm.ToolCall{call("c1", "a"), call("c2", "b")}}}}, counter)
	id := mustStart(t, first)
	if err := first.Run(ctx, id); !errors.Is(err, errCrash) {
		t.Fatalf("first Run: want crash, got %v", err)
	}

	if err := newAgent(st, &llm.Scripted{Replies: []llm.Message{{Text: "done"}}}, counter).Run(ctx, id); err != nil {
		t.Fatalf("resume: %v", err)
	}
	checkLog(t, load(t, st, id), event.ReasonDone)
	counter.want(t, map[string]int{"a": 1, "b": 1})
}

// 在每一次写事件的时候都“杀”一次进程，然后恢复。不管死在哪，最后的日志都要完整。
func TestResumeAtEveryPoint(t *testing.T) {
	replies := []llm.Message{
		{Text: "step 1", ToolCalls: []llm.ToolCall{call("c1", "a")}},
		{Text: "step 2", ToolCalls: []llm.ToolCall{call("c2", "b"), call("c3", "a")}},
		{Text: "done"},
	}
	// 不崩的话，Run 一共写 10 条事件（session_started 由 Start 写，不算在内）。
	const appends = 10

	for k := 1; k <= appends; k++ {
		t.Run(fmt.Sprintf("crash at write %d", k), func(t *testing.T) {
			ctx := context.Background()
			st := openStore(t)
			counter := newCountTool()
			model := turnModel(replies)

			n := 0
			first := newAgent(crashWhen(st, func(event.Event) bool { n++; return n == k }), model, counter)
			id := mustStart(t, first)
			if err := first.Run(ctx, id); !errors.Is(err, errCrash) {
				t.Fatalf("first Run: want crash, got %v", err)
			}
			if err := newAgent(st, model, counter).Run(ctx, id); err != nil {
				t.Fatalf("resume: %v", err)
			}
			checkLog(t, load(t, st, id), event.ReasonDone)
			// 最多有一个工具被打断，所以最多多执行一次。
			a, b := counter.get("a"), counter.get("b")
			if a < 2 || b < 1 || a+b > 4 {
				t.Errorf("executions a=%d b=%d, want a=2 b=1 (one extra allowed)", a, b)
			}
		})
	}
}

// ---------- 测试工具 ----------

var errCrash = errors.New("simulated crash")

// crashStore 在 crash(e) 第一次返回 true 时开始，所有 Append 都失败，模拟进程死掉。
type crashStore struct {
	store.Store
	crash   func(event.Event) bool
	mu      sync.Mutex
	crashed bool
}

func crashWhen(s store.Store, crash func(event.Event) bool) *crashStore {
	return &crashStore{Store: s, crash: crash}
}

func (c *crashStore) Append(ctx context.Context, e event.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.crashed || (e.Seq > 1 && c.crash(e)) {
		c.crashed = true
		return errCrash
	}
	return c.Store.Append(ctx, e)
}

// countTool 记录每个 key 被执行了几次，返回 "key=次数"。
// 同一个 countTool 在“崩溃前后”的两个 Agent 之间共用，代表真实世界里已经发生的副作用。
type countTool struct {
	mu sync.Mutex
	n  map[string]int
}

func newCountTool() *countTool { return &countTool{n: map[string]int{}} }

func (*countTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "count", Description: "test tool", Parameters: map[string]any{"type": "object"}}
}

func (c *countTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n[in.Key]++
	return fmt.Sprintf("%s=%d", in.Key, c.n[in.Key]), nil
}

func (c *countTool) get(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[key]
}

func (c *countTool) want(t *testing.T, want map[string]int) {
	t.Helper()
	for k, v := range want {
		if got := c.get(k); got != v {
			t.Errorf("tool executed with key %q %d times, want %d", k, got, v)
		}
	}
}

// turnModel 按“消息里已经有几条 assistant 消息”决定回哪一条，
// 所以崩溃后重新调用，拿到的回复和崩溃前一样。
func turnModel(replies []llm.Message) llm.Model { return turnModelFunc(replies) }

type turnModelFunc []llm.Message

func (r turnModelFunc) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolSpec) (llm.Message, error) {
	i := 0
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant {
			i++
		}
	}
	if i >= len(r) {
		return llm.Message{}, fmt.Errorf("turnModel: no reply for turn %d", i+1)
	}
	reply := r[i]
	reply.Role = llm.RoleAssistant
	return reply, nil
}

func call(id, key string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: "count", Args: fmt.Sprintf(`{"key":%q}`, key)}
}

func newAgent(s store.Store, m llm.Model, counter *countTool) *Agent {
	return &Agent{
		Store: s,
		Model: m,
		Tools: func(string) *tools.Registry { return tools.NewRegistry(counter) },
	}
}

func openStore(t *testing.T) *store.SQLite {
	t.Helper()
	s, err := store.OpenSQLite(filepath.Join(t.TempDir(), "agentkeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustStart(t *testing.T, a *Agent) string {
	t.Helper()
	id, err := a.Start(context.Background(), event.SessionStarted{Task: "test task", Workdir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func load(t *testing.T, s store.Store, id string) []event.Event {
	t.Helper()
	events, err := s.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func build(t *testing.T, sid string, payloads ...event.Payload) []event.Event {
	t.Helper()
	var out []event.Event
	for i, p := range payloads {
		e, err := event.New(sid, int64(i+1), p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func wantTypes(t *testing.T, events []event.Event, want ...event.Type) {
	t.Helper()
	got := make([]event.Type, len(events))
	for i, e := range events {
		got[i] = e.Type
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("event types:\n got  %v\n want %v", got, want)
	}
}

func wantMessages(t *testing.T, got, want []llm.Message) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("got %d messages, want %d:\n got  %+v\n want %+v", len(got), len(want), got, want)
		return
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Role != w.Role || g.Text != w.Text || g.ToolCallID != w.ToolCallID ||
			fmt.Sprint(g.ToolCalls) != fmt.Sprint(w.ToolCalls) {
			t.Errorf("message %d:\n got  %+v\n want %+v", i, g, w)
		}
	}
}

// checkLog 检查一份跑完的日志：
//   - seq 从 1 连续
//   - 每个工具调用恰好有一条 tool_finished，并且在它之前有 tool_started
//   - 最后一条是 session_finished，原因是 reason
func checkLog(t *testing.T, events []event.Event, reason string) {
	t.Helper()
	calls := map[string]bool{}
	started := map[string]bool{}
	finished := map[string]int{}
	for i, e := range events {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d", i, e.Seq)
		}
		switch e.Type {
		case event.TypeAssistantMessage:
			p, _ := event.Decode[event.AssistantMessage](e)
			for _, c := range p.ToolCalls {
				calls[c.ID] = true
			}
		case event.TypeToolStarted:
			p, _ := event.Decode[event.ToolStarted](e)
			started[p.CallID] = true
		case event.TypeToolFinished:
			p, _ := event.Decode[event.ToolFinished](e)
			if !calls[p.CallID] {
				t.Errorf("seq %d: tool_finished for unknown call %s", e.Seq, p.CallID)
			}
			if !started[p.CallID] {
				t.Errorf("seq %d: tool_finished for %s without tool_started", e.Seq, p.CallID)
			}
			finished[p.CallID]++
		}
	}
	for id := range calls {
		if finished[id] != 1 {
			t.Errorf("call %s has %d tool_finished events, want 1", id, finished[id])
		}
	}
	last := events[len(events)-1]
	p, err := event.Decode[event.SessionFinished](last)
	if err != nil {
		t.Fatalf("last event: %v", err)
	}
	if p.Reason != reason {
		t.Errorf("finished with %q, want %q", p.Reason, reason)
	}
}
