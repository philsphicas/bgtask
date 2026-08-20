package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/philsphicas/bgtask/internal/server"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// Selection validation is exercised through the bulk actions endpoint,
// since parseSelection itself is unexported.

func TestBulkAction_SelectionRequiresExactlyOneMode(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"none", `{"selection":{}}`},
		{"refs and all", `{"selection":{"refs":["a"],"all":true}}`},
		{"refs and labels", `{"selection":{"refs":["a"],"labels":["x"]}}`},
		{"labels and all", `{"selection":{"labels":["x"],"all":true}}`},
		{"all three", `{"selection":{"refs":["a"],"labels":["x"],"all":true}}`},
	}

	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := postJSON(t, ts.URL+"/api/v1/actions/stop", tc.body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for %s", resp.StatusCode, tc.body)
			}
			var body server.ErrorResponseJSON
			mustDecode(t, resp, &body)
			if body.Error.Code != string(taskservice.CodeInvalidArgument) {
				t.Errorf("code = %q, want %q", body.Error.Code, taskservice.CodeInvalidArgument)
			}
		})
	}
}

func TestBulkAction_SelectionByRefsAccepted(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"sel-a","command":["sleep","1"]}`)

	resp := postJSON(t, ts.URL+"/api/v1/actions/stop", `{"selection":{"refs":["sel-a"]}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBulkAction_SelectionByLabelsAccepted(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"sel-b","command":["sleep","1"],"labels":["group1"]}`)

	resp := postJSON(t, ts.URL+"/api/v1/actions/stop", `{"selection":{"labels":["group1"]}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBulkAction_SelectionAllAccepted(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"sel-c","command":["sleep","1"]}`)

	resp := postJSON(t, ts.URL+"/api/v1/actions/stop", `{"selection":{"all":true}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestBulkAction_CleanupNeedsNoSelection verifies cleanup ignores/does not
// require a selection at all.
func TestBulkAction_CleanupNeedsNoSelection(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := postJSON(t, ts.URL+"/api/v1/actions/cleanup", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body server.BatchResponseJSON
	mustDecode(t, resp, &body)
}

func TestBulkAction_UnknownActionRejected(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := postJSON(t, ts.URL+"/api/v1/actions/frobnicate", `{"selection":{"all":true}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body server.ErrorResponseJSON
	mustDecode(t, resp, &body)
	if body.Error.Code != string(taskservice.CodeInvalidArgument) {
		t.Errorf("code = %q, want %q", body.Error.Code, taskservice.CodeInvalidArgument)
	}
}
