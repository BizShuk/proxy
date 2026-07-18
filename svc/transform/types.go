// Package transform implements directed pairwise LLM protocol transforms.
package transform

import (
	"context"

	"github.com/bizshuk/proxy/model"
)

// RequestTransform converts a source request into a target request.
type RequestTransform func(context.Context, model.RequestEnvelope) (model.TransformResult, error)

// ResponseTransform converts a successful target response back to the source format.
type ResponseTransform func(context.Context, model.ResponseEnvelope) (model.TransformResult, error)

// StreamTransformFactory creates isolated state for one streaming exchange.
type StreamTransformFactory func(model.Exchange) (StreamTransform, error)

// StreamTransform converts complete SSE frames for one request.
type StreamTransform interface {
	Push(context.Context, model.SSEFrame) ([]model.SSEFrame, error)
	Close(context.Context) ([]model.SSEFrame, error)
}

// Pair owns both directions needed for one source-to-target exchange.
type Pair struct {
	From      model.Format
	To        model.Format
	Request   RequestTransform
	Response  ResponseTransform
	NewStream StreamTransformFactory
}

func newPair(
	from model.Format,
	to model.Format,
	request RequestTransform,
	response ResponseTransform,
	stream StreamTransformFactory,
) Pair {
	return Pair{From: from, To: to, Request: request, Response: response, NewStream: stream}
}
