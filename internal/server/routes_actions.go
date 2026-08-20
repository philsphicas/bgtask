package server

import (
	"fmt"
	"net/http"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

// allowedActions is the allowlist for POST /api/v1/actions/{action}.
var allowedActions = map[string]bool{
	"start":   true,
	"stop":    true,
	"restart": true,
	"remove":  true,
	"cleanup": true,
}

// bulkAction dispatches a bulk operation across a Selection (refs, labels,
// or all). "cleanup" needs no selection at all -- it always targets every
// non-running task -- so its Selection/Force/Timeout body fields, if
// present, are ignored.
func (a *api) bulkAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if !allowedActions[action] {
		writeErr(w, r, http.StatusBadRequest, string(taskservice.CodeInvalidArgument), action, "",
			fmt.Sprintf("unknown action %q (expected one of: start, stop, restart, remove, cleanup)", action), false)
		return
	}

	var body BulkActionBodyJSON
	if !decodeOptionalJSONBody(w, r, a.opts.MaxActionBodyBytes, &body) {
		return
	}

	reqID := requestIDFromContext(r.Context())

	if action == "cleanup" {
		result, err := a.svc.Cleanup(r.Context(), taskservice.CleanupRequest{})
		a.respondBatch(w, r, result, err, reqID)
		return
	}

	sel, serr := parseSelection(body.Selection)
	if serr != nil {
		writeErr(w, r, http.StatusBadRequest, string(taskservice.CodeInvalidArgument), "", "", serr.Error(), false)
		return
	}
	timeout, err := parseDuration(body.Timeout)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, string(taskservice.CodeInvalidArgument), "", "", fmt.Sprintf("invalid timeout: %v", err), false)
		return
	}

	var result *taskservice.BatchResult
	var svcErr error
	switch action {
	case "start":
		result, svcErr = a.svc.Start(r.Context(), taskservice.StartRequest{Selection: sel})
	case "stop":
		result, svcErr = a.svc.Stop(r.Context(), taskservice.StopRequest{Selection: sel, Force: body.Force, Timeout: timeout})
	case "restart":
		result, svcErr = a.svc.Restart(r.Context(), taskservice.RestartRequest{Selection: sel, Force: body.Force})
	case "remove":
		result, svcErr = a.svc.Remove(r.Context(), taskservice.RemoveRequest{Selection: sel, Force: body.Force, Timeout: timeout})
	}
	a.respondBatch(w, r, result, svcErr, reqID)
}

// respondBatch renders a taskservice batch outcome:
//
//   - a best-effort (labels/all) selection never fails as a whole: err is
//     nil, and the response is always 200 with per-item results/errors in
//     Items.
//   - an explicit refs selection is processed fail-fast; if err is
//     non-nil, that is a genuine request-level failure and its code maps
//     to the matching HTTP status as usual, but the partial batch progress
//     made before the failure is still included as Items.
//   - if result itself is nil (the initial task snapshot for a
//     labels/all selection could not be taken), there is nothing
//     batch-shaped to report and this is just a normal error response.
func (a *api) respondBatch(w http.ResponseWriter, r *http.Request, result *taskservice.BatchResult, err error, requestID string) {
	if result == nil {
		writeServiceError(w, r, err)
		return
	}

	items := make([]BatchItemJSON, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, toBatchItemJSON(requestID, item))
	}

	if err != nil {
		svcErr := asServiceError(err)
		writeRetryAfter(w, svcErr)
		writeJSON(w, statusForCode(svcErr.Code), BatchErrorResponseJSON{
			Error: errorEnvelopeFor(err, requestID),
			Items: items,
		})
		return
	}

	writeJSON(w, http.StatusOK, BatchResponseJSON{Items: items})
}
