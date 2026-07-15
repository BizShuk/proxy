// Package proxy assembles the proxy HTTP surface. It builds its own
// gin.Engine (rather than gosdk's server.Run, which owns its engine and does
// not expose a route hook) while still reusing gosdk's middleware and the
// health/ping router groups.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/bizshuk/agentsdk/config"
	"github.com/bizshuk/proxy/adaptor"
	"github.com/bizshuk/gosdk/mw"
	"github.com/bizshuk/gosdk/router"
	"github.com/gin-gonic/gin"
)

const shutdownTimeout = 10 * time.Second

// Server holds the assembled engine and its runtime config.
type Server struct {
	cfg    *config.ProxyConfig
	engine *gin.Engine
}

// New builds the engine with the full middleware stack and route table.
func New(cfg *config.ProxyConfig) *Server {
	if cfg.Debug == "off" {
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

	s := &Server{cfg: cfg, engine: engine}
	s.registerRoutes()
	return s
}

// registerRoutes wires the public API surface. Handlers are stubbed at P0 and
// filled in per milestone (anthropic → translate → codex → cursor).
func (s *Server) registerRoutes() {
	s.engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	ad, err := adaptor.New(s.cfg)
	if err != nil {
		slog.Error("failed to initialize adaptor", "err", err)
	}

	keys := s.cfg.APIKeySet()
	v1 := s.engine.Group("/v1", requireAPIKey(keys), rateLimitPerIP())
	{
		if ad != nil {
			v1.GET("/models", ad.HandleModels())
			v1.POST("/chat/completions", ad.HandleChatCompletions())
			v1.POST("/responses", ad.HandleResponses())
			v1.POST("/messages", ad.HandleMessages())
			v1.POST("/messages/count_tokens", ad.HandleCountTokens())
		} else {
			v1.GET("/models", notImplemented("models"))
			v1.POST("/chat/completions", notImplemented("chat/completions"))
			v1.POST("/responses", notImplemented("responses"))
			v1.POST("/messages", notImplemented("messages"))
			v1.POST("/messages/count_tokens", notImplemented("count_tokens"))
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
	srv := &http.Server{Addr: addr, Handler: s.engine}

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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}
	<-listenErr
	return nil
}
