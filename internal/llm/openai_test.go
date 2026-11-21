package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 用一个假的 OpenAI 服务检查请求和响应的转换：
// 第一次返回一个工具调用；第二次检查请求里带上了 assistant 的 tool_calls 和 tool 结果。
func TestOpenAIToolCallRoundTrip(t *testing.T) {
	var requests []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		requests = append(requests, req)

		w.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			io.WriteString(w, `{"id":"1","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]}}]}`)
			return
		}
		io.WriteString(w, `{"id":"2","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"done"}}]}`)
	}))
	defer srv.Close()

	m := NewOpenAI(srv.URL, "test-key", "m")
	tools := []ToolSpec{{Name: "bash", Description: "run", Parameters: map[string]any{"type": "object"}}}
	msgs := []Message{{Role: RoleSystem, Text: "sys"}, {Role: RoleUser, Text: "task"}}

	reply, err := m.Complete(context.Background(), msgs, tools)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.ToolCalls) != 1 || reply.ToolCalls[0] != (ToolCall{ID: "call_1", Name: "bash", Args: `{"command":"ls"}`}) {
		t.Fatalf("reply = %+v", reply)
	}

	msgs = append(msgs, reply, Message{Role: RoleTool, Text: "a.txt", ToolCallID: "call_1"})
	reply, err = m.Complete(context.Background(), msgs, tools)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "done" || len(reply.ToolCalls) != 0 {
		t.Fatalf("reply = %+v", reply)
	}

	first := requests[0]
	if first["model"] != "m" {
		t.Errorf("model = %v", first["model"])
	}
	if ts, _ := first["tools"].([]any); len(ts) != 1 {
		t.Errorf("tools = %v", first["tools"])
	}

	sent, _ := requests[1]["messages"].([]any)
	if len(sent) != 4 {
		t.Fatalf("second request has %d messages, want 4: %v", len(sent), sent)
	}
	asst, _ := sent[2].(map[string]any)
	calls, _ := asst["tool_calls"].([]any)
	if asst["role"] != "assistant" || len(calls) != 1 {
		t.Errorf("assistant message = %v", asst)
	}
	tool, _ := sent[3].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" || tool["content"] != "a.txt" {
		t.Errorf("tool message = %v", tool)
	}
}
