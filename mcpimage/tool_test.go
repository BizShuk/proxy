package mcpimage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratorGenerateSavesFilesAndReturnsImageContent(t *testing.T) {
	outputDir := t.TempDir()
	client := &recordingImageClient{
		images: []GeneratedImage{
			{
				Data:          testPNGBytes(),
				MIMEType:      "image/png",
				RevisedPrompt: "a revised prompt",
			},
		},
	}
	generator, err := NewGenerator(client, Config{
		APIKey:    "proxy-secret",
		Model:     DEFAULT_MODEL,
		OutputDir: outputDir,
	})
	require.NoError(t, err)

	result, output, err := generator.Generate(
		context.Background(),
		nil,
		GenerateImageInput{Prompt: "a cat in space"},
	)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	require.Len(t, output.Files, 1)
	assert.Equal(t, DEFAULT_MODEL, output.Model)
	assert.Equal(t, "image/png", output.Files[0].MIMEType)
	assert.Equal(t, "a revised prompt", output.Files[0].RevisedPrompt)
	assert.True(t, filepath.IsAbs(output.Files[0].Path))
	saved, err := os.ReadFile(output.Files[0].Path)
	require.NoError(t, err)
	assert.Equal(t, testPNGBytes(), saved)

	require.Len(t, result.Content, 2)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, output.Files[0].Path)
	image, ok := result.Content[1].(*mcp.ImageContent)
	require.True(t, ok)
	assert.Equal(t, testPNGBytes(), image.Data)
	assert.Equal(t, "image/png", image.MIMEType)

	require.Len(t, client.requests, 1)
	assert.Equal(t, DEFAULT_MODEL, client.requests[0].Model)
	assert.Equal(t, "a cat in space", client.requests[0].Prompt)
}

func TestGeneratorGenerateForwardsImageOptions(t *testing.T) {
	client := &recordingImageClient{
		images: []GeneratedImage{{Data: testPNGBytes(), MIMEType: "image/png"}},
	}
	generator, err := NewGenerator(client, Config{
		APIKey:    "proxy-secret",
		Model:     DEFAULT_MODEL,
		OutputDir: t.TempDir(),
	})
	require.NoError(t, err)

	_, _, err = generator.Generate(context.Background(), nil, GenerateImageInput{
		Prompt:      "a cat in space",
		Model:       "custom-image-model",
		N:           2,
		AspectRatio: "16:9",
		Resolution:  "2k",
	})

	require.NoError(t, err)
	require.Len(t, client.requests, 1)
	assert.Equal(t, GenerateRequest{
		Prompt:      "a cat in space",
		Model:       "custom-image-model",
		N:           2,
		AspectRatio: "16:9",
		Resolution:  "2k",
	}, client.requests[0])
}

func TestGeneratorGenerateSavesRelativeOutputInsideProject(t *testing.T) {
	projectDir := t.TempDir()
	client := &recordingImageClient{
		images: []GeneratedImage{{Data: testPNGBytes(), MIMEType: "image/png"}},
	}
	generator, err := NewGenerator(client, Config{
		APIKey:     "proxy-secret",
		Model:      DEFAULT_MODEL,
		OutputDir:  "images/generated",
		ProjectDir: projectDir,
	})
	require.NoError(t, err)

	_, output, err := generator.Generate(
		context.Background(),
		nil,
		GenerateImageInput{Prompt: "a cat in space"},
	)

	require.NoError(t, err)
	require.Len(t, output.Files, 1)
	assert.True(t, strings.HasPrefix(output.Files[0].Path, "images/generated/"))
	_, err = os.Stat(filepath.Join(projectDir, filepath.FromSlash(output.Files[0].Path)))
	require.NoError(t, err)
}

func TestGeneratorGenerateRejectsInvalidInputBeforeCallingProxy(t *testing.T) {
	client := &recordingImageClient{}
	generator, err := NewGenerator(client, Config{
		APIKey:    "proxy-secret",
		Model:     DEFAULT_MODEL,
		OutputDir: t.TempDir(),
	})
	require.NoError(t, err)

	tests := []GenerateImageInput{
		{Prompt: " "},
		{Prompt: "cat", N: -1},
		{Prompt: "cat", N: 11},
	}
	for _, input := range tests {
		_, _, err := generator.Generate(context.Background(), nil, input)
		require.Error(t, err)
	}
	assert.Empty(t, client.requests)
}

func TestGeneratorGenerateRejectsEscapingOutputBeforeCallingProxy(t *testing.T) {
	client := &recordingImageClient{
		images: []GeneratedImage{{Data: testPNGBytes(), MIMEType: "image/png"}},
	}
	generator, err := NewGenerator(client, Config{
		APIKey:     "proxy-secret",
		Model:      DEFAULT_MODEL,
		OutputDir:  "../outside",
		ProjectDir: t.TempDir(),
	})
	require.NoError(t, err)

	_, _, err = generator.Generate(
		context.Background(),
		nil,
		GenerateImageInput{Prompt: "a cat in space"},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must stay inside the project")
	assert.Empty(t, client.requests)
}

type recordingImageClient struct {
	requests []GenerateRequest
	images   []GeneratedImage
	err      error
}

func (c *recordingImageClient) Generate(_ context.Context, request GenerateRequest) ([]GeneratedImage, error) {
	c.requests = append(c.requests, request)
	return c.images, c.err
}
