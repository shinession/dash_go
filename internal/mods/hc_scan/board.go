package hcscan

import (
	"context"
	"sort"
	"strings"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/version"
)

// Me answers who the user is and which processes they may start a session for.
func (s *Service) Me(ctx context.Context, u auth.User) (*MeView, error) {
	procs, err := s.Perms.Processes(ctx, u, false)
	if err != nil {
		s.Log.Error("permission check failed", "ee", u.EE, "error", err)
		return nil, newError(502, "permission_check_failed")
	}
	// the list is never nil: the page reads its length, and a person without permission must get [] and not null
	allowed := []string{}
	for _, p := range s.Processes { // keep the order of HC_PROCESS_LIST
		if contains(procs, p) {
			allowed = append(allowed, p)
		}
	}
	now := s.Now()
	return &MeView{
		User:      UserView{EE: u.EE, Name: u.Name},
		Processes: allowed,
		Version:   version.Version,
		Config: ClientConfig{
			IDPattern:      s.ids.Pattern(),
			PhotoURL:       s.Settings.PhotoURLTemplate,
			PollMS:         s.Settings.PollMillis,
			ProductionDate: ProductionDate(now, s.Settings.ProductionDayOffsetHours).Format("2006-01-02"),
			DefaultShift:   s.defaultShift(now),
			RecentDays:     s.Settings.RecentDays,
		},
	}, nil
}

func (s *Service) defaultShift(now time.Time) string {
	if h := now.Hour(); h >= s.Settings.AMFromHour && h < s.Settings.PMFromHour {
		return ShiftAM
	}
	return ShiftPM
}

// Board returns the whole state of a session for the page.
func (s *Service) Board(ctx context.Context, u auth.User, k Key) (*BoardView, error) {
	if err := s.check(ctx, u, k, false); err != nil {
		return nil, err
	}
	pending, err := s.Drafts.Entries(ctx, k)
	if err != nil {
		return nil, s.internal("read draft", err)
	}
	scans, err := s.Drafts.Scans(ctx, k)
	if err != nil {
		return nil, s.internal("read scan log", err)
	}
	d, err := s.loadDay(ctx, k)
	if err != nil {
		return nil, s.internal("load session data", err)
	}
	return s.buildBoard(ctx, d, pending, scans), nil
}

// entryView adds the current warnings and the MTR level of the person on every machine to a draft line. The
// sentences are in the language of the request.
func (s *Service) entryView(ctx context.Context, d *dayData, e Entry, used map[string]float64) EntryView {
	flags := append(append([]Flag{}, e.Flags...), s.dynamicFlags(d, e, used)...)
	levels := make(map[string]int, len(d.machines.machines))
	for _, m := range d.machines.machines {
		levels[m.ID] = d.levelOn(e.EE, m)
	}
	return EntryView{Entry: e, Reason: say(ctx, e.ReasonMsg, e.Reason), Photo: s.photo(e.EE), Flags: flagViews(ctx, flags), Levels: levels, Warn: hasWarn(flags)}
}

func (s *Service) buildBoard(ctx context.Context, d *dayData, pending map[string]Entry, scans map[string]ScanRecord) *BoardView {
	used := s.usage(d, pending)
	today := ProductionDate(s.Now(), s.Settings.ProductionDayOffsetHours)
	bv := &BoardView{
		Process: d.key.Process, Date: d.key.Date.Format("2006-01-02"), Shift: d.key.Shift, ServerTime: s.Now(),
		Locked: MonthLocked(d.key.Date, today),
	}

	// machine cards: people already saved in the database plus the pending lines
	members := map[string][]MemberView{}
	for _, r := range d.records {
		ee := strings.ToUpper(r.EE)
		if e, ok := pending[ee]; ok && (e.Source == SourceAuto || e.Source == SourceManual) {
			continue
		}
		if NormShift(firstNonEmpty(r.Actual, r.Schedule)) != d.key.Shift {
			continue
		}
		work, _, _ := s.splitEntries(r.Entries)
		_, scanned := scans[ee]
		for _, h := range work {
			if m, ok := d.machines.Resolve(h.Machine); ok {
				members[m.ID] = append(members[m.ID], MemberView{EE: ee, Name: d.nameOf(ee), HC: h.HC, Level: d.levelOn(ee, m), Saved: true, Photo: s.photo(ee), Scaned: scanned})
			}
		}
	}
	for _, e := range pending {
		if (e.Source == SourceAuto || e.Source == SourceManual) && e.MachineID != "" {
			if m, ok := d.machines.byID[e.MachineID]; ok {
				members[m.ID] = append(members[m.ID], MemberView{EE: e.EE, Name: e.Name, HC: e.HC, Level: d.levelOn(e.EE, m), Saved: false, Photo: s.photo(e.EE), Scaned: true})
			}
		}
	}
	unmapped := 0
	for _, m := range d.machines.machines {
		list := members[m.ID]
		if list == nil {
			list = []MemberView{}
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		if m.Routine() && m.CIMID == "" {
			unmapped++
		}
		bv.Machines = append(bv.Machines, MachineView{
			ID: m.ID, Name: m.Name, Type: m.Type, Routine: m.Routine(), MTRMapped: m.CIMID != "", Budget: m.Budget,
			Used: round2(used[m.ID]), Over: m.Budget > 0 && used[m.ID] > m.Budget+eps, Members: list,
		})
	}

	// pending lines, newest first
	for _, e := range pending {
		v := s.entryView(ctx, d, e, used)
		bv.Entries = append(bv.Entries, v)
		bv.Totals.Pending++
		switch {
		case e.Source == SourceNone || e.MachineID == "":
			bv.Totals.Unassigned++
		default:
			bv.Totals.Assigned++
		}
		if v.Warn {
			bv.Totals.Warnings++
		}
	}
	sort.SliceStable(bv.Entries, func(i, j int) bool { return bv.Entries[i].ScannedAt.After(bv.Entries[j].ScannedAt) })

	// expected people who have not scanned yet
	for _, p := range d.dir.Roster(d.key.Process) {
		rec, hasRec := d.recByEE[p.EE]
		effective := d.schedule(p.Shift)
		if hasRec && strings.TrimSpace(rec.Actual) != "" {
			effective = rec.Actual
		}
		if NormShift(effective) != d.key.Shift {
			continue
		}
		bv.Totals.Expected++
		_, scanned := scans[p.EE]
		if _, isPending := pending[p.EE]; scanned || isPending {
			bv.Totals.ExpectedScanned++
			continue
		}
		pv := PersonView{EE: p.EE, Name: p.Name, Shift: p.Shift, Schedule: d.schedule(p.Shift), Photo: s.photo(p.EE)}
		if hasRec {
			if work, _, _ := s.splitEntries(rec.Entries); len(work) > 0 {
				pv.Assigned = work[0].Machine
			}
		}
		bv.NotScanned = append(bv.NotScanned, pv)
	}
	for _, r := range scans {
		bv.Totals.Scanned++
		if r.ConfirmedAt != nil {
			bv.Totals.Saved++
		}
	}
	bv.MTR = MTRInfo{Employees: len(d.mtr.Levels), Unmapped: unmapped}
	if !d.mtr.DataTime.IsZero() {
		bv.MTR.DataTime = d.mtr.DataTime.Format("2006-01-02 15:04")
	}
	if bv.Entries == nil {
		bv.Entries = []EntryView{}
	}
	if bv.NotScanned == nil {
		bv.NotScanned = []PersonView{}
	}
	return bv
}
