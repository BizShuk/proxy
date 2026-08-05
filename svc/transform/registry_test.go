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
	for _, from := range model.CLIENT_FORMATS {
		for _, to := range model.CLIENT_FORMATS {
			pairs = append(pairs, noOpPair(from, to))
		}
	}
	for _, to := range model.PROVIDER_FORMATS {
		pairs = append(pairs, noOpPair(model.FORMAT_ANTHROPIC_MESSAGES, to))
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

// A provider-only format has no client decoder, so registering it as a source
// is a wiring bug the registry must reject rather than quietly accept.
func TestNewRegistryRejectsProviderFormatAsSource(t *testing.T) {
	var pairs []Pair
	for _, from := range model.CLIENT_FORMATS {
		for _, to := range model.CLIENT_FORMATS {
			pairs = append(pairs, noOpPair(from, to))
		}
	}
	pairs = append(pairs, noOpPair(model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_ANTIGRAVITY))

	_, err := NewRegistry(pairs...)
	require.NoError(t, err)

	_, err = NewRegistry(append(pairs, noOpPair(model.FORMAT_ANTIGRAVITY, model.FORMAT_ANTHROPIC_MESSAGES))...)
	require.ErrorContains(t, err, "provider-only")
}

// Declaring a provider format without any client able to reach it leaves dead
// routing surface, so construction must fail instead of shipping it.
func TestNewRegistryRequiresProviderFormatReachable(t *testing.T) {
	var pairs []Pair
	for _, from := range model.CLIENT_FORMATS {
		for _, to := range model.CLIENT_FORMATS {
			pairs = append(pairs, noOpPair(from, to))
		}
	}

	_, err := NewRegistry(pairs...)
	require.ErrorContains(t, err, "no client source")
}

type terminalTestStream struct{}

func (s *terminalTestStream) Push(_ context.Context, frame model.SSEFrame) ([]model.SSEFrame, error) {
	return []model.SSEFrame{frame}, nil
}

func (s *terminalTestStream) Close(context.Context) ([]model.SSEFrame, error) { return nil, nil }
