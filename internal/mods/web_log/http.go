package weblog

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/i18n"
	"dash_go/internal/core/shell"
)

//go:embed web i18n
var webFS embed.FS

func pageSpec(prefix string) shell.PageSpec {
	return shell.PageSpec{
		TitleKey:       "wl.title",
		AppKey:         "wl.title",
		AppIcon:        "weblog",
		RequireLogin:   false,
		HelpHref:       prefix + "/help",
		Guide:          true,
		Styles:         []string{prefix + "/static/app.css"},
		Scripts:        []string{prefix + "/static/vendor/echarts.common.min.js", prefix + "/static/app.js"},
		ClientPrefixes: []string{"wl.ui."},
	}
}

func helpPageSpec(prefix string) shell.PageSpec {
	return shell.PageSpec{
		TitleKey: "wl.help.title",
		AppKey:   "wl.title",
		AppIcon:  "weblog",
		HelpHref: prefix + "/help",
		Guide:    true,
	}
}

// SessionResolver identifies an optional signed-in user without rejecting anonymous visitors.
type SessionResolver func(http.ResponseWriter, *http.Request) (auth.User, bool)

// Routes mounts the page, user guide, static files, and JSON API below prefix (for example /dash_go/web_log).
// The trend chart and metadata endpoints are publicly readable; the real-time Redis log endpoint (/api/live)
// is strictly guarded by login (guard) AND active Auth."T_USER_ALL"."Type" = 'admin'.
func (s *Service) Routes(mux *http.ServeMux, prefix string, guard func(http.Handler) http.Handler, sessionFn SessionResolver, sh *shell.Shell) error {
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

	mux.Handle("GET "+prefix+"/api/meta", noStore(s.handleMeta(sessionFn)))
	mux.Handle("GET "+prefix+"/api/trend", noStore(s.handleTrend))
	mux.Handle("GET "+prefix+"/api/live", guard(noStore(s.handleLive)))
	return nil
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

func noStore(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h(w, r)
	}
}

func (s *Service) handleMeta(sessionFn SessionResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var u *auth.User
		if sessionFn != nil {
			if user, ok := sessionFn(w, r); ok {
				u = &user
			}
		} else if user, ok := auth.UserFrom(r.Context()); ok {
			u = &user
		}
		refresh := parseBoolParam(r.URL.Query().Get("refresh"))
		meta, err := s.Meta(r.Context(), u, refresh)
		respond(w, r, meta, err)
	}
}

func (s *Service) handleTrend(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := TrendQuery{
		Mode:        q.Get("mode"),
		From:        q.Get("from"),
		To:          q.Get("to"),
		App:         q.Get("app"),
		GroupBy:     q.Get("group_by"),
		Hosts:       splitMultiParam(q["host"]),
		Categories:  splitMultiParam(q["category"]),
		ExcludeIdle: parseBoolParam(q.Get("exclude_idle")),
		Refresh:     parseBoolParam(q.Get("refresh")),
	}
	res, err := s.Trend(r.Context(), req)
	respond(w, r, res, err)
}

func (s *Service) handleLive(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	limit := DefaultLiveLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			respond[any](w, r, nil, newError(http.StatusBadRequest, "bad_request"))
			return
		}
		limit = n
	}
	res, err := s.Live(r.Context(), u, limit)
	respond(w, r, res, err)
}

func parseBoolParam(raw string) bool {
	s := strings.TrimSpace(raw)
	return s == "1" || strings.EqualFold(s, "true") || strings.EqualFold(s, "yes")
}

func splitMultiParam(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if t := strings.TrimSpace(part); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
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
