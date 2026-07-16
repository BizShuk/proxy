package transform

import (
	"context"
	"testing"

	"github.com/bizshuk/proxy/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func noOpPair(from, to protocol.Format) Pair {
	return Pair{
		From: from,
		To:   to,
		Request: func(_ context.Context, request protocol.RequestEnvelope) (protocol.TransformResult, error) {
			return protocol.TransformResult{Body: request.Body}, nil
		},
		Response: func(_ context.Context, response protocol.ResponseEnvelope) (protocol.TransformResult, error) {
			return protocol.TransformResult{Body: response.Body}, nil
		},
		NewStream: func(protocol.Exchange) (StreamTransform, error) {
			return &terminalTestStream{}, nil
		},
	}
}

func TestNewRegistryRequiresNineUniqueCompletePairs(t *testing.T) {
	var pairs []Pair
	for _, from := range protocol.ALL_FORMATS {
		for _, to := range protocol.ALL_FORMATS {
			pairs = append(pairs, noOpPair(from, to))
		}
	}

	registry, err := NewRegistry(pairs...)
	require.NoError(t, err)
	_, ok := registry.Lookup(protocol.FORMAT_ANTHROPIC_MESSAGES, protocol.FORMAT_OPENAI_RESPONSES)
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

func (s *terminalTestStream) Push(_ context.Context, frame protocol.SSEFrame) ([]protocol.SSEFrame, error) {
	return []protocol.SSEFrame{frame}, nil
}

func (s *terminalTestStream) Close(context.Context) ([]protocol.SSEFrame, error) { return nil, nil }
