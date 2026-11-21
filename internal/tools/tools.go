// Package tools 是 agent 能调用的工具。第一步只有三个：读文件、写文件、跑命令，
// 都直接在本机的工作目录里执行，第二步再换成沙箱。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/zszz3/agentkeel/internal/llm"
)

// Tool 是一个工具。Run 返回的字符串会原样作为 tool 消息发给模型。
type Tool interface {
	Spec() llm.ToolSpec
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry(ts ...Tool) *Registry {
	r := &Registry{tools: map[string]Tool{}}
	for _, t := range ts {
		r.Register(t)
	}
	return r
}

// Default 返回第一步用的三个工具，都限定在 workdir 里。
func Default(workdir string) *Registry {
	return NewRegistry(
		ReadFile{Workdir: workdir},
		WriteFile{Workdir: workdir},
		Bash{Workdir: workdir},
	)
}

func (r *Registry) Register(t Tool) {
	r.tools[t.Spec().Name] = t
}

// Specs 按名字排序，保证每次发给模型的工具列表顺序一样。
func (r *Registry) Specs() []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(r.tools))
	for _, t := range r.tools {
		specs = append(specs, t.Spec())
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs
}

// Call 执行一次工具调用。工具出错不返回 error，而是把错误写进输出、isError 置为 true，
// 交给模型自己处理。
func (r *Registry) Call(ctx context.Context, name, args string) (output string, isError bool) {
	t, ok := r.tools[name]
	if !ok {
		return fmt.Sprintf("unknown tool %q", name), true
	}
	if args == "" {
		args = "{}"
	}
	out, err := t.Run(ctx, json.RawMessage(args))
	if err != nil {
		if out != "" {
			return out + "\n" + err.Error(), true
		}
		return err.Error(), true
	}
	return out, false
}
