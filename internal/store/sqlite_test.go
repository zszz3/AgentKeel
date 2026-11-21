package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zszz3/agentkeel/internal/event"
)

func open(t *testing.T) *SQLite {
	t.Helper()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustEvent(t *testing.T, sid string, seq int64, p event.Payload) event.Event {
	t.Helper()
	e, err := event.New(sid, seq, p)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestAppendLoad(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if err := s.Append(ctx, mustEvent(t, "s1", 1, event.SessionStarted{Task: "fix"})); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, mustEvent(t, "s1", 2, event.UserMessage{Text: "hi"})); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Seq != 1 || got[1].Type != event.TypeUserMessage {
		t.Fatalf("unexpected events: %+v", got)
	}
	msg, err := event.Decode[event.UserMessage](got[1])
	if err != nil || msg.Text != "hi" {
		t.Fatalf("decode: %+v, %v", msg, err)
	}

	empty, err := s.Load(ctx, "nope")
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing session: %v, %v", empty, err)
	}
}

func TestAppendRejectsWrongSeq(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if err := s.Append(ctx, mustEvent(t, "s1", 2, event.UserMessage{})); !IsConflict(err) {
		t.Fatalf("gap: want conflict, got %v", err)
	}
	if err := s.Append(ctx, mustEvent(t, "s1", 1, event.SessionStarted{})); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, mustEvent(t, "s1", 1, event.UserMessage{})); !IsConflict(err) {
		t.Fatalf("duplicate: want conflict, got %v", err)
	}
}

// 两个写者抢同一个 seq，只能有一个成功。
func TestConcurrentAppend(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := OpenSQLite(filepath.Join(dir, "shared.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenSQLite(filepath.Join(dir, "shared.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	for seq := int64(1); seq <= 20; seq++ {
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, s := range []*SQLite{a, b} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = s.Append(ctx, mustEvent(t, "s1", seq, event.UserMessage{Text: "x"}))
			}()
		}
		wg.Wait()
		ok := 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case !IsConflict(err):
				t.Fatalf("seq %d: unexpected error %v", seq, err)
			}
		}
		if ok != 1 {
			t.Fatalf("seq %d: %d writers succeeded, want 1", seq, ok)
		}
	}
}
