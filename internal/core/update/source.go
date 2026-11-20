package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Source reads the manifest and the program from one address.
type Source interface {
	// Address is the address of the manifest, as it may be written to the log.
	Address() string
	// Manifest reads and checks the manifest.
	Manifest(ctx context.Context) (Manifest, error)
	// Open opens the program the manifest names. size is -1 when the source does not say how big it is.
	Open(ctx context.Context, m Manifest) (rc io.ReadCloser, size int64, err error)
}

const (
	manifestTimeout = 30 * time.Second
	maxRedirects    = 5
)

// IsHTTP reports whether an address is an http(s) address rather than a path.
func IsHTTP(address string) bool {
	l := strings.ToLower(address)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// NewSource makes the source of an address: an http(s) address, or the absolute path of the manifest file. An address
// that ends in a slash, and a path of a folder, name the folder of a release: the manifest is dash_go.json in it.
// client is used for the http(s) requests and may be nil.
func NewSource(address string, client *http.Client) (Source, error) {
	if client == nil {
		client = defaultClient()
	}
	if IsHTTP(address) {
		u, err := url.Parse(address)
		if err != nil || u.Host == "" || u.User != nil {
			return nil, errors.New("update: the address of the manifest must be http(s)://host/path with no user name or password")
		}
		if u.Path == "" {
			u.Path = "/"
		}
		if strings.HasSuffix(u.Path, "/") {
			u = u.JoinPath(ManifestFile)
		}
		return &httpSource{addr: u, client: client}, nil
	}
	if !filepath.IsAbs(address) {
		return nil, errors.New("update: the address of the manifest must be an http(s) address or an absolute path")
	}
	return &fileSource{address: address, folder: strings.HasSuffix(address, `\`) || strings.HasSuffix(address, "/"), client: client}, nil
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func defaultClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

// get asks for an address and answers only a 200: the application that serves the static files answers 202 and a
// picture for a file that is not there, which must never be taken for a manifest or a program.
func get(ctx context.Context, client *http.Client, target string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "dash_go-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s answered %s", requestHost(target), resp.Status)
	}
	return resp, nil
}

func requestHost(target string) string {
	if u, err := url.Parse(target); err == nil && u.Host != "" {
		return u.Host
	}
	return "the server"
}

// readManifest reads a manifest from a stream that may be longer than a manifest ever is.
func readManifest(r io.Reader) (Manifest, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxManifestBytes+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("read the manifest: %w", err)
	}
	if len(data) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: longer than %d bytes", ErrManifest, MaxManifestBytes)
	}
	return ParseManifest(data)
}

// httpSource reads from an http(s) address; the program is found relative to the manifest.
type httpSource struct {
	addr   *url.URL
	client *http.Client
}

func (s *httpSource) Address() string { return s.addr.Redacted() }

func (s *httpSource) Manifest(ctx context.Context) (Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, manifestTimeout)
	defer cancel()
	resp, err := get(ctx, s.client, s.addr.String())
	if err != nil {
		return Manifest{}, err
	}
	defer resp.Body.Close()
	return readManifest(resp.Body)
}

func (s *httpSource) Open(ctx context.Context, m Manifest) (io.ReadCloser, int64, error) {
	ref, err := url.Parse(m.PackageURL)
	if err != nil {
		return nil, 0, errors.New("package_url is not an address")
	}
	target := s.addr.ResolveReference(ref)
	if (target.Scheme != "http" && target.Scheme != "https") || target.User != nil {
		return nil, 0, errors.New("package_url must be an http(s) address with no user name or password when the manifest is one")
	}
	resp, err := get(ctx, s.client, target.String())
	if err != nil {
		return nil, 0, err
	}
	return resp.Body, resp.ContentLength, nil
}

// fileSource reads the manifest from a file; the program is found next to it unless package_url says otherwise.
type fileSource struct {
	address string // as it was configured: the manifest file, or the folder of a release
	folder  bool   // the address ends in a separator, so it is a folder whether it can be reached or not
	client  *http.Client
}

// path is the manifest file. An address without a trailing separator is looked at each time it is used: a share that
// cannot be reached when the service starts is the folder of the release all the same once it can.
func (s *fileSource) path() string {
	if s.folder || isDir(s.address) {
		return filepath.Join(s.address, ManifestFile)
	}
	return s.address
}

func (s *fileSource) Address() string { return s.path() }

func (s *fileSource) Manifest(context.Context) (Manifest, error) {
	f, err := os.Open(s.path())
	if err != nil {
		return Manifest{}, fmt.Errorf("read the manifest: %w", err)
	}
	defer f.Close()
	return readManifest(f)
}

func (s *fileSource) Open(ctx context.Context, m Manifest) (io.ReadCloser, int64, error) {
	if IsHTTP(m.PackageURL) {
		u, err := url.Parse(m.PackageURL)
		if err != nil || u.Host == "" || u.User != nil {
			return nil, 0, errors.New("package_url must be an http(s) address with no user name or password")
		}
		resp, err := get(ctx, s.client, u.String())
		if err != nil {
			return nil, 0, err
		}
		return resp.Body, resp.ContentLength, nil
	}
	program := m.PackageURL
	if !filepath.IsAbs(program) {
		program = filepath.Join(filepath.Dir(s.path()), filepath.FromSlash(program))
	}
	f, err := os.Open(program)
	if err != nil {
		return nil, 0, fmt.Errorf("open the program: %w", err)
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		_ = f.Close()
		return nil, 0, errors.New("package_url is not a file")
	}
	return f, st.Size(), nil
}
