package transform

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	ag "github.com/bizshuk/agentsdk/provider/antigravity"
	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/bizshuk/proxy/model/antigravity"
	"github.com/google/uuid"
)

// ANTIGRAVITY_REQUEST_ID_PREFIX marks proxy-issued envelope request IDs.
const ANTIGRAVITY_REQUEST_ID_PREFIX = "agent-"

// AnthropicToAntigravityRequest converts an Anthropic Messages request into the
// Antigravity envelope. The envelope's `project` is left blank: it comes from
// the credential, which only the upstream client holds.
func AnthropicToAntigravityRequest(_ context.Context, env model.RequestEnvelope) (model.TransformResult, error) {
	src, err := anthropic.DecodeRequest(env.Body)
	if err != nil {
		return model.TransformResult{}, invalidRequest("decode anthropic request", err)
	}

	losses := make([]model.SemanticLoss, 0, 4)
	inner := antigravity.Inner{Contents: []antigravity.Content{}}

	systemInstruction, err := anthropicSystemToAntigravity(src.System)
	if err != nil {
		return model.TransformResult{}, err
	}
	inner.SystemInstruction = systemInstruction

	contents, contentLosses, err := anthropicMessagesToAntigravity(src.Messages)
	if err != nil {
		return model.TransformResult{}, err
	}
	inner.Contents = contents
	losses = append(losses, contentLosses...)

	tools, err := anthropicToolsToAntigravity(src.Tools)
	if err != nil {
		return model.TransformResult{}, err
	}
	inner.Tools = tools
	toolConfig, choiceLosses := anthropicToolChoiceToAntigravity(src.ToolChoice)
	inner.ToolConfig = toolConfig
	losses = append(losses, choiceLosses...)
	// The gateway's Claude models reject tool arguments that skip upstream
	// validation, and unlike the Gemini families they do not default to it.
	// This overrides any caller-derived mode: without it the tools fail.
	if len(tools) > 0 && isAntigravityClaudeModel(env.Model) {
		inner.ToolConfig = &antigravity.ToolConfig{
			FunctionCallingConfig: antigravity.FunctionCallingConfig{
				Mode: antigravity.MODE_VALIDATED,
			},
		}
	}

	inner.GenerationConfig = anthropicGenerationConfig(src, env.Model)
	inner.SessionID = antigravitySessionID(contents)

	dst := antigravity.Request{
		Model:       env.Model,
		UserAgent:   antigravity.USER_AGENT,
		RequestType: antigravity.REQUEST_TYPE_AGENT,
		RequestID:   ANTIGRAVITY_REQUEST_ID_PREFIX + uuid.NewString(),
		Request:     inner,
	}

	body, err := antigravity.Encode(dst)
	if err != nil {
		return model.TransformResult{}, fmt.Errorf("encode antigravity request: %w", err)
	}
	return model.TransformResult{Body: body, Losses: losses}, nil
}

func anthropicSystemToAntigravity(system anthropic.ContentList) (*antigravity.Content, error) {
	parts := make([]antigravity.Part, 0, len(system))
	for _, block := range system {
		if block.Type != "text" {
			return nil, unsupportedFeature("system", "Antigravity system instructions support only text blocks")
		}
		if block.Text == "" {
			continue
		}
		parts = append(parts, antigravity.Part{Text: block.Text})
	}
	if len(parts) == 0 {
		return nil, nil
	}
	// The gateway wants the system turn labelled as the caller, not a distinct
	// system role — Gemini has no system role on the wire.
	return &antigravity.Content{Role: antigravity.ROLE_USER, Parts: parts}, nil
}

func anthropicMessagesToAntigravity(messages []anthropic.Message) ([]antigravity.Content, []model.SemanticLoss, error) {
	contents := make([]antigravity.Content, 0, len(messages))
	losses := make([]model.SemanticLoss, 0, 2)
	// tool_result references a tool_use by ID, but functionResponse is keyed by
	// name, so the mapping has to be carried forward across messages.
	toolNameByID := make(map[string]string)

	for index, message := range messages {
		role := antigravity.ROLE_USER
		if message.Role == "assistant" {
			role = antigravity.ROLE_MODEL
		}
		parts, blockLosses, err := anthropicBlocksToAntigravity(index, message.Content, toolNameByID)
		if err != nil {
			return nil, nil, err
		}
		losses = append(losses, blockLosses...)
		if len(parts) == 0 {
			// The gateway rejects a turn with no initialized part.
			continue
		}
		if role == antigravity.ROLE_MODEL {
			parts = orderAntigravityModelParts(parts)
		}
		contents = append(contents, antigravity.Content{Role: role, Parts: parts})
	}
	return contents, losses, nil
}

func anthropicBlocksToAntigravity(
	messageIndex int,
	blocks []anthropic.Content,
	toolNameByID map[string]string,
) ([]antigravity.Part, []model.SemanticLoss, error) {
	parts := make([]antigravity.Part, 0, len(blocks))
	losses := make([]model.SemanticLoss, 0, 2)

	for blockIndex, block := range blocks {
		if len(block.CacheControl) > 0 {
			losses = append(losses, model.SemanticLoss{
				Field:  fmt.Sprintf("messages[%d].content[%d].cache_control", messageIndex, blockIndex),
				Reason: "Antigravity has no explicit cache-control breakpoint",
			})
		}
		switch block.Type {
		case "text":
			if block.Text == "" {
				continue
			}
			parts = append(parts, antigravity.Part{Text: block.Text})
		case "thinking":
			// The gateway validates thought signatures and rejects unsigned
			// thinking, so an unsigned block cannot be replayed at all.
			if block.Signature == "" || block.Thinking == "" {
				losses = append(losses, model.SemanticLoss{
					Field:  fmt.Sprintf("messages[%d].content[%d]", messageIndex, blockIndex),
					Reason: "Antigravity rejects thinking blocks without both text and a signature",
				})
				continue
			}
			parts = append(parts, antigravity.Part{
				Thought:          true,
				Text:             block.Thinking,
				ThoughtSignature: block.Signature,
			})
		case "tool_use":
			args, err := anthropicToolInput(block.Input)
			if err != nil {
				return nil, nil, invalidRequest(
					fmt.Sprintf("messages[%d].content[%d].input", messageIndex, blockIndex), err)
			}
			if block.ID != "" && block.Name != "" {
				toolNameByID[block.ID] = block.Name
			}
			// Gemini rejects a replayed functionCall that has lost its thought
			// signature. Clients that strip unknown fields drop it from the
			// tool_use block, so fall back to what the gateway issued for this
			// call ID on the turn that produced it.
			signature := block.Signature
			if signature == "" {
				signature = antigravityToolSignatures.Lookup(block.ID)
			}
			if signature == "" {
				losses = append(losses, model.SemanticLoss{
					Field:  fmt.Sprintf("messages[%d].content[%d].signature", messageIndex, blockIndex),
					Reason: "tool_use has no thought signature; Antigravity rejects replayed function calls without one",
				})
			}
			parts = append(parts, antigravity.Part{
				ThoughtSignature: signature,
				FunctionCall: &antigravity.FunctionCall{
					ID:   block.ID,
					Name: block.Name,
					Args: args,
				},
			})
		case "tool_result":
			resultParts, err := anthropicToolResultToAntigravity(block, toolNameByID)
			if err != nil {
				return nil, nil, invalidRequest(
					fmt.Sprintf("messages[%d].content[%d]", messageIndex, blockIndex), err)
			}
			parts = append(parts, resultParts...)
		case "image":
			if block.Source == nil || block.Source.Type != "base64" {
				return nil, nil, unsupportedFeature(
					fmt.Sprintf("messages[%d].content[%d].source", messageIndex, blockIndex),
					"Antigravity accepts only base64 inline image sources")
			}
			parts = append(parts, antigravity.Part{InlineData: &antigravity.InlineData{
				MIMEType: block.Source.MediaType,
				Data:     block.Source.Data,
			}})
		default:
			return nil, nil, unsupportedFeature(
				fmt.Sprintf("messages[%d].content[%d].type", messageIndex, blockIndex),
				fmt.Sprintf("Antigravity does not support Anthropic content block %q", block.Type))
		}
	}
	return parts, losses, nil
}

func anthropicToolInput(input json.RawMessage) (map[string]any, error) {
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" || trimmed == "null" {
		return map[string]any{}, nil
	}
	// Some clients send tool input as a JSON-encoded string rather than an object.
	if trimmed[0] == '"' {
		var nested string
		if err := json.Unmarshal(input, &nested); err != nil {
			return nil, err
		}
		trimmed = strings.TrimSpace(nested)
	}
	arguments := map[string]any{}
	if err := json.Unmarshal([]byte(trimmed), &arguments); err != nil {
		return nil, fmt.Errorf("tool input must be a JSON object")
	}
	return arguments, nil
}

// anthropicToolResultToAntigravity builds the functionResponse part plus any
// images the result carried. Images become sibling parts of the same turn:
// agentsdk's FunctionResponse has no parts field to nest them under, and the
// gateway reads inline media from either position.
func anthropicToolResultToAntigravity(
	block anthropic.Content,
	toolNameByID map[string]string,
) ([]antigravity.Part, error) {
	if block.ToolUseID == "" {
		return nil, fmt.Errorf("tool_result requires tool_use_id")
	}
	name, found := toolNameByID[block.ToolUseID]
	if !found {
		// Without the originating tool_use in this request the name is
		// unrecoverable; the ID is the only stable handle left.
		name = block.ToolUseID
	}

	result, imageParts, err := anthropicToolResultContent(block.Content)
	if err != nil {
		return nil, err
	}
	parts := []antigravity.Part{{FunctionResponse: &antigravity.FunctionResponse{
		ID:       block.ToolUseID,
		Name:     name,
		Response: map[string]any{"result": result},
	}}}
	return append(parts, imageParts...), nil
}

// anthropicToolResultContent splits a tool_result payload into the result value
// and any inline images it carried.
func anthropicToolResultContent(raw json.RawMessage) (any, []antigravity.Part, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil, nil
	}
	if trimmed[0] != '[' {
		var value any
		if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
			return nil, nil, fmt.Errorf("tool_result content is not valid JSON")
		}
		return value, nil, nil
	}

	var blocks []anthropic.Content
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, nil, fmt.Errorf("decode tool_result content: %w", err)
	}
	var (
		images   []antigravity.Part
		nonImage []any
	)
	for _, entry := range blocks {
		if entry.Type == "image" && entry.Source != nil && entry.Source.Type == "base64" {
			images = append(images, antigravity.Part{InlineData: &antigravity.InlineData{
				MIMEType: entry.Source.MediaType,
				Data:     entry.Source.Data,
			}})
			continue
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			return nil, nil, err
		}
		var value any
		if err := json.Unmarshal(encoded, &value); err != nil {
			return nil, nil, err
		}
		nonImage = append(nonImage, value)
	}

	switch len(nonImage) {
	case 0:
		return "", images, nil
	case 1:
		return nonImage[0], images, nil
	default:
		return nonImage, images, nil
	}
}

// orderAntigravityModelParts puts thinking first and function calls last. The
// gateway splits a model turn at each functionCall boundary, so a text part
// after a call would insert an extra assistant turn between tool_use and
// tool_result — which Anthropic clients then reject.
func orderAntigravityModelParts(parts []antigravity.Part) []antigravity.Part {
	if len(parts) < 2 {
		return parts
	}
	ordered := make([]antigravity.Part, 0, len(parts))
	for _, part := range parts {
		if part.Thought {
			ordered = append(ordered, part)
		}
	}
	for _, part := range parts {
		if !part.Thought && part.FunctionCall == nil {
			ordered = append(ordered, part)
		}
	}
	for _, part := range parts {
		if !part.Thought && part.FunctionCall != nil {
			ordered = append(ordered, part)
		}
	}
	return ordered
}

func anthropicToolsToAntigravity(tools []anthropic.Tool) ([]antigravity.Tool, error) {
	declarations := make([]antigravity.FunctionDeclaration, 0, len(tools))
	for index, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" {
			return nil, invalidRequest(fmt.Sprintf("tools[%d].name", index), fmt.Errorf("name must not be blank"))
		}
		if len(tool.InputSchema) > 0 && !json.Valid(tool.InputSchema) {
			return nil, invalidRequest(fmt.Sprintf("tools[%d].input_schema", index), fmt.Errorf("schema is not valid JSON"))
		}
		declarations = append(declarations, antigravity.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
			// The gateway validates this as a protobuf Schema and rejects the
			// whole request by name on any unsupported keyword, so the client's
			// JSON Schema has to be narrowed to that subset first.
			Parameters: antigravity.SanitizeSchema(tool.InputSchema),
		})
	}
	if len(declarations) == 0 {
		return nil, nil
	}
	return []antigravity.Tool{{FunctionDeclarations: declarations}}, nil
}

func anthropicToolChoiceToAntigravity(choice *anthropic.ToolChoice) (*antigravity.ToolConfig, []model.SemanticLoss) {
	if choice == nil {
		return nil, nil
	}
	config := antigravity.FunctionCallingConfig{}
	var losses []model.SemanticLoss
	switch choice.Type {
	case "auto":
		config.Mode = antigravity.MODE_AUTO
	case "none":
		config.Mode = antigravity.MODE_NONE
	case "any":
		config.Mode = antigravity.MODE_ANY
	case "tool":
		// The gateway's functionCallingConfig has a mode and nothing else, so
		// "call exactly this tool" narrows to "call some tool".
		config.Mode = antigravity.MODE_ANY
		if choice.Name != "" {
			losses = append(losses, model.SemanticLoss{
				Field:  "tool_choice.name",
				Reason: "Antigravity can force a tool call but cannot restrict it to one named tool",
			})
		}
	default:
		return nil, nil
	}
	return &antigravity.ToolConfig{FunctionCallingConfig: config}, losses
}

// anthropicGenerationConfig builds the sampling block. Thinking is
// family-specific: the gateway takes snake_case keys for Claude models and
// camelCase for Gemini, and each family rejects the other's spelling.
func anthropicGenerationConfig(src *anthropic.Request, modelName string) *antigravity.GenerationConfig {
	config := &antigravity.GenerationConfig{
		Temperature: src.Temperature,
		TopP:        src.TopP,
	}
	config.MaxOutputTokens = src.MaxTokens
	config.StopSequences = src.StopSequences

	if src.Thinking != nil && src.Thinking.Type == "enabled" && src.Thinking.BudgetTokens > 0 {
		budget := src.Thinking.BudgetTokens
		if isAntigravityClaudeModel(modelName) {
			config.ThinkingConfig = antigravity.ClaudeThinkingConfig{
				IncludeThoughts: true, ThinkingBudget: budget,
			}
			// The gateway requires max_tokens strictly above the budget.
			if config.MaxOutputTokens <= budget {
				config.MaxOutputTokens = budget + ag.CLAUDE_THINKING_HEADROOM
			}
		} else {
			config.ThinkingConfig = antigravity.GeminiThinkingConfig{
				IncludeThoughts: true, ThinkingBudget: budget,
			}
		}
	}
	// Gemini rejects anything above its ceiling outright, so clamp rather than
	// let the caller's max_tokens fail the whole request.
	if !isAntigravityClaudeModel(modelName) && config.MaxOutputTokens > ag.GEMINI_MAX_OUTPUT_TOKENS {
		config.MaxOutputTokens = ag.GEMINI_MAX_OUTPUT_TOKENS
	}

	if config.Temperature == nil && config.TopP == nil && config.MaxOutputTokens == 0 &&
		len(config.StopSequences) == 0 && config.ThinkingConfig == nil {
		return nil
	}
	return config
}

func isAntigravityClaudeModel(modelName string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelName)), "claude")
}

// antigravitySessionID derives a stable session handle from the first user text
// so retries of one conversation land on the same upstream session.
func antigravitySessionID(contents []antigravity.Content) string {
	for _, content := range contents {
		if content.Role != antigravity.ROLE_USER {
			continue
		}
		for _, part := range content.Parts {
			if part.Text == "" {
				continue
			}
			sum := sha256.Sum256([]byte(part.Text))
			value := int64(binary.BigEndian.Uint64(sum[:8])) & 0x7FFFFFFFFFFFFFFF
			return "-" + strconv.FormatInt(value, 10)
		}
	}
	return ""
}
