package antigravity

// Google OAuth support for Antigravity. The flow follows Google's public
// OAuth2 endpoints (PKCE, no client_secret) and matches what CLIProxyAPI
// documents at https://help.router-for-me/configuration/provider/antigravity:
//
//	Authorize : https://accounts.google.com/o/oauth2/v2/auth  (browser redirect)
//	Token     : https://oauth2.googleapis.com/token
//	Callback  : http://localhost:51121/callback
//
// The Antigravity client_id is embedded in the public CLI binaries so it is
// not a secret.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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
	// OAuthAuthorizeURL is the Google OAuth2 authorize endpoint.
	OAuthAuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"

	// OAuthTokenURL is the Google OAuth2 token endpoint.
	OAuthTokenURL = "https://oauth2.googleapis.com/token"

	// OAuthClientID is the Antigravity CLI's public client id.
	// TODO: confirm against the live Antigravity client config.
	OAuthClientID = "antigravity-cli"

	// OAuthClientSecret is intentionally empty — Antigravity uses a
	// PKCE-only public-client flow (no client_secret).
	OAuthClientSecret = ""

	// OAuthRedirectURI is the loopback URL the Antigravity CLI registers
	// with Google. The CLIProxyAPI docs reference port 51121.
	OAuthRedirectURI = "http://localhost:51121/callback"

	// OAuthScope requests the minimum set needed for Antigravity access.
	// cloud-platform covers Vertex AI / general Google API access.
	OAuthScope = "openid email profile https://www.googleapis.com/auth/cloud-platform"

	// OAuthCallbackPort is the loopback port the callback listener binds.
	OAuthCallbackPort = 51121
)

// OAuthToken is the raw payload returned by Google's /token endpoint.
type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	IDToken      string `json:"id_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// OAuthCredentials is the locally-stored credential shape used by
// NewWithOAuth. Mirrors bizshuk/auth.model.Credential but is kept local
// so this package stays self-contained.
type OAuthCredentials struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Email        string // populated after first login
}

// IsExpired reports whether the token needs a refresh. A 60s grace window
// prevents mid-flight expirations from breaking the current call.
func (c OAuthCredentials) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Until(c.ExpiresAt) < 60*time.Second
}

// AuthorizeURL builds the URL the user should open in their browser to
// start the OAuth flow. state is opaque; challenge is the S256 PKCE
// challenge derived from a freshly generated verifier (the caller stores
// the verifier to redeem the code later).
func AuthorizeURL(state, challenge string) string {
	params := url.Values{}
	params.Set("client_id", OAuthClientID)
	params.Set("redirect_uri", OAuthRedirectURI)
	params.Set("response_type", "code")
	params.Set("scope", OAuthScope)
	params.Set("state", state)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("access_type", "offline")
	params.Set("prompt", "consent")
	return OAuthAuthorizeURL + "?" + params.Encode()
}

// ExchangeCode performs the authorization-code → token exchange. Used as
// the second leg of the PKCE flow once the user has been redirected back
// to the loopback listener.
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return OAuthToken{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return OAuthToken{}, fmt.Errorf("antigravity oauth: status %d: %s", resp.StatusCode, body)
	}
	var t OAuthToken
	if err := json.Unmarshal(body, &t); err != nil {
		return OAuthToken{}, fmt.Errorf("antigravity oauth: decode: %w", err)
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

// OpenBrowser launches the system browser to authorizeURL and blocks
// until a one-shot listener on OAuthCallbackPort captures the redirect
// carrying the `code` query parameter. Returns the captured code.
//
// The listener is bound to OAuthCallbackPort (51121) on all interfaces —
// matches the redirect URI registered with Google. If the port is busy
// the function returns an error; callers should detect a port conflict
// and surface it to the user.
func OpenBrowser(ctx context.Context, authorizeURL string) (string, error) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("antigravity oauth: no code in callback")
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		codeCh <- code
		fmt.Fprint(w, "Antigravity login complete. You may close this tab.")
	})

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", OAuthCallbackPort))
	if err != nil {
		return "", fmt.Errorf("antigravity oauth: listen :%d: %w", OAuthCallbackPort, err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
	}()

	if err := openBrowser(authorizeURL); err != nil {
		return "", err
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

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "linux":
		cmd = exec.Command("xdg-open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		return fmt.Errorf("antigravity oauth: unsupported platform %s", runtime.GOOS)
	}
	return cmd.Start()
}