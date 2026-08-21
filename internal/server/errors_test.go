package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/server"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// The following tests exercise the taskservice.Code -> HTTP status mapping
// indirectly through real routes, since the mapping function itself is
// unexported.
func TestErrorMapping_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/tasks/does-not-exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	var body server.ErrorResponseJSON
	mustDecode(t, resp, &body)
	if body.Error.Code != string(taskservice.CodeNotFound) {
		t.Errorf("code = %q, want %q", body.Error.Code, taskservice.CodeNotFound)
	}
	if body.Error.Retryable {
		t.Error("not_found should not be retryable")
	}
}

func TestErrorMapping_Conflict_DuplicateName(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"dup","command":["sleep","1"]}`)

	resp := postJSON(t, ts.URL+"/api/v1/tasks", `{"name":"dup","command":["sleep","1"]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
	var body server.ErrorResponseJSON
	mustDecode(t, resp, &body)
	if body.Error.Code != string(taskservice.CodeConflict) {
		t.Errorf("code = %q, want %q", body.Error.Code, taskservice.CodeConflict)
	}
}

func TestErrorMapping_FailedPrecondition_StartAlreadyRunning(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"already-running","command":["sleep","1"]}`)

	resp := postJSON(t, ts.URL+"/api/v1/tasks/already-running/start", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
	var body server.ErrorResponseJSON
	mustDecode(t, resp, &body)
	if body.Error.Code != string(taskservice.CodeFailedPrecondition) {
		t.Errorf("code = %q, want %q", body.Error.Code, taskservice.CodeFailedPrecondition)
	}
}

func TestErrorMapping_InvalidArgument(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := postJSON(t, ts.URL+"/api/v1/tasks", `{"command":[]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	var body server.ErrorResponseJSON
	mustDecode(t, resp, &body)
	if body.Error.Code != string(taskservice.CodeInvalidArgument) {
		t.Errorf("code = %q, want %q", body.Error.Code, taskservice.CodeInvalidArgument)
	}
}

func TestErrorEnvelope_MalformedJSON(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := postJSON(t, ts.URL+"/api/v1/tasks", `{not valid json`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestBodyLimit_RunOversized verifies the per-route body limit on the run
// endpoint: a body larger than the configured MaxRunBodyBytes is rejected
// with 413, not silently truncated or accepted.
func TestBodyLimit_RunOversized(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{
		Service:         svc,
		Expose:          []server.Exposure{server.ExposeREST},
		MaxRunBodyBytes: 64,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	huge := `{"command":["echo","` + strings.Repeat("x", 1000) + `"]}`
	resp := postJSON(t, ts.URL+"/api/v1/tasks", huge)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
	var body server.ErrorResponseJSON
	mustDecode(t, resp, &body)
	if body.Error.Code != "payload_too_large" {
		t.Errorf("code = %q, want %q", body.Error.Code, "payload_too_large")
	}
}

func TestBodyLimit_LabelsOversized(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{
		Service:            svc,
		Expose:             []server.Exposure{server.ExposeREST},
		MaxLabelsBodyBytes: 16,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"lbl","command":["sleep","1"]}`)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/lbl/labels", bytes.NewBufferString(`{"labels":["a-very-long-label-value-that-exceeds-the-limit"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

// TestBusyError_SetsRetryAfter provokes a real CodeBusy by holding a
// task's lock externally (via the same on-disk state.Store) while a
// request that needs to acquire it runs with a short LockWaitTimeout, then
// verifies the resulting response is 429 with a Retry-After header.
func TestBusyError_SetsRetryAfter(t *testing.T) {
	svc, _ := newTestService(t)
	svc.LockWaitTimeout = 200 * time.Millisecond
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	run := runOK(t, ts.URL, `{"name":"busytask","command":["sleep","1"]}`)

	lease, err := svc.Store.LockTaskContext(context.Background(), run.Task.ID)
	if err != nil {
		t.Fatalf("LockTaskContext: %v", err)
	}
	defer lease.Unlock()

	resp := postJSON(t, ts.URL+"/api/v1/tasks/busytask/rename", `{"new_name":"renamed"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("expected a Retry-After header on a busy error")
	}
	var body server.ErrorResponseJSON
	mustDecode(t, resp, &body)
	if body.Error.Code != string(taskservice.CodeBusy) {
		t.Errorf("code = %q, want %q", body.Error.Code, taskservice.CodeBusy)
	}
	if !body.Error.Retryable {
		t.Error("busy errors should be retryable")
	}
}

// --- helpers -----------------------------------------------------------

func mustNewRESTServer(t *testing.T, svc *taskservice.Service) *server.Server {
	t.Helper()
	srv, err := server.New(server.Options{Service: svc, Expose: []server.Exposure{server.ExposeREST}})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return srv
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewBufferString(body)) //nolint:gosec // test URL is an httptest server
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func runOK(t *testing.T, baseURL, body string) server.RunResponseJSON {
	t.Helper()
	resp := postJSON(t, baseURL+"/api/v1/tasks", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("run POST status = %d, want 201 (body=%s)", resp.StatusCode, body)
	}
	var out server.RunResponseJSON
	mustDecode(t, resp, &out)
	return out
}

func mustDecode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
