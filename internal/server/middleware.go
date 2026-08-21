package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

type ctxKey int

const requestIDCtxKey ctxKey = iota

// requestIDFromContext returns the request ID stashed by
// requestIDMiddleware, or "" if none is present (e.g. outside a request,
// such as in a unit test that builds a context.Background()).
func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDCtxKey).(string)
	return id
}

// newRequestID returns a random 16-byte hex-encoded request identifier.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read on the standard library's Reader does not fail
		// in practice; fall back to a fixed, clearly-synthetic ID rather
		// than panicking a request over it.
		return "unavailable"
	}
	return hex.EncodeToString(b[:])
}

// requestIDMiddleware assigns a request ID (surfaced via the X-Request-Id
// response header and in the error envelope) to every request, and stores
// it in the request context so every inner layer -- recovery, logging,
// error responses -- can reference the same value. It is applied
// outermost so all of those layers see it.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), requestIDCtxKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// recoverMiddleware turns a panic anywhere downstream into a logged event
// plus a well-formed 500 error envelope, instead of the connection being
// dropped or a bare stack trace reaching the client.
func recoverMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					reqID := requestIDFromContext(r.Context())
					logger.Error("panic recovered",
						"panic", fmt.Sprint(rec),
						"request_id", reqID,
						"method", r.Method,
						"path", r.URL.Path,
						"stack", string(debug.Stack()),
					)
					writeJSON(w, http.StatusInternalServerError, ErrorResponseJSON{Error: ErrorEnvelope{
						Code:      string(taskservice.CodeInternal),
						Message:   "internal server error",
						Retryable: false,
						RequestID: reqID,
					}})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder wraps an http.ResponseWriter to capture the status code
// actually written, for use by loggingMiddleware.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status = http.StatusOK
		s.wrote = true
	}
	return s.ResponseWriter.Write(b)
}

// loggingMiddleware logs one structured line per completed request: method,
// path, status, duration, request ID, and remote address.
func loggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", requestIDFromContext(r.Context()),
				"remote_addr", r.RemoteAddr,
			)
		})
	}
}

// authMiddleware is the auth seam: with auth == nil (the default) it is a
// no-op passthrough. /healthz is always exempt, so health checks never
// depend on credentials that may not exist yet during rollout.
func authMiddleware(auth AuthFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if auth == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			if err := auth(r); err != nil {
				writeErr(w, r, http.StatusUnauthorized, "unauthorized", "", "", err.Error(), false)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// OriginMiddleware builds a middleware that enforces an exact-match Origin
// allow-list: requests with no Origin header (the common case for
// non-browser clients like curl or the bgtask CLI) are always allowed;
// requests carrying an Origin header not present in allowed are rejected
// with 403 Forbidden. It is exported so the same policy can wrap the MCP
// handler too, in addition to being applied to the whole server (both are
// wrapped together in New).
func OriginMiddleware(allowed []string) func(http.Handler) http.Handler {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		allowedSet[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := allowedSet[origin]; ok {
				next.ServeHTTP(w, r)
				return
			}
			writeErr(w, r, http.StatusForbidden, "origin_not_allowed", "", "",
				fmt.Sprintf("origin %q is not allowed", origin), false)
		})
	}
}

// originMiddleware is an unexported alias kept for readability at the call
// site in New; it is identical to OriginMiddleware.
func originMiddleware(allowed []string) func(http.Handler) http.Handler {
	return OriginMiddleware(allowed)
}
