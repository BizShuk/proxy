package transform

import (
	"context"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func noOpPair(from, to model.Format) Pair {
	return Pair{
		From: from,
		To:   to,
		Request: func(_ context.Context, request model.RequestEnvelope) (model.TransformResult, error) {
			return model.TransformResult{Body: request.Body}, nil
		},
		Response: func(_ context.Context, response model.ResponseEnvelope) (model.TransformResult, error) {
			return model.TransformResult{Body: response.Body}, nil
		},
		NewStream: func(model.Exchange) (StreamTransform, error) {
			return &terminalTestStream{}, nil
		},
	}
}

func TestNewRegistryRequiresNineUniqueCompletePairs(t *testing.T) {
	var pairs []Pair
	for _, from := range model.ALL_FORMATS {
		for _, to := range model.ALL_FORMATS {
			pairs = append(pairs, noOpPair(from, to))
		}
	}

	registry, err := NewRegistry(pairs...)
	require.NoError(t, err)
	_, ok := registry.Lookup(model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_OPENAI_RESPONSES)
	assert.True(t, ok)

	_, err = NewRegistry(pairs[:8]...)
	require.ErrorContains(t, err, "missing pair")
	_, err = NewRegistry(append(pairs, pairs[0])...)
	require.ErrorContains(t, err, "duplicate pair")

	nilRequest := append([]Pair(nil), pairs...)
	nilRequest[0].Request = nil
	_, err = NewRegistry(nilRequest...)
	require.ErrorContains(t, err, "nil request")
}

type terminalTestStream struct{}

func (s *terminalTestStream) Push(_ context.Context, frame model.SSEFrame) ([]model.SSEFrame, error) {
	return []model.SSEFrame{frame}, nil
}

func (s *terminalTestStream) Close(context.Context) ([]model.SSEFrame, error) { return nil, nil }
