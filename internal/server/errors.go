package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

// ErrorEnvelope is bgtask's consistent error shape: every failure response
// (from taskservice, from request-decoding, from routing) is reported this
// way, so a client only ever needs one parser.
type ErrorEnvelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Ref       string `json:"ref,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	Retryable bool   `json:"retryable"`
	RequestID string `json:"request_id,omitempty"`
}

// ErrorResponseJSON is the top-level body of a single-error response.
type ErrorResponseJSON struct {
	Error ErrorEnvelope `json:"error"`
}

// BatchErrorResponseJSON is used when an explicit (refs-based) bulk
// selection fails fast partway through: it carries the same error envelope
// as any other failure, plus the partial batch progress (Items) made
// before the failure, so a caller isn't left guessing what already ran.
type BatchErrorResponseJSON struct {
	Error ErrorEnvelope   `json:"error"`
	Items []BatchItemJSON `json:"items,omitempty"`
}

// statusForCode maps a taskservice.Code to the HTTP status used for it.
// failed_precondition shares 409 with conflict: both describe a request
// that collides with the task's current state rather than being malformed
// on its own (that's invalid_argument, 400) or simply missing (not_found,
// 404).
func statusForCode(code taskservice.Code) int {
	switch code {
	case taskservice.CodeInvalidArgument:
		return http.StatusBadRequest
	case taskservice.CodeNotFound:
		return http.StatusNotFound
	case taskservice.CodeConflict, taskservice.CodeFailedPrecondition:
		return http.StatusConflict
	case taskservice.CodeBusy:
		return http.StatusTooManyRequests
	case taskservice.CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	case taskservice.CodeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// asServiceError normalizes any error into a *taskservice.Error so every
// response goes through the same code -> status mapping, wrapping an
// error that isn't (or doesn't wrap) one as an internal error.
func asServiceError(err error) *taskservice.Error {
	var svcErr *taskservice.Error
	if errors.As(err, &svcErr) {
		return svcErr
	}
	return taskservice.Internal("", "", "", err)
}

func errorEnvelopeFor(err error, requestID string) ErrorEnvelope {
	svcErr := asServiceError(err)
	return ErrorEnvelope{
		Code:      string(svcErr.Code),
		Message:   svcErr.Message,
		Ref:       svcErr.Ref,
		TaskID:    svcErr.TaskID,
		Retryable: svcErr.Retryable,
		RequestID: requestID,
	}
}

// writeRetryAfter sets a Retry-After hint on busy (lock-contention)
// errors, which are expected to succeed on a prompt retry.
func writeRetryAfter(w http.ResponseWriter, svcErr *taskservice.Error) {
	if svcErr.Code == taskservice.CodeBusy {
		w.Header().Set("Retry-After", "1")
	}
}

// writeServiceError maps a taskservice error (or any other error, treated
// as internal) to its HTTP status and writes the standard envelope.
func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	svcErr := asServiceError(err)
	writeRetryAfter(w, svcErr)
	writeJSON(w, statusForCode(svcErr.Code), ErrorResponseJSON{
		Error: errorEnvelopeFor(err, requestIDFromContext(r.Context())),
	})
}

// writeErr builds and writes a standalone error envelope for failures that
// originate in the HTTP layer itself (malformed JSON, bad query
// parameters, an unrecognized bulk action) rather than from taskservice.
func writeErr(w http.ResponseWriter, r *http.Request, status int, code, ref, taskID, msg string, retryable bool) {
	writeJSON(w, status, ErrorResponseJSON{Error: ErrorEnvelope{
		Code:      code,
		Message:   msg,
		Ref:       ref,
		TaskID:    taskID,
		Retryable: retryable,
		RequestID: requestIDFromContext(r.Context()),
	}})
}

// writeJSON writes v as a JSON response body with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSONBody decodes a required JSON request body into dst, applying
// limit as a hard cap via http.MaxBytesReader. On failure it writes the
// appropriate error response (413 for an oversized body, 400 for
// malformed/empty JSON) and returns false; callers must return immediately
// when it does.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			writeErr(w, r, http.StatusBadRequest, string(taskservice.CodeInvalidArgument), "", "", "request body is required", false)
			return false
		}
		writeDecodeError(w, r, limit, err)
		return false
	}
	return true
}

// decodeOptionalJSONBody is like decodeJSONBody, but an absent/empty body
// leaves dst untouched (its zero value) instead of failing, for routes
// whose request body is entirely optional (e.g. stop/restart with no
// overrides).
func decodeOptionalJSONBody(w http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	if r.ContentLength == 0 {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		writeDecodeError(w, r, limit, err)
		return false
	}
	return true
}

func writeDecodeError(w http.ResponseWriter, r *http.Request, limit int64, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeErr(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "", "",
			fmt.Sprintf("request body exceeds the %d byte limit for this route", limit), false)
		return
	}
	writeErr(w, r, http.StatusBadRequest, string(taskservice.CodeInvalidArgument), "", "",
		fmt.Sprintf("invalid JSON body: %v", err), false)
}
