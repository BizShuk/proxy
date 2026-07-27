package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptionsCommandListsCodexBeforeGrokImageModels(t *testing.T) {
	command, _, err := ProxyCmd.Find([]string{"options"})
	require.NoError(t, err)

	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	require.NoError(t, command.RunE(command, nil))

	text := output.String()
	codexIndex := strings.Index(text, "Codex models:")
	openAIImageIndex := strings.Index(text, "OpenAI image-generation models:")
	grokImageIndex := strings.Index(text, "Grok image-generation models:")
	require.NotEqual(t, -1, codexIndex)
	require.NotEqual(t, -1, openAIImageIndex)
	require.NotEqual(t, -1, grokImageIndex)
	assert.Less(t, codexIndex, openAIImageIndex)
	assert.Less(t, openAIImageIndex, grokImageIndex)

	for _, model := range []string{
		"gpt-5",
		"gpt-5-mini",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-image-2",
		"grok-imagine-image-quality",
	} {
		assert.Contains(t, text, "  "+model)
	}
}

func TestOptionsCommandHasModelsAlias(t *testing.T) {
	command, _, err := ProxyCmd.Find([]string{"models"})

	require.NoError(t, err)
	assert.Equal(t, "options", command.Name())
}
