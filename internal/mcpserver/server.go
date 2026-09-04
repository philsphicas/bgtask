// Package mcpserver implements bgtask's MCP (Model Context Protocol)
// surface: a stateless Streamable HTTP handler exposing bgtask's task
// lifecycle as a fixed set of bgtask_* tools, backed by the same
// taskservice.Service used by the CLI and the REST API (internal/server).
//
// Every tool's typed Input struct is the tool's JSON input schema (via
// struct reflection and "jsonschema" tag descriptions), and every
// successful call auto-populates structured output content from the
// typed Output struct. Domain failures (task not found, name conflict,
// busy lock, etc.) are reported as CallToolResult.IsError results whose
// Output carries a structured ToolError -- never as bare, unstructured
// prose -- so a calling model can inspect the machine-readable
// code/message/ref/task_id/retryable fields and self-correct.
package mcpserver

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// handlers holds the taskservice dependency shared by every MCP tool
// handler, mirroring internal/server's unexported api type for the REST
// adapter.
type handlers struct {
	svc *taskservice.Service
}

// boolPtr returns a pointer to b, for the optional *bool fields of
// mcp.ToolAnnotations.
func boolPtr(b bool) *bool { return &b }

// NewHandler builds bgtask's MCP Streamable HTTP handler: a stateless,
// JSON-response server exposing exactly the bgtask_* tool surface over
// svc. version is reported to MCP clients as the server implementation's
// version (bgtask's own build version).
//
// The handler is stateless (StreamableHTTPOptions.Stateless), matching
// bgtask's REST API: every call is a self-contained POST with no
// server-retained session state, and no server-initiated requests or
// notifications are used. JSONResponse is set because, with no
// server-initiated interactions, a plain JSON response per call is
// simpler than negotiating an SSE stream for a single reply.
// PropagateRequestCancellation ties a tool call's context to the
// underlying HTTP request, so an aborted request doesn't leave taskservice
// work running past its caller. The SDK's own localhost Host-header (DNS
// rebinding) protection remains enabled; bgtask's Origin allow-list is
// applied by internal/server's middleware around every route, including
// this one.
func NewHandler(svc *taskservice.Service, version string) http.Handler {
	srv := newServer(svc, version)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv
	}, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		PropagateRequestCancellation: true,
		MaxRequestBodyBytes:          mcp.DefaultMaxRequestBodyBytes,
	})
}

// newServer builds the mcp.Server with every bgtask_* tool registered. It
// is split out from NewHandler so tests can drive the *mcp.Server
// directly (e.g. over an in-memory transport) without an HTTP round trip.
func newServer(svc *taskservice.Service, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "bgtask",
		Version: version,
	}, nil)
	h := &handlers{svc: svc}

	mcp.AddTool(s, &mcp.Tool{
		Name: "bgtask_list",
		Description: "List bgtask-managed tasks as compact, newest-first summaries. Filter by states and/or labels; " +
			"use states=[\"running\"] when asked what is currently running. Results default to 20 and are capped at 100; " +
			"pass next_cursor back as cursor for another page. Call bgtask_get for full argv, cwd, PIDs, restart/health config, or log path.",
		Annotations: &mcp.ToolAnnotations{Title: "Tasks: List", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPtr(false)},
	}, h.list)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "bgtask_get",
		Description: "Get one task by name or ID with full configuration and freshly resolved status, including exact argv, cwd, env key names, restart/health settings, PIDs, ports, timestamps, and log path.",
		Annotations: &mcp.ToolAnnotations{Title: "Tasks: Get", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPtr(false)},
	}, h.get)

	mcp.AddTool(s, &mcp.Tool{
		Name: "bgtask_run",
		Description: "Launch a new background task under bgtask's supervision: a detached, restartable process whose output is " +
			"captured to a log file and whose lifecycle (running/exited/dead) can be queried later. Fails with a conflict error " +
			"if a task with the same name already exists, unless replace_existing is set (destructive: stops and permanently " +
			"replaces the existing task).",
		Annotations: &mcp.ToolAnnotations{Title: "Tasks: Run", DestructiveHint: boolPtr(true), IdempotentHint: false, OpenWorldHint: boolPtr(true)},
	}, h.run)

	addLogsTool(s, h)

	mcp.AddTool(s, &mcp.Tool{
		Name: "bgtask_start",
		Description: "Re-launch existing stopped tasks; use bgtask_run to create a new task. Set exactly one top-level selector: " +
			"refs (fail-fast, max 50), labels (OR match, best-effort), or all (best-effort).",
		Annotations: &mcp.ToolAnnotations{Title: "Tasks: Start", DestructiveHint: boolPtr(false), IdempotentHint: false, OpenWorldHint: boolPtr(true)},
	}, h.start)

	mcp.AddTool(s, &mcp.Tool{
		Name: "bgtask_stop",
		Description: "Stop tasks selected by exactly one top-level selector: refs (fail-fast, max 50), labels (OR match, " +
			"best-effort), or all (best-effort). Destructive: signals " +
			"graceful shutdown (or, with force, immediately SIGKILLs the process). A task that is already stopped is reported " +
			"as a no-op, not an error.",
		Annotations: &mcp.ToolAnnotations{Title: "Tasks: Stop", DestructiveHint: boolPtr(true), IdempotentHint: true, OpenWorldHint: boolPtr(true)},
	}, h.stop)

	mcp.AddTool(s, &mcp.Tool{
		Name: "bgtask_restart",
		Description: "Restart running tasks selected by exactly one top-level selector: refs (fail-fast, max 50), labels " +
			"(OR match, best-effort), or all (best-effort). Destructive: " +
			"signals a restart (or, with force, kills the child before restarting). A task that is not running fails with a " +
			"failed_precondition error for that item.",
		Annotations: &mcp.ToolAnnotations{Title: "Tasks: Restart", DestructiveHint: boolPtr(true), IdempotentHint: false, OpenWorldHint: boolPtr(true)},
	}, h.restart)

	mcp.AddTool(s, &mcp.Tool{
		Name: "bgtask_remove",
		Description: "Stop (if running) and permanently delete task state and logs, selected by exactly one top-level selector: " +
			"refs (fail-fast, max 50), labels (OR match, best-effort), or all (best-effort). " +
			"selection mode must be set. Destructive and irreversible: once removed, a task's history and logs are gone.",
		Annotations: &mcp.ToolAnnotations{Title: "Tasks: Remove", DestructiveHint: boolPtr(true), IdempotentHint: false, OpenWorldHint: boolPtr(true)},
	}, h.remove)

	mcp.AddTool(s, &mcp.Tool{
		Name: "bgtask_cleanup",
		Description: "Permanently delete on-disk state for every non-running (exited, dead, or unknown) task. Running tasks are " +
			"left alone and reported as a no-op. Destructive and irreversible, but safe to call repeatedly (a second call simply " +
			"finds nothing left to remove). Takes no parameters.",
		Annotations: &mcp.ToolAnnotations{Title: "Tasks: Cleanup", DestructiveHint: boolPtr(true), IdempotentHint: true, OpenWorldHint: boolPtr(false)},
	}, h.cleanup)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "bgtask_rename",
		Description: "Rename a task. The new name must be unique among all tasks; the task keeps its ID and all other configuration.",
		Annotations: &mcp.ToolAnnotations{Title: "Tasks: Rename", DestructiveHint: boolPtr(false), IdempotentHint: true, OpenWorldHint: boolPtr(false)},
	}, h.rename)

	mcp.AddTool(s, &mcp.Tool{
		Name: "bgtask_set_labels",
		Description: "Destructive: wholesale-replace a task's labels with exactly the given set. This is not additive -- any " +
			"label not included is removed. Pass an empty (or omitted) labels list to clear all labels.",
		Annotations: &mcp.ToolAnnotations{Title: "Tasks: Set Labels", DestructiveHint: boolPtr(true), IdempotentHint: true, OpenWorldHint: boolPtr(false)},
	}, h.setLabels)

	return s
}
