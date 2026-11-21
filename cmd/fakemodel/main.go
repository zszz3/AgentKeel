// fakemodel 是一个假的 OpenAI 兼容服务，按固定剧本回复，用来在不花钱、结果可重复的情况下演示崩溃恢复。
//
// 剧本对应 testdata/fixtest：先跑一个慢的 go test（给你留出 kill -9 的时间），再改代码，再验证。
// 第几轮由请求里 assistant 消息的条数决定，所以恢复后重新请求也能拿到正确的下一步。
//
//	go run ./cmd/fakemodel -addr 127.0.0.1:18777
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
)

const fix = `package fixtest

import "sort"

// Median 返回 xs 的中位数，xs 为空时返回 0。不修改 xs。
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
`

type call struct {
	name string
	args map[string]string
}

var script = []struct {
	text  string
	calls []call
}{
	{"先跑一下测试看看。", []call{{"bash", map[string]string{"command": "sleep 5; go test ./... 2>&1 | tail -5"}}}},
	{"偶数长度没有取中间两个数的平均，而且原地排序改了输入。", []call{{"write_file", map[string]string{"path": "median.go", "content": fix}}}},
	{"再跑一遍。", []call{{"bash", map[string]string{"command": "go test ./..."}}}},
	{"修好了：先复制再排序，偶数长度取中间两个数的平均。", nil},
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18777", "listen address")
	flag.Parse()

	http.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		turn := 0
		for _, m := range req.Messages {
			if m.Role == "assistant" {
				turn++
			}
		}
		if turn >= len(script) {
			http.Error(w, fmt.Sprintf("no reply scripted for turn %d", turn), http.StatusBadRequest)
			return
		}
		step := script[turn]
		log.Printf("model call: turn %d", turn)

		msg := map[string]any{"role": "assistant", "content": step.text}
		finish := "stop"
		if len(step.calls) > 0 {
			var tcs []map[string]any
			for i, c := range step.calls {
				var args strings.Builder
				enc := json.NewEncoder(&args)
				enc.SetEscapeHTML(false)
				enc.Encode(c.args)
				tcs = append(tcs, map[string]any{
					"id":       fmt.Sprintf("call_%d_%d", turn, i),
					"type":     "function",
					"function": map[string]any{"name": c.name, "arguments": strings.TrimSpace(args.String())},
				})
			}
			msg["tool_calls"] = tcs
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": fmt.Sprint(turn), "object": "chat.completion", "created": 0, "model": req.Model,
			"choices": []any{map[string]any{"index": 0, "finish_reason": finish, "message": msg}},
		})
	})

	log.Printf("fake model listening on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
