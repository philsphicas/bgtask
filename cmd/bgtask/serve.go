package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/philsphicas/bgtask/internal/mcpserver"
	"github.com/philsphicas/bgtask/internal/server"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// ServeCmd starts bgtask's foreground server: a single process exposing a
// REST API and/or an MCP endpoint over HTTP, in contrast to the CLI's usual
// pattern of one short-lived invocation per operation.
type ServeCmd struct {
	Bind        string   `help:"Address to bind." default:"127.0.0.1"`
	Port        int      `help:"Port to listen on (0 picks a free port)." default:"8420"`
	Expose      []string `help:"Interface(s) to expose (repeatable): mcp, rest." enum:"mcp,rest" default:"mcp,rest"`
	AllowOrigin []string `help:"Allowed Origin header value for browser-based clients (repeatable)." placeholder:"ORIGIN"`
}

// mcpHandler is the explicit mounting seam for bgtask's MCP endpoint: a
// stateless Streamable HTTP handler exposing the bgtask_* tool surface
// (internal/mcpserver) over svc. version is reported to MCP clients as the
// server implementation's version.
func mcpHandler(svc *taskservice.Service, version string) http.Handler {
	return mcpserver.NewHandler(svc, version)
}

// toExposures converts the CLI's validated string slice to
// []server.Exposure.
func toExposures(values []string) []server.Exposure {
	out := make([]server.Exposure, len(values))
	for i, v := range values {
		out[i] = server.Exposure(v)
	}
	return out
}

// buildServer constructs the server.Server for this invocation without
// binding a socket or blocking, so its construction-time validation can be
// exercised directly in a unit test. mcpVersion is reported to MCP clients
// as the server implementation's version (typically bgtask's own build
// version, main.version).
func (c *ServeCmd) buildServer(svc *taskservice.Service, logger *slog.Logger, mcpVersion string) (*server.Server, error) {
	return server.New(server.Options{
		Service:      svc,
		Expose:       toExposures(c.Expose),
		AllowOrigins: c.AllowOrigin,
		MCPHandler:   mcpHandler(svc, mcpVersion),
		Logger:       logger,
	})
}

// startupEvent is the single machine-readable JSON line bgtask serve
// prints to stdout once it is listening, so tooling can discover the
// resolved address (notably useful with --port 0) without scraping
// human-oriented log output.
type startupEvent struct {
	Event string `json:"event"`
	Addr  string `json:"addr"`
	PID   int    `json:"pid"`
}

func printStartupLine(w io.Writer, addr string) error {
	return json.NewEncoder(w).Encode(startupEvent{Event: "listening", Addr: addr, PID: os.Getpid()})
}

func (c *ServeCmd) Run(svc *taskservice.Service) error {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	srv, err := c.buildServer(svc, logger, version)
	if err != nil {
		return err
	}

	ln, err := srv.Listen(c.Bind, c.Port)
	if err != nil {
		return err
	}

	if err := printStartupLine(os.Stdout, ln.Addr().String()); err != nil {
		return fmt.Errorf("print startup line: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return <-serveErr
	case err := <-serveErr:
		return err
	}
}
