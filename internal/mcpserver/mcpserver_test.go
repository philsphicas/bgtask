package mcpserver_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/philsphicas/bgtask/internal/mcpserver"
)

// connectClient starts an MCP client and connects it to the Streamable
// HTTP endpoint at url, registering cleanup to close the session.
func connectClient(t *testing.T, url string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "bgtask-test-client", Version: "v0.0.0"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool calls the named tool with args and decodes its structured
// output content into an Out value, returning both the decoded value and
// the raw *mcp.CallToolResult (so callers can also inspect IsError/Content).
func callTool[Out any](t *testing.T, cs *mcp.ClientSession, name string, args any) (Out, *mcp.CallToolResult) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	var out Out
	if res.StructuredContent != nil {
		b, merr := json.Marshal(res.StructuredContent)
		if merr != nil {
			t.Fatalf("marshal structured content: %v", merr)
		}
		if uerr := json.Unmarshal(b, &out); uerr != nil {
			t.Fatalf("unmarshal structured content into %T: %v", out, uerr)
		}
	}
	return out, res
}

// TestDiscovery_ExactToolSet verifies the MCP surface exposes exactly the
// 11 documented bgtask_* tools -- no more, no less.
func TestDiscovery_ExactToolSet(t *testing.T) {
	svc, _ := newTestService(t)
	ts := httptest.NewServer(mcpserver.NewHandler(svc, "test"))
	defer ts.Close()

	cs := connectClient(t, ts.URL)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	sort.Strings(got)

	want := []string{
		"bgtask_cleanup",
		"bgtask_get",
		"bgtask_list",
		"bgtask_logs",
		"bgtask_remove",
		"bgtask_rename",
		"bgtask_restart",
		"bgtask_run",
		"bgtask_set_labels",
		"bgtask_start",
		"bgtask_stop",
	}
	if len(got) != len(want) {
		t.Fatalf("tools = %v (%d), want %v (%d)", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tools[%d] = %q, want %q (full list: %v)", i, got[i], want[i], got)
		}
	}

	// Every tool must document a non-empty description, so an LLM caller
	// isn't left guessing about destructive/replace_existing semantics.
	for _, tool := range res.Tools {
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("tool %q has an empty description", tool.Name)
		}
	}
}

// TestRunListGetLifecycle drives bgtask_run -> bgtask_list -> bgtask_get
// end to end through the real Streamable HTTP handler and the official
// SDK client.
func TestRunListGetLifecycle(t *testing.T) {
	svc, _ := newTestService(t)
	ts := httptest.NewServer(mcpserver.NewHandler(svc, "test"))
	defer ts.Close()
	cs := connectClient(t, ts.URL)

	runOut, runRes := callTool[mcpserver.RunOutput](t, cs, "bgtask_run", mcpserver.RunInput{
		Name:    "demo",
		Command: []string{"sleep", "10"},
		Labels:  []string{"demo-label"},
	})
	if runRes.IsError {
		t.Fatalf("bgtask_run reported an error: %+v", runOut.Error)
	}
	if runOut.Task == nil || runOut.Task.Name != "demo" {
		t.Fatalf("bgtask_run: task = %+v, want name %q", runOut.Task, "demo")
	}
	if runOut.PID == 0 {
		t.Errorf("bgtask_run: PID = 0, want non-zero")
	}
	if runOut.Task.Status.State != "running" {
		t.Errorf("bgtask_run: status.state = %q, want %q", runOut.Task.Status.State, "running")
	}

	listOut, listRes := callTool[mcpserver.ListOutput](t, cs, "bgtask_list", mcpserver.ListInput{})
	if listRes.IsError {
		t.Fatalf("bgtask_list reported an error: %+v", listOut.Error)
	}
	if len(listOut.Tasks) != 1 || listOut.Tasks[0].Name != "demo" {
		t.Fatalf("bgtask_list: tasks = %+v, want exactly one task named %q", listOut.Tasks, "demo")
	}

	listByLabel, _ := callTool[mcpserver.ListOutput](t, cs, "bgtask_list", mcpserver.ListInput{Labels: []string{"demo-label"}})
	if len(listByLabel.Tasks) != 1 {
		t.Fatalf("bgtask_list by label: tasks = %+v, want exactly one match", listByLabel.Tasks)
	}

	getOut, getRes := callTool[mcpserver.GetOutput](t, cs, "bgtask_get", mcpserver.GetInput{Ref: "demo"})
	if getRes.IsError {
		t.Fatalf("bgtask_get reported an error: %+v", getOut.Error)
	}
	if getOut.Task == nil || getOut.Task.ID != runOut.Task.ID {
		t.Fatalf("bgtask_get: task = %+v, want ID %q", getOut.Task, runOut.Task.ID)
	}

	// A nonexistent ref must fail with a structured not_found error, not a
	// protocol-level error and not bare prose.
	missingOut, missingRes := callTool[mcpserver.GetOutput](t, cs, "bgtask_get", mcpserver.GetInput{Ref: "does-not-exist"})
	if !missingRes.IsError {
		t.Fatal("bgtask_get on a missing ref: expected IsError = true")
	}
	if missingOut.Error == nil || missingOut.Error.Code != "not_found" {
		t.Errorf("bgtask_get on a missing ref: Error = %+v, want Code = %q", missingOut.Error, "not_found")
	}
}

// TestRun_DuplicateNameIsStructuredConflict verifies that a domain failure
// (a duplicate task name without replace_existing) surfaces as a failed
// tool result whose structured output still carries the machine-readable
// code/message/ref/retryable fields, not just prose.
func TestRun_DuplicateNameIsStructuredConflict(t *testing.T) {
	svc, _ := newTestService(t)
	ts := httptest.NewServer(mcpserver.NewHandler(svc, "test"))
	defer ts.Close()
	cs := connectClient(t, ts.URL)

	in := mcpserver.RunInput{Name: "dup", Command: []string{"sleep", "10"}}
	if _, res := callTool[mcpserver.RunOutput](t, cs, "bgtask_run", in); res.IsError {
		t.Fatalf("first bgtask_run unexpectedly failed")
	}

	out, res := callTool[mcpserver.RunOutput](t, cs, "bgtask_run", in)
	if !res.IsError {
		t.Fatal("second bgtask_run with a duplicate name: expected IsError = true")
	}
	if out.Task != nil {
		t.Errorf("second bgtask_run: Task = %+v, want nil (no task was launched)", out.Task)
	}
	if out.Error == nil {
		t.Fatal("second bgtask_run: Error is nil, want a structured ToolError")
	}
	if out.Error.Code != "conflict" {
		t.Errorf("second bgtask_run: Error.Code = %q, want %q", out.Error.Code, "conflict")
	}
	if out.Error.Message == "" {
		t.Error("second bgtask_run: Error.Message is empty")
	}
	if out.Error.Retryable {
		t.Error("second bgtask_run: Error.Retryable = true, want false (a conflict is not transient)")
	}

	// The auto-generated text content must also carry the structured JSON
	// (not just the error string), so a caller without structured-content
	// support still gets machine-readable fields.
	found := false
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, `"code":"conflict"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected text content to include the structured error JSON; content = %+v", res.Content)
	}
}

// TestRun_RedactsEnvValues verifies that environment variable values
// supplied to bgtask_run never appear anywhere in a subsequent bgtask_get
// response -- only the variable names (env_keys) do.
func TestRun_RedactsEnvValues(t *testing.T) {
	svc, _ := newTestService(t)
	ts := httptest.NewServer(mcpserver.NewHandler(svc, "test"))
	defer ts.Close()
	cs := connectClient(t, ts.URL)

	const secretValue = "super-secret-token-value"
	_, runRes := callTool[mcpserver.RunOutput](t, cs, "bgtask_run", mcpserver.RunInput{
		Name:    "secretive",
		Command: []string{"sleep", "10"},
		Env:     map[string]string{"API_TOKEN": secretValue},
	})
	if runRes.IsError {
		t.Fatalf("bgtask_run unexpectedly failed")
	}

	getOut, getRes := callTool[mcpserver.GetOutput](t, cs, "bgtask_get", mcpserver.GetInput{Ref: "secretive"})
	if getRes.IsError {
		t.Fatalf("bgtask_get unexpectedly failed")
	}
	if getOut.Task == nil {
		t.Fatal("bgtask_get: Task is nil")
	}
	found := false
	for _, k := range getOut.Task.EnvKeys {
		if k == "API_TOKEN" {
			found = true
		}
	}
	if !found {
		t.Errorf("EnvKeys = %v, want it to include %q", getOut.Task.EnvKeys, "API_TOKEN")
	}

	// Belt and suspenders: the secret value must not appear anywhere in
	// either response's raw wire representation.
	for _, res := range []*mcp.CallToolResult{runRes, getRes} {
		raw, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}
		if strings.Contains(string(raw), secretValue) {
			t.Errorf("result leaked the secret env value: %s", raw)
		}
	}
}

// TestLogs_BoundedTailValidation exercises bgtask_logs's tail bounds: the
// default, an explicit in-range value, and out-of-range values that must
// fail with a structured invalid_argument error rather than a protocol
// error.
func TestLogs_BoundedTailValidation(t *testing.T) {
	svc, _ := newTestService(t)
	ts := httptest.NewServer(mcpserver.NewHandler(svc, "test"))
	defer ts.Close()
	cs := connectClient(t, ts.URL)

	if _, res := callTool[mcpserver.RunOutput](t, cs, "bgtask_run", mcpserver.RunInput{
		Name: "logged", Command: []string{"sleep", "10"},
	}); res.IsError {
		t.Fatalf("bgtask_run unexpectedly failed")
	}

	// No tail specified: the default bound applies and the call succeeds.
	defaultOut, defaultRes := callTool[mcpserver.LogsOutput](t, cs, "bgtask_logs", mcpserver.LogsInput{Ref: "logged"})
	if defaultRes.IsError {
		t.Fatalf("bgtask_logs with default tail failed: %+v", defaultOut.Error)
	}

	tail := 5000
	if _, res := callTool[mcpserver.LogsOutput](t, cs, "bgtask_logs", mcpserver.LogsInput{Ref: "logged", Tail: &tail}); res.IsError {
		t.Errorf("bgtask_logs with tail = %d (the max) unexpectedly failed", tail)
	}

	tooMany := 5001
	tooManyOut, tooManyRes := callTool[mcpserver.LogsOutput](t, cs, "bgtask_logs", mcpserver.LogsInput{Ref: "logged", Tail: &tooMany})
	if !tooManyRes.IsError {
		t.Fatal("bgtask_logs with tail = 5001: expected IsError = true")
	}
	if tooManyOut.Error == nil || tooManyOut.Error.Code != "invalid_argument" {
		t.Errorf("bgtask_logs with tail = 5001: Error = %+v, want Code = %q", tooManyOut.Error, "invalid_argument")
	}

	negative := -1
	negOut, negRes := callTool[mcpserver.LogsOutput](t, cs, "bgtask_logs", mcpserver.LogsInput{Ref: "logged", Tail: &negative})
	if !negRes.IsError {
		t.Fatal("bgtask_logs with tail = -1: expected IsError = true")
	}
	if negOut.Error == nil || negOut.Error.Code != "invalid_argument" {
		t.Errorf("bgtask_logs with tail = -1: Error = %+v, want Code = %q", negOut.Error, "invalid_argument")
	}

	zero := 0
	zeroOut, zeroRes := callTool[mcpserver.LogsOutput](t, cs, "bgtask_logs", mcpserver.LogsInput{Ref: "logged", Tail: &zero})
	if zeroRes.IsError {
		t.Fatalf("bgtask_logs with tail = 0: unexpectedly failed: %+v", zeroOut.Error)
	}
	if len(zeroOut.Entries) != 0 {
		t.Errorf("bgtask_logs with tail = 0: Entries = %v, want empty", zeroOut.Entries)
	}
}

// TestLifecycle_SelectionMustBeExactlyOne verifies stop/start/restart/
// remove all reject a Selection that specifies zero or more than one of
// refs/labels/all, with a structured invalid_argument error.
func TestLifecycle_SelectionMustBeExactlyOne(t *testing.T) {
	svc, _ := newTestService(t)
	ts := httptest.NewServer(mcpserver.NewHandler(svc, "test"))
	defer ts.Close()
	cs := connectClient(t, ts.URL)

	out, res := callTool[mcpserver.StopOutput](t, cs, "bgtask_stop", mcpserver.StopInput{
		Selection: mcpserver.SelectionInput{Refs: []string{"a"}, All: true},
	})
	if !res.IsError {
		t.Fatal("bgtask_stop with refs+all both set: expected IsError = true")
	}
	if out.Error == nil || out.Error.Code != "invalid_argument" {
		t.Errorf("Error = %+v, want Code = %q", out.Error, "invalid_argument")
	}

	emptyOut, emptyRes := callTool[mcpserver.StopOutput](t, cs, "bgtask_stop", mcpserver.StopInput{})
	if !emptyRes.IsError {
		t.Fatal("bgtask_stop with no selection set: expected IsError = true")
	}
	if emptyOut.Error == nil || emptyOut.Error.Code != "invalid_argument" {
		t.Errorf("Error = %+v, want Code = %q", emptyOut.Error, "invalid_argument")
	}
}

// TestLifecycle_StopStartRestartRemoveCleanup exercises the full bulk
// lifecycle surface against an "all" selection.
func TestLifecycle_StopStartRestartRemoveCleanup(t *testing.T) {
	svc, _ := newTestService(t)
	ts := httptest.NewServer(mcpserver.NewHandler(svc, "test"))
	defer ts.Close()
	cs := connectClient(t, ts.URL)

	if _, res := callTool[mcpserver.RunOutput](t, cs, "bgtask_run", mcpserver.RunInput{
		Name: "bulk", Command: []string{"sleep", "10"},
	}); res.IsError {
		t.Fatalf("bgtask_run unexpectedly failed")
	}

	stopOut, stopRes := callTool[mcpserver.StopOutput](t, cs, "bgtask_stop", mcpserver.StopInput{
		Selection: mcpserver.SelectionInput{All: true},
	})
	if stopRes.IsError {
		t.Fatalf("bgtask_stop failed: %+v", stopOut.Error)
	}
	if len(stopOut.Items) != 1 || !stopOut.Items[0].Changed {
		t.Fatalf("bgtask_stop: items = %+v, want exactly one changed item", stopOut.Items)
	}

	startOut, startRes := callTool[mcpserver.StartOutput](t, cs, "bgtask_start", mcpserver.StartInput{
		Selection: mcpserver.SelectionInput{Refs: []string{"bulk"}},
	})
	if startRes.IsError {
		t.Fatalf("bgtask_start failed: %+v", startOut.Error)
	}
	if len(startOut.Items) != 1 || !startOut.Items[0].Changed {
		t.Fatalf("bgtask_start: items = %+v, want exactly one changed item", startOut.Items)
	}

	restartOut, restartRes := callTool[mcpserver.RestartOutput](t, cs, "bgtask_restart", mcpserver.RestartInput{
		Selection: mcpserver.SelectionInput{Labels: []string{"nonexistent-label"}},
	})
	if restartRes.IsError {
		t.Fatalf("bgtask_restart by label failed: %+v", restartOut.Error)
	}
	if len(restartOut.Items) != 0 {
		t.Errorf("bgtask_restart by a matching-nothing label: items = %+v, want empty", restartOut.Items)
	}

	removeOut, removeRes := callTool[mcpserver.RemoveOutput](t, cs, "bgtask_remove", mcpserver.RemoveInput{
		Selection: mcpserver.SelectionInput{All: true},
		Force:     true,
	})
	if removeRes.IsError {
		t.Fatalf("bgtask_remove failed: %+v", removeOut.Error)
	}
	if len(removeOut.Items) != 1 || !removeOut.Items[0].Changed {
		t.Fatalf("bgtask_remove: items = %+v, want exactly one changed item", removeOut.Items)
	}

	cleanupOut, cleanupRes := callTool[mcpserver.CleanupOutput](t, cs, "bgtask_cleanup", mcpserver.CleanupInput{})
	if cleanupRes.IsError {
		t.Fatalf("bgtask_cleanup failed: %+v", cleanupOut.Error)
	}
	if len(cleanupOut.Items) != 0 {
		t.Errorf("bgtask_cleanup after remove: items = %+v, want empty (nothing left)", cleanupOut.Items)
	}
}

// TestRenameAndSetLabels exercises bgtask_rename and bgtask_set_labels,
// including set_labels' wholesale-replace (not additive) semantics.
func TestRenameAndSetLabels(t *testing.T) {
	svc, _ := newTestService(t)
	ts := httptest.NewServer(mcpserver.NewHandler(svc, "test"))
	defer ts.Close()
	cs := connectClient(t, ts.URL)

	if _, res := callTool[mcpserver.RunOutput](t, cs, "bgtask_run", mcpserver.RunInput{
		Name: "old-name", Command: []string{"sleep", "10"}, Labels: []string{"keep-me"},
	}); res.IsError {
		t.Fatalf("bgtask_run unexpectedly failed")
	}

	renameOut, renameRes := callTool[mcpserver.RenameOutput](t, cs, "bgtask_rename", mcpserver.RenameInput{
		Ref: "old-name", NewName: "new-name",
	})
	if renameRes.IsError {
		t.Fatalf("bgtask_rename failed: %+v", renameOut.Error)
	}
	if renameOut.Task == nil || renameOut.Task.Name != "new-name" {
		t.Fatalf("bgtask_rename: task = %+v, want name %q", renameOut.Task, "new-name")
	}

	labelsOut, labelsRes := callTool[mcpserver.SetLabelsOutput](t, cs, "bgtask_set_labels", mcpserver.SetLabelsInput{
		Ref: "new-name", Labels: []string{"replacement-label"},
	})
	if labelsRes.IsError {
		t.Fatalf("bgtask_set_labels failed: %+v", labelsOut.Error)
	}
	if labelsOut.Task == nil {
		t.Fatal("bgtask_set_labels: Task is nil")
	}
	if len(labelsOut.Task.Labels) != 1 || labelsOut.Task.Labels[0] != "replacement-label" {
		t.Errorf("bgtask_set_labels: Labels = %v, want [replacement-label] (wholesale replace, not additive)", labelsOut.Task.Labels)
	}
}
