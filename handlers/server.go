// Package proxy assembles the proxy HTTP surface. It builds its own
// gin.Engine (rather than gosdk's server.Run, which owns its engine and does
// not expose a route hook) while still reusing gosdk's middleware and the
// health/ping router groups.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/bizshuk/auth/active"
	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/auth/provider"
	"github.com/bizshuk/gosdk/file"
	"github.com/bizshuk/gosdk/mw"
	"github.com/bizshuk/gosdk/router"
	pxconfig "github.com/bizshuk/proxy/config"
	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/svc/transform"
	"github.com/bizshuk/proxy/svc/upstream"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
)

const (
	SHUTDOWN_TIMEOUT    = 10 * time.Second
	READ_HEADER_TIMEOUT = 10 * time.Second
	IDLE_TIMEOUT        = 2 * time.Minute
	MAX_HEADER_BYTES    = 1 << 20
	BYTES_PER_MEBIBYTE  = int64(1 << 20)
	MAX_BODY_LIMIT_MB   = math.MaxInt64 / BYTES_PER_MEBIBYTE
)

// Server holds the assembled engine and its runtime config.
type Server struct {
	cfg      *pxconfig.Config
	engine   *gin.Engine
	handler  *Handler
	realtime *RealtimeHandler
}

// New builds the engine with the full middleware stack and route table.
func New(cfg *pxconfig.Config) (*Server, error) {
	if cfg == nil {
		return nil, fmt.Errorf("new proxy server: config is required")
	}
	if cfg.BodyLimit <= 0 || int64(cfg.BodyLimit) > MAX_BODY_LIMIT_MB {
		return nil, fmt.Errorf("new proxy server: body limit must be between 1 and %d MB", MAX_BODY_LIMIT_MB)
	}
	store, err := file.NewStore[*authmodel.Credential](cfg.AuthDir,
		file.WithDirPerm(0o700), file.WithFilePerm(0o600))
	if err != nil {
		return nil, fmt.Errorf("new proxy server credential store: %w", err)
	}
	credentials := upstream.NewCredentialResolver(store, func(credential *authmodel.Credential) (authmodel.Authenticator, error) {
		return provider.For(credential)
	}, os.LookupEnv, active.Lookup)
	catalog, err := upstream.DefaultCatalog()
	if err != nil {
		return nil, fmt.Errorf("new proxy server catalog: %w", err)
	}
	modelRouter, err := catalog.NewRouter()
	if err != nil {
		return nil, fmt.Errorf("new proxy server router: %w", err)
	}
	registry, err := transform.NewDefaultRegistry()
	if err != nil {
		return nil, fmt.Errorf("new proxy server transform registry: %w", err)
	}
	client, err := upstream.NewClient(http.DefaultClient, cfg.Timeouts)
	if err != nil {
		return nil, fmt.Errorf("new proxy server upstream client: %w", err)
	}
	observer, err := NewTransformObserver(slog.Default(), otel.Meter("github.com/bizshuk/proxy/proxy"))
	if err != nil {
		return nil, fmt.Errorf("new proxy server observer: %w", err)
	}
	// Phase C: wire the agentsdk provider dispatcher alongside the
	// legacy Profile/Catalog. The dispatcher carries the live
	// provider.Adapter instances (anthropic / ollama / grok /
	// antigravity / codex / minimax / google) so /v1/models can serve
	// their bundled catalogs.
	//
	// The store — not the proxy's resolver — is what goes in: agentsdk's
	// credential package builds its own resolver per family, keyed by the
	// auth route it owns the mapping for. The proxy's CredentialResolver
	// stays on the request path, where its job is error-surface mapping.
	//
	// Prefer auth-store credentials (OAuth login + API keys), falling
	// back to env vars for any family without a stored credential.
	dispatcher, err := upstream.NewDispatcherWithAuthAndEnv(store)
	if err != nil {
		return nil, fmt.Errorf("new proxy server dispatcher: %w", err)
	}
	handler, err := NewHandler(HandlerDeps{
		Router: modelRouter, Registry: registry, Catalog: catalog,
		Dispatcher:  dispatcher,
		Credentials: credentials, Client: client, Observer: observer,
		MaxBodyBytes: int64(cfg.BodyLimit) * BYTES_PER_MEBIBYTE,
	})
	if err != nil {
		return nil, fmt.Errorf("new proxy server handler: %w", err)
	}
	var realtimeHandler *RealtimeHandler
	if cfg.Realtime.Enabled {
		realtimeHandler, err = NewRealtimeHandler(RealtimeHandlerDeps{
			Catalog:           catalog,
			Credentials:       credentials,
			MaxConnections:    cfg.Realtime.MaxConnections,
			MaxHandshakeBytes: cfg.Realtime.MaxHandshakeBytes,
		})
		if err != nil {
			return nil, fmt.Errorf("new proxy server realtime handler: %w", err)
		}
	}
	// Default to release mode; GIN_MODE env var still overrides when set.
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(mw.CorrelationID())
	engine.Use(mw.Helmet())
	engine.Use(corsLocalhost())

	// gosdk-provided operational endpoints (/healthz, /ping).
	router.HealthRouterGroup(engine)
	router.PingRouterGroup(engine)

	s := &Server{cfg: cfg, engine: engine, handler: handler, realtime: realtimeHandler}
	s.registerRoutes()
	return s, nil
}

// registerRoutes wires the public API surface to the generic pairwise handler.
func (s *Server) registerRoutes() {
	s.engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	keys := s.cfg.APIKeySet()
	v1 := s.engine.Group("/v1", requireAPIKey(keys), rateLimitPerIP())
	{
		v1.GET("/models", s.handler.HandleModels())
		v1.POST("/chat/completions", s.handler.Handle(model.FORMAT_OPENAI_CHAT))
		v1.POST("/responses", s.handler.Handle(model.FORMAT_OPENAI_RESPONSES))
		v1.POST("/messages", s.handler.Handle(model.FORMAT_ANTHROPIC_MESSAGES))
		v1.POST("/messages/count_tokens", s.handler.HandleCountTokens())
		v1.POST("/images/generations", s.handler.HandleImageGenerations())
		v1.POST("/images/edits", s.handler.HandleImageEdits())
		if s.realtime != nil {
			v1.GET("/realtime", s.realtime.HandleWebSocket())
			v1.POST("/realtime/calls", s.realtime.HandleHandshake(upstream.OPENAI_REALTIME_CALLS_ENDPOINT))
			v1.POST("/realtime/client_secrets", s.realtime.HandleHandshake(upstream.OPENAI_REALTIME_CLIENT_SECRETS_ENDPOINT))
		}
	}

	admin := s.engine.Group("/admin", requireAPIKey(keys))
	{
		admin.GET("/accounts", notImplemented("admin/accounts"))
		admin.GET("/stats", notImplemented("admin/stats"))
		admin.POST("/reload", notImplemented("admin/reload"))
	}
}

func notImplemented(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error": gin.H{"message": name + " not implemented yet"},
		})
	}
}

// Run starts the listener and blocks until ctx is cancelled, then performs a
// graceful shutdown. Mirrors gosdk/server.Run's lifecycle for consistency.
func (s *Server) Run(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	srv := newHTTPServer(addr, s.engine)

	listenErr := make(chan error, 1)
	go func() {
		slog.Info("proxy listening", "addr", addr)
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
			return
		}
		listenErr <- nil
	}()

	select {
	case err := <-listenErr:
		if err != nil {
			return fmt.Errorf("server listener: %w", err)
		}
		return nil
	case <-ctx.Done():
		slog.Info("shutdown requested", "reason", ctx.Err())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), SHUTDOWN_TIMEOUT)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}
	<-listenErr
	return nil
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: READ_HEADER_TIMEOUT,
		IdleTimeout:       IDLE_TIMEOUT,
		MaxHeaderBytes:    MAX_HEADER_BYTES,
		// Streaming responses such as SSE must not be terminated by a fixed deadline.
		WriteTimeout: 0,
	}
}
