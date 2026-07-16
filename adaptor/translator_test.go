package adaptor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTranslateAnthropicToOpenAI(t *testing.T) {
	// 1. Text payload with system instructions
	temp := 0.7
	src := &AnthropicRequest{
		Model:       "claude-3-5-sonnet-latest",
		System:      "You are a helpful assistant",
		MaxTokens:   1000,
		Temperature: &temp,
		Messages: []AnthropicMessage{
			{
				Role: "user",
				Content: []AnthropicContent{
					{Type: "text", Text: "Hello"},
				},
			},
		},
	}

	dst := TranslateAnthropicToOpenAI(src)

	if dst.Model != src.Model {
		t.Errorf("expected model %q, got %q", src.Model, dst.Model)
	}
	if dst.MaxTokens != src.MaxTokens {
		t.Errorf("expected max_tokens %d, got %d", src.MaxTokens, dst.MaxTokens)
	}
	if dst.Temperature == nil || *dst.Temperature != *src.Temperature {
		t.Errorf("expected temperature %v, got %v", src.Temperature, dst.Temperature)
	}

	if len(dst.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(dst.Messages))
	}

	if dst.Messages[0].Role != "system" || dst.Messages[0].Content != "You are a helpful assistant" {
		t.Errorf("unexpected system message: %+v", dst.Messages[0])
	}
	if dst.Messages[1].Role != "user" || dst.Messages[1].Content != "Hello" {
		t.Errorf("unexpected user message: %+v", dst.Messages[1])
	}
}

func TestTranslateOpenAIToAnthropic(t *testing.T) {
	src := &OpenAIChatRequest{
		Model:     "gpt-4o",
		MaxTokens: 2000,
		Messages: []OpenAIMessage{
			{Role: "system", Content: "System prompt"},
			{Role: "user", Content: "Hello world"},
		},
	}

	dst := TranslateOpenAIToAnthropic(src)

	if dst.Model != src.Model {
		t.Errorf("expected model %q, got %q", src.Model, dst.Model)
	}
	if dst.MaxTokens != src.MaxTokens {
		t.Errorf("expected max_tokens %d, got %d", src.MaxTokens, dst.MaxTokens)
	}
	if dst.System != "System prompt" {
		t.Errorf("expected system prompt %q, got %v", "System prompt", dst.System)
	}

	if len(dst.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(dst.Messages))
	}
	if dst.Messages[0].Role != "user" || len(dst.Messages[0].Content) != 1 || dst.Messages[0].Content[0].Text != "Hello world" {
		t.Errorf("unexpected message mapping: %+v", dst.Messages[0])
	}
}

func TestTranslateOpenAIChunkToAnthropic(t *testing.T) {
	chunk := &OpenAIStreamChunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567,
		Model:   "gpt-4o",
		Choices: []OpenAIStreamChoice{
			{
				Index: 0,
				Delta: OpenAIStreamDelta{
					Content: "Hello",
				},
			},
		},
	}

	events, stopReason, stopSeq := TranslateOpenAIChunkToAnthropic(chunk)
	if len(events) != 2 {
		t.Fatalf("expected 2 stream events, got %d", len(events))
	}
	if events[0] != "content_block_delta" {
		t.Errorf("expected event 'content_block_delta', got %q", events[0])
	}
	if !strings.Contains(events[1], `"text": "Hello"`) {
		t.Errorf("expected text content 'Hello' in event data, got %q", events[1])
	}
	if stopReason != "" || stopSeq != "" {
		t.Errorf("did not expect stop reasons, got stopReason=%q stopSeq=%q", stopReason, stopSeq)
	}
}

func TestTranslateAnthropicSSEToOpenAI(t *testing.T) {
	data := `{"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "hello"}}`
	line, err := TranslateAnthropicSSEToOpenAI("content_block_delta", data, "chatcmpl-123", "claude-3-5-sonnet-latest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasPrefix(line, "data:") {
		t.Fatalf("expected data prefix, got %q", line)
	}

	var parsed map[string]any
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("failed to parse SSE JSON: %v", err)
	}

	choices, ok := parsed["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("missing choices: %+v", parsed)
	}
	choice := choices[0].(map[string]any)
	delta := choice["delta"].(map[string]any)
	if delta["content"] != "hello" {
		t.Errorf("expected content 'hello', got %v", delta["content"])
	}
}

func TestUnmarshalAnthropicMessage(t *testing.T) {
	// Case 1: content is a string
	rawStr := `{"role": "user", "content": "hello"}`
	var msg1 AnthropicMessage
	if err := json.Unmarshal([]byte(rawStr), &msg1); err != nil {
		t.Fatalf("unexpected error unmarshaling string content: %v", err)
	}
	if msg1.Role != "user" {
		t.Errorf("expected role 'user', got %q", msg1.Role)
	}
	if len(msg1.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg1.Content))
	}
	if msg1.Content[0].Type != "text" || msg1.Content[0].Text != "hello" {
		t.Errorf("unexpected content block: %+v", msg1.Content[0])
	}

	// Case 2: content is a block list
	rawBlock := `{"role": "user", "content": [{"type": "text", "text": "hello"}]}`
	var msg2 AnthropicMessage
	if err := json.Unmarshal([]byte(rawBlock), &msg2); err != nil {
		t.Fatalf("unexpected error unmarshaling block content: %v", err)
	}
	if msg2.Role != "user" {
		t.Errorf("expected role 'user', got %q", msg2.Role)
	}
	if len(msg2.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg2.Content))
	}
	if msg2.Content[0].Type != "text" || msg2.Content[0].Text != "hello" {
		t.Errorf("unexpected content block: %+v", msg2.Content[0])
	}
}

func TestTranslateAnthropicToOpenAIExtra(t *testing.T) {
	// Test 1: Tool Choice Translation
	req := &AnthropicRequest{
		Model: "claude-3-5-sonnet-latest",
		ToolChoice: map[string]any{
			"type": "tool",
			"name": "get_weather",
		},
	}
	openaiReq := TranslateAnthropicToOpenAI(req)
	tcMap, ok := openaiReq.ToolChoice.(map[string]any)
	if !ok {
		t.Fatalf("expected ToolChoice to be mapped to a map, got %T", openaiReq.ToolChoice)
	}
	if tcMap["type"] != "function" {
		t.Errorf("expected type 'function', got %v", tcMap["type"])
	}
	fnMap, ok := tcMap["function"].(map[string]any)
	if !ok || fnMap["name"] != "get_weather" {
		t.Errorf("expected function name 'get_weather', got %+v", tcMap["function"])
	}

	// Test 2: Multimodal Image Translation
	reqMultimodal := &AnthropicRequest{
		Model: "claude-3-5-sonnet-latest",
		Messages: []AnthropicMessage{
			{
				Role: "user",
				Content: []AnthropicContent{
					{Type: "text", Text: "Look at this image:"},
					{
						Type: "image",
						Source: &AnthropicSource{
							Type:      "base64",
							MediaType: "image/jpeg",
							Data:      "dGVzdGltYWdl", // "testimage" in base64
						},
					},
				},
			},
		},
	}

	openaiReqMM := TranslateAnthropicToOpenAI(reqMultimodal)
	if len(openaiReqMM.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(openaiReqMM.Messages))
	}
	msg := openaiReqMM.Messages[0]
	if msg.Role != "user" {
		t.Errorf("expected role 'user', got %q", msg.Role)
	}
	parts, ok := msg.Content.([]OpenAIContentPart)
	if !ok {
		t.Fatalf("expected Content to be []OpenAIContentPart, got %T", msg.Content)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "Look at this image:" {
		t.Errorf("unexpected first content part: %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "data:image/jpeg;base64,dGVzdGltYWdl" {
		t.Errorf("unexpected second content part: %+v", parts[1])
	}
}

func TestTranslateReasoningAndThinking(t *testing.T) {
	// Test 1: TranslateAnthropicToOpenAI with Thinking enabled
	req := &AnthropicRequest{
		Model: "claude-3-5-sonnet-latest",
		Thinking: &AnthropicThinking{
			Type:         "enabled",
			BudgetTokens: 2048,
		},
		Messages: []AnthropicMessage{
			{
				Role: "assistant",
				Content: []AnthropicContent{
					{Type: "thinking", Thinking: "Let me think..."},
					{Type: "text", Text: "Hello!"},
				},
			},
		},
	}
	openaiReq := TranslateAnthropicToOpenAI(req)
	if openaiReq.ReasoningEffort != "medium" {
		t.Errorf("expected ReasoningEffort 'medium', got %q", openaiReq.ReasoningEffort)
	}
	if len(openaiReq.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(openaiReq.Messages))
	}
	if openaiReq.Messages[0].ReasoningContent != "Let me think..." {
		t.Errorf("expected ReasoningContent 'Let me think...', got %q", openaiReq.Messages[0].ReasoningContent)
	}
	if openaiReq.Messages[0].Content != "Hello!" {
		t.Errorf("expected Content 'Hello!', got %v", openaiReq.Messages[0].Content)
	}

	// Test 2: TranslateOpenAIToAnthropic with ReasoningEffort set
	openAIReq := &OpenAIChatRequest{
		Model:           "o1-mini",
		ReasoningEffort: "high",
		Messages: []OpenAIMessage{
			{
				Role:             "assistant",
				Content:          "Hi there!",
				ReasoningContent: "Thinking high effort...",
			},
		},
	}
	anthropicReq := TranslateOpenAIToAnthropic(openAIReq)
	if anthropicReq.Thinking == nil || anthropicReq.Thinking.Type != "enabled" || anthropicReq.Thinking.BudgetTokens != 4096 {
		t.Errorf("unexpected Thinking config: %+v", anthropicReq.Thinking)
	}
	if len(anthropicReq.Messages) != 1 {
		t.Fatalf("expected 1 Anthropic message, got %d", len(anthropicReq.Messages))
	}
	msg := anthropicReq.Messages[0]
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(msg.Content))
	}
	if msg.Content[0].Type != "thinking" || msg.Content[0].Thinking != "Thinking high effort..." {
		t.Errorf("unexpected thinking block: %+v", msg.Content[0])
	}
	if msg.Content[1].Type != "text" || msg.Content[1].Text != "Hi there!" {
		t.Errorf("unexpected text block: %+v", msg.Content[1])
	}

	// Test 3: TranslateAnthropicSSEToOpenAI with thinking delta
	sseLine, err := TranslateAnthropicSSEToOpenAI("content_block_delta", `{"type": "content_block_delta", "index": 0, "delta": {"type": "thinking_delta", "thinking": "Step 1"}}`, "chat-123", "o1")
	if err != nil {
		t.Fatalf("failed to translate Anthropic SSE: %v", err)
	}
	if !strings.Contains(sseLine, `"reasoning_content":"Step 1"`) {
		t.Errorf("expected translated SSE line to contain reasoning_content, got %q", sseLine)
	}

	// Test 4: TranslateOpenAIChunkToAnthropic with reasoning_content delta
	chunk := &OpenAIStreamChunk{
		Choices: []OpenAIStreamChoice{
			{
				Index: 0,
				Delta: OpenAIStreamDelta{
					ReasoningContent: "Thinking delta",
				},
			},
		},
	}
	events, _, _ := TranslateOpenAIChunkToAnthropic(chunk)
	if len(events) != 2 || events[0] != "content_block_delta" || !strings.Contains(events[1], `"thinking": "Thinking delta"`) {
		t.Errorf("unexpected events: %+v", events)
	}
}
