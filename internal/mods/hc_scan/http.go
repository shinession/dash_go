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
)

//go:embed web
var webFS embed.FS

// Routes mounts the page, its static files and the JSON API below prefix (for example /dash_go/hc_scan).
func (s *Service) Routes(mux *http.ServeMux, prefix string, guard func(http.Handler) http.Handler) error {
	static, err := fs.Sub(webFS, "web")
	if err != nil {
		return err
	}
	mux.HandleFunc("GET "+prefix, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
	})
	files := http.StripPrefix(prefix+"/", http.FileServerFS(static))
	mux.Handle("GET "+prefix+"/{$}", noCache(files))
	mux.Handle("GET "+prefix+"/static/", noCache(http.StripPrefix(prefix+"/static/", fileServerAt(static, "static"))))

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

func fileServerAt(fsys fs.FS, dir string) http.Handler {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServerFS(sub)
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
	respond(w, me, err)
}

func (s *Service) handleBoard(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	k, err := parseKey(r.URL.Query().Get("process"), r.URL.Query().Get("date"), r.URL.Query().Get("shift"))
	if err != nil {
		respond[any](w, nil, err)
		return
	}
	b, err := s.Board(r.Context(), u, k)
	respond(w, b, err)
}

func (s *Service) handleScan(w http.ResponseWriter, r *http.Request) {
	u, req, k, ok := s.session(w, r)
	if !ok {
		return
	}
	res, err := s.Scan(r.Context(), u, k, req.Input)
	respond(w, res, err)
}

func (s *Service) handleAssign(w http.ResponseWriter, r *http.Request) {
	u, req, k, ok := s.session(w, r)
	if !ok {
		return
	}
	e, err := s.Assign(r.Context(), u, k, req.EE, req.Machine)
	respond(w, e, err)
}

func (s *Service) handleRemove(w http.ResponseWriter, r *http.Request) {
	u, req, k, ok := s.session(w, r)
	if !ok {
		return
	}
	err := s.Remove(r.Context(), u, k, req.EE)
	respond(w, map[string]bool{"ok": true}, err)
}

func (s *Service) handleReset(w http.ResponseWriter, r *http.Request) {
	u, _, k, ok := s.session(w, r)
	if !ok {
		return
	}
	err := s.Reset(r.Context(), u, k)
	respond(w, map[string]bool{"ok": true}, err)
}

func (s *Service) handleConfirm(w http.ResponseWriter, r *http.Request) {
	u, req, k, ok := s.session(w, r)
	if !ok {
		return
	}
	res, err := s.Confirm(r.Context(), u, k, req.Ack)
	if err == nil && len(res.NeedsAck) > 0 {
		// nothing was written: the page shows the warnings and sends the request again with ack=true
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Some lines have warnings that must be acknowledged.", "code": "needs_ack", "needs_ack": res.NeedsAck})
		return
	}
	respond(w, res, err)
}

// session decodes a POST body and the session key in it.
func (s *Service) session(w http.ResponseWriter, r *http.Request) (auth.User, sessionRequest, Key, bool) {
	u, _ := auth.UserFrom(r.Context())
	var req sessionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	if err := dec.Decode(&req); err != nil {
		respond[any](w, nil, newError(http.StatusBadRequest, "bad_request", "The request body is not valid JSON."))
		return u, req, Key{}, false
	}
	k, err := parseKey(req.Process, req.Date, req.Shift)
	if err != nil {
		respond[any](w, nil, err)
		return u, req, Key{}, false
	}
	return u, req, k, true
}

func parseKey(process, date, shift string) (Key, error) {
	d, err := time.Parse("2006-01-02", strings.TrimSpace(date))
	if err != nil {
		return Key{}, newError(http.StatusBadRequest, "bad_date", "The date must look like 2026-11-19.")
	}
	return Key{Process: strings.TrimSpace(process), Date: DateOnly(d), Shift: strings.ToUpper(strings.TrimSpace(shift))}, nil
}

func respond[T any](w http.ResponseWriter, v T, err error) {
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			writeJSON(w, e.Status, map[string]any{"error": e.Msg, "code": e.Code})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Internal error.", "code": "internal"})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
