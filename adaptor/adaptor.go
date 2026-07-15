package adaptor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bizshuk/agentsdk/auth"
	"github.com/bizshuk/agentsdk/auth/provider"
	"github.com/bizshuk/agentsdk/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DEFAULT_EXPIRY_SKEW is the lead buffer to check for token expiration.
const DEFAULT_EXPIRY_SKEW = 5 * time.Minute

// Adaptor coordinates model routing, credential resolution, protocol translation, and proxying.
type Adaptor struct {
	cfg   *config.ProxyConfig
	store *auth.FileStore
}

// New creates a new Adaptor instance.
func New(cfg *config.ProxyConfig) (*Adaptor, error) {
	store, err := auth.NewFileStore(cfg.AuthDir)
	if err != nil {
		return nil, err
	}
	return &Adaptor{cfg: cfg, store: store}, nil
}

// loadActiveMap loads active credentials mapping from active.json.
func (a *Adaptor) loadActiveMap() (map[string]string, error) {
	activePath := filepath.Join(a.store.Dir(), "active.json")
	data, err := os.ReadFile(activePath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, err
	}
	var active map[string]string
	if err := json.Unmarshal(data, &active); err != nil {
		return nil, err
	}
	return active, nil
}

// loadCredential resolves a credential for the given provider.
func (a *Adaptor) loadCredential(providerName string) (*auth.Credential, error) {
	active, err := a.loadActiveMap()
	if err == nil {
		if credName, ok := active[providerName]; ok {
			cred, err := a.store.Load(credName)
			if err == nil {
				return cred, nil
			}
		}
	}

	// Fall back to listing and taking first available
	creds, err := a.store.List()
	if err != nil {
		return nil, err
	}
	for _, cred := range creds {
		if cred.Provider == providerName {
			return cred, nil
		}
	}
	return nil, fmt.Errorf("no stored credentials for %s", providerName)
}

// ClientInfo holds endpoint, credentials key/token, and default headers.
type ClientInfo struct {
	BaseURL     string
	APIKey      string
	AuthHeader  string // "Authorization" or "x-api-key"
	AuthValue   string
	IsOAuth     bool
	IsMinimax   bool
}

// resolveClientInfo builds ClientInfo for the provider, using FileStore or environment fallback.
func (a *Adaptor) resolveClientInfo(providerName string) (*ClientInfo, error) {
	cred, err := a.loadCredential(providerName)
	if err == nil {
		// Active check / Refresh
		if cred.Expired(DEFAULT_EXPIRY_SKEW) {
			slog.Info("refreshing credential", "provider", providerName, "account", cred.Account)
			authProvider, err := provider.For(cred)
			if err == nil {
				newCred, err := authProvider.Refresh(context.Background(), cred)
				if err == nil {
					_ = a.store.Save(newCred)
					cred = newCred
				} else {
					slog.Error("failed to refresh credential", "err", err)
				}
			}
		}

		info := &ClientInfo{
			BaseURL: cred.BaseURL,
		}

		if cred.Kind == auth.KIND_API_KEY {
			info.APIKey = cred.APIKey
		} else if cred.Kind == auth.KIND_OAUTH {
			info.APIKey = cred.AccessToken
			info.IsOAuth = true
		} else if cred.Kind == auth.KIND_SERVICE_ACCOUNT {
			// Vertex AI / Google Service Account flow. In a proxy context, we verify STS tokens.
			authProvider, err := provider.For(cred)
			if err == nil {
				res, err := authProvider.Verify(context.Background(), cred)
				if err == nil && res.Credential != nil {
					_ = a.store.Save(res.Credential)
					info.APIKey = res.Credential.AccessToken
				} else {
					info.APIKey = cred.AccessToken
				}
			} else {
				info.APIKey = cred.AccessToken
			}
		}

		switch providerName {
		case "anthropic":
			if info.BaseURL == "" {
				info.BaseURL = "https://api.anthropic.com"
			}
			info.AuthHeader = "x-api-key"
			info.AuthValue = info.APIKey
		case "openai":
			if info.BaseURL == "" {
				info.BaseURL = "https://api.openai.com"
			}
			info.AuthHeader = "Authorization"
			info.AuthValue = "Bearer " + info.APIKey
		case "xai":
			if info.BaseURL == "" {
				info.BaseURL = "https://api.x.ai"
			}
			info.AuthHeader = "Authorization"
			info.AuthValue = "Bearer " + info.APIKey
		case "minimax":
			if info.BaseURL == "" {
				info.BaseURL = "https://api.minimax.io/anthropic"
			}
			info.AuthHeader = "x-api-key" // MiniMax's Anthropic endpoint accepts x-api-key or Authorization
			info.AuthValue = info.APIKey
			info.IsMinimax = true
		}
		return info, nil
	}

	// Environment variable fallback
	info := &ClientInfo{}
	switch providerName {
	case "anthropic":
		info.APIKey = os.Getenv("ANTHROPIC_API_KEY")
		info.BaseURL = "https://api.anthropic.com"
		info.AuthHeader = "x-api-key"
		info.AuthValue = info.APIKey
	case "openai":
		info.APIKey = os.Getenv("OPENAI_API_KEY")
		info.BaseURL = "https://api.openai.com"
		info.AuthHeader = "Authorization"
		info.AuthValue = "Bearer " + info.APIKey
	case "xai":
		info.APIKey = os.Getenv("XAI_API_KEY")
		info.BaseURL = "https://api.x.ai"
		info.AuthHeader = "Authorization"
		info.AuthValue = "Bearer " + info.APIKey
	case "minimax":
		info.APIKey = os.Getenv("MINIMAX_API_KEY")
		info.BaseURL = "https://api.minimax.io/anthropic"
		info.AuthHeader = "x-api-key"
		info.AuthValue = info.APIKey
		info.IsMinimax = true
	}

	if info.APIKey == "" {
		return nil, fmt.Errorf("no credential or environment variable found for provider %s", providerName)
	}

	return info, nil
}

// getProviderForModel maps the model parameter to target provider.
func getProviderForModel(model string) string {
	m := strings.ToLower(model)
	if strings.Contains(m, "claude-") || strings.Contains(m, "sonnet") || strings.Contains(m, "haiku") || strings.Contains(m, "opus") {
		return "anthropic"
	}
	if strings.Contains(m, "gpt-") || strings.Contains(m, "o1-") || strings.Contains(m, "o3-") {
		return "openai"
	}
	if strings.Contains(m, "grok-") {
		return "xai"
	}
	if strings.Contains(m, "minimax-") || strings.Contains(m, "minimax") {
		return "minimax"
	}
	return "anthropic" // Default fallback
}

// HandleModels lists advertised models.
func (a *Adaptor) HandleModels() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Basic static catalog covering common models
		models := []gin.H{
			{"id": "claude-3-5-sonnet-latest", "owned_by": "anthropic"},
			{"id": "claude-3-5-haiku-latest", "owned_by": "anthropic"},
			{"id": "claude-3-opus-latest", "owned_by": "anthropic"},
			{"id": "gpt-4o", "owned_by": "openai"},
			{"id": "gpt-4o-mini", "owned_by": "openai"},
			{"id": "grok-beta", "owned_by": "xai"},
			{"id": "minimax-m3", "owned_by": "minimax"},
		}
		c.JSON(http.StatusOK, gin.H{"data": models})
	}
}

// HandleMessages handles Anthropic Messages endpoint (/v1/messages).
func (a *Adaptor) HandleMessages() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req AnthropicRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}

		targetProvider := getProviderForModel(req.Model)
		info, err := a.resolveClientInfo(targetProvider)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}

		if targetProvider == "anthropic" || targetProvider == "minimax" {
			// Direct proxy using Anthropic Messages format
			a.proxyAnthropicDirect(c, info, "/v1/messages", &req)
		} else {
			// Translate from Anthropic Messages -> OpenAI Chat completions
			openaiReq := TranslateAnthropicToOpenAI(&req)
			a.proxyOpenAIChatTranslated(c, info, openaiReq, &req)
		}
	}
}

// HandleChatCompletions handles OpenAI Chat Completions endpoint (/v1/chat/completions).
func (a *Adaptor) HandleChatCompletions() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req OpenAIChatRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}

		targetProvider := getProviderForModel(req.Model)
		info, err := a.resolveClientInfo(targetProvider)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}

		if targetProvider == "openai" || targetProvider == "xai" {
			// Direct OpenAI Chat Completions proxy
			a.proxyOpenAIDirect(c, info, "/v1/chat/completions", &req)
		} else {
			// Translate from OpenAI Chat Completions -> Anthropic Messages
			anthropicReq := TranslateOpenAIToAnthropic(&req)
			a.proxyAnthropicMessagesTranslated(c, info, anthropicReq, &req)
		}
	}
}

// HandleResponses handles OpenAI Responses API (/v1/responses).
func (a *Adaptor) HandleResponses() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CodexResponsePayload
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}

		targetProvider := getProviderForModel(req.Model)
		info, err := a.resolveClientInfo(targetProvider)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}

		if targetProvider == "openai" {
			a.proxyOpenAIDirect(c, info, "/v1/responses", &req)
		} else {
			// Translate responses payload to anthropic
			anthropicReq := &AnthropicRequest{
				Model:  req.Model,
				Stream: req.Stream,
			}
			if req.Instructions != "" {
				anthropicReq.System = req.Instructions
			}
			for _, msg := range req.Input {
				anthropicReq.Messages = append(anthropicReq.Messages, AnthropicMessage{
					Role:    msg.Role,
					Content: msg.Content,
				})
			}
			for _, tool := range req.Tools {
				anthropicReq.Tools = append(anthropicReq.Tools, AnthropicTool{
					Name:        tool.Function.Name,
					Description: tool.Function.Description,
					InputSchema: tool.Function.Parameters,
				})
			}
			a.proxyAnthropicMessagesTranslatedToResponses(c, info, anthropicReq, &req)
		}
	}
}

// HandleCountTokens handles token counting endpoint.
func (a *Adaptor) HandleCountTokens() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Mock token count for basic compatibility
		c.JSON(http.StatusOK, gin.H{"input_tokens": 100})
	}
}

// ---------------------------------------------------------------------------
// Upstream proxy implementations
// ---------------------------------------------------------------------------

func (a *Adaptor) proxyAnthropicDirect(c *gin.Context, info *ClientInfo, path string, originalReq *AnthropicRequest) {
	url := info.BaseURL + path
	bodyBytes, _ := json.Marshal(originalReq)

	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(info.AuthHeader, info.AuthValue)
	req.Header.Set("anthropic-version", "2023-06-01")
	if originalReq.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	defer resp.Body.Close()

	copyResponseHeadersAndBody(c, resp, originalReq.Stream)
}

func (a *Adaptor) proxyOpenAIDirect(c *gin.Context, info *ClientInfo, path string, originalReq any) {
	url := info.BaseURL + path
	bodyBytes, _ := json.Marshal(originalReq)

	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(info.AuthHeader, info.AuthValue)

	isStream := false
	if r, ok := originalReq.(*OpenAIChatRequest); ok && r.Stream {
		req.Header.Set("Accept", "text/event-stream")
		isStream = true
	} else if r, ok := originalReq.(*CodexResponsePayload); ok && r.Stream {
		req.Header.Set("Accept", "text/event-stream")
		isStream = true
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	defer resp.Body.Close()

	copyResponseHeadersAndBody(c, resp, isStream)
}

func (a *Adaptor) proxyOpenAIChatTranslated(c *gin.Context, info *ClientInfo, openaiReq *OpenAIChatRequest, orig *AnthropicRequest) {
	// Call OpenAI Chat Completions upstream
	url := info.BaseURL + "/v1/chat/completions"
	bodyBytes, _ := json.Marshal(openaiReq)

	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(info.AuthHeader, info.AuthValue)
	if orig.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.Status(resp.StatusCode)
		_, _ = io.Copy(c.Writer, resp.Body)
		return
	}

	if orig.Stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Writer.Flush()

		reader := bufio.NewReader(resp.Body)
		// First Anthropic events: message_start
		_, _ = c.Writer.Write([]byte("event: message_start\ndata: {\"type\": \"message_start\", \"message\": {\"id\": \"msg-stream\", \"type\": \"message\", \"role\": \"assistant\", \"content\": [], \"model\": \"" + orig.Model + "\"}}\n\n"))
		c.Writer.Flush()

		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				break
			}
			lineStr := strings.TrimSpace(string(line))
			if !strings.HasPrefix(lineStr, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
			if payload == "[DONE]" {
				_, _ = c.Writer.Write([]byte("event: message_stop\ndata: {\"type\": \"message_stop\"}\n\n"))
				c.Writer.Flush()
				break
			}

			var chunk OpenAIStreamChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err == nil {
				events, _, _ := TranslateOpenAIChunkToAnthropic(&chunk)
				for i := 0; i < len(events); i += 2 {
					evtType := events[i]
					evtData := events[i+1]
					_, _ = c.Writer.Write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", evtType, evtData)))
				}
				c.Writer.Flush()
			}
		}
	} else {
		var chatResp OpenAIChatResponse
		if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to decode OpenAI response: " + err.Error()}})
			return
		}
		anthropicResp := TranslateOpenAIToAnthropicResponse(&chatResp, orig.Model)
		c.JSON(http.StatusOK, anthropicResp)
	}
}

func (a *Adaptor) proxyAnthropicMessagesTranslated(c *gin.Context, info *ClientInfo, anthropicReq *AnthropicRequest, orig *OpenAIChatRequest) {
	// Call Anthropic Messages upstream
	url := info.BaseURL + "/v1/messages"
	bodyBytes, _ := json.Marshal(anthropicReq)

	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(info.AuthHeader, info.AuthValue)
	req.Header.Set("anthropic-version", "2023-06-01")
	if orig.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.Status(resp.StatusCode)
		_, _ = io.Copy(c.Writer, resp.Body)
		return
	}

	chatID := "chatcmpl-" + uuid.New().String()

	if orig.Stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Writer.Flush()

		reader := bufio.NewReader(resp.Body)
		var currentEvent string

		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				break
			}
			lineStr := strings.TrimSpace(string(line))
			if strings.HasPrefix(lineStr, "event:") {
				currentEvent = strings.TrimSpace(strings.TrimPrefix(lineStr, "event:"))
			} else if strings.HasPrefix(lineStr, "data:") {
				dataPayload := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
				openAIEventLine, err := TranslateAnthropicSSEToOpenAI(currentEvent, dataPayload, chatID, orig.Model)
				if err == nil && openAIEventLine != "" {
					_, _ = c.Writer.Write([]byte(openAIEventLine))
					c.Writer.Flush()
				}
			}
		}
	} else {
		var anthropicResp AnthropicResponse
		if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to decode Anthropic response: " + err.Error()}})
			return
		}

		// Translate back to OpenAI Chat Completion Response
		chatResp := OpenAIChatResponse{
			ID:      chatID,
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   orig.Model,
			Usage: OpenAIChatUsage{
				PromptTokens:     anthropicResp.Usage.InputTokens,
				CompletionTokens: anthropicResp.Usage.OutputTokens,
				TotalTokens:      anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens,
			},
		}

		var textContent string
		var toolCalls []OpenAIToolCall
		for _, block := range anthropicResp.Content {
			switch block.Type {
			case "text":
				textContent += block.Text
			case "tool_use":
				argsBytes, _ := block.Input.MarshalJSON()
				toolCalls = append(toolCalls, OpenAIToolCall{
					ID:   block.ID,
					Type: "function",
					Function: OpenAIFunctionCall{
						Name:      block.Name,
						Arguments: string(argsBytes),
					},
				})
			}
		}

		finishReason := mapAnthropicStopReasonToOpenAI(anthropicResp.StopReason)
		chatResp.Choices = []OpenAIChatChoice{
			{
				Index: 0,
				Message: OpenAIMessage{
					Role:      "assistant",
					Content:   textContent,
					ToolCalls: toolCalls,
				},
				FinishReason: finishReason,
			},
		}

		c.JSON(http.StatusOK, chatResp)
	}
}

func (a *Adaptor) proxyAnthropicMessagesTranslatedToResponses(c *gin.Context, info *ClientInfo, anthropicReq *AnthropicRequest, orig *CodexResponsePayload) {
	// For Responses API, we run the Anthropic upstream call and translate
	// back the non-streaming or streaming messages.
	url := info.BaseURL + "/v1/messages"
	bodyBytes, _ := json.Marshal(anthropicReq)

	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(info.AuthHeader, info.AuthValue)
	req.Header.Set("anthropic-version", "2023-06-01")
	if orig.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.Status(resp.StatusCode)
		_, _ = io.Copy(c.Writer, resp.Body)
		return
	}

	if orig.Stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Writer.Flush()

		reader := bufio.NewReader(resp.Body)
		var currentEvent string

		// Responses SSE uses specialized output item streams. We will map
		// text delta and function delta events.
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				break
			}
			lineStr := strings.TrimSpace(string(line))
			if strings.HasPrefix(lineStr, "event:") {
				currentEvent = strings.TrimSpace(strings.TrimPrefix(lineStr, "event:"))
			} else if strings.HasPrefix(lineStr, "data:") {
				dataPayload := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
				var payload map[string]any
				if err := json.Unmarshal([]byte(dataPayload), &payload); err == nil {
					switch currentEvent {
					case "content_block_delta":
						dMap, _ := payload["delta"].(map[string]any)
						if dMap != nil {
							if txt, ok := dMap["text"].(string); ok {
								// Format: response.output_text.delta
								evtData := fmt.Sprintf(`{"delta": %s}`, stringifyJSON(txt))
								_, _ = c.Writer.Write([]byte(fmt.Sprintf("event: response.output_text.delta\ndata: %s\n\n", evtData)))
							}
						}
					case "content_block_start":
						block, _ := payload["content_block"].(map[string]any)
						if block != nil && block["type"] == "tool_use" {
							// Format: response.output_item.added
							evtData := fmt.Sprintf(`{"item": {"type": "function_call", "id": %s, "call_id": %s, "name": %s, "arguments": ""}}`,
								stringifyJSON(block["id"].(string)), stringifyJSON(block["id"].(string)), stringifyJSON(block["name"].(string)))
							_, _ = c.Writer.Write([]byte(fmt.Sprintf("event: response.output_item.added\ndata: %s\n\n", evtData)))
						}
					case "message_stop":
						// Format: response.completed
						_, _ = c.Writer.Write([]byte("event: response.completed\ndata: {}\n\n"))
					}
					c.Writer.Flush()
				}
			}
		}
	} else {
		var anthropicResp AnthropicResponse
		if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to decode Anthropic response: " + err.Error()}})
			return
		}

		responsesResp := TranslateResponsesToAnthropicMessage(nil, orig.Model) // helper stub
		// Build the actual output list
		var outputItems []CodexOutputItem
		var textContent string
		for _, block := range anthropicResp.Content {
			switch block.Type {
			case "text":
				textContent += block.Text
			case "tool_use":
				argsBytes, _ := block.Input.MarshalJSON()
				outputItems = append(outputItems, CodexOutputItem{
					Type:      "function_call",
					CallID:    block.ID,
					Name:      block.Name,
					Arguments: string(argsBytes),
				})
			}
		}
		if textContent != "" {
			outputItems = append(outputItems, CodexOutputItem{
				Type:    "message",
				Content: []CodexContentBlock{{Type: "output_text", Text: textContent}},
			})
		}

		responsesResp.Content = nil
		c.JSON(http.StatusOK, gin.H{
			"model":  orig.Model,
			"output": outputItems,
		})
	}
}

func copyResponseHeadersAndBody(c *gin.Context, resp *http.Response, isStream bool) {
	for k, vv := range resp.Header {
		for _, v := range vv {
			c.Header(k, v)
		}
	}
	c.Status(resp.StatusCode)

	if isStream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Writer.Flush()
	}

	_, _ = io.Copy(c.Writer, resp.Body)
}
