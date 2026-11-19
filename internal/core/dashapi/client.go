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
	"strings"
	"time"
)

// Errors returned by the client.
var (
	ErrUnauthorized = errors.New("dash api rejected the request signature")
	ErrNotAllowed   = errors.New("user is unknown or deactivated")
)

// Profile is the answer of the hc_user_processes function.
type Profile struct {
	Name      string   `json:"name"`
	EECode    string   `json:"ee_code"`
	Processes []string `json:"processes"`
}

// Client calls the Python API.
type Client struct {
	base string
	salt string
	hc   *http.Client
	now  func() time.Time
}

// New returns a client. baseURL is the API root including /api, for example http://localhost/api.
func New(baseURL, salt string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{base: strings.TrimRight(baseURL, "/"), salt: salt, hc: &http.Client{Timeout: timeout}, now: time.Now}
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
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ { // the second attempt covers a signature that expired at the hour change or a burst limit
		p, retry, err := c.hcProfileOnce(ctx, user)
		if err == nil {
			return p, nil
		}
		lastErr = err
		if !retry {
			break
		}
		select {
		case <-ctx.Done():
			return Profile{}, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return Profile{}, lastErr
}

func (c *Client) hcProfileOnce(ctx context.Context, user string) (Profile, bool, error) {
	body, _ := json.Marshal(map[string]string{"func": "hc_user_processes", "user": user})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/user/hc_scan", bytes.NewReader(body))
	if err != nil {
		return Profile{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", Signature(c.salt, c.now()))
	resp, err := c.hc.Do(req)
	if err != nil {
		return Profile{}, true, fmt.Errorf("dash api request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
		var p Profile
		if err := json.Unmarshal(raw, &p); err != nil {
			return Profile{}, false, fmt.Errorf("dash api answer is not valid JSON: %w", err)
		}
		return p, false, nil
	case http.StatusAccepted:
		return Profile{}, false, ErrNotAllowed
	case http.StatusUnauthorized:
		return Profile{}, true, ErrUnauthorized
	case http.StatusForbidden: // "Too many requests": the endpoint refuses calls closer than 10 ms
		return Profile{}, true, fmt.Errorf("dash api is rate limiting this host (status 403)")
	default:
		return Profile{}, false, fmt.Errorf("dash api answered status %d", resp.StatusCode)
	}
}
