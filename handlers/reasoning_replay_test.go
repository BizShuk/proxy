package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlerCodexReasoningReplayCompletesTwoTurnToolLoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var upstreamCalls atomic.Int32
	upstreamBodies := make(chan []byte, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "cannot read request", http.StatusInternalServerError)
			return
		}
		upstreamBodies <- body
		w.Header().Set("Content-Type", "text/event-stream")
		switch upstreamCalls.Add(1) {
		case 1:
			_, _ = io.WriteString(w, codexReasoningToolCallSSE())
		case 2:
			_, _ = io.WriteString(w, codexToolResultSSE())
		default:
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	handler := newHandlerForCredential(t, oauthCred("openai", server.URL), server.Client())
	router := gin.New()
	router.POST("/model", handler.Handle(model.FORMAT_ANTHROPIC_MESSAGES))

	firstRequest, err := anthropic.Encode(anthropic.Request{
		Model:     "openai/gpt-5.6-sol",
		MaxTokens: 64,
		Messages: []anthropic.Message{{
			Role: "user", Content: anthropic.ContentList{{Type: "text", Text: "show the workspace path"}},
		}},
		Tools: []anthropic.Tool{{
			Name: "Bash", Description: "Run a command",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
		}},
	})
	require.NoError(t, err)
	firstResponse := httptest.NewRecorder()
	router.ServeHTTP(firstResponse, httptest.NewRequest(http.MethodPost, "/model", bytes.NewReader(firstRequest)))

	require.Equal(t, http.StatusOK, firstResponse.Code, firstResponse.Body.String())
	firstMessage, err := anthropic.DecodeResponse(firstResponse.Body.Bytes())
	require.NoError(t, err)
	require.Len(t, firstMessage.Content, 2)
	assert.Equal(t, "thinking", firstMessage.Content[0].Type)
	assert.Empty(t, firstMessage.Content[0].Thinking)
	require.NotEmpty(t, firstMessage.Content[0].Signature)
	assert.Equal(t, "tool_use", firstMessage.Content[1].Type)
	assert.Equal(t, "call_1", firstMessage.Content[1].ID)

	secondRequest, err := anthropic.Encode(anthropic.Request{
		Model:     "openai/gpt-5.6-sol",
		MaxTokens: 64,
		Messages: []anthropic.Message{
			{Role: "assistant", Content: firstMessage.Content},
			{Role: "user", Content: anthropic.ContentList{{
				Type: "tool_result", ToolUseID: "call_1", Content: json.RawMessage(`"/workspace"`),
			}}},
		},
		Tools: []anthropic.Tool{{
			Name: "Bash", Description: "Run a command",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
		}},
	})
	require.NoError(t, err)
	secondResponse := httptest.NewRecorder()
	router.ServeHTTP(secondResponse, httptest.NewRequest(http.MethodPost, "/model", bytes.NewReader(secondRequest)))

	require.Equal(t, http.StatusOK, secondResponse.Code, secondResponse.Body.String())
	secondMessage, err := anthropic.DecodeResponse(secondResponse.Body.Bytes())
	require.NoError(t, err)
	require.Len(t, secondMessage.Content, 1)
	assert.Equal(t, "done", secondMessage.Content[0].Text)
	assert.Equal(t, int32(2), upstreamCalls.Load())

	firstUpstreamBody := <-upstreamBodies
	assertCodexNormalizedRequest(t, firstUpstreamBody)
	secondUpstreamBody := <-upstreamBodies
	assertCodexNormalizedRequest(t, secondUpstreamBody)
	assertReasoningReplayToolResult(t, secondUpstreamBody)
}

func assertCodexNormalizedRequest(t *testing.T, body []byte) {
	t.Helper()
	var request map[string]any
	require.NoError(t, json.Unmarshal(body, &request))
	assert.Equal(t, true, request["stream"])
	assert.Equal(t, false, request["store"])
}

func assertReasoningReplayToolResult(t *testing.T, body []byte) {
	t.Helper()
	var request map[string]any
	require.NoError(t, json.Unmarshal(body, &request))
	input, ok := request["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 3)

	reasoning := input[0].(map[string]any)
	assert.Equal(t, "reasoning", reasoning["type"])
	assert.Equal(t, "reasoning_1", reasoning["id"])
	assert.Equal(t, "encrypted-reasoning", reasoning["encrypted_content"])
	require.Contains(t, reasoning, "summary")
	assert.Equal(t, []any{}, reasoning["summary"])

	functionCall := input[1].(map[string]any)
	assert.Equal(t, "function_call", functionCall["type"])
	assert.Equal(t, "call_1", functionCall["call_id"])
	assert.NotContains(t, functionCall, "summary")
	functionOutput := input[2].(map[string]any)
	assert.Equal(t, "function_call_output", functionOutput["type"])
	assert.Equal(t, "call_1", functionOutput["call_id"])
	assert.NotContains(t, functionOutput, "summary")
}

func codexReasoningToolCallSSE() string {
	return "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.6-sol\",\"status\":\"in_progress\"}}\n\n" +
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"reasoning_1\",\"type\":\"reasoning\",\"summary\":[],\"status\":\"in_progress\"}}\n\n" +
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"reasoning_1\",\"type\":\"reasoning\",\"summary\":[],\"encrypted_content\":\"encrypted-reasoning\",\"status\":\"completed\"}}\n\n" +
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"function_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"Bash\",\"arguments\":\"\"}}\n\n" +
		"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"function_1\",\"call_id\":\"call_1\",\"delta\":\"{\\\"command\\\":\\\"pwd\\\"}\"}\n\n" +
		"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"function_1\",\"call_id\":\"call_1\",\"arguments\":\"{\\\"command\\\":\\\"pwd\\\"}\"}\n\n" +
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"function_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"Bash\",\"arguments\":\"{\\\"command\\\":\\\"pwd\\\"}\"}}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.6-sol\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"total_tokens\":14}}}\n\n"
}

func codexToolResultSSE() string {
	return "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_2\",\"model\":\"gpt-5.6-sol\",\"status\":\"in_progress\"}}\n\n" +
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"message_1\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"in_progress\",\"content\":[]}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"message_1\",\"delta\":\"done\"}\n\n" +
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"message_1\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_2\",\"model\":\"gpt-5.6-sol\",\"status\":\"completed\",\"usage\":{\"input_tokens\":12,\"output_tokens\":2,\"total_tokens\":14}}}\n\n"
}
