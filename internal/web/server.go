// Package web serves the phone app over HTTP, behind Tailscale Serve.
//
// The container publishes the web port on the NAS host's loopback address
// only, so every request arrives through Serve. That is what makes Serve's
// headers trustworthy: Tailscale-User-Login names the calling tailnet user,
// and X-Forwarded-For carries the calling device's tailnet address. Nothing
// else is trusted.
//
// Every request must carry the one allowed login, or it is refused before it
// reaches a route. Only GET (and HEAD) read and only POST changes: a read is
// registered with HandleRead and can only be reached by GET, and a change is
// registered with HandleChange and can only be reached by POST with a valid
// anti-forgery token. A change can also be limited to the approval devices.
// Every response carries a strict Content-Security-Policy and the other
// security headers, and is marked no-store by default.
package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/risk"
	"github.com/alexherrero/sherwood/internal/store"
)

// Server timeouts. A phone on the tailnet sends small forms and reads small
// pages, so these are short; the write timeout leaves room for a slow view.
const (
	ReadHeaderTimeout = 5 * time.Second
	ReadTimeout       = 15 * time.Second
	WriteTimeout      = 30 * time.Second
	IdleTimeout       = 120 * time.Second
	// ShutdownTimeout is how long Serve waits for requests in flight once
	// shutdown starts. The Compose file's stop grace period is longer.
	ShutdownTimeout = 10 * time.Second
	// MaxHeaderBytes caps a request's headers.
	MaxHeaderBytes = 32 << 10
	// MaxFormBytes caps a POST body.
	MaxFormBytes = 64 << 10
)

// Config is what a Server needs.
type Config struct {
	// Login is the one tailnet login allowed in, as Serve reports it in
	// Tailscale-User-Login.
	Login string
	// ApprovalDevices are the tailnet addresses allowed to make changes
	// registered with ApprovalDevicesOnly.
	ApprovalDevices []netip.Addr
	// Clock dates and checks anti-forgery tokens.
	Clock clock.Clock
	// Logger receives one line per request, with the matched route pattern
	// and never the raw path, query or headers.
	Logger *slog.Logger
	// Key signs anti-forgery tokens. Leave it nil to draw a random key, which
	// makes every token from an earlier run stale.
	Key []byte
	// Store is the database the views read from. The views only read it.
	Store *store.Store
	// Mode is the trading mode the settings file names, shown on Today.
	Mode config.Mode
	// Push is the web push channel. Leave it nil when notifications are not
	// set up; Settings then says so.
	Push PushChannel
	// Switch is the stop switch, which Today shows and its two actions throw
	// and reset. Required.
	Switch *halt.Switch
	// Strategy is the loaded strategy file, whose limits, sectors and
	// watchlist the approval re-check reads. Required.
	Strategy *config.Strategy
	// Calendar is the market calendar the risk check uses. Required.
	Calendar *clock.Calendar
	// Checker is the risk check an approval runs again, risk.Standard{}
	// outside tests. Required.
	Checker risk.Checker
	// BrokerHealthy says whether the broker adapter is answering, for the
	// approval's risk check. Required.
	BrokerHealthy func(context.Context) bool
	// Orders cancels open orders at the broker when the stop switch is
	// thrown: the execution layer. Leave it nil when no broker adapter is
	// built (live mode, until live trading): the switch still declines the
	// waiting buys, and Today says open orders could not be cancelled.
	Orders OrderCanceller
}

// Server is the phone app's HTTP server.
type Server struct {
	login   string
	devices map[netip.Addr]bool
	clock   clock.Clock
	logger  *slog.Logger
	tokens  *tokener
	store   *store.Store
	mode    config.Mode
	push    PushChannel
	stop    *halt.Switch
	// What an approval's risk check needs.
	strategy      *config.Strategy
	calendar      *clock.Calendar
	checker       risk.Checker
	brokerHealthy func(context.Context) bool
	orders        OrderCanceller
	mux           *http.ServeMux
	handler       http.Handler
	// reads lists every read pattern registered, for tests.
	reads []string
}

// New builds a Server with the app shell (the six views, the static files
// and the service worker) and the health check. Actions are added with
// HandleChange.
func New(cfg Config) (*Server, error) {
	if cfg.Login == "" {
		return nil, errors.New("web: the allowed tailnet login is empty")
	}
	if cfg.Clock == nil || cfg.Logger == nil {
		return nil, errors.New("web: a clock and a logger are required")
	}
	if cfg.Store == nil {
		return nil, errors.New("web: the database is required")
	}
	if cfg.Switch == nil {
		return nil, errors.New("web: the stop switch is required")
	}
	if cfg.Strategy == nil || cfg.Calendar == nil || cfg.Checker == nil || cfg.BrokerHealthy == nil {
		return nil, errors.New("web: the strategy file, the calendar, the risk check and the broker's health are required")
	}
	if cfg.Mode != config.ModePaper && cfg.Mode != config.ModeLive {
		return nil, fmt.Errorf("web: the trading mode must be %q or %q", config.ModePaper, config.ModeLive)
	}
	tk, err := newTokener(cfg.Key)
	if err != nil {
		return nil, err
	}
	s := &Server{
		login:   cfg.Login,
		devices: make(map[netip.Addr]bool, len(cfg.ApprovalDevices)),
		clock:   cfg.Clock,
		logger:  cfg.Logger,
		tokens:  tk,
		store:   cfg.Store,
		mode:    cfg.Mode,
		push:    cfg.Push,
		stop:    cfg.Switch,

		strategy:      cfg.Strategy,
		calendar:      cfg.Calendar,
		checker:       cfg.Checker,
		brokerHealthy: cfg.BrokerHealthy,
		orders:        cfg.Orders,
		mux:           http.NewServeMux(),
	}
	for _, a := range cfg.ApprovalDevices {
		s.devices[a.Unmap()] = true
	}
	s.handler = s.guard(s.mux)

	s.mountShell()
	s.mountPush()
	s.mountStopSwitch()
	s.mountApprovals()
	s.mountLimits()
	s.HandleRead("/health", http.HandlerFunc(health))
	return s, nil
}

// ChangeOption limits who may make a change.
type ChangeOption int

// ApprovalDevicesOnly limits a change to the approval devices: approving a
// buy, and throwing and resetting the stop switch.
const ApprovalDevicesOnly ChangeOption = 1

// HandleRead registers a view. It answers GET and HEAD only, so it must never
// change anything. pattern is a path pattern with no method, such as
// "/portfolio".
func (s *Server) HandleRead(pattern string, h http.Handler) {
	s.mux.Handle(http.MethodGet+" "+pattern, h)
	s.reads = append(s.reads, pattern)
}

// HandleChange registers an action. It answers POST only, after the request's
// anti-forgery token has passed, and, with ApprovalDevicesOnly, only from an
// approval device. pattern is a path pattern with no method.
func (s *Server) HandleChange(pattern string, h http.Handler, opts ...ChangeOption) {
	devicesOnly := false
	for _, o := range opts {
		if o == ApprovalDevicesOnly {
			devicesOnly = true
		}
	}
	s.mux.Handle(http.MethodPost+" "+pattern, s.checkChange(h, devicesOnly))
}

// Handler returns the server's whole handler, guards included.
func (s *Server) Handler() http.Handler { return s.handler }

// Serve answers requests on ln until ctx ends, then shuts down: it stops
// accepting, lets requests in flight finish for up to ShutdownTimeout, and
// returns nil. It closes ln. Any other failure is returned.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	hs := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
		MaxHeaderBytes:    MaxHeaderBytes,
		// net/http's own messages are free text that can carry addresses;
		// the guard logs every request through the allowlisted logger.
		ErrorLog: log.New(io.Discard, "", 0),
		BaseContext: func(net.Listener) context.Context {
			return context.WithoutCancel(ctx)
		},
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	select {
	case err := <-errc:
		return fmt.Errorf("web server: %w", err)
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
	defer cancel()
	err := hs.Shutdown(sctx)
	if serr := <-errc; !errors.Is(serr, http.ErrServerClosed) && err == nil {
		err = serr
	}
	if err != nil {
		return fmt.Errorf("web server shutdown: %w", err)
	}
	return nil
}

// health answers "ok" so the operator can check the page is reachable.
func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n")
}
