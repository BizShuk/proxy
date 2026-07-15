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
