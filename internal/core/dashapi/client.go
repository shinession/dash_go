// Package dashapi is the client of the Python dash backend (appV2.py, route /api/user/...).
//
// Permissions of the HC processes live in the Python code (check_daily_attn_permission reads the name based
// AuthGroup lists and the MQPD_AUTH_LIST of the config). The Go service does not copy that logic; it asks the
// backend, so there is one source of truth while the migration is under way.
package dashapi

import (
	"bytes"
	"context"
	"crypto/md5" // #nosec G501 -- the Python API defines this request signature (md5 of salt and hour); it is not a password hash
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Errors returned by the client.
var (
	ErrUnauthorized   = errors.New("dash api rejected the request signature")
	ErrNotAllowed     = errors.New("user is unknown or deactivated")
	ErrBadCredentials = errors.New("account or password is wrong, or the account is deactivated")
	// ErrFunctionUnknown means the backend answered 204, which is what /user answers to a function it does not have:
	// it is older than dash_go, or it is not the backend that was meant.
	ErrFunctionUnknown = errors.New("dash api does not know this function")
)

// Profile is the answer of the hc_user_processes function.
type Profile struct {
	Name      string   `json:"name"`
	EECode    string   `json:"ee_code"`
	Type      string   `json:"type,omitempty"`
	Processes []string `json:"processes"`
}

// IsAdmin reports whether the Dash user profile has Type "admin" (case-insensitive).
// An older backend that omits "type" defaults safely to non-admin.
func (p Profile) IsAdmin() bool {
	return strings.EqualFold(strings.TrimSpace(p.Type), "admin")
}

// Client calls the Python API.
type Client struct {
	base string
	root string // base without credentials, for logs and messages
	salt string
	hc   *http.Client
	now  func() time.Time
}

// New returns a client. baseURL is the API root including /api, for example http://localhost/api.
func New(baseURL, salt string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	base := strings.TrimRight(baseURL, "/")
	return &Client{base: base, root: withoutCredentials(base), salt: salt, hc: &http.Client{Timeout: timeout}, now: time.Now}
}

// Root is the API root this client asks, as it may be written to a log. It tells which backend answered: a development
// machine whose api_server points at production reaches a backend that is older than the code that asks it.
func (c *Client) Root() string { return c.root }

func withoutCredentials(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(address that cannot be read)"
	}
	u.User = nil
	return u.String()
}

// Signature returns the Authorization value the Python endpoint expects: md5("<salt>_<yymmddHH>") with the
// local time of the server (the Python code uses the naive local time as well).
func Signature(salt string, t time.Time) string {
	sum := md5.Sum([]byte(salt + "_" + t.Format("06010215"))) // #nosec G401
	return hex.EncodeToString(sum[:])
}

// HCProfile returns the display name and the HC processes the user may work on. The user is an employee code
// or a name.
func (c *Client) HCProfile(ctx context.Context, user string) (Profile, error) {
	var p Profile
	err := withRetry(ctx, func() (bool, error) {
		status, raw, retry, err := c.post(ctx, map[string]string{"func": "hc_user_processes", "user": user})
		if err != nil {
			return retry, err
		}
		switch status {
		case http.StatusOK:
			if err := json.Unmarshal(raw, &p); err != nil {
				return false, fmt.Errorf("dash api answer is not valid JSON: %w", err)
			}
			return false, nil
		case http.StatusAccepted:
			return false, ErrNotAllowed
		default:
			return c.refusal(status)
		}
	})
	return p, err
}

// VerifyUser checks an account and its password with the backend (func verify_user): nil when they are right and
// the account is active. The backend answers 202 for an unknown account, a wrong password and a deactivated
// account alike, so ErrBadCredentials does not tell which one it was. The user is an employee code, a name or an
// e-mail address. Neither the user nor the password is logged here or kept.
func (c *Client) VerifyUser(ctx context.Context, user, password string) error {
	return withRetry(ctx, func() (bool, error) {
		status, _, retry, err := c.post(ctx, map[string]string{"func": "verify_user", "user": user, "password": password})
		if err != nil {
			return retry, err
		}
		switch status {
		case http.StatusOK:
			return false, nil
		case http.StatusAccepted:
			return false, ErrBadCredentials
		default:
			return c.refusal(status)
		}
	})
}

// Check finds out whether this host can use the backend, with a question that reads and changes nothing: the function
// hc_user_processes without a user. A backend that can be reached, accepts the signature of this host and has the
// function answers 202 ("user is unknown or deactivated"), which is the expected answer here and counts as success.
// The failures are the ones people would meet: ErrUnauthorized (api.salt_3 or the clock of the two hosts differ),
// ErrFunctionUnknown (an older backend, or not the one that was meant) and an address that cannot be reached.
func (c *Client) Check(ctx context.Context) error {
	return withRetry(ctx, func() (bool, error) {
		status, _, retry, err := c.post(ctx, map[string]string{"func": "hc_user_processes"})
		if err != nil {
			return retry, err
		}
		switch status {
		case http.StatusOK, http.StatusAccepted:
			return false, nil
		default:
			return c.refusal(status)
		}
	})
}

// withRetry runs one request of the API. The second attempt covers a signature that expired at the hour change or
// a burst limit; a definite answer is never repeated.
func withRetry(ctx context.Context, attempt func() (retry bool, err error)) error {
	var lastErr error
	for i := 0; i < 2; i++ {
		retry, err := attempt()
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return lastErr
}

// post sends one JSON request to /user/hc_scan. retry is true for a failure that may be gone a moment later.
func (c *Client) post(ctx context.Context, payload map[string]string) (status int, raw []byte, retry bool, err error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/user/hc_scan", bytes.NewReader(body))
	if err != nil {
		return 0, nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", Signature(c.salt, c.now()))
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, true, fmt.Errorf("dash api request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, raw, false, nil
}

// refusal maps a status that is neither success nor the definite "no" of the function to an error.
func (c *Client) refusal(status int) (bool, error) {
	switch status {
	case http.StatusUnauthorized:
		return true, fmt.Errorf("%w: %s", ErrUnauthorized, c.root)
	case http.StatusForbidden: // "Too many requests": the endpoint refuses calls closer than 10 ms
		return true, fmt.Errorf("dash api %s is rate limiting this host (status 403)", c.root)
	case http.StatusNoContent:
		return false, fmt.Errorf("%w: %s answered status 204; it is older than dash_go or is not the backend that was meant (check dash_api_url and api_server)", ErrFunctionUnknown, c.root)
	default:
		return false, fmt.Errorf("dash api %s answered status %d", c.root, status)
	}
}
