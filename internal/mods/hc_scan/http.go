package hcscan

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/i18n"
	"dash_go/internal/core/shell"
)

//go:embed web i18n
var webFS embed.FS

// pageSpec describes the page of the module for the shell: the title and the name next to the logo, the files of
// the page, and the prefixes of the catalog its script needs (hc.ui. holds the texts of the page itself).
func pageSpec(prefix string) shell.PageSpec {
	return shell.PageSpec{
		TitleKey: "hc.title", AppKey: "hc.title", RequireLogin: true,
		HelpHref:       prefix + "/help",
		Guide:          true,
		Styles:         []string{prefix + "/static/app.css"},
		Scripts:        []string{prefix + "/static/app.js"},
		ClientPrefixes: []string{"hc.ui."},
	}
}

func helpPageSpec(prefix string) shell.PageSpec {
	return shell.PageSpec{
		TitleKey: "hc.help.title", AppKey: "hc.title",
		HelpHref: prefix + "/help",
		Guide:    true,
	}
}

// Routes mounts the page, its static files and the JSON API below prefix (for example /dash_go/hc_scan). The page
// itself is rendered by the shell, which also sends a visitor who is not signed in to the login page.
func (s *Service) Routes(mux *http.ServeMux, prefix string, guard func(http.Handler) http.Handler, sh *shell.Shell) error {
	static, err := fs.Sub(webFS, "web/static")
	if err != nil {
		return err
	}
	page, err := sh.NewPage(pageSpec(prefix), webFS, "web/index.html")
	if err != nil {
		return err
	}
	helpPage, err := sh.NewPage(helpPageSpec(prefix), webFS, "web/help.html")
	if err != nil {
		return err
	}
	mux.HandleFunc("GET "+prefix, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
	})
	mux.Handle("GET "+prefix+"/{$}", page.Handler(nil))
	mux.Handle("GET "+prefix+"/help", helpPage.Handler(func(*http.Request) any { return prefix }))
	mux.Handle("GET "+prefix+"/static/", noCache(http.StripPrefix(prefix+"/static/", http.FileServerFS(static))))

	api := func(method, path string, h http.HandlerFunc) {
		mux.Handle(method+" "+prefix+"/api/"+path, guard(noStore(h)))
	}
	api("GET", "me", s.handleMe)
	api("GET", "board", s.handleBoard)
	api("POST", "scan", s.handleScan)
	api("POST", "assign", s.handleAssign)
	api("POST", "remove", s.handleRemove)
	api("POST", "reset", s.handleReset)
	api("POST", "confirm", s.handleConfirm)
	return nil
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

func noStore(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h(w, r)
	})
}

type sessionRequest struct {
	Process string `json:"process"`
	Date    string `json:"date"`
	Shift   string `json:"shift"`
	Input   string `json:"input"`
	EE      string `json:"ee"`
	Machine string `json:"machine_id"`
	Ack     bool   `json:"ack"`
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	me, err := s.Me(r.Context(), u)
	respond(w, r, me, err)
}

func (s *Service) handleBoard(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	k, err := parseKey(r.URL.Query().Get("process"), r.URL.Query().Get("date"), r.URL.Query().Get("shift"))
	if err != nil {
		respond[any](w, r, nil, err)
		return
	}
	b, err := s.Board(r.Context(), u, k)
	respond(w, r, b, err)
}

func (s *Service) handleScan(w http.ResponseWriter, r *http.Request) {
	u, req, k, ok := s.session(w, r)
	if !ok {
		return
	}
	res, err := s.Scan(r.Context(), u, k, req.Input)
	respond(w, r, res, err)
}

func (s *Service) handleAssign(w http.ResponseWriter, r *http.Request) {
	u, req, k, ok := s.session(w, r)
	if !ok {
		return
	}
	e, err := s.Assign(r.Context(), u, k, req.EE, req.Machine)
	respond(w, r, e, err)
}

func (s *Service) handleRemove(w http.ResponseWriter, r *http.Request) {
	u, req, k, ok := s.session(w, r)
	if !ok {
		return
	}
	err := s.Remove(r.Context(), u, k, req.EE)
	respond(w, r, map[string]bool{"ok": true}, err)
}

func (s *Service) handleReset(w http.ResponseWriter, r *http.Request) {
	u, _, k, ok := s.session(w, r)
	if !ok {
		return
	}
	err := s.Reset(r.Context(), u, k)
	respond(w, r, map[string]bool{"ok": true}, err)
}

func (s *Service) handleConfirm(w http.ResponseWriter, r *http.Request) {
	u, req, k, ok := s.session(w, r)
	if !ok {
		return
	}
	res, err := s.Confirm(r.Context(), u, k, req.Ack)
	if err == nil && len(res.NeedsAck) > 0 {
		// nothing was written: the page shows the warnings and sends the request again with ack=true
		writeJSON(w, http.StatusConflict, map[string]any{"error": i18n.T(r.Context(), "hc.err.needs_ack", nil), "code": "needs_ack", "needs_ack": res.NeedsAck})
		return
	}
	respond(w, r, res, err)
}

// session decodes a POST body and the session key in it.
func (s *Service) session(w http.ResponseWriter, r *http.Request) (auth.User, sessionRequest, Key, bool) {
	u, _ := auth.UserFrom(r.Context())
	var req sessionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	if err := dec.Decode(&req); err != nil {
		respond[any](w, r, nil, newError(http.StatusBadRequest, "bad_request"))
		return u, req, Key{}, false
	}
	k, err := parseKey(req.Process, req.Date, req.Shift)
	if err != nil {
		respond[any](w, r, nil, err)
		return u, req, Key{}, false
	}
	return u, req, k, true
}

func parseKey(process, date, shift string) (Key, error) {
	d, err := time.Parse("2006-01-02", strings.TrimSpace(date))
	if err != nil {
		return Key{}, newError(http.StatusBadRequest, "bad_date")
	}
	return Key{Process: strings.TrimSpace(process), Date: DateOnly(d), Shift: strings.ToUpper(strings.TrimSpace(shift))}, nil
}

// respond answers with v, or with the JSON of an error whose sentence is in the language of the request.
func respond[T any](w http.ResponseWriter, r *http.Request, v T, err error) {
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			args := make(i18n.P, len(e.Args))
			for k, v := range e.Args {
				args[k] = v
			}
			if step, ok := args["step"].(string); ok { // the step of a failed request names a text of the catalog
				args["step"] = i18n.T(r.Context(), step, nil)
			}
			writeJSON(w, e.Status, map[string]any{"error": i18n.T(r.Context(), e.Key, args), "code": e.Code})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": i18n.T(r.Context(), "err.internal", nil), "code": "internal"})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
