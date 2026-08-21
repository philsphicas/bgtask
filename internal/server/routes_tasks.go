package server

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

// defaultLogTail and maxLogTail bound the "tail" query parameter on
// GET /tasks/{ref}/logs: bounded by default, and capped so a single
// request can never demand an unbounded read (no follow/streaming is
// offered at this layer).
const (
	defaultLogTail = 200
	maxLogTail     = 5000
)

func (a *api) listTasks(w http.ResponseWriter, r *http.Request) {
	labels := r.URL.Query()["label"]
	result, err := a.svc.List(r.Context(), taskservice.ListRequest{Labels: labels})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	tasks := make([]TaskJSON, 0, len(result.Tasks))
	for _, t := range result.Tasks {
		tasks = append(tasks, toTaskJSON(t.Public()))
	}
	writeJSON(w, http.StatusOK, ListTasksResponseJSON{Tasks: tasks})
}

func (a *api) runTask(w http.ResponseWriter, r *http.Request) {
	var body RunRequestJSON
	if !decodeJSONBody(w, r, a.opts.MaxRunBodyBytes, &body) {
		return
	}
	if len(body.Command) == 0 {
		writeErr(w, r, http.StatusBadRequest, string(taskservice.CodeInvalidArgument), body.Name, "", "command must not be empty", false)
		return
	}
	restartDelay, err := parseDuration(body.RestartDelay)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, string(taskservice.CodeInvalidArgument), body.Name, "", fmt.Sprintf("invalid restart_delay: %v", err), false)
		return
	}
	healthInterval, err := parseDuration(body.HealthInterval)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, string(taskservice.CodeInvalidArgument), body.Name, "", fmt.Sprintf("invalid health_interval: %v", err), false)
		return
	}

	cwd := body.Cwd
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, string(taskservice.CodeInternal), body.Name, "", "could not determine the server's working directory", false)
			return
		}
		cwd = wd
	}

	result, err := a.svc.Run(r.Context(), taskservice.RunRequest{
		Name:            body.Name,
		Command:         body.Command,
		Cwd:             cwd,
		EnvOverrides:    body.Env,
		Labels:          body.Labels,
		Restart:         body.Restart,
		RestartDelay:    restartDelay,
		HealthCheck:     body.HealthCheck,
		HealthInterval:  healthInterval,
		AutoRm:          body.AutoRm,
		ReplaceExisting: body.ReplaceExisting,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	resp := RunResponseJSON{
		Task:             toTaskJSON(result.Task.Public()),
		PID:              result.PID,
		NoBreakaway:      result.NoBreakaway,
		ReplacedExisting: result.ReplacedExisting,
		AutoRemoved:      result.AutoRemoved,
	}
	if result.ImmediateExit != nil {
		resp.ImmediateExit = &ExitedJSON{
			Code:   result.ImmediateExit.Code,
			Signal: result.ImmediateExit.Signal,
			At:     timeString(result.ImmediateExit.ExitedAt),
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (a *api) getTask(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	result, err := a.svc.Get(r.Context(), ref)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTaskJSON(result.Task.Public()))
}

// deleteTask removes a single task, identified by the path, mirroring
// `bgtask rm <ref>`. It is modeled as an explicit single-ref Selection so
// any failure (not found, ambiguous name, busy) surfaces as a normal,
// request-level error rather than a batch item error.
func (a *api) deleteTask(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	force := parseBoolQuery(r, "force")
	result, err := a.svc.Remove(r.Context(), taskservice.RemoveRequest{
		Selection: taskservice.Selection{Names: []string{ref}},
		Force:     force,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toBatchItemJSON(requestIDFromContext(r.Context()), result.Items[0]))
}

func (a *api) startTask(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	result, err := a.svc.Start(r.Context(), taskservice.StartRequest{
		Selection: taskservice.Selection{Names: []string{ref}},
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toBatchItemJSON(requestIDFromContext(r.Context()), result.Items[0]))
}

func (a *api) stopTask(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	var body StopBodyJSON
	if !decodeOptionalJSONBody(w, r, a.opts.MaxStopBodyBytes, &body) {
		return
	}
	timeout, err := parseDuration(body.Timeout)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, string(taskservice.CodeInvalidArgument), ref, "", fmt.Sprintf("invalid timeout: %v", err), false)
		return
	}
	result, err := a.svc.Stop(r.Context(), taskservice.StopRequest{
		Selection: taskservice.Selection{Names: []string{ref}},
		Force:     body.Force,
		Timeout:   timeout,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toBatchItemJSON(requestIDFromContext(r.Context()), result.Items[0]))
}

func (a *api) restartTask(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	var body RestartBodyJSON
	if !decodeOptionalJSONBody(w, r, a.opts.MaxStopBodyBytes, &body) {
		return
	}
	result, err := a.svc.Restart(r.Context(), taskservice.RestartRequest{
		Selection: taskservice.Selection{Names: []string{ref}},
		Force:     body.Force,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toBatchItemJSON(requestIDFromContext(r.Context()), result.Items[0]))
}

func (a *api) renameTask(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	var body RenameBodyJSON
	if !decodeJSONBody(w, r, a.opts.MaxRenameBodyBytes, &body) {
		return
	}
	if strings.TrimSpace(body.NewName) == "" {
		writeErr(w, r, http.StatusBadRequest, string(taskservice.CodeInvalidArgument), ref, "", "new_name must not be empty", false)
		return
	}
	if _, err := a.svc.Rename(r.Context(), taskservice.RenameRequest{Ref: ref, NewName: body.NewName}); err != nil {
		writeServiceError(w, r, err)
		return
	}
	got, err := a.svc.Get(r.Context(), body.NewName)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTaskJSON(got.Task.Public()))
}

func (a *api) setLabels(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	var body LabelsBodyJSON
	if !decodeJSONBody(w, r, a.opts.MaxLabelsBodyBytes, &body) {
		return
	}
	if _, err := a.svc.SetLabels(r.Context(), taskservice.SetLabelsRequest{Ref: ref, Labels: body.Labels}); err != nil {
		writeServiceError(w, r, err)
		return
	}
	got, err := a.svc.Get(r.Context(), ref)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTaskJSON(got.Task.Public()))
}

func (a *api) taskLogs(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	q := r.URL.Query()

	tail := defaultLogTail
	if v := q.Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > maxLogTail {
			writeErr(w, r, http.StatusBadRequest, string(taskservice.CodeInvalidArgument), ref, "",
				fmt.Sprintf("tail must be an integer between 0 and %d", maxLogTail), false)
			return
		}
		tail = n
	}

	since, err := parseDuration(q.Get("since"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, string(taskservice.CodeInvalidArgument), ref, "", fmt.Sprintf("invalid since: %v", err), false)
		return
	}

	all := parseBoolQuery(r, "all")
	stdoutOnly := parseBoolQuery(r, "stdout")
	stderrOnly := parseBoolQuery(r, "stderr")

	result, err := a.svc.Logs(r.Context(), taskservice.LogsRequest{
		Ref:    ref,
		Tail:   tail,
		Since:  since,
		All:    all,
		Stdout: stdoutOnly,
		Stderr: stderrOnly,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	entries := make([]LogEntryJSON, 0, len(result.Entries))
	for _, e := range result.Entries {
		entries = append(entries, toLogEntryJSON(e))
	}
	writeJSON(w, http.StatusOK, LogsResponseJSON{Entries: entries, HasAnyLogFile: result.HasAnyLogFile})
}
