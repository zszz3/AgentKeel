package llm

import (
	"context"
	"errors"
	"fmt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

// OpenAI 走 OpenAI Chat Completions 接口。兼容这个接口的服务改 baseURL 就能用。
type OpenAI struct {
	client openai.Client
	model  string
}

func NewOpenAI(baseURL, apiKey, model string, opts ...option.RequestOption) *OpenAI {
	all := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		all = append(all, option.WithBaseURL(baseURL))
	}
	all = append(all, opts...)
	return &OpenAI{client: openai.NewClient(all...), model: model}
}

func (o *OpenAI) Complete(ctx context.Context, msgs []Message, tools []ToolSpec) (Message, error) {
	params := openai.ChatCompletionNewParams{
		Model:    o.model,
		Messages: toOpenAIMessages(msgs),
	}
	for _, t := range tools {
		params.Tools = append(params.Tools, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Parameters:  shared.FunctionParameters(t.Parameters),
		}))
	}

	resp, err := o.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return Message{}, fmt.Errorf("llm: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Message{}, errors.New("llm: response has no choices")
	}

	m := resp.Choices[0].Message
	out := Message{Role: RoleAssistant, Text: m.Content}
	for _, tc := range m.ToolCalls {
		if tc.Type != "function" {
			continue
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}
	return out, nil
}

func toOpenAIMessages(msgs []Message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem:
			out = append(out, openai.SystemMessage(m.Text))
		case RoleUser:
			out = append(out, openai.UserMessage(m.Text))
		case RoleTool:
			out = append(out, openai.ToolMessage(m.Text, m.ToolCallID))
		case RoleAssistant:
			var p openai.ChatCompletionAssistantMessageParam
			if m.Text != "" {
				p.Content.OfString = openai.String(m.Text)
			}
			for _, tc := range m.ToolCalls {
				p.ToolCalls = append(p.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: tc.ID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: tc.Args,
						},
					},
				})
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &p})
		}
	}
	return out
}
