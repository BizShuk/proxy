package upstream

import (
	"net/http"
	"strings"

	sdkanthropic "github.com/bizshuk/agentsdk/provider/anthropic"
	ag "github.com/bizshuk/agentsdk/provider/antigravity"
	sdkcodex "github.com/bizshuk/agentsdk/provider/codex"
	sdkgrok "github.com/bizshuk/agentsdk/provider/grok"
	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/proxy/model"
)

// Client-identity headers are the ones a gateway gates on IN ADDITION to the
// credential: who is calling, which client version, which surface. They are
// provider facts, not transport policy, so client.go must not know them —
// it calls whatever hook the selected Profile carries and stays a plain
// HTTP sender.
//
// Every value below is read from the vendor's agentsdk provider package.
// Nothing in this file may introduce a literal header name or version; a
// new one means agentsdk's provider package is missing a constant.

// Surface distinguishes the endpoint families a provider identifies
// differently on. xAI is the reason it exists: Imagine is served from the
// public host and rejects the cli-chat-proxy identity headers that
// inference requires.
type Surface string

const (
	SURFACE_INFERENCE Surface = "inference"
	SURFACE_IMAGE     Surface = "image"
)

// IdentityRequest is everything an identity hook may read. It is a value:
// a hook mutates Header and nothing else.
type IdentityRequest struct {
	Credential *authmodel.Credential
	Envelope   model.RequestEnvelope
	Header     http.Header
	Surface    Surface
}

// ApplyIdentityHeaders stamps one provider's client-identity headers.
// Profiles whose gateway authenticates on the credential alone leave it nil.
type ApplyIdentityHeaders func(IdentityRequest)

// applyAnthropicIdentity sends the dated wire contract every request needs,
// plus the two headers the OAuth surface additionally refuses without.
func applyAnthropicIdentity(request IdentityRequest) {
	request.Header.Set(sdkanthropic.APIVersionHeader, sdkanthropic.APIVersion)
	if request.Credential.Kind != authmodel.KIND_OAUTH {
		return
	}
	request.Header.Set(sdkanthropic.DirectBrowserAccessHeader, sdkanthropic.DirectBrowserAccessValue)
	ensureCommaSeparatedHeader(request.Header, sdkanthropic.OAuthBetaHeader, sdkanthropic.OAuthBetaValue)
}

// applyCodexIdentity sends the CLI identity chatgpt.com matches on. The
// account id selects which ChatGPT account a multi-account token bills
// against and comes from the credential, never from the caller.
func applyCodexIdentity(request IdentityRequest) {
	request.Header.Set(sdkcodex.OriginatorHeader, sdkcodex.CodexOriginator)
	request.Header.Set(sdkcodex.VersionHeader, sdkcodex.CodexVersion)
	request.Header.Set("User-Agent", sdkcodex.CodexUserAgent())
	if accountID := strings.TrimSpace(request.Credential.AccountID); accountID != "" {
		request.Header.Set(sdkcodex.AccountIDHeader, accountID)
	}
}

// applyAntigravityIdentity sends the IDE identity the Cloud Code gateway
// gates on; a request without it is answered 403 even with a valid token.
func applyAntigravityIdentity(request IdentityRequest) {
	request.Header.Set("User-Agent", ag.UserAgent())
	request.Header.Set("X-Client-Name", ag.CLIENT_NAME)
	request.Header.Set("X-Client-Version", ag.ClientVersion)
	request.Header.Set("x-goog-api-client", ag.GOOG_API_CLIENT)
}

// applyXAIIdentity covers the API-key flavor, which only identifies itself
// on the image surface — inference on the public host needs the key alone.
func applyXAIIdentity(request IdentityRequest) {
	if request.Surface != SURFACE_IMAGE {
		return
	}
	applyXAIImageIdentity(request.Header)
}

// applyXAIGrokOAuthIdentity covers the OAuth flavor. cli-chat-proxy wants
// the full CLI identity plus per-request tracking ids; the public image
// host wants only the short form, so the two surfaces diverge here.
func applyXAIGrokOAuthIdentity(request IdentityRequest) {
	if request.Surface == SURFACE_IMAGE {
		applyXAIImageIdentity(request.Header)
		return
	}

	header := request.Header
	header.Set(sdkgrok.TokenAuthHeader, sdkgrok.TokenAuthValue)
	header.Set(sdkgrok.AuthenticateResponseHeader, sdkgrok.AuthenticateResponseValue)
	header.Set(sdkgrok.ClientVersionHeader, sdkgrok.ClientVersion)
	header.Set(sdkgrok.ClientIdentifierHeader, CLIENT_IDENTIFIER)
	header.Set(sdkgrok.ClientModeHeader, sdkgrok.DefaultClientMode)
	header.Set("User-Agent", sdkgrok.UserAgent(CLIENT_IDENTIFIER))
	header.Set(sdkgrok.ModelOverrideHeader, strings.TrimSpace(request.Envelope.Model))

	// Tracking ids correlate a tool loop. The caller may supply them; when
	// it does not, each falls back to the next-broadest id it has so a
	// multi-turn conversation still groups.
	requestID := firstNonBlankHeader(header, sdkgrok.RequestIDHeader, "x-request-id")
	if requestID != "" {
		header.Set(sdkgrok.RequestIDHeader, requestID)
	}
	conversationID := firstNonBlankHeader(header, sdkgrok.ConversationIDHeader)
	if conversationID == "" {
		conversationID = requestID
	}
	if conversationID != "" {
		header.Set(sdkgrok.ConversationIDHeader, conversationID)
	}
	sessionID := firstNonBlankHeader(header, sdkgrok.SessionIDHeader)
	if sessionID == "" {
		sessionID = conversationID
	}
	if sessionID != "" {
		header.Set(sdkgrok.SessionIDHeader, sessionID)
	}
	if strings.TrimSpace(header.Get(sdkgrok.AgentIDHeader)) == "" {
		header.Set(sdkgrok.AgentIDHeader, CLIENT_IDENTIFIER)
	}

	// The account is a property of the token, so a caller-supplied value is
	// dropped rather than merged.
	header.Del(sdkgrok.UserIDHeader)
	if accountID := strings.TrimSpace(request.Credential.AccountID); accountID != "" {
		header.Set(sdkgrok.UserIDHeader, accountID)
	}
}

func applyXAIImageIdentity(header http.Header) {
	header.Set(sdkgrok.ClientVersionHeader, sdkgrok.ClientVersion)
	header.Set(sdkgrok.ClientIdentifierHeader, CLIENT_IDENTIFIER)
	header.Set("User-Agent", sdkgrok.UserAgent(CLIENT_IDENTIFIER))
}
