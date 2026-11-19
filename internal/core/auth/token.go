// Package auth signs and verifies the short lived tokens dash_go uses for login.
//
// The Dash application (IE.py) knows who is logged in, the Go service does not. Dash therefore opens
// {base}/auth/sso?t=<token> with an "sso" token signed with the shared api.salt_3 secret. The service verifies
// the signature, consumes the one-time nonce and answers with its own signed "session" cookie.
//
// Token format: base64url(JSON claims) + "." + base64url(HMAC-SHA256(key, base64url(JSON claims))), where
// key = HMAC-SHA256(secret, "dash_go/<kind>/v1"). The Python side (LocalListV3.build_dash_go_sso_url) produces the
// same format, which a cross-language test vector in token_test.go pins down.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Token kinds.
const (
	KindSSO     = "sso"
	KindSession = "session"

	tokenVersion = 1
	clockSkew    = 60 * time.Second
)

// Errors returned by Parse.
var (
	ErrInvalid = errors.New("invalid token")
	ErrExpired = errors.New("token expired")
)

var b64 = base64.RawURLEncoding

// Claims is the signed payload.
type Claims struct {
	V    int    `json:"v"`
	K    string `json:"k"`
	EE   string `json:"ee"`   // employee code of the logged in user
	Name string `json:"name"` // display name, the value the Python code stores in update_user
	Iat  int64  `json:"iat"`
	Exp  int64  `json:"exp"`
	N    string `json:"n,omitempty"` // one-time nonce of an sso token
}

// Signer creates and verifies tokens.
type Signer struct {
	secret []byte
}

// NewSigner returns a Signer for the shared secret.
func NewSigner(secret string) (*Signer, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("api salt is empty: set api.salt_3 in the Dash config or DASH_GO_API_SALT")
	}
	return &Signer{secret: []byte(secret)}, nil
}

func (s *Signer) key(kind string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte("dash_go/" + kind + "/v1"))
	return mac.Sum(nil)
}

func (s *Signer) mac(kind, body string) []byte {
	mac := hmac.New(sha256.New, s.key(kind))
	mac.Write([]byte(body))
	return mac.Sum(nil)
}

// Sign returns the token for the claims. V is always set to the current token version.
func (s *Signer) Sign(c Claims) (string, error) {
	if c.K != KindSSO && c.K != KindSession {
		return "", errors.New("unknown token kind")
	}
	c.V = tokenVersion
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	body := b64.EncodeToString(payload)
	return body + "." + b64.EncodeToString(s.mac(c.K, body)), nil
}

// Parse verifies the signature, the kind and the validity period of a token.
func (s *Signer) Parse(token, kind string, now time.Time) (Claims, error) {
	body, sig, ok := strings.Cut(token, ".")
	if !ok || body == "" || sig == "" {
		return Claims{}, ErrInvalid
	}
	got, err := b64.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.mac(kind, body)) {
		return Claims{}, ErrInvalid
	}
	payload, err := b64.DecodeString(body)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Claims{}, ErrInvalid
	}
	if c.V != tokenVersion || c.K != kind || strings.TrimSpace(c.EE) == "" {
		return Claims{}, ErrInvalid
	}
	if now.Unix() >= c.Exp {
		return Claims{}, ErrExpired
	}
	if time.Unix(c.Iat, 0).After(now.Add(clockSkew)) {
		return Claims{}, ErrInvalid
	}
	return c, nil
}
