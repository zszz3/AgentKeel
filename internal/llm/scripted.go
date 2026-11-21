package llm

import (
	"context"
	"fmt"
	"sync"
)

// Scripted 按顺序返回事先写好的回复，测试用，不花 token。
// 每次调用收到的消息记在 Calls 里，测试可以检查模型看到了什么。
type Scripted struct {
	Replies []Message

	mu    sync.Mutex
	calls [][]Message
}

func (s *Scripted) Complete(ctx context.Context, msgs []Message, tools []ToolSpec) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, append([]Message(nil), msgs...))
	n := len(s.calls)
	if n > len(s.Replies) {
		return Message{}, fmt.Errorf("scripted: no reply for call %d", n)
	}
	r := s.Replies[n-1]
	r.Role = RoleAssistant
	return r, nil
}

// Calls 返回每次调用时收到的消息。
func (s *Scripted) Calls() [][]Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]Message(nil), s.calls...)
}
