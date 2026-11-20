package hcscan

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/i18n"
)

// Error is returned by the service; the HTTP layer maps it to a status code and a JSON body. The sentence is not
// made here: Key names a text of the catalog and Args fills its placeholders, so the answer is in the language of
// the request. Without a Key the text is hc.err.<Code>.
type Error struct {
	Status int
	Code   string
	Key    string
	Args   i18n.P
}

func (e *Error) Error() string { return e.Code }

// newError makes an error whose text is hc.err.<code> of the catalog.
func newError(status int, code string, args ...i18n.P) *Error {
	e := &Error{Status: status, Code: code, Key: "hc.err." + code}
	if len(args) > 0 {
		e.Args = args[0]
	}
	return e
}

// newErrorKey makes an error whose text is another key of the catalog than hc.err.<code> (two texts share a code).
func newErrorKey(status int, code, key string, args i18n.P) *Error {
	return &Error{Status: status, Code: code, Key: key, Args: args}
}

// Settings are the module settings (config key modules.hc_scan). Every field has a default.
type Settings struct {
	EmployeeIDPattern        string `json:"employee_id_pattern"`
	DraftStore               string `json:"draft_store"` // "redis" (default) or "memory" (single instance, development)
	DraftTTLHours            int    `json:"draft_ttl_hours"`
	RecentDays               int    `json:"recent_days"`
	PhotoURLTemplate         string `json:"photo_url_template"` // default: the photo_url_template of the service
	DashAPIURL               string `json:"dash_api_url"`       // default: the dash_api_url of the service, http://<api_server>/api
	DashDataDir              string `json:"dash_data_dir"`      // data folder of the Python app, holds DailyAttnData.log
	ProductionDayOffsetHours int    `json:"production_day_offset_hours"`
	AMFromHour               int    `json:"am_from_hour"`
	PMFromHour               int    `json:"pm_from_hour"`
	PollMillis               int    `json:"poll_ms"`
	// LeaderActivityNames are the names of the non-routine rows leaders are placed on (see leader.go).
	LeaderActivityNames []string `json:"leader_activity_names"`
	// LeaderByHistory recognises leaders by their most recent record until their direct type is Shift Leader.
	LeaderByHistory bool `json:"leader_by_history"`
}

// DefaultSettings returns the defaults.
func DefaultSettings() Settings {
	return Settings{
		EmployeeIDPattern:        DefaultIDPattern,
		DraftStore:               "redis",
		DraftTTLHours:            48,
		RecentDays:               7,
		ProductionDayOffsetHours: 7,
		AMFromHour:               7,
		PMFromHour:               19,
		PollMillis:               3000,
		LeaderActivityNames:      []string{"Group Leader"},
		LeaderByHistory:          true,
	}
}

// Permissions tells which HC processes a user may work on.
type Permissions interface {
	// Processes returns the processes of the user. fresh bypasses any cache (used before writing).
	Processes(ctx context.Context, u auth.User, fresh bool) ([]string, error)
}

// Deps are the collaborators of the Service.
type Deps struct {
	Repo      Repository
	Drafts    DraftStore
	ChangeLog ChangeLog
	Perms     Permissions
	Settings  Settings
	Processes []string // HC_PROCESS_LIST
	LeaveList []string // leave options (SHIFT_LEAVE_PATTERN keys from the third on)
	Log       *slog.Logger
	Now       func() time.Time
}

// Service holds the business logic of the module.
type Service struct {
	Deps
	ids       *IDParser
	locks     keyedMutex
	machinesC *ttlCache[[]Machine]
	mtrC      *ttlCache[MTRData]
	calC      *ttlCache[map[string]string]
	dirC      *ttlCache[*Directory]
}

// NewService validates the dependencies and returns the service.
func NewService(d Deps) (*Service, error) {
	if d.Repo == nil || d.Drafts == nil || d.Perms == nil {
		return nil, fmt.Errorf("hc_scan: repository, draft store and permissions are required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Settings.RecentDays <= 0 {
		d.Settings.RecentDays = 7
	}
	ids, err := NewIDParser(d.Settings.EmployeeIDPattern)
	if err != nil {
		return nil, err
	}
	s := &Service{Deps: d, ids: ids}
	s.machinesC = newTTLCache[[]Machine](time.Minute, d.Now, d.Log)
	s.mtrC = newTTLCache[MTRData](time.Minute, d.Now, d.Log)
	s.calC = newTTLCache[map[string]string](5*time.Minute, d.Now, d.Log)
	s.dirC = newTTLCache[*Directory](time.Minute, d.Now, d.Log)
	return s, nil
}

// ---- helpers shared by the service files ----

type ttlCache[V any] struct {
	mu    sync.Mutex
	ttl   time.Duration
	now   func() time.Time
	log   *slog.Logger
	items map[string]cacheItem[V]
}

type cacheItem[V any] struct {
	v  V
	at time.Time
}

func newTTLCache[V any](ttl time.Duration, now func() time.Time, log *slog.Logger) *ttlCache[V] {
	return &ttlCache[V]{ttl: ttl, now: now, log: log, items: map[string]cacheItem[V]{}}
}

// get returns the cached value or loads it. When loading fails and an older value exists, the older value is
// served (and the failure is logged), so a short database outage does not stop the scanning.
func (c *ttlCache[V]) get(key string, load func() (V, error)) (V, error) {
	c.mu.Lock()
	it, ok := c.items[key]
	c.mu.Unlock()
	if ok && c.now().Sub(it.at) < c.ttl {
		return it.v, nil
	}
	v, err := load()
	if err != nil {
		if ok {
			c.log.Warn("serving stale cached data after a load failure", "key", key, "error", err)
			return it.v, nil
		}
		var zero V
		return zero, err
	}
	c.mu.Lock()
	c.items[key] = cacheItem[V]{v: v, at: c.now()}
	c.mu.Unlock()
	return v, nil
}

type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*refMutex
}

type refMutex struct {
	sync.Mutex
	refs int
}

// lock serialises the operations of one scanning session, so two terminals cannot take the last vacancy twice.
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*refMutex{}
	}
	rm := k.m[key]
	if rm == nil {
		rm = &refMutex{}
		k.m[key] = rm
	}
	rm.refs++
	k.mu.Unlock()
	rm.Lock()
	return func() {
		rm.Unlock()
		k.mu.Lock()
		rm.refs--
		if rm.refs == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}

// dayData is everything one request needs to know about a session. Reference data comes from short lived
// caches, the day records are always read fresh.
type dayData struct {
	key      Key
	machines *machineIndex
	mtr      MTRData
	cal      map[string]string
	dir      *Directory
	records  []DayRecord
	recByEE  map[string]DayRecord
}

func (s *Service) loadDay(ctx context.Context, k Key) (*dayData, error) {
	mprocs := MachineProcesses(k.Process)
	pkey := strings.Join(mprocs, ",")
	dateKey := k.Date.Format("2006-01-02")
	machines, err := s.machinesC.get(pkey, func() ([]Machine, error) { return s.Repo.Machines(ctx, mprocs) })
	if err != nil {
		return nil, fmt.Errorf("read machines: %w", err)
	}
	mtr, err := s.mtrC.get(pkey, func() (MTRData, error) { return s.Repo.MTR(ctx, mprocs) })
	if err != nil {
		return nil, fmt.Errorf("read MTR records: %w", err)
	}
	cal, err := s.calC.get(dateKey, func() (map[string]string, error) { return s.Repo.CalendarDay(ctx, k.Date) })
	if err != nil {
		return nil, fmt.Errorf("read shift calendar: %w", err)
	}
	dir, err := s.dirC.get(dateKey, func() (*Directory, error) {
		mes, err := s.Repo.MESSnapshot(ctx, k.Date)
		if err != nil {
			return nil, err
		}
		members, err := s.Repo.Members(ctx, k.Date)
		if err != nil {
			return nil, err
		}
		return BuildDirectory(k.Date, mes, members), nil
	})
	if err != nil {
		return nil, fmt.Errorf("read employees: %w", err)
	}
	records, err := s.Repo.DayRecords(ctx, k.Date)
	if err != nil {
		return nil, fmt.Errorf("read attendance records: %w", err)
	}
	mapped := make([]Machine, len(machines))
	for i, m := range machines {
		m.CIMID = mtr.NameToCIM[strings.TrimSpace(m.Name)]
		mapped[i] = m
	}
	d := &dayData{key: k, machines: newMachineIndex(mapped), mtr: mtr, cal: cal, dir: dir, records: records, recByEE: make(map[string]DayRecord, len(records))}
	for _, r := range records {
		d.recByEE[strings.ToUpper(r.EE)] = r
	}
	return d, nil
}

// schedule returns the day type of the shift code on the date of the session.
func (d *dayData) schedule(shiftCode string) string {
	col := ShiftColumn(shiftCode)
	if col == "" || d.cal == nil {
		return ""
	}
	return d.cal[col]
}

// levelOn is the MTR level of the person on the machine: -1 for an activity that needs no proficiency, 0 for no
// record (or no MTR mapping), otherwise 1 to 3.
func (d *dayData) levelOn(ee string, m Machine) int {
	if !m.Routine() {
		return -1
	}
	if m.CIMID == "" {
		return 0
	}
	return d.mtr.Levels[ee][m.CIMID]
}

func (d *dayData) nameOf(ee string) string {
	if p, ok := d.dir.Lookup(ee); ok && p.Name != "" {
		return p.Name
	}
	return ee
}

func (s *Service) isLeave(name string) bool { return contains(s.LeaveList, name) }

// splitEntries separates the leave options from the machines of a record and sums the leave HC.
func (s *Service) splitEntries(entries []HCEntry) (work, leave []HCEntry, leaveHC float64) {
	for _, h := range entries {
		if h.HC <= 0 || strings.TrimSpace(h.Machine) == "" {
			continue
		}
		if s.isLeave(h.Machine) {
			leave = append(leave, h)
			leaveHC += h.HC
		} else {
			work = append(work, h)
		}
	}
	return work, leave, round2(leaveHC)
}

// availHC is the HC a person can put on a machine. A full day of leave counts as 1, because a person who scans
// the badge is present and the shift leader confirms the exception.
func availHC(leaveHC float64) float64 {
	if leaveHC >= 1-eps {
		return 1
	}
	return round2(1 - leaveHC)
}

// usage sums the HC on every machine: the saved records of the same session plus the pending draft lines.
// A pending line replaces the saved record of the same person.
func (s *Service) usage(d *dayData, pending map[string]Entry) map[string]float64 {
	used := map[string]float64{}
	for _, r := range d.records {
		if e, ok := pending[strings.ToUpper(r.EE)]; ok && (e.Source == SourceAuto || e.Source == SourceManual) {
			continue
		}
		if NormShift(firstNonEmpty(r.Actual, r.Schedule)) != d.key.Shift {
			continue
		}
		work, _, _ := s.splitEntries(r.Entries)
		for _, h := range work {
			if m, ok := d.machines.Resolve(h.Machine); ok {
				used[m.ID] += h.HC
			}
		}
	}
	for _, e := range pending {
		if (e.Source == SourceAuto || e.Source == SourceManual) && e.MachineID != "" {
			used[e.MachineID] += e.HC
		}
	}
	return used
}

// recentDays counts, per machine, the days of the history the person worked on it.
func (s *Service) recentDays(d *dayData, history []DayRecord) map[string]int {
	out := map[string]int{}
	for _, r := range history {
		seen := map[string]bool{}
		work, _, _ := s.splitEntries(r.Entries)
		for _, h := range work {
			if m, ok := d.machines.Resolve(h.Machine); ok {
				seen[m.ID] = true
			}
		}
		for id := range seen {
			out[id]++
		}
	}
	return out
}

func (s *Service) photo(ee string) string {
	return strings.ReplaceAll(s.Settings.PhotoURLTemplate, "{ee_code}", ee)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// check validates the session key and the permission of the user.
func (s *Service) check(ctx context.Context, u auth.User, k Key, fresh bool) error {
	if !contains(s.Processes, k.Process) {
		return newError(400, "bad_process", i18n.P{"process": clip(k.Process, 30)})
	}
	if k.Shift != ShiftAM && k.Shift != ShiftPM {
		return newError(400, "bad_shift")
	}
	today := ProductionDate(s.Now(), s.Settings.ProductionDayOffsetHours)
	oldest := time.Date(today.Year(), today.Month()-1, 1, 0, 0, 0, 0, time.UTC)
	if k.Date.After(today.AddDate(0, 0, 1)) || k.Date.Before(oldest) {
		return newErrorKey(400, "bad_date", "hc.err.bad_date_range", i18n.P{"oldest": oldest.Format("2006-01-02"), "newest": today.AddDate(0, 0, 1).Format("2006-01-02")})
	}
	procs, err := s.Perms.Processes(ctx, u, fresh)
	if err != nil {
		s.Log.Error("permission check failed", "ee", u.EE, "error", err)
		return newError(502, "permission_check_failed")
	}
	if !contains(procs, k.Process) {
		return newError(403, "forbidden", i18n.P{"process": k.Process})
	}
	return nil
}
