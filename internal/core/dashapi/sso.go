package dashapi

import (
	"bytes"
	"context"
	"crypto/md5" // #nosec G501 -- the Python API defines this request signature; it is not a password hash
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"dash_go/internal/core/pyobj"
)

// Errors of the single sign-on side.
var (
	// ErrSSOPending means the sign-on service has not recorded anything for the token (yet).
	ErrSSOPending = errors.New("single sign-on has not answered yet")
	// ErrSSOUnreadable means the answer is not the result of the sign-on service.
	ErrSSOUnreadable = errors.New("the single sign-on result cannot be read")
	// ErrSSOUnavailable means the sign-on side could not be asked.
	ErrSSOUnavailable = errors.New("the single sign-on service cannot be reached")
)

// SSOResult is what the Windows single sign-on service recorded for one login token, after the browser that opened
// its page was identified by its Windows account.
type SSOResult struct {
	// Status is "ok" when the Windows user was identified, "no_user" when the browser sent none.
	Status string
	// ECode is the employee code, the part of the Windows account name after the domain.
	ECode string
	// WindowsUser is the account as Windows names it (DOMAIN\user). It is for the log only.
	WindowsUser string
	// IP is the address of the browser as the sign-on service saw it.
	IP string
	// At is when the service recorded the result; zero when it did not say.
	At time.Time
}

var ssoToken = regexp.MustCompile(`^[a-z0-9]{16,40}$`) // the shape the Python function accepts

const maxSSOText = 254 // runes of a text the sign-on service stored

// SSOClient reads sign-on results from the backend that sits next to the sign-on service (route
// /api/sso_result/<anything> of appV2.py, the one the Dash application uses). Reading a result deletes it there:
// a result can be used once.
type SSOClient struct {
	origin string // http://host[:port]
	salt   string // api.salt_2
	hc     *http.Client
	now    func() time.Time
}

// NewSSO returns a client for the sign-on service at host (a host name, host:port or http(s)://host[:port]).
func NewSSO(host, salt2 string, timeout time.Duration) (*SSOClient, error) {
	origin, err := SSOOrigin(host)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(salt2) == "" {
		return nil, errors.New("api.salt_2 is empty: the sign-on service answers only requests signed with it")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &SSOClient{origin: origin, salt: salt2, hc: &http.Client{Timeout: timeout}, now: time.Now}, nil
}

// SSOOrigin turns the sso_server setting into scheme://host[:port]. The scheme defaults to http.
func SSOOrigin(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", errors.New("the sign-on server is empty")
	}
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	u, err := url.Parse(host)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("the sign-on server %q is not a host name, host:port or http(s)://host[:port]", host)
	}
	return u.Scheme + "://" + u.Host, nil
}

// LoginURL is the page of the sign-on service the browser opens for a token.
func (c *SSOClient) LoginURL(token string) string {
	return c.origin + "/sso/api/login?token=" + url.QueryEscape(token)
}

// SSOSignature returns the Authorization value of the sign-on endpoint: md5(salt_2) + "_" + md5(yymmddHH), with the
// local time of the server like the Python code.
func SSOSignature(salt2 string, t time.Time) string {
	a := md5.Sum([]byte(salt2))                // #nosec G401
	b := md5.Sum([]byte(t.Format("06010215"))) // #nosec G401
	return hex.EncodeToString(a[:]) + "_" + hex.EncodeToString(b[:])
}

// Result asks for the result of a token. It returns ErrSSOPending while there is none, ErrSSOUnreadable when the
// answer is not a result, and ErrSSOUnavailable when the service cannot be asked.
func (c *SSOClient) Result(ctx context.Context, token string) (SSOResult, error) {
	if !ssoToken.MatchString(token) {
		return SSOResult{}, fmt.Errorf("%w: the token has the wrong shape", ErrSSOUnreadable)
	}
	var out SSOResult
	err := withRetry(ctx, func() (bool, error) {
		res, retry, err := c.resultOnce(ctx, token)
		out = res
		return retry, err
	})
	return out, err
}

func (c *SSOClient) resultOnce(ctx context.Context, token string) (SSOResult, bool, error) {
	body := pyobj.EncodeStrings(map[string]string{"method": "get", "func": "sso_result", "sso_token": token})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+"/api/sso_result/anything", bytes.NewReader(body))
	if err != nil {
		return SSOResult{}, false, err
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", SSOSignature(c.salt, c.now()))
	resp, err := c.hc.Do(req)
	if err != nil {
		return SSOResult{}, true, fmt.Errorf("%w: %v", ErrSSOUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10+1))
	if err != nil {
		return SSOResult{}, true, fmt.Errorf("%w: %v", ErrSSOUnavailable, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized: // the signature of the last hour: asked again once
		return SSOResult{}, true, fmt.Errorf("%w: the sign-on endpoint rejected the request signature", ErrSSOUnavailable)
	case http.StatusForbidden:
		return SSOResult{}, true, fmt.Errorf("%w: the sign-on endpoint is rate limiting this host", ErrSSOUnavailable)
	default:
		return SSOResult{}, false, fmt.Errorf("%w: the sign-on endpoint answered status %d", ErrSSOUnavailable, resp.StatusCode)
	}
	if len(raw) > 64<<10 {
		return SSOResult{}, false, fmt.Errorf("%w: the answer is too large", ErrSSOUnreadable)
	}
	res, err := parseSSOResult(raw)
	return res, false, err
}

func parseSSOResult(raw []byte) (SSOResult, error) {
	v, err := pyobj.Decode(raw)
	if err != nil {
		return SSOResult{}, fmt.Errorf("%w: %v", ErrSSOUnreadable, err)
	}
	d, ok := v.(map[string]any)
	if !ok {
		return SSOResult{}, fmt.Errorf("%w: the result is not a dict", ErrSSOUnreadable)
	}
	var res SSOResult
	for key, dst := range map[string]*string{"status": &res.Status, "ee_code": &res.ECode, "windows_user": &res.WindowsUser, "ip": &res.IP} {
		val, present := d[key]
		if !present || val == nil {
			continue
		}
		text, ok := val.(string)
		if !ok || !plainText(text) {
			return SSOResult{}, fmt.Errorf("%w: %s is not a plain text", ErrSSOUnreadable, key)
		}
		*dst = strings.TrimSpace(text)
	}
	if res.Status == "" {
		return SSOResult{}, fmt.Errorf("%w: the result has no status", ErrSSOUnreadable)
	}
	if ts, present := d["ts"]; present && ts != nil {
		sec, ok := pyobj.Float(ts)
		if !ok || sec < 0 || sec > 253402300799 {
			return SSOResult{}, fmt.Errorf("%w: ts is not a time", ErrSSOUnreadable)
		}
		whole, frac := math.Modf(sec)
		res.At = time.Unix(int64(whole), int64(frac*1e9)).UTC()
	}
	if res.Status == "pending" {
		return SSOResult{}, ErrSSOPending
	}
	return res, nil
}

// plainText reports whether a text is short, valid UTF-8 and free of control characters.
func plainText(s string) bool {
	if len(s) > 4*maxSSOText || utf8.RuneCountInString(s) > maxSSOText || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
