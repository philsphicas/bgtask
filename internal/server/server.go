// Package server implements bgtask's foreground server runtime: a single
// net/http.Server exposing a REST API and an MCP endpoint over one shared
// mux. It owns request-level concerns -- timeouts, body limits, request
// IDs, panic recovery, structured logging, origin checking, and an auth
// seam -- so the CLI (cmd/bgtask) only has to parse flags and call
// Listen/Serve.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

// Exposure names one of the interfaces a Server can expose. Additional
// values must not be introduced without updating New's validation.
type Exposure string

// The two exposures a Server currently understands. Both are constructed by
// New; ExposeMCP additionally requires Options.MCPHandler to be set (see
// its doc comment).
const (
	ExposeREST Exposure = "rest"
	ExposeMCP  Exposure = "mcp"
)

// AuthFunc authenticates/authorizes an incoming request. Returning a
// non-nil error rejects the request with 401 Unauthorized. AuthFunc is a
// seam: Options.Auth is nil by default (no authentication performed) until
// a later change wires in a real credential check.
type AuthFunc func(*http.Request) error

// Default request-handling tuning. WriteTimeout is intentionally not
// configurable here and is always left at its zero value (no deadline):
// bgtask does not currently stream any response, but a future SSE/MCP
// stream must not be cut off by a blanket write deadline.
const (
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 30 * time.Second
	defaultIdleTimeout       = 120 * time.Second

	// Per-route request body limits. Run's body is the largest since it
	// carries the command, its arguments, and environment overrides;
	// everything else is a small, fixed-shape request.
	defaultMaxRunBodyBytes    = 1 << 20 // 1 MiB
	defaultMaxActionBodyBytes = 256 * 1024
	defaultMaxLabelsBodyBytes = 64 * 1024
	defaultMaxRenameBodyBytes = 4 * 1024
	defaultMaxStopBodyBytes   = 4 * 1024

	// defaultMCPPath is where the MCP handler is mounted when exposed.
	defaultMCPPath = "/mcp"
)

// Options configures a Server. Service and MCPHandler are validated
// against Expose by New: requesting an exposure without the dependency it
// needs to serve is a construction error, not a runtime one.
type Options struct {
	// Service backs the REST API. Required if Expose contains ExposeREST.
	Service *taskservice.Service

	// Expose lists which interfaces to mount. Must be non-empty and may
	// only contain ExposeREST/ExposeMCP.
	Expose []Exposure

	// AllowOrigins is the exact-match allow-list used by the origin
	// middleware (see OriginMiddleware). Requests with no Origin header
	// are always allowed; requests with an Origin header not in this list
	// are rejected with 403, regardless of route.
	AllowOrigins []string

	// MCPHandler is the explicit mounting seam for the MCP endpoint.
	// Passing ExposeMCP in Expose without a non-nil MCPHandler is a clear,
	// immediate construction error from New, rather than a silently-missing
	// route. cmd/bgtask wires in internal/mcpserver.NewHandler here.
	MCPHandler http.Handler

	// MCPPath is where MCPHandler is mounted. Defaults to "/mcp".
	MCPPath string

	// Auth is the authentication/authorization seam; nil (the default)
	// performs no check. /healthz is always exempt.
	Auth AuthFunc

	// Logger receives structured request/error logs. Defaults to
	// slog.Default().
	Logger *slog.Logger

	// Timeouts, applied to the underlying http.Server. Zero/negative
	// values fall back to the package defaults; WriteTimeout is not
	// configurable (see above).
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	IdleTimeout       time.Duration

	// Per-route request body size limits, in bytes. Zero/negative values
	// fall back to the package defaults.
	MaxRunBodyBytes    int64
	MaxActionBodyBytes int64
	MaxLabelsBodyBytes int64
	MaxRenameBodyBytes int64
	MaxStopBodyBytes   int64
}

func (o *Options) applyDefaults() {
	if o.MCPPath == "" {
		o.MCPPath = defaultMCPPath
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.ReadHeaderTimeout <= 0 {
		o.ReadHeaderTimeout = defaultReadHeaderTimeout
	}
	if o.ReadTimeout <= 0 {
		o.ReadTimeout = defaultReadTimeout
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = defaultIdleTimeout
	}
	if o.MaxRunBodyBytes <= 0 {
		o.MaxRunBodyBytes = defaultMaxRunBodyBytes
	}
	if o.MaxActionBodyBytes <= 0 {
		o.MaxActionBodyBytes = defaultMaxActionBodyBytes
	}
	if o.MaxLabelsBodyBytes <= 0 {
		o.MaxLabelsBodyBytes = defaultMaxLabelsBodyBytes
	}
	if o.MaxRenameBodyBytes <= 0 {
		o.MaxRenameBodyBytes = defaultMaxRenameBodyBytes
	}
	if o.MaxStopBodyBytes <= 0 {
		o.MaxStopBodyBytes = defaultMaxStopBodyBytes
	}
}

// exposureSet validates opts.Expose and returns it as a set. It is the
// single place that decides whether a requested exposure is satisfiable
// given the rest of Options.
func exposureSet(opts Options) (map[Exposure]bool, error) {
	if len(opts.Expose) == 0 {
		return nil, fmt.Errorf("server: at least one exposure (%q or %q) must be configured", ExposeREST, ExposeMCP)
	}
	set := make(map[Exposure]bool, len(opts.Expose))
	for _, e := range opts.Expose {
		switch e {
		case ExposeREST, ExposeMCP:
			set[e] = true
		default:
			return nil, fmt.Errorf("server: unknown exposure %q (expected %q or %q)", e, ExposeREST, ExposeMCP)
		}
	}
	if set[ExposeREST] && opts.Service == nil {
		return nil, fmt.Errorf("server: rest exposure requires a non-nil Options.Service")
	}
	if set[ExposeMCP] && opts.MCPHandler == nil {
		return nil, fmt.Errorf("server: mcp exposure requested but no MCP handler is configured; pass Options.MCPHandler, or omit %q from Expose", ExposeMCP)
	}
	return set, nil
}

// Server is a constructed, listen-ready bgtask server. Construct with New,
// bind a listener with Listen (or supply your own via Serve), and run the
// blocking accept loop with Serve. Listen and Serve are separate so tests
// (and callers that need the resolved address before blocking) never have
// to race a fixed port.
type Server struct {
	opts    Options
	handler http.Handler
	http    *http.Server
}

// New validates opts and builds a Server. It returns a descriptive error
// (rather than panicking or silently degrading) if an exposure is
// requested without the dependency it needs -- most notably, ExposeMCP
// without a non-nil Options.MCPHandler.
func New(opts Options) (*Server, error) {
	exposed, err := exposureSet(opts)
	if err != nil {
		return nil, err
	}
	opts.applyDefaults()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)

	if exposed[ExposeREST] {
		a := &api{svc: opts.Service, opts: opts}
		a.register(mux)
	}
	if exposed[ExposeMCP] {
		mux.Handle(opts.MCPPath, opts.MCPHandler)
	}

	var handler http.Handler = mux
	handler = originMiddleware(opts.AllowOrigins)(handler)
	handler = authMiddleware(opts.Auth)(handler)
	handler = loggingMiddleware(opts.Logger)(handler)
	handler = recoverMiddleware(opts.Logger)(handler)
	handler = requestIDMiddleware(handler)

	httpSrv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: opts.ReadHeaderTimeout,
		ReadTimeout:       opts.ReadTimeout,
		WriteTimeout:      0,
		IdleTimeout:       opts.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(opts.Logger.Handler(), slog.LevelError),
	}

	return &Server{opts: opts, handler: handler, http: httpSrv}, nil
}

// Handler returns the fully-wrapped http.Handler (middleware + mux), for
// tests that want to drive it directly (e.g. via httptest.NewServer or
// httptest.NewRecorder) without binding a real socket.
func (s *Server) Handler() http.Handler { return s.handler }

// Listen resolves bind:port into a listening TCP socket and returns it
// without blocking. Passing port 0 asks the OS to assign a free port;
// the resolved address is available via ln.Addr(). Listen and Serve are
// split specifically so a caller (or test) can learn the resolved address
// before entering the blocking accept loop.
func (s *Server) Listen(bind string, port int) (net.Listener, error) {
	addr := net.JoinHostPort(bind, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("server: listen on %s: %w", addr, err)
	}
	return ln, nil
}

// Serve runs the blocking accept loop on ln until Shutdown is called (or
// ln/the server otherwise stops), returning nil on a clean shutdown
// instead of the sentinel http.ErrServerClosed.
func (s *Server) Serve(ln net.Listener) error {
	err := s.http.Serve(ln)
	if err != nil && errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully stops the server, waiting for in-flight requests to
// finish (or ctx to expire, whichever comes first).
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
