// OAuth support for xAI Grok (SuperGrok / X Premium subscription).
//
// The flow follows xAI's public OAuth2 endpoints documented at
// https://docs.x.ai/docs/authentication:
//
//	Authorize : https://accounts.x.ai/oauth/authorize    (browser redirect)
//	Token     : https://api.x.ai/v1/oauth/token
//
// We use PKCE S256; the redirect URI is a localhost loopback. The
// client_id is xAI's public CLI client id; it is embedded in the xAI CLI
// binary so it is not a secret.
//
// Note: xAI's public OAuth URLs are still settling — the constants below
// are plausible defaults that match the documented shape; if xAI ships a
// different exact path we'll update via a single constant edit.

package grok

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
	// OAuthAuthorizeURL is where the user lands to start the PKCE flow.
	OAuthAuthorizeURL = "https://accounts.x.ai/oauth/authorize"

	// OAuthTokenURL is the token endpoint for both code-exchange and
	// refresh-token grants.
	OAuthTokenURL = "https://api.x.ai/v1/oauth/token"

	// OAuthClientID is xAI's public CLI client id. It is not a secret.
	OAuthClientID = "xai-cli"

	// OAuthRedirectURI is the loopback the OAuth flow returns to. xAI's
	// public docs accept this URI for native / CLI flows.
	OAuthRedirectURI = "http://localhost:1455/auth/callback"

	// OAuthScope requests the permissions needed to call chat-completions.
	OAuthScope = "chat:completions"
)

// OAuthToken is the raw payload returned by /v1/oauth/token.
type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope,omitempty"`
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

// ExchangeCode performs the authorization-code -> token exchange. Used
// as the second leg of the PKCE flow once the user has pasted back the
// code (or the redirect server captured it).
func ExchangeCode(ctx context.Context, code, verifier string) (OAuthToken, error) {
	if code == "" {
		return OAuthToken{}, errors.New("grok oauth: code is empty")
	}
	if verifier == "" {
		return OAuthToken{}, errors.New("grok oauth: code_verifier is empty")
	}
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
	if refresh == "" {
		return OAuthToken{}, errors.New("grok oauth: refresh_token is empty")
	}
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
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return OAuthToken{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return OAuthToken{}, fmt.Errorf("grok oauth: status %d: %s", resp.StatusCode, body)
	}
	var t OAuthToken
	if err := json.Unmarshal(body, &t); err != nil {
		return OAuthToken{}, fmt.Errorf("grok oauth: decode: %w", err)
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
		return "", errors.New("grok oauth: state is required")
	}
	_, challenge, err := GeneratePKCE()
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
	return u.String(), nil
}

// OpenBrowser launches the system browser to authorizeURL and blocks
// until a one-shot localhost listener captures the redirect carrying the
// `code` query parameter. Returns the captured code.
//
// The listener is bound to 127.0.0.1:1455 (matching OAuthRedirectURI);
// the redirect URL registered with xAI is the standard callback URL.
// If the platform open command fails the user can still paste the URL
// manually and the listener will still capture the code.
func OpenBrowser(ctx context.Context, authorizeURL string) (string, error) {
	mux := http.NewServeMux()
	codeCh := make(chan string, 1)
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<h1>Grok OAuth</h1><p>Code: <code>%s</code></p><p>You can close this window.</p>", code)
		if code != "" {
			select {
			case codeCh <- code:
			default:
			}
		}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return "", fmt.Errorf("grok oauth: listen: %w", err)
	}
	defer ln.Close()

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := openBrowserOS(authorizeURL); err != nil {
		return "", fmt.Errorf("grok oauth: open browser: %w", err)
	}

	select {
	case code := <-codeCh:
		return code, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(2 * time.Minute):
		return "", errors.New("grok oauth: timed out waiting for callback")
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