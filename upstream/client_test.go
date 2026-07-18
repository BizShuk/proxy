package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bizshuk/agentsdk/auth/auth"
	"github.com/bizshuk/proxy/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientValidatesDependenciesAndClonesTransport(t *testing.T) {
	validTimeouts := timeoutConfig()
	tests := []struct {
		name    string
		client  *http.Client
		cfg     TimeoutConfig
		wantErr bool
	}{
		{name: "nil client", cfg: validTimeouts, wantErr: true},
		{name: "zero messages timeout", client: http.DefaultClient, cfg: TimeoutConfig{StreamMessagesMs: 1, CountTokensMs: 1}, wantErr: true},
		{name: "zero stream timeout", client: http.DefaultClient, cfg: TimeoutConfig{MessagesMs: 1, CountTokensMs: 1}, wantErr: true},
		{name: "zero count timeout", client: http.DefaultClient, cfg: TimeoutConfig{MessagesMs: 1, StreamMessagesMs: 1}, wantErr: true},
		{name: "valid", client: http.DefaultClient, cfg: validTimeouts},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewClient(tc.client, tc.cfg)
			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, client)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, client)
		})
	}

	transport := &http.Transport{ResponseHeaderTimeout: 45 * time.Second}
	injected := &http.Client{Transport: transport, Timeout: time.Millisecond}
	client, err := NewClient(injected, validTimeouts)
	require.NoError(t, err)
	assert.Zero(t, client.httpClient.Timeout)
	assert.Equal(t, time.Millisecond, injected.Timeout)
	assert.Equal(t, 45*time.Second, transport.ResponseHeaderTimeout)
	cloned, ok := client.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	assert.NotSame(t, transport, cloned)
	assert.Equal(t, time.Duration(validTimeouts.MessagesMs)*time.Millisecond, cloned.ResponseHeaderTimeout)
}

func TestClientDoNeverFollowsRedirects(t *testing.T) {
	redirects := []struct {
		name        string
		locationFor func(string) string
	}{
		{
			name: "different hostname",
			locationFor: func(targetURL string) string {
				return strings.Replace(targetURL, "127.0.0.1", "localhost", 1)
			},
		},
		{
			name:        "same hostname different port",
			locationFor: func(targetURL string) string { return targetURL },
		},
	}
	credentials := []struct {
		name       string
		profileID  string
		target     protocol.Format
		credential *auth.Credential
	}{
		{
			name:       "Authorization",
			profileID:  "xai",
			target:     protocol.FORMAT_OPENAI_RESPONSES,
			credential: &auth.Credential{Provider: "xai", Kind: auth.KIND_API_KEY, APIKey: "secret"},
		},
		{
			name:       "x-api-key",
			profileID:  "anthropic",
			target:     protocol.FORMAT_ANTHROPIC_MESSAGES,
			credential: &auth.Credential{Provider: "anthropic", Kind: auth.KIND_API_KEY, APIKey: "secret"},
		},
	}
	for _, redirect := range redirects {
		for _, credential := range credentials {
			t.Run(redirect.name+"/"+credential.name, func(t *testing.T) {
				var redirected bool
				var authorization string
				var apiKey string
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					redirected = true
					authorization = r.Header.Get("Authorization")
					apiKey = r.Header.Get("x-api-key")
					w.WriteHeader(http.StatusOK)
				}))
				defer target.Close()

				source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Location", redirect.locationFor(target.URL))
					w.WriteHeader(http.StatusTemporaryRedirect)
				}))
				defer source.Close()

				injected := source.Client()
				injected.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
				client, err := NewClient(injected, timeoutConfig())
				require.NoError(t, err)
				profile := defaultProfile(t, credential.profileID)
				profile.BaseURL = source.URL

				response, err := client.Do(context.Background(), profile, credential.credential, protocol.RequestEnvelope{
					TargetFormat: credential.target, Body: []byte(`{}`),
				})
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())

				assert.Equal(t, http.StatusTemporaryRedirect, response.StatusCode)
				assert.False(t, redirected)
				assert.Empty(t, authorization)
				assert.Empty(t, apiKey)
			})
		}
	}
}

func TestClientDoUsesProfileEndpointAndSanitizedHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/responses", r.URL.Path)
		assert.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("x-api-key"))
		assert.Empty(t, r.Header.Get("X-Forwarded-Authorization"))
		assert.Equal(t, "req_client", r.Header.Get("x-request-id"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Empty(t, r.Header.Get("Accept"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{"model":"grok-4.5","input":"hi"}`, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","status":"completed"}`))
	}))
	defer server.Close()

	profile := testXAIProfile(server.URL)
	client, err := NewClient(server.Client(), timeoutConfig())
	require.NoError(t, err)
	resp, err := client.Do(context.Background(), profile, &auth.Credential{Provider: "xai", Kind: auth.KIND_API_KEY, APIKey: "secret"}, protocol.RequestEnvelope{
		TargetFormat: protocol.FORMAT_OPENAI_RESPONSES,
		Headers: http.Header{
			"x-request-id":              {"req_client"},
			"Authorization":             {"Bearer client-secret"},
			"X-Forwarded-Authorization": {"Bearer forwarded-secret"},
		},
		Body: []byte(`{"model":"grok-4.5","input":"hi"}`),
	})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

func TestClientDoJoinsBasePathAndAllowsCredentialOverride(t *testing.T) {
	paths := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client, err := NewClient(server.Client(), timeoutConfig())
	require.NoError(t, err)
	profile := defaultProfile(t, "minimax")
	profile.BaseURL = server.URL + "/anthropic"
	resp, err := client.Do(context.Background(), profile, &auth.Credential{
		Provider: "minimax", Kind: auth.KIND_API_KEY, APIKey: "secret",
	}, protocol.RequestEnvelope{TargetFormat: protocol.FORMAT_ANTHROPIC_MESSAGES, Body: []byte(`{}`)})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "/anthropic/v1/messages", <-paths)

	profile.BaseURL = "https://unused.example.com/base"
	resp, err = client.Do(context.Background(), profile, &auth.Credential{
		Provider: "minimax", Kind: auth.KIND_API_KEY, APIKey: "secret", BaseURL: server.URL + "/gateway",
	}, protocol.RequestEnvelope{TargetFormat: protocol.FORMAT_ANTHROPIC_MESSAGES, Body: []byte(`{}`)})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "/gateway/v1/messages", <-paths)
}

func TestClientDoRejectsUnsafeBaseURLs(t *testing.T) {
	client, err := NewClient(http.DefaultClient, timeoutConfig())
	require.NoError(t, err)
	profile := defaultProfile(t, "xai")
	tests := []string{
		"",
		"//api.example.com",
		"ftp://api.example.com",
		"http://api.example.com",
		"http://192.168.1.10",
		"https:///v1",
		"https://user:secret@api.example.com",
		"https://api.example.com?debug=true",
		"https://api.example.com#fragment",
	}
	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			profile.BaseURL = rawURL
			_, err := client.Do(context.Background(), profile, &auth.Credential{
				Provider: "xai", Kind: auth.KIND_API_KEY, APIKey: "secret",
			}, protocol.RequestEnvelope{TargetFormat: protocol.FORMAT_OPENAI_RESPONSES, Body: []byte(`{}`)})
			var proxyErr *protocol.ProxyError
			require.ErrorAs(t, err, &proxyErr)
			assert.Equal(t, protocol.ERROR_UNAVAILABLE, proxyErr.Kind)
		})
	}
}

func TestClientDoAppliesProviderSpecificHeaders(t *testing.T) {
	tests := []struct {
		name       string
		profileID  string
		credential *auth.Credential
		target     protocol.Format
		stream     bool
		headers    http.Header
		assert     func(*testing.T, http.Header)
	}{
		{
			name: "Anthropic API key", profileID: "anthropic", target: protocol.FORMAT_ANTHROPIC_MESSAGES,
			credential: &auth.Credential{Provider: "anthropic", Kind: auth.KIND_API_KEY, APIKey: "anthropic-key"},
			assert: func(t *testing.T, header http.Header) {
				assert.Equal(t, "anthropic-key", header.Get("x-api-key"))
				assert.Empty(t, header.Get("Authorization"))
				assert.Equal(t, ANTHROPIC_VERSION, header.Get("anthropic-version"))
			},
		},
		{
			name: "Anthropic OAuth", profileID: "anthropic", target: protocol.FORMAT_ANTHROPIC_MESSAGES, stream: true,
			credential: &auth.Credential{Provider: "anthropic", Kind: auth.KIND_OAUTH, AccessToken: "anthropic-token"},
			headers:    http.Header{"anthropic-beta": {"tools-2024-04-04", ANTHROPIC_OAUTH_BETA + ", tools-2024-04-04"}},
			assert: func(t *testing.T, header http.Header) {
				assert.Equal(t, "Bearer anthropic-token", header.Get("Authorization"))
				assert.Empty(t, header.Get("x-api-key"))
				assert.Equal(t, "true", header.Get("anthropic-dangerous-direct-browser-access"))
				assert.ElementsMatch(t, []string{"tools-2024-04-04", ANTHROPIC_OAUTH_BETA}, commaSeparatedValues(header.Values("anthropic-beta")))
				assert.Equal(t, "text/event-stream", header.Get("Accept"))
			},
		},
		{
			name: "MiniMax", profileID: "minimax", target: protocol.FORMAT_ANTHROPIC_MESSAGES,
			credential: &auth.Credential{Provider: "minimax", Kind: auth.KIND_API_KEY, APIKey: "minimax-key"},
			assert: func(t *testing.T, header http.Header) {
				assert.Equal(t, "minimax-key", header.Get("x-api-key"))
				assert.Empty(t, header.Get("Authorization"))
			},
		},
		{
			name: "OpenAI API", profileID: "openai-api", target: protocol.FORMAT_OPENAI_RESPONSES,
			credential: &auth.Credential{Provider: "openai", Kind: auth.KIND_API_KEY, APIKey: "openai-key"},
			assert: func(t *testing.T, header http.Header) {
				assert.Equal(t, "Bearer openai-key", header.Get("Authorization"))
			},
		},
		{
			name: "Codex OAuth", profileID: "openai-codex-oauth", target: protocol.FORMAT_OPENAI_RESPONSES, stream: true,
			credential: &auth.Credential{Provider: "openai", Kind: auth.KIND_OAUTH, AccessToken: "codex-token", AccountID: "acct-uuid"},
			assert: func(t *testing.T, header http.Header) {
				assert.Equal(t, "Bearer codex-token", header.Get("Authorization"))
				assert.Equal(t, "acct-uuid", header.Get("ChatGPT-Account-ID"))
				assert.Equal(t, DEFAULT_CODEX_ORIGINATOR, header.Get("originator"))
				assert.Equal(t, DEFAULT_CODEX_VERSION, header.Get("version"))
				assert.Equal(t, expectedCodexUserAgent(), header.Get("User-Agent"))
			},
		},
		{
			name: "xAI", profileID: "xai", target: protocol.FORMAT_OPENAI_RESPONSES,
			credential: &auth.Credential{Provider: "xai", Kind: auth.KIND_API_KEY, APIKey: "xai-key"},
			assert: func(t *testing.T, header http.Header) {
				assert.Equal(t, "Bearer xai-key", header.Get("Authorization"))
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tc.assert(t, r.Header)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			profile := defaultProfile(t, tc.profileID)
			profile.BaseURL = server.URL
			client, err := NewClient(server.Client(), timeoutConfig())
			require.NoError(t, err)

			resp, err := client.Do(context.Background(), profile, tc.credential, protocol.RequestEnvelope{
				TargetFormat: tc.target,
				Stream:       tc.stream,
				Headers:      tc.headers,
				Body:         []byte(`{}`),
			})
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
		})
	}
}

func TestClientDoOmitsEmptyCodexAccountID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("ChatGPT-Account-ID"))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	profile := defaultProfile(t, "openai-codex-oauth")
	profile.BaseURL = server.URL
	client, err := NewClient(server.Client(), timeoutConfig())
	require.NoError(t, err)

	resp, err := client.Do(context.Background(), profile, &auth.Credential{
		Provider: "openai", Kind: auth.KIND_OAUTH, AccessToken: "token",
	}, protocol.RequestEnvelope{TargetFormat: protocol.FORMAT_OPENAI_RESPONSES, Stream: true, Body: []byte(`{}`)})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

func TestClientDoUsesRequestTimeoutAndCancelsOnBodyClose(t *testing.T) {
	tests := []struct {
		name        string
		stream      bool
		wantTimeout time.Duration
	}{
		{name: "non-stream", wantTimeout: 2 * time.Second},
		{name: "stream", stream: true, wantTimeout: 5 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var captured context.Context
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				captured = req.Context()
				deadline, ok := captured.Deadline()
				require.True(t, ok)
				remaining := time.Until(deadline)
				assert.Greater(t, remaining, tc.wantTimeout-500*time.Millisecond)
				assert.LessOrEqual(t, remaining, tc.wantTimeout)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Request:    req,
				}, nil
			})
			injected := &http.Client{Transport: transport, Timeout: time.Nanosecond}
			client, err := NewClient(injected, timeoutConfig())
			require.NoError(t, err)
			profile := defaultProfile(t, "xai")

			resp, err := client.Do(context.Background(), profile, &auth.Credential{
				Provider: "xai", Kind: auth.KIND_API_KEY, APIKey: "secret",
			}, protocol.RequestEnvelope{TargetFormat: protocol.FORMAT_OPENAI_RESPONSES, Stream: tc.stream, Body: []byte(`{}`)})
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			select {
			case <-captured.Done():
			case <-time.After(time.Second):
				t.Fatal("request context was not canceled when response body closed")
			}
		})
	}
}

func TestClientDoPropagatesCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	client, err := NewClient(&http.Client{Transport: transport}, timeoutConfig())
	require.NoError(t, err)
	profile := defaultProfile(t, "xai")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.Do(ctx, profile, &auth.Credential{
			Provider: "xai", Kind: auth.KIND_API_KEY, APIKey: "secret",
		}, protocol.RequestEnvelope{TargetFormat: protocol.FORMAT_OPENAI_RESPONSES, Body: []byte(`{}`)})
		result <- err
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		var proxyErr *protocol.ProxyError
		require.ErrorAs(t, err, &proxyErr)
		assert.Equal(t, protocol.ERROR_UPSTREAM, proxyErr.Kind)
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("upstream request did not observe caller cancellation")
	}
}

func TestClientDoMapsTransportFailures(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantKind protocol.ErrorKind
		status   int
	}{
		{name: "deadline", err: context.DeadlineExceeded, wantKind: protocol.ERROR_TIMEOUT, status: http.StatusGatewayTimeout},
		{name: "network timeout", err: timeoutError{}, wantKind: protocol.ERROR_TIMEOUT, status: http.StatusGatewayTimeout},
		{name: "generic upstream", err: errors.New("dial refused"), wantKind: protocol.ERROR_UPSTREAM, status: http.StatusBadGateway},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, tc.err
			})}, timeoutConfig())
			require.NoError(t, err)
			_, err = client.Do(context.Background(), defaultProfile(t, "xai"), &auth.Credential{
				Provider: "xai", Kind: auth.KIND_API_KEY, APIKey: "secret",
			}, protocol.RequestEnvelope{TargetFormat: protocol.FORMAT_OPENAI_RESPONSES, Body: []byte(`{}`)})
			var proxyErr *protocol.ProxyError
			require.ErrorAs(t, err, &proxyErr)
			assert.Equal(t, tc.wantKind, proxyErr.Kind)
			assert.Equal(t, tc.status, proxyErr.StatusCode())
		})
	}
}

func TestClientCountTokensUsesNativeEndpointAndTimeout(t *testing.T) {
	var capturedContext context.Context
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		capturedContext = req.Context()
		assert.Equal(t, "/v1/messages/count_tokens", req.URL.Path)
		assert.Equal(t, "anthropic-key", req.Header.Get("x-api-key"))
		assert.Equal(t, ANTHROPIC_VERSION, req.Header.Get("anthropic-version"))
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{"model":"claude-3-5-sonnet-latest","messages":[]}`, string(body))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"input_tokens":7}`)),
			Request:    req,
		}, nil
	})
	client, err := NewClient(&http.Client{Transport: transport}, timeoutConfig())
	require.NoError(t, err)
	profile := defaultProfile(t, "anthropic")
	profile.BaseURL = "https://api.example.com"

	resp, err := client.CountTokens(context.Background(), profile, &auth.Credential{
		Provider: "anthropic", Kind: auth.KIND_API_KEY, APIKey: "anthropic-key",
	}, protocol.RequestEnvelope{TargetFormat: protocol.FORMAT_ANTHROPIC_MESSAGES, Body: []byte(`{"model":"claude-3-5-sonnet-latest","messages":[]}`)})
	require.NoError(t, err)
	deadline, ok := capturedContext.Deadline()
	require.True(t, ok)
	remaining := time.Until(deadline)
	assert.Greater(t, remaining, 1500*time.Millisecond)
	assert.LessOrEqual(t, remaining, 2*time.Second)
	require.NoError(t, resp.Body.Close())
}

func TestClientCountTokensRejectsUnsupportedProfile(t *testing.T) {
	client, err := NewClient(http.DefaultClient, timeoutConfig())
	require.NoError(t, err)

	_, err = client.CountTokens(context.Background(), defaultProfile(t, "xai"), &auth.Credential{
		Provider: "xai", Kind: auth.KIND_API_KEY, APIKey: "secret",
	}, protocol.RequestEnvelope{TargetFormat: protocol.FORMAT_OPENAI_RESPONSES, Body: []byte(`{}`)})
	var proxyErr *protocol.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, protocol.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
	assert.Equal(t, http.StatusNotImplemented, proxyErr.StatusCode())
}

func testXAIProfile(baseURL string) Profile {
	catalog, err := DefaultCatalog()
	if err != nil {
		panic(err)
	}
	profile, ok := catalog.Lookup("xai")
	if !ok {
		panic("default xAI profile missing")
	}
	profile.BaseURL = baseURL
	return profile
}

func timeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		MessagesMs: 2000, StreamMessagesMs: 5000, CountTokensMs: 2000,
	}
}

func defaultProfile(t *testing.T, id string) Profile {
	t.Helper()
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	profile, ok := catalog.Lookup(id)
	require.True(t, ok)
	return profile
}

func commaSeparatedValues(values []string) []string {
	var result []string
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item != "" && !containsString(result, item) {
				result = append(result, item)
			}
		}
	}
	return result
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func expectedCodexUserAgent() string {
	platform := "linux"
	switch runtime.GOOS {
	case "darwin":
		platform = "macos"
	case "windows":
		platform = "windows"
	}
	architecture := "x86_64"
	if runtime.GOARCH == "arm64" {
		architecture = "arm64"
	}
	return fmt.Sprintf("%s/%s (%s; %s)", DEFAULT_CODEX_ORIGINATOR, DEFAULT_CODEX_VERSION, platform, architecture)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "transport timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
