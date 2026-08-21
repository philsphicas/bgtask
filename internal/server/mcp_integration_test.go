package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/philsphicas/bgtask/internal/mcpserver"
	"github.com/philsphicas/bgtask/internal/server"
)

// mcpToolNames connects an MCP client to url and returns the discovered
// tool names, failing the test on any error.
func mcpToolNames(t *testing.T, url string) []string {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "bgtask-test-client", Version: "v0.0.0"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// TestMCP_DefaultCombinedExposeConstructsAndBothWork mirrors bgtask serve's
// default --expose (mcp and rest, both mounted on the same server): with a
// real internal/mcpserver.NewHandler wired in as Options.MCPHandler,
// construction must succeed and both the MCP tool surface and the REST API
// must work side by side.
func TestMCP_DefaultCombinedExposeConstructsAndBothWork(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{
		Service:    svc,
		Expose:     []server.Exposure{server.ExposeMCP, server.ExposeREST},
		MCPHandler: mcpserver.NewHandler(svc, "test"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	names := mcpToolNames(t, ts.URL+"/mcp")
	if len(names) != 11 {
		t.Errorf("discovered %d MCP tools, want 11: %v", len(names), names)
	}

	resp, err := http.Get(ts.URL + "/api/v1/tasks")
	if err != nil {
		t.Fatalf("GET /api/v1/tasks: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/v1/tasks status = %d, want 200", resp.StatusCode)
	}
}

// TestMCP_OnlyExposureWorks verifies an MCP-only server (no REST) serves
// the full bgtask_* tool surface, while the REST API is not mounted.
func TestMCP_OnlyExposureWorks(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{
		Expose:     []server.Exposure{server.ExposeMCP},
		MCPHandler: mcpserver.NewHandler(svc, "test"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	names := mcpToolNames(t, ts.URL+"/mcp")
	if len(names) != 11 {
		t.Errorf("discovered %d MCP tools, want 11: %v", len(names), names)
	}

	resp, err := http.Get(ts.URL + "/api/v1/tasks")
	if err != nil {
		t.Fatalf("GET /api/v1/tasks: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /api/v1/tasks status = %d, want 404 (rest not mounted)", resp.StatusCode)
	}
}

// TestMCP_OriginRejectedThroughFullServerWrapper verifies that bgtask's
// Origin allow-list middleware -- applied once, around the whole handler
// stack -- also protects the real MCP endpoint, not just REST routes: a
// disallowed Origin header must be rejected with 403 before the request
// ever reaches the MCP handler.
func TestMCP_OriginRejectedThroughFullServerWrapper(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{
		Expose:       []server.Exposure{server.ExposeMCP},
		MCPHandler:   mcpserver.NewHandler(svc, "test"),
		AllowOrigins: []string{"https://allowed.example"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (disallowed Origin)", resp.StatusCode)
	}

	// Sanity check: the same request with no Origin header at all (the
	// common case for non-browser MCP clients) is not rejected by the
	// origin middleware -- any remaining failure must come from the MCP
	// layer itself, not from a 403.
	req2, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Accept", "application/json, text/event-stream")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("POST /mcp (no Origin): %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode == http.StatusForbidden {
		t.Errorf("status = %d, want anything but 403 when no Origin header is sent", resp2.StatusCode)
	}
}
