package mcpimage

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GenerateImageInput is the MCP tool input.
type GenerateImageInput struct {
	Prompt      string `json:"prompt" jsonschema:"the complete image prompt"`
	Model       string `json:"model,omitempty" jsonschema:"optional image model override"`
	N           int    `json:"n,omitempty" jsonschema:"number of images, from 1 to 10"`
	AspectRatio string `json:"aspect_ratio,omitempty" jsonschema:"optional aspect ratio such as 1:1 or 16:9"`
	Resolution  string `json:"resolution,omitempty" jsonschema:"optional resolution such as 1k or 2k"`
}

// GenerateImageOutput is the MCP tool's structured result.
type GenerateImageOutput struct {
	Model string               `json:"model"`
	Files []GeneratedImageFile `json:"files"`
}

// GeneratedImageFile describes one image written to disk.
type GeneratedImageFile struct {
	Path          string `json:"path"`
	MIMEType      string `json:"mime_type"`
	Bytes         int    `json:"bytes"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type imageClient interface {
	Generate(context.Context, GenerateRequest) ([]GeneratedImage, error)
}

// Generator implements the generate_image MCP tool.
type Generator struct {
	client       imageClient
	defaultModel string
	outputDir    string
	projectDir   string
}

// NewGenerator returns an image generator backed by the proxy client.
func NewGenerator(client imageClient, cfg Config) (*Generator, error) {
	if client == nil {
		return nil, fmt.Errorf("new image generator: client is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("new image generator: default model must not be blank")
	}
	if strings.TrimSpace(cfg.OutputDir) == "" {
		return nil, fmt.Errorf("new image generator: output directory must not be blank")
	}
	return &Generator{
		client:       client,
		defaultModel: cfg.Model,
		outputDir:    cfg.OutputDir,
		projectDir:   cfg.ProjectDir,
	}, nil
}

// Generate calls the proxy, saves every image, and returns file and inline
// image content to the MCP client.
func (g *Generator) Generate(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input GenerateImageInput,
) (*mcp.CallToolResult, GenerateImageOutput, error) {
	if strings.TrimSpace(input.Prompt) == "" {
		return nil, GenerateImageOutput{}, fmt.Errorf("generate_image: prompt must not be blank")
	}
	if input.N < 0 || input.N > 10 {
		return nil, GenerateImageOutput{}, fmt.Errorf(
			"generate_image: n must be between 1 and 10 when provided",
		)
	}

	modelName := strings.TrimSpace(input.Model)
	if modelName == "" {
		modelName = g.defaultModel
	}
	rootDir, outputDir, err := g.resolveOutputDir(ctx, request)
	if err != nil {
		return nil, GenerateImageOutput{}, err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, GenerateImageOutput{}, fmt.Errorf("generate_image: create output directory: %w", err)
	}

	images, err := g.client.Generate(ctx, GenerateRequest{
		Model:       modelName,
		Prompt:      input.Prompt,
		N:           input.N,
		AspectRatio: input.AspectRatio,
		Resolution:  input.Resolution,
	})
	if err != nil {
		return nil, GenerateImageOutput{}, err
	}
	if len(images) == 0 {
		return nil, GenerateImageOutput{}, fmt.Errorf("generate_image: proxy returned no images")
	}

	output := GenerateImageOutput{
		Model: modelName,
		Files: make([]GeneratedImageFile, 0, len(images)),
	}
	content := make([]mcp.Content, 0, len(images)+1)
	savedPaths := make([]string, 0, len(images))
	completed := false
	defer func() {
		if completed {
			return
		}
		for _, savedPath := range savedPaths {
			_ = os.Remove(savedPath)
		}
	}()

	for _, image := range images {
		savedPath, err := saveImage(outputDir, image)
		if err != nil {
			return nil, GenerateImageOutput{}, err
		}
		savedPaths = append(savedPaths, savedPath)
		displayPath := displayPath(rootDir, savedPath)
		output.Files = append(output.Files, GeneratedImageFile{
			Path:          displayPath,
			MIMEType:      image.MIMEType,
			Bytes:         len(image.Data),
			RevisedPrompt: image.RevisedPrompt,
		})
		content = append(content, &mcp.ImageContent{
			Data:     image.Data,
			MIMEType: image.MIMEType,
		})
	}

	var summary strings.Builder
	summary.WriteString("Generated image files:\n")
	for _, file := range output.Files {
		summary.WriteString("- ")
		summary.WriteString(file.Path)
		summary.WriteByte('\n')
	}
	content = append([]mcp.Content{&mcp.TextContent{Text: strings.TrimSpace(summary.String())}}, content...)
	completed = true
	return &mcp.CallToolResult{Content: content}, output, nil
}

func (g *Generator) resolveOutputDir(
	ctx context.Context,
	request *mcp.CallToolRequest,
) (string, string, error) {
	if filepath.IsAbs(g.outputDir) {
		return "", filepath.Clean(g.outputDir), nil
	}
	cleanOutputDir := filepath.Clean(g.outputDir)
	if cleanOutputDir == ".." || strings.HasPrefix(cleanOutputDir, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("generate_image: relative output directory must stay inside the project")
	}

	rootDir := ""
	if request != nil && request.Session != nil {
		roots, err := request.Session.ListRoots(ctx, nil)
		if err == nil && len(roots.Roots) > 0 {
			rootDir = fileRootPath(roots.Roots[0].URI)
		}
		if ctx.Err() != nil {
			return "", "", fmt.Errorf("generate_image: resolve project root: %w", ctx.Err())
		}
	}
	if rootDir == "" {
		rootDir = strings.TrimSpace(g.projectDir)
	}
	if rootDir == "" {
		workingDir, err := os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("generate_image: resolve working directory: %w", err)
		}
		rootDir = workingDir
	}
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return "", "", fmt.Errorf("generate_image: resolve absolute project root: %w", err)
	}
	return rootDir, filepath.Join(rootDir, cleanOutputDir), nil
}

func fileRootPath(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" {
		return ""
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		return ""
	}
	pathValue, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return ""
	}
	return filepath.FromSlash(pathValue)
}

func saveImage(outputDir string, image GeneratedImage) (string, error) {
	extension, err := imageExtension(image.MIMEType)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(outputDir, "proxy-imagegen-*"+extension)
	if err != nil {
		return "", fmt.Errorf("generate_image: create image file: %w", err)
	}
	filePath := file.Name()
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(filePath)
		}
	}()

	if _, err := file.Write(image.Data); err != nil {
		return "", fmt.Errorf("generate_image: write image file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("generate_image: close image file: %w", err)
	}
	success = true
	return filePath, nil
}

func imageExtension(mimeType string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png":
		return ".png", nil
	case "image/jpeg":
		return ".jpg", nil
	case "image/webp":
		return ".webp", nil
	case "image/gif":
		return ".gif", nil
	default:
		return "", fmt.Errorf("generate_image: unsupported image MIME type %q", mimeType)
	}
}

func displayPath(rootDir, filePath string) string {
	if rootDir == "" {
		return filePath
	}
	relative, err := filepath.Rel(rootDir, filePath)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filePath
	}
	return filepath.ToSlash(relative)
}
