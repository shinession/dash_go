package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dash_go/internal/core/dashapi"
)

// Errors of the sign-in.
var (
	// ErrInvalidCredentials means the account or the password is wrong, or the account is deactivated. It never
	// tells which one, so a visitor cannot find out whether an account exists.
	ErrInvalidCredentials = errors.New("auth: invalid account or password")
	// ErrUnavailable means the user directory could not be asked. It is not the fault of the account.
	ErrUnavailable = errors.New("auth: the user directory cannot be reached")
	// ErrNoEmployeeCode means the account exists but has no employee code, which every record of dash_go needs.
	ErrNoEmployeeCode = errors.New("auth: the account has no employee code")
	// ErrSSOPending means the sign-on service has not recorded anything for the token (yet).
	ErrSSOPending = errors.New("auth: single sign-on has not answered yet")
)

// Identity is who signed in: the employee code and the name shown in the header.
type Identity struct {
	EE   string
	Name string
}

// Directory checks the accounts of the Dash application.
type Directory interface {
	// Login checks an account (employee code, name or e-mail) and its password.
	Login(ctx context.Context, user, password string) (Identity, error)
	// Identify finds the active account of an employee code, without a password (the Windows sign-in).
	Identify(ctx context.Context, ee string) (Identity, error)
}

// SSOResult is what the Windows single sign-on service recorded for one login token.
type SSOResult struct {
	Status      string // "ok", or "no_user" when the browser sent no Windows user
	ECode       string // employee code from the Windows account
	WindowsUser string // DOMAIN\user, for the log only
	IP          string // address of the browser as the sign-on service saw it
	At          time.Time
}

// SSOSource is the Windows single sign-on service.
type SSOSource interface {
	// LoginURL is the page of the sign-on service the browser opens for a token.
	LoginURL(token string) string
	// Result reads the result of a token once: ErrSSOPending while there is none, ErrInvalidCredentials when what is
	// there cannot be trusted, ErrUnavailable when the service cannot be asked.
	Result(ctx context.Context, token string) (SSOResult, error)
}

// dashDirectory asks the Python backend: the passwords and the accounts stay where they are, dash_go keeps no copy.
type dashDirectory struct{ api *dashapi.Client }

// NewDashDirectory returns the Directory backed by the Python API (functions verify_user and hc_user_processes).
func NewDashDirectory(api *dashapi.Client) Directory { return dashDirectory{api: api} }

func (d dashDirectory) Login(ctx context.Context, user, password string) (Identity, error) {
	if err := d.api.VerifyUser(ctx, user, password); err != nil {
		if errors.Is(err, dashapi.ErrBadCredentials) {
			return Identity{}, ErrInvalidCredentials
		}
		return Identity{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return d.identity(ctx, user)
}

func (d dashDirectory) Identify(ctx context.Context, ee string) (Identity, error) {
	return d.identity(ctx, ee)
}

func (d dashDirectory) identity(ctx context.Context, user string) (Identity, error) {
	p, err := d.api.HCProfile(ctx, user)
	switch {
	case errors.Is(err, dashapi.ErrNotAllowed):
		return Identity{}, ErrInvalidCredentials
	case err != nil:
		return Identity{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	ee, name := strings.TrimSpace(p.EECode), strings.TrimSpace(p.Name)
	if ee == "" {
		return Identity{}, ErrNoEmployeeCode
	}
	if name == "" {
		name = ee
	}
	return Identity{EE: ee, Name: name}, nil
}

type dashSSO struct{ c *dashapi.SSOClient }

// NewDashSSO returns the SSOSource backed by the sign-on service of the Dash application.
func NewDashSSO(c *dashapi.SSOClient) SSOSource { return dashSSO{c: c} }

func (d dashSSO) LoginURL(token string) string { return d.c.LoginURL(token) }

func (d dashSSO) Result(ctx context.Context, token string) (SSOResult, error) {
	r, err := d.c.Result(ctx, token)
	switch {
	case errors.Is(err, dashapi.ErrSSOPending):
		return SSOResult{}, ErrSSOPending
	case errors.Is(err, dashapi.ErrSSOUnreadable):
		return SSOResult{}, ErrInvalidCredentials
	case err != nil:
		return SSOResult{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return SSOResult{Status: r.Status, ECode: r.ECode, WindowsUser: r.WindowsUser, IP: r.IP, At: r.At}, nil
}
