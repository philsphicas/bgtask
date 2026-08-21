package server_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/philsphicas/bgtask/internal/server"
)

func TestRunTask_Defaults(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	out := runOK(t, ts.URL, `{"command":["sleep","1"]}`)

	if out.Task.Name == "" {
		t.Error("expected an auto-generated name")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	if out.Task.Cwd != wd {
		t.Errorf("Cwd = %q, want the server process cwd %q", out.Task.Cwd, wd)
	}
	if out.PID == 0 {
		t.Error("expected a non-zero PID")
	}
}

func TestRunTask_ReplaceExistingDefaultsFalse(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"once","command":["sleep","1"]}`)

	// Re-running with the same name and no explicit replace_existing must
	// fail with a conflict, unlike the CLI's historical always-replace
	// default.
	resp := postJSON(t, ts.URL+"/api/v1/tasks", `{"name":"once","command":["sleep","1"]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (replace_existing defaults to false)", resp.StatusCode)
	}
}

func TestRunTask_ReplaceExistingExplicitTrue(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"replaceme","command":["sleep","1"]}`)

	resp := postJSON(t, ts.URL+"/api/v1/tasks", `{"name":"replaceme","command":["sleep","1"],"replace_existing":true}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 with replace_existing:true", resp.StatusCode)
	}
	var out server.RunResponseJSON
	mustDecode(t, resp, &out)
	if !out.ReplacedExisting {
		t.Error("expected ReplacedExisting to be true")
	}
}

func TestRunTask_RedactsEnvValues(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	out := runOK(t, ts.URL, `{"name":"secretive","command":["sleep","1"],"env":{"API_TOKEN":"topsecret","PORT":"8080"}}`)

	if len(out.Task.EnvKeys) != 2 {
		t.Fatalf("EnvKeys = %v, want 2 keys", out.Task.EnvKeys)
	}
	for _, k := range out.Task.EnvKeys {
		if k != "API_TOKEN" && k != "PORT" {
			t.Errorf("unexpected EnvKeys entry %q", k)
		}
	}

	// Fetch again via GET and via list, and verify command/cwd remain
	// visible while no raw env value ever appears.
	getResp, err := http.Get(ts.URL + "/api/v1/tasks/secretive")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer getResp.Body.Close()
	var got server.TaskJSON
	mustDecode(t, getResp, &got)
	assertVisibleFieldsPresent(t, got)

	listResp, err := http.Get(ts.URL + "/api/v1/tasks")
	if err != nil {
		t.Fatalf("GET list: %v", err)
	}
	defer listResp.Body.Close()
	var list server.ListTasksResponseJSON
	mustDecode(t, listResp, &list)
	for _, task := range list.Tasks {
		assertVisibleFieldsPresent(t, task)
	}
}

func assertVisibleFieldsPresent(t *testing.T, task server.TaskJSON) {
	t.Helper()
	if len(task.Command) == 0 {
		t.Error("expected command to remain visible")
	}
	if task.Cwd == "" {
		t.Error("expected cwd to remain visible")
	}
}

func TestListTasks_LabelFilter(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"web1","command":["sleep","1"],"labels":["web"]}`)
	runOK(t, ts.URL, `{"name":"db1","command":["sleep","1"],"labels":["db"]}`)

	resp, err := http.Get(ts.URL + "/api/v1/tasks?label=web")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var list server.ListTasksResponseJSON
	mustDecode(t, resp, &list)
	if len(list.Tasks) != 1 || list.Tasks[0].Name != "web1" {
		t.Errorf("filtered list = %+v, want only web1", list.Tasks)
	}
}

func TestListTasks_NoFilterReturnsAll(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"one","command":["sleep","1"]}`)
	runOK(t, ts.URL, `{"name":"two","command":["sleep","1"]}`)

	resp, err := http.Get(ts.URL + "/api/v1/tasks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var list server.ListTasksResponseJSON
	mustDecode(t, resp, &list)
	if len(list.Tasks) != 2 {
		t.Errorf("len(Tasks) = %d, want 2", len(list.Tasks))
	}
}

func TestGetTask_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/tasks/nope")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDeleteTask(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"deleteme","command":["sleep","1"]}`)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/tasks/deleteme", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var item server.BatchItemJSON
	mustDecode(t, resp, &item)
	if !item.Changed {
		t.Error("expected Changed=true for a successful delete")
	}

	getResp, err := http.Get(ts.URL + "/api/v1/tasks/deleteme")
	if err != nil {
		t.Fatalf("GET after delete: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Errorf("status after delete = %d, want 404", getResp.StatusCode)
	}
}

func TestStopStartRestartTask(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"lifecycle","command":["sleep","1"]}`)

	stopResp := postJSON(t, ts.URL+"/api/v1/tasks/lifecycle/stop", `{"force":true}`)
	defer stopResp.Body.Close()
	if stopResp.StatusCode != http.StatusOK {
		t.Fatalf("stop status = %d, want 200", stopResp.StatusCode)
	}
	var stopItem server.BatchItemJSON
	mustDecode(t, stopResp, &stopItem)
	if !stopItem.Changed || !stopItem.Forced {
		t.Errorf("stop item = %+v, want Changed and Forced", stopItem)
	}

	startResp := postJSON(t, ts.URL+"/api/v1/tasks/lifecycle/start", "")
	defer startResp.Body.Close()
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, want 200", startResp.StatusCode)
	}
	var startItem server.BatchItemJSON
	mustDecode(t, startResp, &startItem)
	if startItem.PID == 0 {
		t.Error("expected a non-zero PID after start")
	}

	restartResp := postJSON(t, ts.URL+"/api/v1/tasks/lifecycle/restart", "")
	defer restartResp.Body.Close()
	if restartResp.StatusCode != http.StatusOK {
		t.Fatalf("restart status = %d, want 200", restartResp.StatusCode)
	}
	var restartItem server.BatchItemJSON
	mustDecode(t, restartResp, &restartItem)
	if !restartItem.Changed {
		t.Error("expected Changed=true after restart")
	}
}

func TestRenameTask(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"old-name","command":["sleep","1"]}`)

	resp := postJSON(t, ts.URL+"/api/v1/tasks/old-name/rename", `{"new_name":"new-name"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var task server.TaskJSON
	mustDecode(t, resp, &task)
	if task.Name != "new-name" {
		t.Errorf("Name = %q, want %q", task.Name, "new-name")
	}
}

func TestRenameTask_EmptyNewNameRejected(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"rn","command":["sleep","1"]}`)

	resp := postJSON(t, ts.URL+"/api/v1/tasks/rn/rename", `{"new_name":"   "}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSetLabels(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"labelme","command":["sleep","1"]}`)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/labelme/labels", bytes.NewBufferString(`{"labels":["a","b"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var task server.TaskJSON
	mustDecode(t, resp, &task)
	if len(task.Labels) != 2 {
		t.Errorf("Labels = %v, want 2 entries", task.Labels)
	}
}

func TestSetLabels_EmptyClearsLabels(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"labelclear","command":["sleep","1"],"labels":["a","b"]}`)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/labelclear/labels", bytes.NewBufferString(`{"labels":[]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var task server.TaskJSON
	mustDecode(t, resp, &task)
	if len(task.Labels) != 0 {
		t.Errorf("Labels = %v, want empty", task.Labels)
	}
}

func TestLogs_DefaultsAndBounds(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"logtest","command":["sleep","1"]}`)

	// Default request succeeds (tail defaults to 200 internally).
	resp, err := http.Get(ts.URL + "/api/v1/tasks/logtest/logs")
	if err != nil {
		t.Fatalf("GET logs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var logs server.LogsResponseJSON
	mustDecode(t, resp, &logs)

	// tail beyond the max is rejected.
	badResp, err := http.Get(ts.URL + "/api/v1/tasks/logtest/logs?tail=999999")
	if err != nil {
		t.Fatalf("GET logs: %v", err)
	}
	defer badResp.Body.Close()
	if badResp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an over-max tail", badResp.StatusCode)
	}

	// negative tail is rejected.
	negResp, err := http.Get(ts.URL + "/api/v1/tasks/logtest/logs?tail=-1")
	if err != nil {
		t.Fatalf("GET logs: %v", err)
	}
	defer negResp.Body.Close()
	if negResp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a negative tail", negResp.StatusCode)
	}

	// non-numeric tail is rejected.
	nanResp, err := http.Get(ts.URL + "/api/v1/tasks/logtest/logs?tail=abc")
	if err != nil {
		t.Fatalf("GET logs: %v", err)
	}
	defer nanResp.Body.Close()
	if nanResp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a non-numeric tail", nanResp.StatusCode)
	}

	// max tail value (5000) is accepted.
	maxResp, err := http.Get(ts.URL + "/api/v1/tasks/logtest/logs?tail=5000")
	if err != nil {
		t.Fatalf("GET logs: %v", err)
	}
	defer maxResp.Body.Close()
	if maxResp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 for tail=5000", maxResp.StatusCode)
	}

	// stdout+stderr together is rejected by taskservice validation.
	bothResp, err := http.Get(ts.URL + "/api/v1/tasks/logtest/logs?stdout=true&stderr=true")
	if err != nil {
		t.Fatalf("GET logs: %v", err)
	}
	defer bothResp.Body.Close()
	if bothResp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for stdout+stderr", bothResp.StatusCode)
	}

	// invalid since is rejected.
	sinceResp, err := http.Get(ts.URL + "/api/v1/tasks/logtest/logs?since=not-a-duration")
	if err != nil {
		t.Fatalf("GET logs: %v", err)
	}
	defer sinceResp.Body.Close()
	if sinceResp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an invalid since", sinceResp.StatusCode)
	}
}

func TestLogs_AllAndStreamFilters(t *testing.T) {
	svc, _ := newTestService(t)
	srv := mustNewRESTServer(t, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	runOK(t, ts.URL, `{"name":"logfilters","command":["sleep","1"]}`)

	for _, q := range []string{"all=true", "all", "stdout=true", "stderr=true"} {
		resp, err := http.Get(ts.URL + "/api/v1/tasks/logfilters/logs?" + q)
		if err != nil {
			t.Fatalf("GET logs?%s: %v", q, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("query %q: status = %d, want 200", q, resp.StatusCode)
		}
	}
}
