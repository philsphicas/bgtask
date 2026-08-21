package server_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/philsphicas/bgtask/internal/server"
)

func TestRequestID_SetOnEveryResponse(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{Service: svc, Expose: []server.Exposure{server.ExposeREST}})
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

	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("expected X-Request-Id header to be set")
	}
}

func TestRequestID_MatchesErrorEnvelope(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{Service: svc, Expose: []server.Exposure{server.ExposeREST}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/tasks/does-not-exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	headerID := resp.Header.Get("X-Request-Id")
	if headerID == "" {
		t.Fatal("expected X-Request-Id header to be set")
	}

	var body server.ErrorResponseJSON
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.RequestID != headerID {
		t.Errorf("envelope request_id = %q, want %q (header value)", body.Error.RequestID, headerID)
	}
}

// panicHandler is used to exercise recoverMiddleware through the exported
// MCP mounting seam, since the middleware itself is unexported.
type panicHandler struct{}

func (panicHandler) ServeHTTP(http.ResponseWriter, *http.Request) {
	panic("boom")
}

func TestRecoverMiddleware_TurnsPanicInto500(t *testing.T) {
	srv, err := server.New(server.Options{
		Expose:     []server.Exposure{server.ExposeMCP},
		MCPHandler: panicHandler{},
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

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	var body server.ErrorResponseJSON
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "internal" {
		t.Errorf("code = %q, want %q", body.Error.Code, "internal")
	}
	if body.Error.RequestID == "" {
		t.Error("expected the recovered error envelope to carry a request_id")
	}
}

func TestAuthMiddleware_Seam(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{
		Service: svc,
		Expose:  []server.Exposure{server.ExposeREST},
		Auth: func(r *http.Request) error {
			if r.Header.Get("Authorization") == "Bearer good" {
				return nil
			}
			return errors.New("missing or invalid credentials")
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Unauthenticated REST request is rejected.
	resp, err := http.Get(ts.URL + "/api/v1/tasks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}

	// /healthz is always exempt from auth.
	healthResp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer healthResp.Body.Close()
	if healthResp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d, want 200 even with auth configured", healthResp.StatusCode)
	}

	// A valid credential passes through.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/tasks", nil)
	req.Header.Set("Authorization", "Bearer good")
	okResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with auth: %v", err)
	}
	defer okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK {
		t.Errorf("authenticated status = %d, want 200", okResp.StatusCode)
	}
}

func TestAuthMiddleware_NilIsNoop(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{Service: svc, Expose: []server.Exposure{server.ExposeREST}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/tasks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 with no Auth configured", resp.StatusCode)
	}
}

func TestOriginMiddleware_AbsentOriginAllowed(t *testing.T) {
	called := false
	mw := server.OriginMiddleware([]string{"https://allowed.example"})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	h.ServeHTTP(rec, req)

	if !called {
		t.Error("expected the wrapped handler to run for a request with no Origin header")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestOriginMiddleware_AllowedOriginPasses(t *testing.T) {
	mw := server.OriginMiddleware([]string{"https://allowed.example"})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://allowed.example")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for an allow-listed origin", rec.Code)
	}
}

func TestOriginMiddleware_DisallowedOriginRejected(t *testing.T) {
	called := false
	mw := server.OriginMiddleware([]string{"https://allowed.example"})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://evil.example")
	h.ServeHTTP(rec, req)

	if called {
		t.Error("expected the wrapped handler NOT to run for a disallowed origin")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a disallowed origin", rec.Code)
	}
}

func TestOriginMiddleware_EmptyAllowListRejectsAnyOrigin(t *testing.T) {
	mw := server.OriginMiddleware(nil)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://anything.example")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 when the allow-list is empty and Origin is set", rec.Code)
	}
}

func TestOriginMiddleware_IntegratedWithServer(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{
		Service:      svc,
		Expose:       []server.Exposure{server.ExposeREST},
		AllowOrigins: []string{"https://allowed.example"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/tasks", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a disallowed Origin", resp.StatusCode)
	}
}
