package i18n

import (
	"context"
	"net/http"
	"strings"
)

// CookieName is the cookie the language switch sets. It is limited to the base path of the service.
const CookieName = "dash_go_lang"

type ctxKey struct{}

// WithLang stores the language of a request in its context, so code below the HTTP layer can translate.
func WithLang(ctx context.Context, l Lang) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// LangOf returns the language stored by WithLang, English when there is none.
func LangOf(ctx context.Context) Lang {
	if l, ok := ctx.Value(ctxKey{}).(Lang); ok {
		return l
	}
	return EN
}

// T translates key into the language of the context.
func T(ctx context.Context, key string, p P) string { return std.T(LangOf(ctx), key, p) }

// FromRequest picks the language: the cookie wins, then the Accept-Language header, then English.
func FromRequest(r *http.Request) Lang {
	if c, err := r.Cookie(CookieName); err == nil {
		if l, ok := Parse(c.Value); ok {
			return l
		}
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case tag == "zh" || strings.HasPrefix(tag, "zh-"):
			return ZH
		case tag == "en" || strings.HasPrefix(tag, "en-"):
			return EN
		}
	}
	return EN
}

// Middleware puts the language of every request into its context.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithLang(r.Context(), FromRequest(r))))
	})
}

// Cookie is the cookie that remembers the choice of the language switch for a year.
func Cookie(l Lang, path string, secure bool) *http.Cookie {
	return &http.Cookie{Name: CookieName, Value: string(l), Path: path, MaxAge: 365 * 24 * 3600, Secure: secure, SameSite: http.SameSiteLaxMode}
}
