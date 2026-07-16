package route

import (
	"testing"

	"github.com/bizshuk/proxy/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouterResolve(t *testing.T) {
	router, err := NewRouter([]Profile{
		{ID: "anthropic", Qualifiers: []string{"anthropic"}, Prefixes: []string{"claude-"}},
		{ID: "openai", Qualifiers: []string{"openai", "openai-chat"}, Prefixes: []string{"gpt-", "o1-", "o3-"}},
		{ID: "xai", Qualifiers: []string{"xai", "xai-chat"}, Prefixes: []string{"grok-"}},
		{ID: "minimax", Qualifiers: []string{"minimax"}, ExactModels: []string{"MiniMax-Text-01"}, Prefixes: []string{"minimax-"}},
	})
	require.NoError(t, err)

	tests := []struct {
		model        string
		wantProvider string
		wantModel    string
		wantForced   *protocol.Format
		wantErr      bool
	}{
		{model: "xai/grok-4.5", wantProvider: "xai", wantModel: "grok-4.5"},
		{model: "xai-chat/grok-4.5", wantProvider: "xai", wantModel: "grok-4.5", wantForced: formatPtr(protocol.FORMAT_OPENAI_CHAT)},
		{model: "gpt-5", wantProvider: "openai", wantModel: "gpt-5"},
		{model: "openai-chat/gpt-5", wantProvider: "openai", wantModel: "gpt-5", wantForced: formatPtr(protocol.FORMAT_OPENAI_CHAT)},
		{model: "claude-3-5-sonnet-latest", wantProvider: "anthropic", wantModel: "claude-3-5-sonnet-latest"},
		{model: "MINIMAX-TEXT-01", wantProvider: "minimax", wantModel: "MINIMAX-TEXT-01"},
		{model: "unknown-model", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			got, err := router.Resolve(protocol.FORMAT_OPENAI_CHAT, tc.model)
			if tc.wantErr {
				var proxyErr *protocol.ProxyError
				require.ErrorAs(t, err, &proxyErr)
				assert.Equal(t, protocol.ERROR_UNKNOWN_MODEL, proxyErr.Kind)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantProvider, got.ProviderID)
			assert.Equal(t, tc.wantModel, got.Model)
			assert.Equal(t, tc.wantForced, got.ForcedTarget)
		})
	}
}

func TestNewRouterRejectsAmbiguousConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		profiles []Profile
	}{
		{
			name: "duplicate profile ID",
			profiles: []Profile{
				{ID: "openai", Qualifiers: []string{"openai"}, Prefixes: []string{"gpt-"}},
				{ID: "OPENAI", Qualifiers: []string{"second"}, Prefixes: []string{"other-"}},
			},
		},
		{
			name: "duplicate qualifier",
			profiles: []Profile{
				{ID: "openai", Qualifiers: []string{"openai"}, Prefixes: []string{"gpt-"}},
				{ID: "gateway", Qualifiers: []string{"OPENAI"}, Prefixes: []string{"other-"}},
			},
		},
		{
			name:     "empty prefix",
			profiles: []Profile{{ID: "openai", Qualifiers: []string{"openai"}, Prefixes: []string{""}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRouter(tc.profiles)
			require.Error(t, err)
		})
	}
}

func TestRouterResolveRejectsAmbiguousUnqualifiedModel(t *testing.T) {
	router, err := NewRouter([]Profile{
		{ID: "first", Qualifiers: []string{"first"}, Prefixes: []string{"shared-"}},
		{ID: "second", Qualifiers: []string{"second"}, Prefixes: []string{"shared-"}},
	})
	require.NoError(t, err)

	_, err = router.Resolve(protocol.FORMAT_OPENAI_RESPONSES, "shared-model")
	var proxyErr *protocol.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, protocol.ERROR_UNKNOWN_MODEL, proxyErr.Kind)
}

func formatPtr(value protocol.Format) *protocol.Format {
	return &value
}
