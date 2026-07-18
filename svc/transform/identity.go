package transform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/bizshuk/proxy/model/chat"
	"github.com/bizshuk/proxy/model/responses"
)

// AnthropicIdentity returns the validated Anthropic-to-Anthropic pair.
func AnthropicIdentity() Pair {
	return identityPair(model.FORMAT_ANTHROPIC_MESSAGES)
}

// ChatIdentity returns the validated Chat-to-Chat pair.
func ChatIdentity() Pair {
	return identityPair(model.FORMAT_OPENAI_CHAT)
}

// ResponsesIdentity returns the validated Responses-to-Responses pair.
func ResponsesIdentity() Pair {
	return identityPair(model.FORMAT_OPENAI_RESPONSES)
}

func identityPair(format model.Format) Pair {
	return newPair(
		format,
		format,
		identityRequest(format),
		identityResponse(format),
		func(model.Exchange) (StreamTransform, error) {
			return &identityStream{format: format}, nil
		},
	)
}

func identityRequest(format model.Format) RequestTransform {
	return func(ctx context.Context, envelope model.RequestEnvelope) (model.TransformResult, error) {
		if err := ctx.Err(); err != nil {
			return model.TransformResult{}, err
		}
		switch format {
		case model.FORMAT_ANTHROPIC_MESSAGES:
			request, err := anthropic.DecodeRequest(envelope.Body)
			if err != nil {
				return model.TransformResult{}, invalidRequestError(err)
			}
			request.Model = envelope.Model
			return encodeIdentity(anthropic.Encode(request))
		case model.FORMAT_OPENAI_CHAT:
			request, err := chat.DecodeRequest(envelope.Body)
			if err != nil {
				return model.TransformResult{}, invalidRequestError(err)
			}
			request.Model = envelope.Model
			return encodeIdentity(chat.Encode(request))
		case model.FORMAT_OPENAI_RESPONSES:
			request, err := responses.DecodeRequest(envelope.Body)
			if err != nil {
				return model.TransformResult{}, invalidRequestError(err)
			}
			request.Model = envelope.Model
			return encodeIdentity(responses.Encode(request))
		default:
			return model.TransformResult{}, fmt.Errorf("identity request: unknown format %q", format)
		}
	}
}

func identityResponse(format model.Format) ResponseTransform {
	return func(ctx context.Context, envelope model.ResponseEnvelope) (model.TransformResult, error) {
		if err := ctx.Err(); err != nil {
			return model.TransformResult{}, err
		}
		switch format {
		case model.FORMAT_ANTHROPIC_MESSAGES:
			response, err := anthropic.DecodeResponse(envelope.Body)
			if err != nil {
				return model.TransformResult{}, protocolFailure(err)
			}
			return encodeIdentity(anthropic.Encode(response))
		case model.FORMAT_OPENAI_CHAT:
			response, err := chat.DecodeResponse(envelope.Body)
			if err != nil {
				return model.TransformResult{}, protocolFailure(err)
			}
			return encodeIdentity(chat.Encode(response))
		case model.FORMAT_OPENAI_RESPONSES:
			response, err := responses.DecodeResponse(envelope.Body)
			if err != nil {
				return model.TransformResult{}, protocolFailure(err)
			}
			return encodeIdentity(responses.Encode(response))
		default:
			return model.TransformResult{}, fmt.Errorf("identity response: unknown format %q", format)
		}
	}
}

func encodeIdentity(body []byte, err error) (model.TransformResult, error) {
	if err != nil {
		return model.TransformResult{}, protocolFailure(err)
	}
	return model.TransformResult{Body: body}, nil
}

type identityStream struct {
	format   model.Format
	terminal bool
}

func (s *identityStream) Push(ctx context.Context, frame model.SSEFrame) ([]model.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.terminal {
		return nil, protocolFailure(fmt.Errorf("frame received after terminal event"))
	}
	if len(frame.Data) > 0 && !bytes.Equal(frame.Data, []byte("[DONE]")) && !json.Valid(frame.Data) {
		return nil, protocolFailure(fmt.Errorf("invalid JSON data frame"))
	}
	s.terminal = isTerminalFrame(s.format, frame)
	return []model.SSEFrame{frame}, nil
}

func (s *identityStream) Close(ctx context.Context) ([]model.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.terminal {
		return nil, protocolFailure(fmt.Errorf("stream ended before terminal event"))
	}
	return nil, nil
}

func isTerminalFrame(format model.Format, frame model.SSEFrame) bool {
	switch format {
	case model.FORMAT_ANTHROPIC_MESSAGES:
		return frame.Event == "message_stop" || frame.Event == "error" || dataType(frame.Data) == "message_stop" || dataType(frame.Data) == "error"
	case model.FORMAT_OPENAI_CHAT:
		return bytes.Equal(bytes.TrimSpace(frame.Data), []byte("[DONE]"))
	case model.FORMAT_OPENAI_RESPONSES:
		return frame.Event == "response.completed" || frame.Event == "response.failed" || dataType(frame.Data) == "response.completed" || dataType(frame.Data) == "response.failed"
	default:
		return false
	}
}

func dataType(data []byte) string {
	var value struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return ""
	}
	return value.Type
}

func invalidRequestError(err error) error {
	return &model.ProxyError{
		Kind: model.ERROR_INVALID_REQUEST, Status: http.StatusBadRequest,
		Code: "invalid_request", Message: "invalid request", Cause: err,
	}
}

func protocolFailure(err error) error {
	return &model.ProxyError{
		Kind: model.ERROR_PROTOCOL, Status: http.StatusBadGateway,
		Code: "protocol_error", Message: "upstream protocol error", Cause: err,
	}
}
