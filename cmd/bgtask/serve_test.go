package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// parseServeArgs parses args against a standalone ServeCmd, mirroring how
// kong parses it as part of the real CLI, without touching the
// package-level CLI var (so tests can't interfere with each other or with
// main's own parsing).
func parseServeArgs(t *testing.T, args ...string) *ServeCmd {
	t.Helper()
	var cli struct {
		Serve ServeCmd `cmd:""`
	}
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse(append([]string{"serve"}, args...)); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return &cli.Serve
}

func TestServeCmd_Defaults(t *testing.T) {
	c := parseServeArgs(t)
	if c.Bind != "127.0.0.1" {
		t.Errorf("Bind = %q, want %q", c.Bind, "127.0.0.1")
	}
	if c.Port != 8420 {
		t.Errorf("Port = %d, want %d", c.Port, 8420)
	}
	if len(c.Expose) != 2 || c.Expose[0] != "mcp" || c.Expose[1] != "rest" {
		t.Errorf("Expose = %v, want [mcp rest]", c.Expose)
	}
	if len(c.AllowOrigin) != 0 {
		t.Errorf("AllowOrigin = %v, want empty", c.AllowOrigin)
	}
}

func TestServeCmd_PortZeroAccepted(t *testing.T) {
	c := parseServeArgs(t, "--port=0")
	if c.Port != 0 {
		t.Errorf("Port = %d, want 0", c.Port)
	}
}

func TestServeCmd_ExposeRestOnly(t *testing.T) {
	c := parseServeArgs(t, "--expose=rest")
	if len(c.Expose) != 1 || c.Expose[0] != "rest" {
		t.Errorf("Expose = %v, want [rest]", c.Expose)
	}
}

func TestServeCmd_ExposeRepeatable(t *testing.T) {
	c := parseServeArgs(t, "--expose=rest", "--expose=mcp")
	if len(c.Expose) != 2 {
		t.Errorf("Expose = %v, want 2 entries", c.Expose)
	}
}

func TestServeCmd_ExposeRejectsUnknownValue(t *testing.T) {
	var cli struct {
		Serve ServeCmd `cmd:""`
	}
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse([]string{"serve", "--expose=bogus"}); err == nil {
		t.Fatal("expected an error for --expose=bogus")
	}
}

func TestServeCmd_AllowOriginRepeatable(t *testing.T) {
	c := parseServeArgs(t, "--allow-origin=https://a.example", "--allow-origin=https://b.example")
	if len(c.AllowOrigin) != 2 {
		t.Fatalf("AllowOrigin = %v, want 2 entries", c.AllowOrigin)
	}
	if c.AllowOrigin[0] != "https://a.example" || c.AllowOrigin[1] != "https://b.example" {
		t.Errorf("AllowOrigin = %v, unexpected values", c.AllowOrigin)
	}
}

func TestServeCmd_BindOverride(t *testing.T) {
	c := parseServeArgs(t, "--bind=0.0.0.0")
	if c.Bind != "0.0.0.0" {
		t.Errorf("Bind = %q, want %q", c.Bind, "0.0.0.0")
	}
}

// TestBuildServer_DefaultExposeSucceeds is the key construction-seam test
// at the CLI layer: mcpHandler(svc, version) now returns a real MCP
// handler, so `bgtask serve` with no flags (--expose defaults to both mcp
// and rest) must construct successfully.
func TestBuildServer_DefaultExposeSucceeds(t *testing.T) {
	svc := newTestService(t)
	c := parseServeArgs(t)

	srv, err := c.buildServer(svc, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), "test")
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	if srv == nil {
		t.Fatal("expected a non-nil *server.Server")
	}
}

// TestBuildServer_RestOnlySucceeds verifies the escape hatch: passing
// --expose rest still builds a working, REST-only server.
func TestBuildServer_RestOnlySucceeds(t *testing.T) {
	svc := newTestService(t)
	c := parseServeArgs(t, "--expose=rest")

	srv, err := c.buildServer(svc, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), "test")
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	if srv == nil {
		t.Fatal("expected a non-nil *server.Server")
	}
}

// TestBuildServer_MCPOnlySucceeds verifies an MCP-only server (no REST
// fallback) also constructs successfully.
func TestBuildServer_MCPOnlySucceeds(t *testing.T) {
	svc := newTestService(t)
	c := parseServeArgs(t, "--expose=mcp")

	srv, err := c.buildServer(svc, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), "test")
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	if srv == nil {
		t.Fatal("expected a non-nil *server.Server")
	}
}

func TestPrintStartupLine_MachineReadable(t *testing.T) {
	var buf bytes.Buffer
	if err := printStartupLine(&buf, "127.0.0.1:54321"); err != nil {
		t.Fatalf("printStartupLine: %v", err)
	}

	var evt startupEvent
	if err := json.Unmarshal(buf.Bytes(), &evt); err != nil {
		t.Fatalf("startup line is not valid JSON: %v (line: %s)", err, buf.String())
	}
	if evt.Event != "listening" {
		t.Errorf("Event = %q, want %q", evt.Event, "listening")
	}
	if evt.Addr != "127.0.0.1:54321" {
		t.Errorf("Addr = %q, want %q", evt.Addr, "127.0.0.1:54321")
	}
	if evt.PID == 0 {
		t.Error("expected a non-zero PID")
	}

	// Exactly one line: printStartupLine must emit a single record.
	if strings.Count(strings.TrimSpace(buf.String()), "\n") != 0 {
		t.Errorf("expected exactly one line, got: %q", buf.String())
	}
}

// newTestService builds a minimal taskservice.Service around a temp-dir
// store for buildServer tests, which never actually launch a task.
func newTestService(t *testing.T) *taskservice.Service {
	t.Helper()
	return taskservice.New(&state.Store{Root: t.TempDir()})
}
