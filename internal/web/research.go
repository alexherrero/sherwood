package web

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/alexherrero/sherwood/internal/feed"
)

// The research job's two routes, both machine routes.
const (
	exportPath = "/research/export"
	uploadPath = "/feed/upload"
)

// bearerPrefix starts an Authorization header that carries a bearer token.
// The scheme is matched without regard to case, as HTTP allows.
const bearerPrefix = "bearer "

// HandleMachine registers a route for the research job's script on the Mac,
// the one caller that is not a person at the phone app. method is GET or
// POST and pattern is a path pattern with no method.
//
// The login check still applies, so the script must come from a device
// signed in to the tailnet as the allowed login; a tagged device or another
// login is refused before the route is reached. The route then needs
// "Authorization: Bearer <feed token>". It gets no session cookie and needs
// no anti-forgery token, it is refused to a browser, and a POST must send
// JSON of at most feed.MaxFileBytes. The token and the body are never
// logged: the request log has only the method, the route, the status and a
// fixed reason.
func (s *Server) HandleMachine(method, pattern string, h http.Handler) {
	if method != http.MethodGet && method != http.MethodPost {
		panic("web: a machine route is GET or POST, not " + method)
	}
	p := method + " " + pattern
	s.machine[p] = true
	s.mux.Handle(p, s.checkMachine(h))
}

// checkMachine wraps a machine route: no browser, the feed token, and for a
// POST a JSON body under the cap.
func (s *Server) checkMachine(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Every current browser sends Sec-Fetch-Site; the script never does.
		if len(r.Header.Values("Sec-Fetch-Site")) != 0 {
			machineRefuse(w, r, "a browser on a machine route", http.StatusForbidden,
				"This route is for the research job's script, not a browser.")
			return
		}
		if !s.feedToken.Matches(bearer(r)) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="sherwood research"`)
			machineRefuse(w, r, "missing or wrong feed token", http.StatusUnauthorized,
				"This route needs the feed token.")
			return
		}
		if r.Method == http.MethodPost {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				machineRefuse(w, r, "not JSON", http.StatusUnsupportedMediaType,
					"Send the feed file as application/json.")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, feed.MaxFileBytes)
		}
		h.ServeHTTP(w, r)
	})
}

// bearer returns the token of the request's one Authorization header, or ""
// when there is none, more than one, or it is not a bearer token.
func bearer(r *http.Request) string {
	v := r.Header.Values("Authorization")
	if len(v) != 1 || len(v[0]) <= len(bearerPrefix) || !strings.EqualFold(v[0][:len(bearerPrefix)], bearerPrefix) {
		return ""
	}
	return v[0][len(bearerPrefix):]
}

// machineRefuse records reason for the request log and answers with msg as
// JSON.
func machineRefuse(w http.ResponseWriter, r *http.Request, reason refusal, status int, msg string) {
	if p, ok := r.Context().Value(refusalKey).(*refusal); ok {
		*p = reason
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func (s *Server) mountResearch() {
	s.HandleMachine(http.MethodGet, exportPath, http.HandlerFunc(s.serveExport))
	s.HandleMachine(http.MethodPost, uploadPath, http.HandlerFunc(s.serveUpload))
}

// buildExport builds the research export from one read transaction, for
// the stocks held now and the strategy file's watchlist.
func (s *Server) buildExport(r *http.Request) (feed.Export, error) {
	ctx := r.Context()
	tx, err := s.store.Reader().BeginTx(ctx, nil)
	if err != nil {
		return feed.Export{}, err
	}
	defer func() { _ = tx.Rollback() }()
	return feed.BuildExport(ctx, tx, s.clock.Now(), s.strategy.Watchlist)
}

// serveExport answers GET /research/export with the research export.
func (s *Server) serveExport(w http.ResponseWriter, r *http.Request) {
	e, err := s.buildExport(r)
	if err != nil {
		machineRefuse(w, r, "building the export failed", http.StatusInternalServerError,
			"The export could not be built.")
		return
	}
	b, err := json.Marshal(e)
	if err != nil || len(b) > feed.MaxExportBytes {
		machineRefuse(w, r, "the export is too large", http.StatusInternalServerError,
			"The export could not be built.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// uploadAnswer is what POST /feed/upload answers with for a stored feed.
type uploadAnswer struct {
	// Upload is the stored upload's number.
	Upload int64 `json:"upload"`
	// Accepted is how many items were stored.
	Accepted int `json:"accepted"`
	// Dropped lists each item left out, with why.
	Dropped []feed.Drop `json:"dropped"`
}

// serveUpload answers POST /feed/upload: it checks the feed file against an
// export built now, so items are judged against the holdings and signals as
// they are when the file arrives, and stores the upload and its kept items
// in one transaction. It only ever adds rows. An off-schema file is refused
// and nothing is stored.
func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if mbe := (*http.MaxBytesError)(nil); errors.As(err, &mbe) {
			machineRefuse(w, r, "feed file too large", http.StatusRequestEntityTooLarge,
				"The feed file is larger than the limit.")
			return
		}
		machineRefuse(w, r, "unreadable feed file", http.StatusBadRequest, "The feed file could not be read.")
		return
	}
	e, err := s.buildExport(r)
	if err != nil {
		machineRefuse(w, r, "building the export failed", http.StatusInternalServerError,
			"The feed file could not be checked.")
		return
	}
	f, err := feed.Check(body, e.Known())
	if err != nil {
		// The answer says what was wrong so the script can report it; the
		// log keeps only the fixed reason.
		machineRefuse(w, r, "off-schema feed file", http.StatusBadRequest,
			"The feed file was refused: "+err.Error())
		return
	}
	up, err := feed.Save(r.Context(), s.store.Writer(), s.clock.Now(), f)
	if err != nil {
		machineRefuse(w, r, "storing the feed failed", http.StatusInternalServerError,
			"The feed could not be stored.")
		return
	}
	dropped := f.Dropped
	if dropped == nil {
		dropped = []feed.Drop{}
	}
	writeJSON(w, http.StatusOK, uploadAnswer{Upload: up.ID, Accepted: up.Accepted, Dropped: dropped})
}
