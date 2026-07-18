package transform

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/bizshuk/proxy/model/chat"
	"github.com/bizshuk/proxy/model/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const anthropicResponseFixture = `{
  "id":"msg_1","type":"message","role":"assistant","model":"claude",
  "content":[
    {"type":"thinking","thinking":"checked"},
    {"type":"text","text":"done"},
    {"type":"tool_use","id":"call_1","name":"read","input":{"path":"a.txt"}}
  ],
  "stop_reason":"tool_use",
  "usage":{"input_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":4}
}`

const chatResponseFixture = `{
  "id":"chat_1","object":"chat.completion","created":1,"model":"gpt",
  "choices":[{"index":0,"message":{"role":"assistant","content":"done","reasoning_content":"checked","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"a.txt\"}"}}]},"finish_reason":"tool_calls"}],
  "usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}
}`

const responsesResponseFixture = `{
  "id":"resp_1","object":"response","model":"gpt","status":"completed",
  "output":[
    {"id":"reason_1","type":"reasoning","summary":[{"type":"summary_text","text":"checked"}]},
    {"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]},
    {"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"a.txt\"}"}
  ],
  "usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}
}`

type responseSemantics struct {
	text, reasoning, toolName, callID string
	inputTokens, outputTokens         int
	stop                              string
}

func TestNonStreamResponseMatrix(t *testing.T) {
	tests := []struct {
		name       string
		transform  ResponseTransform
		body       string
		fromFormat model.Format
		toFormat   model.Format
	}{
		{name: "anthropic to chat", transform: AnthropicToChatResponse, body: anthropicResponseFixture, fromFormat: model.FORMAT_OPENAI_CHAT, toFormat: model.FORMAT_ANTHROPIC_MESSAGES},
		{name: "chat to anthropic", transform: ChatToAnthropicResponse, body: chatResponseFixture, fromFormat: model.FORMAT_ANTHROPIC_MESSAGES, toFormat: model.FORMAT_OPENAI_CHAT},
		{name: "anthropic to responses", transform: AnthropicToResponsesResponse, body: anthropicResponseFixture, fromFormat: model.FORMAT_OPENAI_RESPONSES, toFormat: model.FORMAT_ANTHROPIC_MESSAGES},
		{name: "responses to anthropic", transform: ResponsesToAnthropicResponse, body: responsesResponseFixture, fromFormat: model.FORMAT_ANTHROPIC_MESSAGES, toFormat: model.FORMAT_OPENAI_RESPONSES},
		{name: "chat to responses", transform: ChatToResponsesResponse, body: chatResponseFixture, fromFormat: model.FORMAT_OPENAI_RESPONSES, toFormat: model.FORMAT_OPENAI_CHAT},
		{name: "responses to chat", transform: ResponsesToChatResponse, body: responsesResponseFixture, fromFormat: model.FORMAT_OPENAI_CHAT, toFormat: model.FORMAT_OPENAI_RESPONSES},
	}

	want := responseSemantics{
		text: "done", reasoning: "checked", toolName: "read", callID: "call_1",
		inputTokens: 10, outputTokens: 4, stop: "tool",
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			envelope := model.ResponseEnvelope{
				Status: http.StatusOK, Body: []byte(tc.body),
				Exchange: model.Exchange{
					OriginalRequest:   model.RequestEnvelope{SourceFormat: tc.fromFormat, Model: "client-model"},
					TranslatedRequest: model.RequestEnvelope{TargetFormat: tc.toFormat, Model: "provider-model"},
					ProviderID:        "test-provider",
				},
			}
			result, err := tc.transform(context.Background(), envelope)
			require.NoError(t, err)
			assertResponseSemantics(t, tc.fromFormat, result.Body, want)
		})
	}
}

func TestAnthropicResponseReportsCacheBucketLoss(t *testing.T) {
	result, err := AnthropicToChatResponse(context.Background(), model.ResponseEnvelope{Body: []byte(anthropicResponseFixture)})
	require.NoError(t, err)
	require.NotEmpty(t, result.Losses)
	assert.Equal(t, "usage.cache_tokens", result.Losses[0].Field)
}

func TestResponseTransformRejectsMalformedToolArguments(t *testing.T) {
	body := []byte(`{"id":"chat_1","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{"}}]},"finish_reason":"tool_calls"}]}`)
	_, err := ChatToAnthropicResponse(context.Background(), model.ResponseEnvelope{Body: body})
	var proxyErr *model.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, model.ERROR_PROTOCOL, proxyErr.Kind)
}

func TestResponsesIncompleteMapsToLength(t *testing.T) {
	body := []byte(`{"id":"resp_1","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}],"usage":{"input_tokens":2,"output_tokens":1}}`)
	result, err := ResponsesToChatResponse(context.Background(), model.ResponseEnvelope{Body: body})
	require.NoError(t, err)
	value, err := chat.DecodeResponse(result.Body)
	require.NoError(t, err)
	require.Len(t, value.Choices, 1)
	assert.Equal(t, "length", value.Choices[0].FinishReason)
}

func TestDecodeUpstreamError(t *testing.T) {
	err := DecodeUpstreamError(http.StatusTooManyRequests, http.Header{
		"Retry-After":   {"7"},
		"x-request-id":  {"req_1"},
		"Authorization": {"Bearer secret"},
	}, []byte(`{"error":{"message":"slow","code":"quota"}}`))
	assert.Equal(t, model.ERROR_RATE_LIMIT, err.Kind)
	assert.Equal(t, 7*time.Second, err.RetryAfter)
	assert.Equal(t, "req_1", err.UpstreamRequestID)
	assert.Equal(t, "slow", err.Message)
	assert.Equal(t, "quota", err.Code)
}

func TestDecodeUpstreamErrorMapsRedirectsToBadGateway(t *testing.T) {
	for _, status := range []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			err := DecodeUpstreamError(status, http.Header{
				"Location":     {"https://redirected.example.com"},
				"x-request-id": {"req_redirect"},
			}, []byte(`{"error":{"message":"moved","code":"redirect"}}`))

			assert.Equal(t, model.ERROR_UPSTREAM, err.Kind)
			assert.Equal(t, http.StatusBadGateway, err.StatusCode())
			assert.Equal(t, "upstream_error", err.Code)
			assert.Equal(t, "upstream request failed", err.Message)
			assert.Equal(t, "req_redirect", err.UpstreamRequestID)
		})
	}

	rateLimit := DecodeUpstreamError(http.StatusTooManyRequests, nil, nil)
	assert.Equal(t, http.StatusTooManyRequests, rateLimit.StatusCode())
}

func TestDecodeUpstreamErrorDoesNotExposeHTML(t *testing.T) {
	err := DecodeUpstreamError(http.StatusBadGateway, nil, []byte(`<html>Bearer secret</html>`))
	assert.Equal(t, "upstream request failed", err.Message)
	assert.NotContains(t, err.Error(), "secret")
}

func assertResponseSemantics(t *testing.T, format model.Format, body []byte, want responseSemantics) {
	t.Helper()
	var got responseSemantics
	switch format {
	case model.FORMAT_ANTHROPIC_MESSAGES:
		value, err := anthropic.DecodeResponse(body)
		require.NoError(t, err)
		got.inputTokens = value.Usage.InputTokens + value.Usage.CacheCreationInputTokens + value.Usage.CacheReadInputTokens
		got.outputTokens = value.Usage.OutputTokens
		got.stop = normalizeStop(value.StopReason)
		for _, block := range value.Content {
			switch block.Type {
			case "text":
				got.text += block.Text
			case "thinking":
				got.reasoning += block.Thinking
			case "tool_use":
				got.toolName, got.callID = block.Name, block.ID
			}
		}
	case model.FORMAT_OPENAI_CHAT:
		value, err := chat.DecodeResponse(body)
		require.NoError(t, err)
		require.NotEmpty(t, value.Choices)
		got.inputTokens, got.outputTokens = value.Usage.PromptTokens, value.Usage.CompletionTokens
		got.stop = normalizeStop(value.Choices[0].FinishReason)
		message := value.Choices[0].Message
		if message.Content != nil {
			got.text = message.Content.Text
		}
		got.reasoning = message.ReasoningContent
		if len(message.ToolCalls) > 0 {
			got.toolName, got.callID = message.ToolCalls[0].Function.Name, message.ToolCalls[0].ID
		}
	case model.FORMAT_OPENAI_RESPONSES:
		value, err := responses.DecodeResponse(body)
		require.NoError(t, err)
		require.NotNil(t, value.Usage)
		got.inputTokens, got.outputTokens = value.Usage.InputTokens, value.Usage.OutputTokens
		got.stop = "stop"
		for _, item := range value.Output {
			switch item.Type {
			case "reasoning":
				for _, part := range item.Summary {
					got.reasoning += part.Text
				}
			case "message":
				for _, part := range item.Content {
					got.text += part.Text
				}
			case "function_call":
				got.toolName, got.callID, got.stop = item.Name, item.CallID, "tool"
			}
		}
	default:
		t.Fatalf("unknown response format %q", format)
	}
	assert.Equal(t, want, got)
}

func normalizeStop(stop string) string {
	switch stop {
	case "tool_use", "tool_calls":
		return "tool"
	case "max_tokens", "length":
		return "length"
	default:
		return "stop"
	}
}
