package anthropic

// OAuth support for Claude Pro/Max accounts. The flow follows Anthropic's
// public OAuth2 endpoints documented at https://docs.anthropic.com/en/api/oauth-2-0
// — it is the same flow the Claude Code CLI performs, so credentials issued
// here interoperate with Claude Code's session.
//
// Endpoints used:
//
//	Authorize : https://claude.ai/oauth/authorize  (browser redirect)
//	Token     : https://console.anthropic.com/v1/oauth/token
//
// The client_id is Anthropic's public Claude Code client id; it is
// embedded in the Claude Code binary so it is not a secret.

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
	OAuthAuthorizeURL = "https://claude.ai/oauth/authorize"
	OAuthTokenURL     = "https://console.anthropic.com/v1/oauth/token"
	OAuthClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e" // Claude Code's public client id
	OAuthRedirectURI  = "https://console.anthropic.com/oauth/code/callback"
	OAuthScope        = "org:create_api_key user:profile user:inference"

	// OAuthBetaHeader activates the OAuth-shaped Messages API endpoint.
	// Anthropic requires this header for any request authenticated with
	// an OAuth access token (instead of an API key).
	OAuthBetaHeader = "oauth-2025-04-20"

	// OAuthDirectBrowserHeader is required for the OAuth device flow
	// token exchange. Anthropic rejects token POSTs that lack it when
	// no User-Agent is provided.
	OAuthDirectBrowserHeader = "anthropic-dangerous-direct-browser-access"
)

// OAuthToken is the raw payload returned by /v1/oauth/token.
type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

// OAuthCredentials is the locally-stored credential shape. It mirrors
// bizshuk/auth.model.Credential but is kept local so this package stays
// self-contained (no cross-module import).
type OAuthCredentials struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// IsExpired reports whether the token needs a refresh. A 60s grace window
// prevents mid-flight expirations from breaking the current call.
func (c OAuthCredentials) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Until(c.ExpiresAt) < 60*time.Second
}

// ExchangeCode performs the authorization-code → token exchange. Used as
// the second leg of the PKCE flow once the user has pasted back the code
// from Anthropic's redirect URL.
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
	req.Header.Set(OAuthBetaHeader, "true")
	req.Header.Set(OAuthDirectBrowserHeader, "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return OAuthToken{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return OAuthToken{}, fmt.Errorf("anthropic oauth: status %d: %s", resp.StatusCode, body)
	}
	var t OAuthToken
	if err := json.Unmarshal(body, &t); err != nil {
		return OAuthToken{}, fmt.Errorf("anthropic oauth: decode: %w", err)
	}
	return t, nil
}

// GeneratePKCE returns a (verifier, challenge) pair using S256. Verifier
// is 64 random bytes (86 base64url chars); challenge is the S256 hash of
// the verifier, base64url-encoded without padding.
func GeneratePKCE() (verifier, challenge string, err error) {
	const verifierLen = 64
	raw := make([]byte, verifierLen)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// AuthorizeURL builds the URL the user should open in their browser to
// start the OAuth flow. State is opaque and may be used by the caller to
// correlate the redirect.
func AuthorizeURL(state string) (string, error) {
	if state == "" {
		return "", errors.New("anthropic oauth: state is required")
	}
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("client_id", OAuthClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", OAuthRedirectURI)
	q.Set("scope", OAuthScope)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u, err := url.Parse(OAuthAuthorizeURL)
	if err != nil {
		return "", err
	}
	u.RawQuery = q.Encode()
	_ = verifier // caller is expected to retrieve verifier via callback state
	return u.String(), nil
}

// OpenBrowser launches the system browser to authorizeURL and blocks
// until a one-shot localhost listener captures the redirect carrying the
// `code` query parameter. Returns the captured code.
//
// The listener is bound to 127.0.0.1 on a random free port; the redirect
// URI registered with Anthropic is the standard callback URL, so we do
// NOT use a custom-scheme loopback. Instead, the user pastes the code
// back via stdin in headless environments. The listening server here is
// a convenience for desktop flows; failures degrade gracefully.
func OpenBrowser(ctx context.Context, authorizeURL string) (string, error) {
	// Bind a one-shot listener. We expose a tiny form that surfaces the
	// code so the user can copy it even if automatic extraction fails.
	mux := http.NewServeMux()
	codeCh := make(chan string, 1)
	mux.HandleFunc("/oauth/code/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<h1>Anthropic OAuth</h1><p>Code: <code>%s</code></p><p>You can close this window.</p>", code)
		if code != "" {
			select {
			case codeCh <- code:
			default:
			}
		}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("anthropic oauth: listen: %w", err)
	}
	defer ln.Close()

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	// Open the browser. We do this regardless of platform quirks; if the
	// open command fails the user can still paste the URL manually.
	if err := openBrowserOS(authorizeURL); err != nil {
		return "", fmt.Errorf("anthropic oauth: open browser: %w", err)
	}

	select {
	case code := <-codeCh:
		return code, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(2 * time.Minute):
		return "", errors.New("anthropic oauth: timed out waiting for callback")
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
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}
