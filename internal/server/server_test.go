package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/philsphicas/bgtask/internal/server"
)

func TestNew_RequiresAtLeastOneExposure(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := server.New(server.Options{Service: svc, Expose: nil})
	if err == nil {
		t.Fatal("expected an error when Expose is empty")
	}
}

func TestNew_RejectsUnknownExposure(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := server.New(server.Options{Service: svc, Expose: []server.Exposure{"bogus"}})
	if err == nil {
		t.Fatal("expected an error for an unknown exposure")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error = %v, want it to mention the bad value", err)
	}
}

func TestNew_RestRequiresService(t *testing.T) {
	_, err := server.New(server.Options{Expose: []server.Exposure{server.ExposeREST}})
	if err == nil {
		t.Fatal("expected an error when rest is exposed with a nil Service")
	}
}

// TestNew_MCPRequiresHandler is the key construction-seam test: requesting
// mcp exposure without a non-nil Options.MCPHandler must fail clearly,
// regardless of whether a real handler is wired in elsewhere (as
// cmd/bgtask does via internal/mcpserver.NewHandler).
func TestNew_MCPRequiresHandler(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := server.New(server.Options{
		Service: svc,
		Expose:  []server.Exposure{server.ExposeMCP, server.ExposeREST},
	})
	if err == nil {
		t.Fatal("expected an error when mcp is exposed with a nil MCPHandler")
	}
	if !strings.Contains(err.Error(), "mcp") {
		t.Errorf("error = %v, want it to mention mcp", err)
	}
}

// stubHandler is a trivial http.Handler used to verify the MCP mounting
// seam without any real MCP dependency.
type stubHandler struct{ body string }

func (s stubHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(s.body))
}

func TestNew_MountsMCPHandlerWhenExposed(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{
		Service:    svc,
		Expose:     []server.Exposure{server.ExposeMCP, server.ExposeREST},
		MCPHandler: stubHandler{body: "mcp-stub-ok"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/mcp")
	if err != nil {
		t.Fatalf("GET /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	// REST must also still work when both exposures are configured.
	resp2, err := http.Get(ts.URL + "/api/v1/tasks")
	if err != nil {
		t.Fatalf("GET /api/v1/tasks: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp2.StatusCode)
	}
}

func TestNew_RestOnly_MCPPathNotMounted(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{
		Service: svc,
		Expose:  []server.Exposure{server.ExposeREST},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/mcp")
	if err != nil {
		t.Fatalf("GET /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (mcp not mounted)", resp.StatusCode)
	}
}

func TestNew_MCPOnly_RestNotMounted(t *testing.T) {
	srv, err := server.New(server.Options{
		Expose:     []server.Exposure{server.ExposeMCP},
		MCPHandler: stubHandler{body: "ok"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/tasks")
	if err != nil {
		t.Fatalf("GET /api/v1/tasks: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (rest not mounted)", resp.StatusCode)
	}
}

// TestHealthz_AlwaysMounted verifies /healthz responds 200 regardless of
// which exposure(s) are configured.
func TestHealthz_AlwaysMounted(t *testing.T) {
	for _, expose := range [][]server.Exposure{
		{server.ExposeREST},
		{server.ExposeMCP},
		{server.ExposeREST, server.ExposeMCP},
	} {
		expose := expose
		t.Run(strings.Join(exposeStrings(expose), "+"), func(t *testing.T) {
			svc, _ := newTestService(t)
			opts := server.Options{Expose: expose}
			for _, e := range expose {
				if e == server.ExposeREST {
					opts.Service = svc
				}
				if e == server.ExposeMCP {
					opts.MCPHandler = stubHandler{body: "ok"}
				}
			}
			srv, err := server.New(opts)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			ts := httptest.NewServer(srv.Handler())
			defer ts.Close()

			resp, err := http.Get(ts.URL + "/healthz")
			if err != nil {
				t.Fatalf("GET /healthz: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200", resp.StatusCode)
			}
		})
	}
}

func exposeStrings(exposures []server.Exposure) []string {
	out := make([]string, len(exposures))
	for i, e := range exposures {
		out[i] = string(e)
	}
	return out
}
