// Package shell is the frame every dash_go page lives in: the header with the logo, the theme button, the language
// switch and the signed-in user, the colours of the light and dark theme, the login page and the landing page.
//
// The pages are rendered by the server (html/template), so the language, the user and the first theme are right
// before the script runs. The modules bring only their own content: they ask NewPage for a Page, give it a template
// that defines "content" and the scripts and styles of the module, and the shell does the rest. The shell files are
// served below {base}/_shell/.
package shell

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/i18n"
)

//go:embed web
var webFS embed.FS

// ModuleLink is a module on the landing page. The title and the description are catalog keys when the Key fields
// are set, so they follow the language.
type ModuleLink struct {
	Name           string
	Title          string
	TitleKey       string
	Icon           string
	Description    string
	DescriptionKey string
	Href           string
	HelpHref       string
}

// Options configure the shell.
type Options struct {
	Base             string // URL prefix without trailing slash, for example /dash_go
	Version          string // version of dash_go; it busts the browser cache of the shell files
	PhotoURLTemplate string // where the pictures of the employees are, {ee_code} is replaced
	DashHome         string // address of the Dash application; the login page links to it
	Auth             *auth.Service
	Modules          []ModuleLink
	Log              *slog.Logger
}

// Shell renders the pages of dash_go.
type Shell struct {
	opts    Options
	layout  *template.Template
	login   *Page
	landing *Page
	help    *Page
}

// New parses the templates of the shell.
func New(opts Options) (*Shell, error) {
	if opts.Auth == nil {
		return nil, fmt.Errorf("shell: the auth service is required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	layout, err := template.ParseFS(webFS, "web/templates/layout.html", "web/templates/logo.html")
	if err != nil {
		return nil, fmt.Errorf("shell: parse the layout: %w", err)
	}
	s := &Shell{opts: opts, layout: layout}
	if s.login, err = s.NewPage(PageSpec{TitleKey: "login.title", Scripts: []string{opts.Base + "/_shell/login.js"}, ClientPrefixes: []string{"login."}}, webFS, "web/templates/login.html"); err != nil {
		return nil, err
	}
	if s.landing, err = s.NewPage(PageSpec{
		TitleKey:    "ui.landing_title",
		HelpHref:    opts.Base + "/help",
		Guide:       true,
		TourAutoKey: "landing",
	}, webFS, "web/templates/landing.html"); err != nil {
		return nil, err
	}
	if s.help, err = s.NewPage(PageSpec{
		TitleKey: "ui.help_title",
		HelpHref: opts.Base + "/help",
		Guide:    true,
	}, webFS, "web/templates/help.html"); err != nil {
		return nil, err
	}
	return s, nil
}

// Mount registers the files of the shell, the language switch, the login page, the landing page and the user guide.
func (s *Shell) Mount(mux *http.ServeMux) {
	static, _ := fs.Sub(webFS, "web/assets")
	files := http.StripPrefix(s.opts.Base+"/_shell/", http.FileServerFS(static))
	mux.Handle("GET "+s.opts.Base+"/_shell/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET "+s.opts.Base+"/lang", s.handleLang)
	mux.HandleFunc("GET "+s.opts.Base+"/login", s.handleLogin)
	mux.HandleFunc("GET "+s.opts.Base+"/help", s.handleHelp)
	mux.HandleFunc("GET "+s.opts.Base+"/{$}", s.handleLanding)
}

// handleLang remembers the language the person picked and goes back to the page the switch was on.
func (s *Shell) handleLang(w http.ResponseWriter, r *http.Request) {
	if l, ok := i18n.Parse(r.URL.Query().Get("set")); ok {
		http.SetCookie(w, i18n.Cookie(l, s.opts.Base+"/", r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")))
	}
	http.Redirect(w, r, s.opts.Auth.SafeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
}

// loginData is what the login page needs.
type loginData struct {
	Password bool
	SSO      bool
	Next     string
	Notice   *notice
	DashHome string
}

// Any reports whether the page offers a way to sign in at all.
func (d loginData) Any() bool { return d.Password || d.SSO }

type notice struct {
	Kind string // "info", "warn" or "err"
	Text string
}

// handleLogin shows the login page. A person who is signed in already goes on to where they wanted to be.
func (s *Shell) handleLogin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	next := s.opts.Auth.SafeNext(q.Get("next"))
	if q.Get("signed_out") == "" {
		if _, ok := s.opts.Auth.Authenticate(w, r); ok {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
	}
	data := loginData{Password: s.opts.Auth.PasswordEnabled(), SSO: s.opts.Auth.SSOEnabled(), Next: next, DashHome: s.opts.DashHome}
	lang := i18n.LangOf(r.Context())
	say := func(kind, key string) { data.Notice = &notice{Kind: kind, Text: i18n.Std().T(lang, key, nil)} }
	switch {
	case q.Get("signed_out") != "":
		say("info", "login.signed_out")
	case q.Get("expired") != "":
		say("warn", "login.expired")
	default:
		switch q.Get("error") {
		case "link_invalid":
			say("err", "login.err_link_invalid")
		case "link_expired":
			say("err", "login.err_link_expired")
		case "link_used":
			say("err", "login.err_link_used")
		case "unavailable":
			say("err", "err.sso_unavailable")
		}
	}
	s.login.Render(w, r, http.StatusOK, data)
}

type landingData struct {
	Version string
	Modules []landingModule
}

type landingModule struct {
	Title, Description, Icon, Href, HelpHref string
}

func (s *Shell) landingViewData(lang i18n.Lang) landingData {
	d := landingData{Version: s.opts.Version}
	for _, m := range s.opts.Modules {
		lm := landingModule{Title: m.Title, Description: m.Description, Icon: m.Icon, Href: m.Href, HelpHref: m.HelpHref}
		if m.TitleKey != "" {
			lm.Title = i18n.Std().T(lang, m.TitleKey, nil)
		}
		if m.DescriptionKey != "" {
			lm.Description = i18n.Std().T(lang, m.DescriptionKey, nil)
		}
		d.Modules = append(d.Modules, lm)
	}
	return d
}

func (s *Shell) handleLanding(w http.ResponseWriter, r *http.Request) {
	s.landing.Render(w, r, http.StatusOK, s.landingViewData(i18n.LangOf(r.Context())))
}

func (s *Shell) handleHelp(w http.ResponseWriter, r *http.Request) {
	s.help.Render(w, r, http.StatusOK, s.landingViewData(i18n.LangOf(r.Context())))
}

// PageSpec describes a page.
type PageSpec struct {
	TitleKey string // catalog key of the title of the browser tab
	AppKey   string // catalog key of the name shown next to the logo; empty for none
	AppIcon  string // optional symbol name in icons.svg shown next to AppKey in the header
	// Styles and Scripts are the URLs of the stylesheets and scripts of the page, after those of the shell.
	Styles  []string
	Scripts []string
	// ClientPrefixes are the key prefixes of the catalog the script of the page needs ("ui." is always sent). Texts
	// whose key starts with anything else stay on the server.
	ClientPrefixes []string
	// RequireLogin sends a visitor without a session to the login page.
	RequireLogin bool
	// HelpHref is the URL of the user guide for this page (opened in a new tab from the header Docs button).
	// Leave empty to hide the Docs button.
	HelpHref string
	// Guide enables the header Guide button (step-by-step tour of the page) and loads tour.js.
	Guide bool
	// TourAutoKey, when non-empty, starts the page tour automatically once on the visitor's first visit
	// (remembered in localStorage). Pages whose main content loads asynchronously leave this empty and call
	// dashGo.tour.autoStart(key) once the screen is ready.
	TourAutoKey string
}

// Page is a page of the shell with the template of its content.
type Page struct {
	sh   *Shell
	spec PageSpec
	tmpl *template.Template
}

// NewPage parses the files of a page. They must define the template "content" (the body of the page). Inside it
// the dot is the view of the shell: .T "key" translates, .Data is what Render was given, .Icon "name" is an icon
// of the sprite, .Base is the URL prefix of the service.
func (s *Shell) NewPage(spec PageSpec, fsys fs.FS, files ...string) (*Page, error) {
	t, err := s.layout.Clone()
	if err != nil {
		return nil, err
	}
	if t, err = t.ParseFS(fsys, files...); err != nil {
		return nil, fmt.Errorf("shell: parse %v: %w", files, err)
	}
	if t.Lookup("content") == nil {
		return nil, fmt.Errorf("shell: %v must define the template \"content\"", files)
	}
	return &Page{sh: s, spec: spec, tmpl: t}, nil
}

// Handler serves the page with the data that dataFn returns for the request (nil for none).
func (p *Page) Handler(dataFn func(*http.Request) any) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.spec.RequireLogin {
			u, ok := p.sh.opts.Auth.Authenticate(w, r)
			if !ok {
				http.Redirect(w, r, p.sh.opts.Auth.LoginPath(r.URL.Path), http.StatusSeeOther)
				return
			}
			r = r.WithContext(auth.WithUser(r.Context(), u))
		}
		var data any
		if dataFn != nil {
			data = dataFn(r)
		}
		p.Render(w, r, http.StatusOK, data)
	})
}

// Render writes the page. The answer is never cached: it shows who is signed in.
func (p *Page) Render(w http.ResponseWriter, r *http.Request, status int, data any) {
	v := p.sh.newView(w, r, p.spec, data)
	var buf bytes.Buffer
	if err := p.tmpl.ExecuteTemplate(&buf, "layout", v); err != nil {
		p.sh.opts.Log.Error("render a page", "path", r.URL.Path, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// view is the dot of every template.
type view struct {
	Lang          string
	IsZh          bool
	Base          string
	AssetVer      string
	Title         string
	App           string
	AppIcon       string
	HelpHref      string
	Guide         bool
	TourAutoKey   string
	Styles        []string
	Scripts       []string
	I18n          map[string]string
	PhotoTemplate string
	User          *userView
	ShowSignIn    bool
	LoginHref     string
	Data          any

	lang       i18n.Lang
	requestURI string
}

type userView struct {
	EE, Name, Initials string
}

type iconRef struct {
	Base, Name string
}

func (s *Shell) newView(w http.ResponseWriter, r *http.Request, spec PageSpec, data any) *view {
	lang := i18n.LangOf(r.Context())
	std := i18n.Std()
	v := &view{
		Lang: string(lang), IsZh: lang == i18n.ZH, Base: s.opts.Base, AssetVer: s.opts.Version,
		Title:    std.T(lang, spec.TitleKey, nil) + " · CIM EDGE",
		HelpHref: spec.HelpHref, Guide: spec.Guide, TourAutoKey: spec.TourAutoKey,
		Styles: spec.Styles, Scripts: spec.Scripts, PhotoTemplate: s.opts.PhotoURLTemplate, Data: data,
		lang: lang, requestURI: r.URL.RequestURI(),
		I18n: std.Subset(lang, append([]string{"ui."}, spec.ClientPrefixes...)...),
	}
	if spec.AppKey != "" {
		v.App = std.T(lang, spec.AppKey, nil)
		v.AppIcon = spec.AppIcon
	}
	onLogin := strings.HasSuffix(r.URL.Path, "/login")
	if u, ok := auth.UserFrom(r.Context()); ok {
		v.User = &userView{EE: u.EE, Name: u.Name, Initials: initials(u.Name)}
	} else if !onLogin {
		if u, ok := s.opts.Auth.Authenticate(w, r); ok {
			v.User = &userView{EE: u.EE, Name: u.Name, Initials: initials(u.Name)}
		}
	} else if u, ok := s.opts.Auth.Session(r); ok {
		v.User = &userView{EE: u.EE, Name: u.Name, Initials: initials(u.Name)}
	}
	v.ShowSignIn = !onLogin
	v.LoginHref = s.opts.Auth.LoginPath(s.opts.Auth.SafeNext(r.URL.Path))
	return v
}

// Tour emits the data-tour attributes that tour.js reads for one step of the page guide: the step key and its
// translated title (<key>.title) and text (<key>.text). When the page has no guide it returns empty, so pages like
// the login page carry no tour markup.
func (v *view) Tour(key string) template.HTMLAttr {
	if !v.Guide {
		return ""
	}
	std := i18n.Std()
	return template.HTMLAttr(` data-tour="` + template.HTMLEscapeString(key) +
		`" data-tour-title="` + template.HTMLEscapeString(std.T(v.lang, key+".title", nil)) +
		`" data-tour-text="` + template.HTMLEscapeString(std.T(v.lang, key+".text", nil)) + `"`)
}

// T translates a key into the language of the request. The optional arguments are name and value pairs for the
// {name} placeholders: {{.T "ui.landing_version" "version" .Data.Version}}.
func (v *view) T(key string, kv ...any) string {
	var p i18n.P
	if len(kv) > 0 {
		p = i18n.P{}
		for i := 0; i+1 < len(kv); i += 2 {
			if name, ok := kv[i].(string); ok {
				p[name] = kv[i+1]
			}
		}
	}
	return i18n.Std().T(v.lang, key, p)
}

// Icon names a symbol of the sprite for the template "icon".
func (v *view) Icon(name string) iconRef { return iconRef{Base: v.Base, Name: name} }

// LangHref is the link of the language switch: it sets the language and comes back to this page.
func (v *view) LangHref(code string) string {
	return v.Base + "/lang?set=" + url.QueryEscape(code) + "&next=" + url.QueryEscape(v.requestURI)
}

// initials are the first letters of the first two words of a name.
func initials(name string) string {
	var out []rune
	for _, w := range strings.Fields(name) {
		out = append(out, []rune(w)[0])
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return strings.ToUpper(string(out))
}
