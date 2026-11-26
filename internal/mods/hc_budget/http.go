package hcbudget

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/i18n"
	"dash_go/internal/core/shell"
)

//go:embed web i18n
var webFS embed.FS

func pageSpec(prefix string) shell.PageSpec {
	return shell.PageSpec{
		TitleKey:       "hb.title",
		AppKey:         "hb.title",
		AppIcon:        "budget",
		RequireLogin:   true,
		HelpHref:       prefix + "/help",
		Guide:          true,
		Styles:         []string{prefix + "/static/app.css"},
		Scripts:        []string{prefix + "/static/app.js"},
		ClientPrefixes: []string{"hb.ui."},
	}
}

func helpPageSpec(prefix string) shell.PageSpec {
	return shell.PageSpec{
		TitleKey: "hb.help.title",
		AppKey:   "hb.title",
		AppIcon:  "budget",
		HelpHref: prefix + "/help",
		Guide:    true,
	}
}

// Routes mounts the page, user guide, static files, and JSON API below prefix (for example /dash_go/hc_budget).
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
	api("GET", "table", s.handleTable)
	api("POST", "save", s.handleSave)
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

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	me, err := s.Me(r.Context(), u)
	respond(w, r, me, err)
}

func (s *Service) handleTable(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	q := r.URL.Query()
	inc := q.Get("include_hidden")
	includeHidden := inc == "1" || strings.EqualFold(inc, "true")
	table, err := s.Table(r.Context(), u, q.Get("process"), includeHidden)
	respond(w, r, table, err)
}

func (s *Service) handleSave(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	var req SaveRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		respond[any](w, r, nil, newError(http.StatusBadRequest, "bad_request"))
		return
	}
	res, err := s.Save(r.Context(), u, req)
	respond(w, r, res, err)
}

func respond[T any](w http.ResponseWriter, r *http.Request, v T, err error) {
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			args := make(i18n.P, len(e.Args))
			for k, val := range e.Args {
				args[k] = val
			}
			if step, ok := args["step"].(string); ok {
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
