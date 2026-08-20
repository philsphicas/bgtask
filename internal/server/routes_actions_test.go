package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/server"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// TestBulkAction_BestEffort_LabelsPerItemErrorsDontFailRequest verifies
// that a labels/all bulk selection is best-effort: even when some items
// fail (here, restarting a task with the target label that is not
// running), the overall response is still 200 with the failure reported
// per-item.
func TestBulkAction_BestEffort_LabelsPerItemErrorsDontFailRequest(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"grp-a","command":["sleep","1"],"labels":["grp"]}`)
	runOK(t, ts.URL, `{"name":"grp-b","command":["sleep","1"],"labels":["grp"]}`)

	// Stop grp-b directly so a subsequent bulk "restart" over the label
	// finds one running (grp-a) and one not-running (grp-b) task.
	stopResp := postJSON(t, ts.URL+"/api/v1/tasks/grp-b/stop", `{"force":true}`)
	stopResp.Body.Close()

	resp := postJSON(t, ts.URL+"/api/v1/actions/restart", `{"selection":{"labels":["grp"]}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a best-effort labels selection", resp.StatusCode)
	}

	var body server.BatchResponseJSON
	mustDecode(t, resp, &body)
	if len(body.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(body.Items))
	}

	var sawSuccess, sawError bool
	for _, item := range body.Items {
		switch {
		case item.Error != nil:
			sawError = true
			if item.Error.Code != string(taskservice.CodeFailedPrecondition) {
				t.Errorf("item error code = %q, want %q", item.Error.Code, taskservice.CodeFailedPrecondition)
			}
		case item.Changed:
			sawSuccess = true
		}
	}
	if !sawSuccess || !sawError {
		t.Errorf("items = %+v, want one success and one per-item error", body.Items)
	}
}

// TestBulkAction_FailFast_RefsSelectionReturnsMappedStatus verifies that
// an explicit refs selection is fail-fast: the first failing ref stops
// processing, and the response's overall HTTP status reflects that
// failure's mapped code (a request-level failure), while still reporting
// the partial batch progress made so far via Items.
func TestBulkAction_FailFast_RefsSelectionReturnsMappedStatus(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"seq-a","command":["sleep","1"]}`)
	// seq-b intentionally does not exist.

	resp := postJSON(t, ts.URL+"/api/v1/actions/stop", `{"selection":{"refs":["seq-a","seq-b"]}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (mapped from the not_found ref)", resp.StatusCode)
	}

	var body server.BatchErrorResponseJSON
	mustDecode(t, resp, &body)
	if body.Error.Code != string(taskservice.CodeNotFound) {
		t.Errorf("error.code = %q, want %q", body.Error.Code, taskservice.CodeNotFound)
	}
	if body.Error.Ref != "seq-b" {
		t.Errorf("error.ref = %q, want %q", body.Error.Ref, "seq-b")
	}
	// The partial progress made before the failure (seq-a succeeding) must
	// still be visible.
	if len(body.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2 (seq-a success + seq-b failure)", len(body.Items))
	}
	if body.Items[0].Ref != "seq-a" || !body.Items[0].Changed {
		t.Errorf("Items[0] = %+v, want a successful seq-a entry", body.Items[0])
	}
	if body.Items[1].Ref != "seq-b" || body.Items[1].Error == nil {
		t.Errorf("Items[1] = %+v, want a failed seq-b entry", body.Items[1])
	}
}

func TestBulkAction_Remove(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"rm-a","command":["sleep","1"]}`)
	runOK(t, ts.URL, `{"name":"rm-b","command":["sleep","1"]}`)

	resp := postJSON(t, ts.URL+"/api/v1/actions/remove", `{"selection":{"all":true},"force":true}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body server.BatchResponseJSON
	mustDecode(t, resp, &body)
	if len(body.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(body.Items))
	}
	for _, item := range body.Items {
		if !item.Changed {
			t.Errorf("item %+v: expected Changed=true", item)
		}
	}

	listResp, err := http.Get(ts.URL + "/api/v1/tasks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer listResp.Body.Close()
	var list server.ListTasksResponseJSON
	mustDecode(t, listResp, &list)
	if len(list.Tasks) != 0 {
		t.Errorf("remaining tasks = %+v, want none after remove --all", list.Tasks)
	}
}

func TestBulkAction_RemoveHonorsTimeout(t *testing.T) {
	svc, env := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"slow-remove","command":["sleep","1"]}`)
	env.mu.Lock()
	env.ignoreStop = true
	env.mu.Unlock()

	start := time.Now()
	resp := postJSON(t, ts.URL+"/api/v1/actions/remove", `{"selection":{"refs":["slow-remove"]},"timeout":"10ms"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("remove took %v; request timeout was not propagated", elapsed)
	}

	var body server.BatchResponseJSON
	mustDecode(t, resp, &body)
	if len(body.Items) != 1 || !body.Items[0].Forced {
		t.Fatalf("items = %+v, want one forced removal", body.Items)
	}
	env.mu.Lock()
	kills := env.kills
	env.mu.Unlock()
	if kills == 0 {
		t.Fatal("expected timeout escalation to kill the supervisor")
	}
}
