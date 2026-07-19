package anthropic

// Translation helpers between core.Message / core.ToolSpec and the
// Anthropic wire format (and back). The split keeps provider.go focused
// on the Provider struct + interface methods; dto.go holds the type
// definitions; this file owns the marshalling logic.

import (
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/bizshuk/agentsdk/core"
)

// toMessageParams converts core messages into the wire-format MessageParam.
// The role mapping strips ROLE_SYSTEM (it goes to RequestBody.System).
func toMessageParams(msgs []core.Message) []MessageParam {
	out := make([]MessageParam, 0, len(msgs))
	for _, m := range msgs {
		role := "user"
		if m.Role == core.ROLE_ASSISTANT {
			role = "assistant"
		}
		var blocks []ContentParam
		for _, c := range m.Parts {
			switch c.Kind {
			case core.PART_KIND_PLAIN_TEXT:
				if c.Text != "" {
					blocks = append(blocks, ContentParam{Type: "text", Text: c.Text})
				}
			case core.PART_KIND_TOOL_USE:
				if c.ToolUse != nil {
					input, _ := json.Marshal(c.ToolUse.Args)
					blocks = append(blocks, ContentParam{
						Type:  "tool_use",
						ID:    c.ToolUse.ID,
						Name:  c.ToolUse.Name,
						Input: input,
					})
				}
			case core.PART_KIND_TOOL_RESULT:
				if c.ToolResult != nil {
					payload, _ := json.Marshal(c.ToolResult.Output)
					blocks = append(blocks, ContentParam{
						Type:      "tool_result",
						ToolUseID: c.ToolResult.CallID,
						Content:   payload,
						IsError:   c.ToolResult.Error != "",
					})
				}
			}
		}
		out = append(out, MessageParam{Role: role, Content: blocks})
	}
	return out
}

// toToolParams converts core tool specs into wire-format tool params. The
// JSON Schema body is forwarded verbatim so callers can hand us either a
// json.RawMessage or a structured object.
func toToolParams(specs []core.ToolSpec) []ToolUnionParam {
	out := make([]ToolUnionParam, 0, len(specs))
	for _, s := range specs {
		schema := ToolInputSchema{Type: "object"}
		if raw, ok := s.Parameters.(json.RawMessage); ok && len(raw) > 0 {
			schema.Properties = raw
		} else if s.Parameters != nil {
			if m, err := json.Marshal(s.Parameters); err == nil {
				schema.Properties = m
			}
		}
		out = append(out, ToolUnionParam{
			OfTool: &ToolParam{
				Name:        s.Name,
				Description: s.Description,
				InputSchema: schema,
			},
		})
	}
	return out
}

// toSDKParams walks the wire-format body and produces the SDK's
// MessageNewParams. Splitting the conversion this way keeps SDK imports
// out of dto.go and lets us swap to a non-SDK transport later.
func toSDKParams(body RequestBody) (anthropic.MessageNewParams, error) {
	var params anthropic.MessageNewParams
	params.Model = anthropic.Model(body.Model)
	params.MaxTokens = int64(body.MaxTokens)
	if body.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: body.System}}
	}
	for _, mp := range body.Messages {
		var blocks []anthropic.ContentBlockParamUnion
		for _, cp := range mp.Content {
			switch cp.Type {
			case "text":
				if cp.Text != "" {
					blocks = append(blocks, anthropic.NewTextBlock(cp.Text))
				}
			case "tool_use":
				var args map[string]any
				_ = json.Unmarshal(cp.Input, &args)
				blocks = append(blocks, anthropic.NewToolUseBlock(cp.ID, args, cp.Name))
			case "tool_result":
				outStr := stringifyJSON(cp.Content)
				isErr := cp.IsError
				blocks = append(blocks, anthropic.NewToolResultBlock(cp.ToolUseID, outStr, isErr))
			}
		}
		params.Messages = append(params.Messages, anthropic.MessageParam{
			Role:    anthropic.MessageParamRole(mp.Role),
			Content: blocks,
		})
	}
	for _, t := range body.Tools {
		if t.OfTool == nil {
			continue
		}
		props := t.OfTool.InputSchema.Properties
		params.Tools = append(params.Tools, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.OfTool.Name,
				Description: anthropic.String(t.OfTool.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: props,
				},
			},
		})
	}
	return params, nil
}

// fromSDKResponse walks the SDK's Message response and folds it into a
// core.ModelResult. We deliberately keep the SDK import in this file
// only — downstream of this call the runtime sees no Anthropic types.
func fromSDKResponse(resp *anthropic.Message) core.ModelResult {
	out := core.ModelResult{
		StopReason: string(resp.StopReason),
		Usage: core.TokenUsage{
			PromptTokens:     int(resp.Usage.InputTokens),
			CompletionTokens: int(resp.Usage.OutputTokens),
			TotalTokens:      int(resp.Usage.InputTokens + resp.Usage.OutputTokens),
		},
	}
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			out.Text += block.Text
		case "tool_use":
			if block.ID != "" {
				var argsMap map[string]any
				_ = json.Unmarshal(block.Input, &argsMap)
				out.ToolCalls = append(out.ToolCalls, core.ToolCall{
					ID:   block.ID,
					Name: block.Name,
					Args: argsMap,
				})
			}
		}
	}
	return out
}

// stringifyJSON best-effort converts a JSON payload to the string form
// Anthropic expects inside tool_result blocks. Strings pass through; any
// other JSON value is re-marshalled so the API receives valid JSON.
func stringifyJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	if x, ok := v.(string); ok {
		return x
	}
	out, _ := json.Marshal(v)
	return string(out)
}

// maxTokensOrDefault returns req.MaxTokens, or 4096 when unset. The
// Anthropic API rejects requests with max_tokens=0; 4096 is the smallest
// value that satisfies Sonnet's hard requirement.
func maxTokensOrDefault(req core.ModelRequest) int {
	if req.MaxTokens > 0 {
		return req.MaxTokens
	}
	return 4096
}
