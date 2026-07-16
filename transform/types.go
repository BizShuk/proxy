// Package transform implements directed pairwise LLM protocol transforms.
package transform

import (
	"context"

	"github.com/bizshuk/proxy/protocol"
)

// RequestTransform converts a source request into a target request.
type RequestTransform func(context.Context, protocol.RequestEnvelope) (protocol.TransformResult, error)

// ResponseTransform converts a successful target response back to the source format.
type ResponseTransform func(context.Context, protocol.ResponseEnvelope) (protocol.TransformResult, error)

// StreamTransformFactory creates isolated state for one streaming exchange.
type StreamTransformFactory func(protocol.Exchange) (StreamTransform, error)

// StreamTransform converts complete SSE frames for one request.
type StreamTransform interface {
	Push(context.Context, protocol.SSEFrame) ([]protocol.SSEFrame, error)
	Close(context.Context) ([]protocol.SSEFrame, error)
}

// Pair owns both directions needed for one source-to-target exchange.
type Pair struct {
	From      protocol.Format
	To        protocol.Format
	Request   RequestTransform
	Response  ResponseTransform
	NewStream StreamTransformFactory
}

func newPair(
	from protocol.Format,
	to protocol.Format,
	request RequestTransform,
	response ResponseTransform,
	stream StreamTransformFactory,
) Pair {
	return Pair{From: from, To: to, Request: request, Response: response, NewStream: stream}
}
