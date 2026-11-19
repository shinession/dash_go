package hcscan

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// DraftStore keeps the draft of a scanning session. Nothing in it reaches the database before the shift leader
// confirms: Entries are the pending lines, Scans remember who scanned (also after the line was saved).
type DraftStore interface {
	Entries(ctx context.Context, k Key) (map[string]Entry, error)
	PutEntry(ctx context.Context, k Key, e Entry) error
	DeleteEntries(ctx context.Context, k Key, ees ...string) error
	Scans(ctx context.Context, k Key) (map[string]ScanRecord, error)
	PutScan(ctx context.Context, k Key, ee string, r ScanRecord) error
	DeleteScans(ctx context.Context, k Key, ees ...string) error
}

// ---- in-memory store (tests and single instance development runs) ----

type memoryDrafts struct {
	mu      sync.Mutex
	entries map[string]map[string]Entry
	scans   map[string]map[string]ScanRecord
}

// NewMemoryDrafts returns a store that lives in the process memory.
func NewMemoryDrafts() DraftStore {
	return &memoryDrafts{entries: map[string]map[string]Entry{}, scans: map[string]map[string]ScanRecord{}}
}

func (m *memoryDrafts) Entries(_ context.Context, k Key) (map[string]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]Entry, len(m.entries[k.id()]))
	for ee, e := range m.entries[k.id()] {
		out[ee] = e
	}
	return out, nil
}

func (m *memoryDrafts) PutEntry(_ context.Context, k Key, e Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries[k.id()] == nil {
		m.entries[k.id()] = map[string]Entry{}
	}
	m.entries[k.id()][e.EE] = e
	return nil
}

func (m *memoryDrafts) DeleteEntries(_ context.Context, k Key, ees ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ee := range ees {
		delete(m.entries[k.id()], ee)
	}
	return nil
}

func (m *memoryDrafts) Scans(_ context.Context, k Key) (map[string]ScanRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]ScanRecord, len(m.scans[k.id()]))
	for ee, r := range m.scans[k.id()] {
		out[ee] = r
	}
	return out, nil
}

func (m *memoryDrafts) PutScan(_ context.Context, k Key, ee string, r ScanRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scans[k.id()] == nil {
		m.scans[k.id()] = map[string]ScanRecord{}
	}
	m.scans[k.id()][ee] = r
	return nil
}

func (m *memoryDrafts) DeleteScans(_ context.Context, k Key, ees ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ee := range ees {
		delete(m.scans[k.id()], ee)
	}
	return nil
}

// ---- Redis store ----

type redisDrafts struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewRedisDrafts keeps the drafts in Redis as JSON hashes below the prefix dash_go:hc_scan:v1. Python never
// reads these keys, so JSON (not pickle) is used.
func NewRedisDrafts(rdb *redis.Client, ttl time.Duration) DraftStore {
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	return &redisDrafts{rdb: rdb, ttl: ttl}
}

func (r *redisDrafts) key(k Key, part string) string {
	return fmt.Sprintf("dash_go:hc_scan:v1:%s:%s", k.id(), part)
}

func (r *redisDrafts) Entries(ctx context.Context, k Key) (map[string]Entry, error) {
	raw, err := r.rdb.HGetAll(ctx, r.key(k, "entries")).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]Entry, len(raw))
	for ee, v := range raw {
		var e Entry
		if err := json.Unmarshal([]byte(v), &e); err != nil {
			return nil, fmt.Errorf("draft entry %s is damaged: %w", ee, err)
		}
		out[ee] = e
	}
	return out, nil
}

func (r *redisDrafts) PutEntry(ctx context.Context, k Key, e Entry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	pipe := r.rdb.TxPipeline()
	pipe.HSet(ctx, r.key(k, "entries"), e.EE, string(b))
	pipe.Expire(ctx, r.key(k, "entries"), r.ttl)
	_, err = pipe.Exec(ctx)
	return err
}

func (r *redisDrafts) DeleteEntries(ctx context.Context, k Key, ees ...string) error {
	if len(ees) == 0 {
		return nil
	}
	return r.rdb.HDel(ctx, r.key(k, "entries"), ees...).Err()
}

func (r *redisDrafts) Scans(ctx context.Context, k Key) (map[string]ScanRecord, error) {
	raw, err := r.rdb.HGetAll(ctx, r.key(k, "scans")).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]ScanRecord, len(raw))
	for ee, v := range raw {
		var s ScanRecord
		if err := json.Unmarshal([]byte(v), &s); err != nil {
			return nil, fmt.Errorf("scan record %s is damaged: %w", ee, err)
		}
		out[ee] = s
	}
	return out, nil
}

func (r *redisDrafts) PutScan(ctx context.Context, k Key, ee string, s ScanRecord) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	pipe := r.rdb.TxPipeline()
	pipe.HSet(ctx, r.key(k, "scans"), ee, string(b))
	pipe.Expire(ctx, r.key(k, "scans"), r.ttl)
	_, err = pipe.Exec(ctx)
	return err
}

func (r *redisDrafts) DeleteScans(ctx context.Context, k Key, ees ...string) error {
	if len(ees) == 0 {
		return nil
	}
	return r.rdb.HDel(ctx, r.key(k, "scans"), ees...).Err()
}

// id is the stable text form of a key: process:yyyymmdd:shift.
func (k Key) id() string {
	return fmt.Sprintf("%s:%s:%s", k.Process, k.Date.Format("20060102"), k.Shift)
}
