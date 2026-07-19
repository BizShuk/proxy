package codex

// OAuth support for ChatGPT Plus/Pro accounts. The flow is the
// standard authorization-code + PKCE path documented by OpenAI:
//
//	Authorize : https://auth.openai.com/oauth/authorize  (browser redirect)
//	Token     : https://auth.openai.com/oauth/token
//
// Client ID is Codex's public client identifier (embedded in the
// Codex CLI binary; not a secret). The redirect URI is bound to
// localhost:1455 to match what ChatGPT's auth server has
// pre-registered for the Codex client.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	OAuthAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	OAuthTokenURL     = "https://auth.openai.com/oauth/token"
	OAuthClientID     = "app-codex-cli"
	OAuthRedirectURI  = "http://localhost:1455/auth/callback"
	OAuthScope        = "openid profile email offline_access"
	OAuthCallbackPort = 1455

	// CodexOriginator identifies requests made through the Codex
	// adapter. The upstream /codex/responses endpoint rejects requests
	// that do not carry this header.
	CodexOriginator = "codex_cli_rs"

	// CodexVersion is the version string the chatgpt.com endpoint
	// expects. Mismatches fall through to a 403 in upstream's bot
	// detection.
	CodexVersion = "0.125.0"
)

// OAuthToken is the raw payload returned by /oauth/token. We map it
// into OAuthCredentials (with absolute ExpiresAt) for storage.
type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	IDToken      string `json:"id_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"` // ChatGPT-Account-ID
}

// OAuthCredentials is the locally-stored credential shape. The
// AccountID is sent as the ChatGPT-Account-ID header — without it,
// the upstream /codex/responses endpoint falls back to per-request
// rate limiting across the user's entire org.
type OAuthCredentials struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	AccountID    string
}

// IsExpired reports whether the token needs a refresh. A 60s grace
// window prevents mid-flight expirations from breaking the current call.
func (c OAuthCredentials) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Until(c.ExpiresAt) < 60*time.Second
}

// AuthorizeURL builds the URL the user should open in their browser
// to start the OAuth flow. State is opaque and may be used by the
// caller to correlate the redirect.
func AuthorizeURL(state, challenge string) string {
	if challenge == "" {
		// Pre-built the URL without challenge; the caller must
		// supply a non-empty challenge from GeneratePKCE.
		challenge = "_"
	}
	params := url.Values{}
	params.Set("client_id", OAuthClientID)
	params.Set("redirect_uri", OAuthRedirectURI)
	params.Set("response_type", "code")
	params.Set("scope", OAuthScope)
	if state != "" {
		params.Set("state", state)
	}
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("id_token_add_organizations", "true")
	return OAuthAuthorizeURL + "?" + params.Encode()
}

// ExchangeCode performs the authorization-code → token exchange. Used
// as the second leg of the PKCE flow once the browser has redirected
// back with `?code=...`.
func ExchangeCode(ctx context.Context, code, verifier string) (OAuthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", OAuthClientID)
	form.Set("code", code)
	form.Set("redirect_uri", OAuthRedirectURI)
	form.Set("code_verifier", verifier)
	return postToken(ctx, form)
}

// RefreshToken swaps a refresh token for a fresh access token.
func RefreshToken(ctx context.Context, refresh string) (OAuthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", OAuthClientID)
	form.Set("refresh_token", refresh)
	return postToken(ctx, form)
}

func postToken(ctx context.Context, form url.Values) (OAuthToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, OAuthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// OpenAI's token endpoint is happy with any User-Agent; we set
	// Codex's so the auth server can record which client issued the
	// request — useful for the user's "connected apps" UI.
	req.Header.Set("User-Agent", CodexUserAgent())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return OAuthToken{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return OAuthToken{}, fmt.Errorf("codex oauth: status %d: %s", resp.StatusCode, body)
	}
	var t OAuthToken
	if err := json.Unmarshal(body, &t); err != nil {
		return OAuthToken{}, fmt.Errorf("codex oauth: decode: %w", err)
	}
	return t, nil
}

// GeneratePKCE returns a (verifier, challenge) pair using S256. The
// verifier is 64 random bytes (86 base64url chars); the challenge is
// the S256 hash of the verifier, base64url-encoded without padding.
func GeneratePKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 64)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// OpenBrowser launches the system browser pointing at authorizeURL
// and blocks until the one-shot localhost listener captures the
// redirect carrying the `code` query parameter. Returns the captured
// code or an error.
//
// The listener is bound to 127.0.0.1:1455 (the registered Codex
// callback port). The user authorizes the app in the browser; the
// browser posts the code back to the local server; we hand it back
// to the caller for ExchangeCode.
func OpenBrowser(ctx context.Context, authorizeURL string) (string, error) {
	if authorizeURL == "" {
		return "", errors.New("codex oauth: authorizeURL is required")
	}

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("codex oauth: no code in callback")
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "Codex login complete. You may close this tab.")
		codeCh <- code
	})

	addr := fmt.Sprintf("127.0.0.1:%d", OAuthCallbackPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("codex oauth: listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
	}()

	if err := openBrowserOS(authorizeURL); err != nil {
		return "", fmt.Errorf("codex oauth: open browser: %w", err)
	}

	select {
	case code := <-codeCh:
		return code, nil
	case err := <-errCh:
		return "", err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func openBrowserOS(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "linux":
		cmd = exec.Command("xdg-open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		return fmt.Errorf("codex oauth: unsupported platform %s", runtime.GOOS)
	}
	return cmd.Start()
}

// CodexUserAgent builds the User-Agent string with platform info.
// Format matches the proxy/svc/upstream codexUserAgent helper so the
// request looks identical to one the Codex CLI itself would send:
//
//	codex_cli_rs/0.125.0 (linux; x86_64)
//	codex_cli_rs/0.125.0 (macos; arm64)
//	codex_cli_rs/0.125.0 (windows; x86_64)
func CodexUserAgent() string {
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
	return fmt.Sprintf("%s/%s (%s; %s)", CodexOriginator, CodexVersion, platform, architecture)
}
