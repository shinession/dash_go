package module

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dash_go/internal/core/config"
)

func TestPGDSNTargetsOnlyTheConfiguredServer(t *testing.T) {
	pc := config.Postgres{Host: "db.example", Port: 55432, Username: "mes user", Password: `p'a\ss word`}
	cfg, err := pgxpool.ParseConfig(pgDSN(pc, "Auth"))
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.ConnConfig
	if c.Host != "db.example" || c.Port != 55432 || c.User != "mes user" || c.Password != `p'a\ss word` || c.Database != "Auth" {
		t.Fatalf("parsed settings: %s %d %s %q %s", c.Host, c.Port, c.User, c.Password, c.Database)
	}
	if c.RuntimeParams["application_name"] != "dash_go" || c.RuntimeParams["statement_timeout"] != "20000" {
		t.Fatalf("runtime parameters: %v", c.RuntimeParams)
	}
	// the fallbacks pgx tries when the first attempt fails (for example the TLS negotiation) must point to the same
	// server: a leftover localhost:5432 would silently connect to a different database
	for _, f := range c.Fallbacks {
		if f.Host != "db.example" || f.Port != 55432 {
			t.Fatalf("fallback points to %s:%d", f.Host, f.Port)
		}
	}
}

func TestRegistry(t *testing.T) {
	if _, err := New("not_compiled_in"); err == nil {
		t.Fatal("an unknown module must be refused")
	}
	Register("registry_test", func() Module { return nil })
	found := false
	for _, n := range Names() {
		if n == "registry_test" {
			found = true
		}
	}
	if !found {
		t.Fatal("a registered module must be listed")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("registering a module twice must panic")
		}
	}()
	Register("registry_test", func() Module { return nil })
}
