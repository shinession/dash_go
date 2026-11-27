package hcscan

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisLockerSerialisesAcrossTwoInstances(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb2 := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	rdb3 := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb2.Close()
	defer rdb3.Close()

	// Build two independent Service instances (simulating s2 and s3) sharing one Redis and one machine with budget 1.
	env2 := newTestEnv(t)
	env3 := newTestEnv(t)
	// Restrict to a single Lv3-capable machine with budget 1 so both employees compete for the exact last vacancy.
	oneMachine := []Machine{{ID: "Winder_515", Process: "Winder", Name: "Winder_515", Type: "Routine", Budget: 1, Seq: 1}}
	env2.repo.machines, env3.repo.machines = oneMachine, oneMachine
	env2.svc.Drafts = NewRedisDrafts(rdb2, 48*time.Hour)
	env3.svc.Drafts = NewRedisDrafts(rdb3, 48*time.Hour)
	env2.svc.Locker = NewRedisLocker(rdb2, 5*time.Second, 5*time.Second)
	env3.svc.Locker = NewRedisLocker(rdb3, 5*time.Second, 5*time.Second)

	// Concurrently scan AB12C01 on s2 and AB12C02 on s3: both qualify for Winder_515 (budget 1).
	var wg sync.WaitGroup
	var res2, res3 *ScanResult
	var err2, err3 error
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		res2, err2 = env2.svc.Scan(context.Background(), testUser, testKey, "AB12C01")
	}()
	go func() {
		defer wg.Done()
		<-start
		res3, err3 = env3.svc.Scan(context.Background(), testUser, testKey, "AB12C02")
	}()
	close(start)
	wg.Wait()

	if err2 != nil || err3 != nil {
		t.Fatalf("concurrent scans failed: err2=%v err3=%v", err2, err3)
	}
	assigned := 0
	unassigned := 0
	for _, r := range []*ScanResult{res2, res3} {
		switch r.Status {
		case "assigned":
			assigned++
		case "unassigned":
			unassigned++
		default:
			t.Fatalf("unexpected scan status %q", r.Status)
		}
	}
	if assigned != 1 || unassigned != 1 {
		t.Fatalf("distributed lock must allow exactly 1 employee to take the last vacancy: assigned=%d unassigned=%d", assigned, unassigned)
	}
}

func TestRedisLockerOwnershipAndLeaseExtension(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	// Short TTL: watchdog must keep the lock alive past TTL while held, then release cleanly on unlock.
	locker := NewRedisLocker(rdb, 120*time.Millisecond, 2*time.Second)
	unlock1, err := locker.Lock(context.Background(), "Winder:20261128:AM")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(220 * time.Millisecond)

	// Another contender with a short wait must still time out because the lease was extended.
	shortLocker := NewRedisLocker(rdb, 120*time.Millisecond, 60*time.Millisecond)
	if _, err := shortLocker.Lock(context.Background(), "Winder:20261128:AM"); err == nil {
		t.Fatal("contender must not acquire a lock whose lease is still being extended")
	}

	// Ownership check: if Redis key was replaced by another token, unlock1 must NOT delete the new owner's key.
	mr.Set(lockKeyPrefix+"Winder:20261128:AM", "other-owner-token")
	unlock1()
	if got, _ := mr.Get(lockKeyPrefix + "Winder:20261128:AM"); got != "other-owner-token" {
		t.Fatalf("stale unlock must not delete another owner's lock, got %q", got)
	}
	mr.Del(lockKeyPrefix + "Winder:20261128:AM")

	// Normal acquire + unlock frees the key immediately.
	unlock2, err := locker.Lock(context.Background(), "Winder:20261128:AM")
	if err != nil {
		t.Fatal(err)
	}
	unlock2()
	unlock2() // idempotent
	if mr.Exists(lockKeyPrefix + "Winder:20261128:AM") {
		t.Fatal("unlock must remove the lock key from Redis")
	}
}

func TestRedisLockerReportsErrorWhenRedisIsDown(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	env := newTestEnv(t)
	env.svc.Locker = NewRedisLocker(rdb, time.Second, time.Second)
	mr.Close()

	_, err := env.svc.Scan(context.Background(), testUser, testKey, "AB12C01")
	if err == nil {
		t.Fatal("Scan must fail when Redis lock store is down")
	}
	se, ok := err.(*Error)
	if !ok || se.Status != 500 || se.Args["step"] != "hc.step.lock_session" {
		t.Fatalf("expected localized lock_session internal error, got %#v", err)
	}
	if !strings.Contains(se.Key, "hc.err.internal") {
		t.Fatalf("unexpected error key: %q", se.Key)
	}
}
