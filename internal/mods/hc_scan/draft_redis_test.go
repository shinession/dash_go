package hcscan

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"dash_go/internal/core/auth"
)

func newRedisDrafts(t *testing.T) (DraftStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewRedisDrafts(rdb, 48*time.Hour), mr
}

func TestRedisDraftsRoundTrip(t *testing.T) {
	store, mr := newRedisDrafts(t)
	ctx := context.Background()
	e := Entry{EE: "AB12C01", Name: "Alice", Source: SourceAuto, MachineID: "Winder_515", HC: 0.5, Level: 3, Flags: []Flag{{Code: FlagNoMTR, Severity: SeverityWarn, Text: "x"}},
		Existing: []HCEntry{{Process: "Winder", Machine: "M", HC: 1, MachineID: "Winder_515"}}, ScannedAt: testNow, ScannedBy: "Lead Er"}
	if err := store.PutEntry(ctx, testKey, e); err != nil {
		t.Fatal(err)
	}
	got, err := store.Entries(ctx, testKey)
	if err != nil || len(got) != 1 || got["AB12C01"].MachineID != "Winder_515" || got["AB12C01"].HC != 0.5 || len(got["AB12C01"].Flags) != 1 ||
		!got["AB12C01"].ScannedAt.Equal(testNow) || got["AB12C01"].Existing[0] != (HCEntry{Process: "Winder", Machine: "M", HC: 1, MachineID: "Winder_515"}) {
		t.Fatalf("entries: %+v %v", got, err)
	}
	key := "dash_go:hc_scan:v1:Winder:20261119:AM:entries"
	if !mr.Exists(key) || mr.TTL(key) <= 47*time.Hour {
		t.Fatalf("the draft key %q must exist with a 48 hour TTL, got %v", key, mr.TTL(key))
	}

	now := testNow
	if err := store.PutScan(ctx, testKey, "AB12C01", ScanRecord{ScannedAt: testNow, ScannedBy: "Lead Er", ConfirmedAt: &now}); err != nil {
		t.Fatal(err)
	}
	scans, err := store.Scans(ctx, testKey)
	if err != nil || scans["AB12C01"].ConfirmedAt == nil || !scans["AB12C01"].ConfirmedAt.Equal(now) {
		t.Fatalf("scans: %+v %v", scans, err)
	}

	other := Key{Process: "Winder", Date: testDate, Shift: ShiftPM}
	if got, _ := store.Entries(ctx, other); len(got) != 0 {
		t.Fatalf("another shift has its own draft: %+v", got)
	}
	if err := store.DeleteEntries(ctx, testKey, "AB12C01", "NOBODY1"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteScans(ctx, testKey, "AB12C01"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteEntries(ctx, testKey); err != nil { // deleting nothing is fine
		t.Fatal(err)
	}
	if got, _ := store.Entries(ctx, testKey); len(got) != 0 {
		t.Fatalf("after delete: %+v", got)
	}
}

func TestRedisDraftsReportDamagedData(t *testing.T) {
	store, mr := newRedisDrafts(t)
	mr.HSet("dash_go:hc_scan:v1:Winder:20261119:AM:entries", "AB12C01", "{not json")
	if _, err := store.Entries(context.Background(), testKey); err == nil || !strings.Contains(err.Error(), "AB12C01") {
		t.Fatalf("a damaged line must be reported, not ignored: %v", err)
	}
}

func TestServiceFlowWithRedisDrafts(t *testing.T) {
	env := newTestEnv(t)
	store, _ := newRedisDrafts(t)
	env.svc.Drafts = store
	ctx := context.Background()
	env.scan(t, "AB12C01")
	env.scan(t, "AB12C03")
	b, err := env.svc.Board(ctx, testUser, testKey)
	if err != nil || b.Totals.Pending != 2 || b.Totals.Assigned != 1 || b.Totals.Unassigned != 1 {
		t.Fatalf("board: %+v %v", b.Totals, err)
	}
	if _, err := env.svc.Confirm(ctx, testUser, testKey, true); err != nil {
		t.Fatal(err)
	}
	b, _ = env.svc.Board(ctx, testUser, testKey)
	if b.Totals.Pending != 1 || b.Totals.Saved != 1 {
		t.Fatalf("after confirm: %+v", b.Totals)
	}
	if err := env.svc.Reset(ctx, testUser, testKey); err != nil {
		t.Fatal(err)
	}
	b, _ = env.svc.Board(ctx, testUser, testKey)
	if b.Totals.Pending != 0 || b.Totals.Scanned != 1 {
		t.Fatalf("reset keeps what was saved: %+v", b.Totals)
	}
}

func TestRedisNonceStore(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	nonces := auth.NewRedisNonces(rdb)
	ctx := context.Background()
	if ok, err := nonces.Consume(ctx, "n1", time.Minute); err != nil || !ok {
		t.Fatalf("first use: %v %v", ok, err)
	}
	if ok, _ := nonces.Consume(ctx, "n1", time.Minute); ok {
		t.Fatal("a nonce works once")
	}
	if ttl := mr.TTL("dash_go:sso:nonce:n1"); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("a nonce expires: %v", ttl)
	}
}
