package dashapi

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dash_go/internal/core/pyobj"
)

// Made by Python 3.12: zlib.compress(pickle.dumps({'status': 'ok', 'ee_code': 'AB12C34', 'windows_user': 'CO\AB12C34',
// 'ip': '10.1.2.3', 'ts': 1700000123.456})), the way pickle_dumps() of the dash application makes it.
const pythonSSOResult = "789c6b60999ac80001b553347ad88a4b124b4a8ba7f430e5674fe9614f4d8d4fce4f4905b21c9d0c8d9c8d4da6f4f09467e6a5e49717c79716a7164de9e172f68f81cb3165164ce9e13034d033d433d23306f24b8aa7b83bde0cf91377575fba540f006e8e249f"

// Made by Python 3.12: zlib.compress(pickle.dumps({'status': 'pending'})).
const pythonSSOPending = "789c6b60992ace0001b5537ad88a4b124b4a8ba7f4b017a4e6a564e6a54f29d603009bff0ab9"

const goodToken = "abcdefghijklmnopqrstuvwx"

func fakeSignOn(t *testing.T, reply func(w http.ResponseWriter, token string)) (*SSOClient, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/sso_result/anything" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != SSOSignature("salt-two", time.Now()) {
			t.Errorf("wrong signature %q", got)
		}
		if r.Header.Get("Content-Type") != "text/plain" {
			t.Errorf("the Python endpoint only reads text/plain, got %q", r.Header.Get("Content-Type"))
		}
		raw, _ := io.ReadAll(r.Body)
		v, err := pyobj.Decode(raw)
		if err != nil {
			t.Errorf("the body is not a zlib pickle: %v", err)
			return
		}
		d := v.(map[string]any)
		if d["method"] != "get" || d["func"] != "sso_result" {
			t.Errorf("unexpected request dict %v", d)
		}
		reply(w, d["sso_token"].(string))
	}))
	t.Cleanup(srv.Close)
	c, err := NewSSO(srv.URL, "salt-two", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c, calls
}

func write(t *testing.T, w http.ResponseWriter, status int, hexBody string) {
	b, err := hex.DecodeString(hexBody)
	if err != nil {
		t.Fatal(err)
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func TestSSOSignatureMatchesPython(t *testing.T) {
	// f"{hashlib.md5(b'salt-two').hexdigest()}_{hashlib.md5(b'26112008').hexdigest()}" computed in Python
	got := SSOSignature("salt-two", time.Date(2026, 11, 20, 8, 15, 0, 0, time.Local))
	if got != "6fc50c8cab431b1140661f2ad82add93_fc0cb9a196b102591553a1c00656e2d4" {
		t.Fatalf("signature = %s", got)
	}
}

func TestSSOResultReadsWhatPythonWrote(t *testing.T) {
	c, _ := fakeSignOn(t, func(w http.ResponseWriter, token string) {
		if token != goodToken {
			t.Errorf("token %q", token)
		}
		write(t, w, 200, pythonSSOResult)
	})
	res, err := c.Result(context.Background(), goodToken)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "ok" || res.ECode != "AB12C34" || res.WindowsUser != `CO\AB12C34` || res.IP != "10.1.2.3" {
		t.Errorf("%+v", res)
	}
	if res.At.UTC().Unix() != 1700000123 {
		t.Errorf("ts: %v", res.At)
	}
}

func TestSSOResultPending(t *testing.T) {
	c, _ := fakeSignOn(t, func(w http.ResponseWriter, _ string) { write(t, w, 200, pythonSSOPending) })
	if _, err := c.Result(context.Background(), goodToken); !errors.Is(err, ErrSSOPending) {
		t.Errorf("want ErrSSOPending, got %v", err)
	}
}

func TestSSOResultRefusesWhatIsNotAResult(t *testing.T) {
	cases := map[string][]byte{
		"not a pickle":  []byte("<html>proxy error</html>"),
		"no status":     pyobj.EncodeStrings(map[string]string{"ee_code": "AB12C34"}),
		"control chars": pyobj.EncodeStrings(map[string]string{"status": "ok", "ee_code": "AB\n12"}),
		"long text":     pyobj.EncodeStrings(map[string]string{"status": "ok", "ee_code": strings.Repeat("A", 400)}),
	}
	for name, body := range cases {
		body := body
		c, _ := fakeSignOn(t, func(w http.ResponseWriter, _ string) { w.WriteHeader(200); _, _ = w.Write(body) })
		if _, err := c.Result(context.Background(), goodToken); !errors.Is(err, ErrSSOUnreadable) {
			t.Errorf("%s: want ErrSSOUnreadable, got %v", name, err)
		}
	}
}

func TestSSOResultWrongTypedFieldsAreUnreadable(t *testing.T) {
	pickled := func(raw string) []byte {
		var b bytes.Buffer
		w := zlib.NewWriter(&b)
		_, _ = w.Write([]byte(raw))
		_ = w.Close()
		return b.Bytes()
	}
	cases := map[string][]byte{
		// {'status': 'ok', 'ee_code': 5}: a number where the employee code should be text
		"ee_code is a number": pickled("\x80\x02}(X\x06\x00\x00\x00statusX\x02\x00\x00\x00okX\x07\x00\x00\x00ee_codeK\x05u."),
		// {'status': 'ok', 'ts': 'yesterday'}: a ts that is text
		"ts is text": pyobj.EncodeStrings(map[string]string{"status": "ok", "ts": "yesterday"}),
		// a list instead of a dict
		"not a dict": pickled("\x80\x02]."),
	}
	for name, raw := range cases {
		if _, err := parseSSOResult(raw); !errors.Is(err, ErrSSOUnreadable) {
			t.Errorf("%s: want ErrSSOUnreadable, got %v", name, err)
		}
	}
	good, _ := hex.DecodeString(pythonSSOResult)
	if _, err := parseSSOResult(good); err != nil {
		t.Fatalf("the good vector must parse: %v", err)
	}
}

func TestSSOResultRefusesABadTokenWithoutAskingTheService(t *testing.T) {
	c, calls := fakeSignOn(t, func(w http.ResponseWriter, _ string) { write(t, w, 200, pythonSSOResult) })
	for _, token := range []string{"", "short", "UPPERCASEUPPERCASEUPPERCASE", "has-dash-has-dash-has-dash", strings.Repeat("a", 41)} {
		if _, err := c.Result(context.Background(), token); !errors.Is(err, ErrSSOUnreadable) {
			t.Errorf("token %q: %v", token, err)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("the service was asked %d times about tokens it would refuse", calls.Load())
	}
}

func TestSSOResultServiceErrors(t *testing.T) {
	status := atomic.Int32{}
	status.Store(401)
	c, calls := fakeSignOn(t, func(w http.ResponseWriter, _ string) { w.WriteHeader(int(status.Load())) })

	if _, err := c.Result(context.Background(), goodToken); !errors.Is(err, ErrSSOUnavailable) {
		t.Errorf("401: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("a rejected signature is retried once (hour change), calls=%d", calls.Load())
	}
	calls.Store(0)
	status.Store(500)
	if _, err := c.Result(context.Background(), goodToken); !errors.Is(err, ErrSSOUnavailable) || calls.Load() != 1 {
		t.Errorf("500 must fail without retry: %v calls=%d", err, calls.Load())
	}
}

func TestSSOServiceDownIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, err := NewSSO(url, "salt-two", 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Result(context.Background(), goodToken); !errors.Is(err, ErrSSOUnavailable) {
		t.Errorf("got %v", err)
	}
}

func TestSSOOrigin(t *testing.T) {
	ok := map[string]string{
		"prod_eng.co.murata.local":  "http://prod_eng.co.murata.local",
		"sso.example.local:8080":    "http://sso.example.local:8080",
		"https://sso.example.local": "https://sso.example.local",
		"http://10.0.0.5:9000/":     "http://10.0.0.5:9000",
		" host.example ":            "http://host.example",
	}
	for in, want := range ok {
		got, err := SSOOrigin(in)
		if err != nil || got != want {
			t.Errorf("SSOOrigin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "  ", "ftp://host", "http://user:pw@host", "http://host/path", "http://host?x=1", "http://host#frag", "http://"} {
		if got, err := SSOOrigin(in); err == nil {
			t.Errorf("SSOOrigin(%q) = %q, want an error", in, got)
		}
	}
}

func TestLoginURLEscapesTheToken(t *testing.T) {
	c, err := NewSSO("sso.example.local", "s", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.LoginURL("abc def&x=1"); got != "http://sso.example.local/sso/api/login?token=abc+def%26x%3D1" {
		t.Errorf("%s", got)
	}
}

func TestNewSSORequiresTheSalt(t *testing.T) {
	if _, err := NewSSO("sso.example.local", "  ", 0); err == nil {
		t.Error("an empty api.salt_2 must be refused at start, not at the first login")
	}
}

func TestVerifyUser(t *testing.T) {
	status := atomic.Int32{}
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/hc_scan" || r.Header.Get("Authorization") != Signature("salt", time.Now()) {
			t.Errorf("unexpected request %s signature ok=%v", r.URL.Path, r.Header.Get("Authorization") == Signature("salt", time.Now()))
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"Response":"x"}`))
	}))
	defer srv.Close()
	c := New(srv.URL+"/api", "salt", time.Second)

	status.Store(200)
	if err := c.VerifyUser(context.Background(), "AB12C34", `p"w\`); err != nil {
		t.Fatalf("200: %v", err)
	}
	if !strings.Contains(gotBody, `"func":"verify_user"`) || !strings.Contains(gotBody, `"user":"AB12C34"`) || !strings.Contains(gotBody, `"password":"p\"w\\"`) {
		t.Errorf("body %s", gotBody)
	}
	status.Store(202)
	if err := c.VerifyUser(context.Background(), "AB12C34", "wrong"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("202: %v", err)
	}
	status.Store(500)
	if err := c.VerifyUser(context.Background(), "AB12C34", "x"); err == nil || errors.Is(err, ErrBadCredentials) {
		t.Errorf("a server error is not a wrong password: %v", err)
	}
}
