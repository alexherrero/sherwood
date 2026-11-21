package web

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// The headers Tailscale Serve adds. Serve removes any copy the caller sent
// before adding its own, and the loopback-only port means nothing but Serve
// can reach the server.
const (
	headerLogin        = "Tailscale-User-Login"
	headerForwardedFor = "X-Forwarded-For"
)

// contentSecurityPolicy allows only the app's own files: no inline script or
// style, no plugins, no framing and no forms sent anywhere else.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self'; connect-src 'self'; manifest-src 'self'; worker-src 'self'; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// routeUnmatched is logged for a request that matched no route.
const routeUnmatched = "unmatched"

type ctxKey int

const (
	sessionKey ctxKey = iota
	refusalKey
)

// statusWriter records the status a handler wrote.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// refusal is why a request was turned away. Its text is logged as the reason
// and is never the request's own data.
type refusal string

// guard is the outermost handler: security headers, the login check, the
// method check, the session cookie (not for a machine route) and the request
// log, in that order.
func (s *Server) guard(next *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.clock.Now()
		sw := &statusWriter{ResponseWriter: w}
		route := routeUnmatched
		if _, p := next.Handler(r); p != "" {
			route = p
		}
		var reason refusal
		defer func() {
			v := recover()
			if v != nil {
				reason = "the handler failed"
				if sw.status == 0 {
					http.Error(sw, "Something went wrong.", http.StatusInternalServerError)
				}
			}
			s.logRequest(r, route, sw.status, reason, s.clock.Now().Sub(start))
			if v == http.ErrAbortHandler { //nolint:errorlint // recover returns the panic value itself
				panic(v) // net/http's own signal to drop the connection quietly
			}
		}()

		setSecurityHeaders(sw.Header())

		if !s.allowedLogin(r) {
			reason = "missing or wrong tailnet login"
			http.Error(sw, "This app is only for its owner's tailnet login.", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodPost:
		default:
			reason = "method not allowed"
			sw.Header().Set("Allow", "GET, HEAD, POST")
			http.Error(sw, "Method not allowed.", http.StatusMethodNotAllowed)
			return
		}

		ctx := r.Context()
		// A machine route has no session: the research job's script keeps
		// no cookie, and its routes check the feed token instead.
		if !s.machine[route] {
			session, ok := sessionFrom(r)
			if !ok {
				session = newSession()
				http.SetCookie(sw, sessionCookie(session))
			}
			ctx = context.WithValue(ctx, sessionKey, &requestSession{id: session, s: s})
		}
		// A refusal inside a change or machine handler is recorded through
		// the context.
		ctx = context.WithValue(ctx, refusalKey, &reason)
		next.ServeHTTP(sw, r.WithContext(ctx))
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
	})
}

func refuse(w http.ResponseWriter, r *http.Request, reason refusal, status int, msg string) {
	if p, ok := r.Context().Value(refusalKey).(*refusal); ok {
		*p = reason
	}
	http.Error(w, msg, status)
}

func (s *Server) logRequest(r *http.Request, route string, status int, reason refusal, d time.Duration) {
	args := []any{"method", r.Method, "route", route, "status", status, "duration", d}
	if reason != "" {
		s.logger.Warn("request refused", append(args, "reason", string(reason))...)
		return
	}
	s.logger.Info("request", args...)
}

func setSecurityHeaders(h http.Header) {
	h.Set("Content-Security-Policy", contentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	// Data is never cached; the app shell overrides this for its own files.
	h.Set("Cache-Control", "no-store")
}

// allowedLogin reports whether the request carries exactly one
// Tailscale-User-Login header equal to the allowed login.
func (s *Server) allowedLogin(r *http.Request) bool {
	v := r.Header.Values(headerLogin)
	return len(v) == 1 && v[0] == s.login
}

// checkChange wraps an action: POST body cap, the cross-site check, the
// anti-forgery token and, when asked, the approval-device check.
func (s *Server) checkChange(h http.Handler, devicesOnly bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The browser says where a request came from. A form posted from
		// another site is refused before its body is read.
		switch r.Header.Get("Sec-Fetch-Site") {
		case "", "same-origin", "none":
		default:
			refuse(w, r, "cross-site request", http.StatusForbidden, "This form must be sent from sherwood itself.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxFormBytes)
		if err := r.ParseForm(); err != nil {
			refuse(w, r, "unreadable form", http.StatusBadRequest, "The form could not be read.")
			return
		}
		sess, _ := r.Context().Value(sessionKey).(*requestSession)
		if sess == nil || !s.tokens.valid(r.PostForm.Get(TokenField), s.login, sess.id, s.clock.Now()) {
			refuse(w, r, "missing or stale anti-forgery token", http.StatusForbidden,
				"This form has expired. Reload the page and try again.")
			return
		}
		if devicesOnly && !s.fromApprovalDevice(r) {
			refuse(w, r, "not an approval device", http.StatusForbidden,
				"Only an approval device can do this. Use the device named in your settings.")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// fromApprovalDevice reports whether the caller's tailnet address, as Serve
// forwards it, is an approval device.
func (s *Server) fromApprovalDevice(r *http.Request) bool {
	a, ok := callerAddr(r)
	return ok && s.devices[a]
}

// callerAddr is the caller's tailnet address as Serve forwards it, unmapped.
// Serve sets X-Forwarded-For to the caller's address; should anything have
// been added before it, the last entry is still the one Serve wrote.
func callerAddr(r *http.Request) (netip.Addr, bool) {
	v := r.Header.Values(headerForwardedFor)
	if len(v) == 0 {
		return netip.Addr{}, false
	}
	parts := strings.Split(v[len(v)-1], ",")
	a, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1]))
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// requestSession is the session a request belongs to, for issuing tokens.
type requestSession struct {
	id string
	s  *Server
}

// Token returns a fresh anti-forgery token for forms on the page answering r.
// Every form that POSTs must carry it in a hidden field named TokenField. It
// returns "" for a request that did not pass through a Server.
func Token(r *http.Request) string {
	sess, ok := r.Context().Value(sessionKey).(*requestSession)
	if !ok {
		return ""
	}
	return sess.s.tokens.issue(sess.s.login, sess.id, sess.s.clock.Now())
}
