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
	otherPending, err := s.Drafts.Entries(ctx, Key{Process: k.Process, Date: k.Date, Shift: oppositeShift(k.Shift)})
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
	bv := s.buildBoard(ctx, d, pending, scans)
	if err := s.boardCandidates(ctx, d, pending, otherPending, bv); err != nil {
		return nil, s.internal("read assignment history", err)
	}
	return bv, nil
}

// entryView adds the current warnings and the MTR level of the person on every machine to a draft line. The
// sentences are in the language of the request.
func (s *Service) entryView(ctx context.Context, d *dayData, e Entry, used map[string]float64) EntryView {
	flags := append(append([]Flag{}, e.Flags...), s.dynamicFlags(d, e, used)...)
	levels := make(map[string]int, len(d.machines.machines))
	for _, m := range d.machines.machines {
		levels[m.ID] = d.levelOn(e.EE, m)
	}
	ids := append([]string{}, e.SelectedMachineIDs()...)
	return EntryView{Entry: e, MachineIDs: ids, Reason: say(ctx, e.ReasonMsg, e.Reason), Photo: s.photo(e.EE), Flags: flagViews(ctx, flags), Levels: levels, Warn: hasWarn(flags)}
}

// placing reports whether the draft line puts its person on at least one machine. Such a line replaces the saved
// record of the person on this page, until it is confirmed.
func placing(pending map[string]Entry, ee string) (Entry, bool) {
	e, ok := pending[ee]
	return e, ok && (e.Source == SourceAuto || e.Source == SourceManual) && e.HasMachine()
}

func (s *Service) buildBoard(ctx context.Context, d *dayData, pending map[string]Entry, scans map[string]ScanRecord) *BoardView {
	used := s.usage(d, pending)
	today := ProductionDate(s.Now(), s.Settings.ProductionDayOffsetHours)
	bv := &BoardView{
		Process: d.key.Process, Date: d.key.Date.Format("2006-01-02"), Shift: d.key.Shift, ServerTime: s.Now(),
		Locked: MonthLocked(d.key.Date, today),
	}

	// machine cards: the people already saved in the database plus the pending lines, only on the machines of this
	// process (see entryHere)
	members := map[string][]MemberView{}
	for _, r := range d.records {
		ee := strings.ToUpper(r.EE)
		if _, ok := placing(pending, ee); ok {
			continue
		}
		if NormShift(firstNonEmpty(r.Actual, r.Schedule)) != d.key.Shift {
			continue
		}
		process := d.personProcess(ee)
		work, _, _ := s.splitEntries(r.Entries)
		_, scanned := scans[ee]
		for _, h := range work {
			if m, ok := d.resolve(h, process); ok {
				members[m.ID] = append(members[m.ID], MemberView{EE: ee, Name: d.nameOf(ee), HC: h.HC, Level: d.levelOn(ee, m), Saved: true, Photo: s.photo(ee), Scaned: scanned, From: d.foreign(process)})
			}
		}
	}
	for _, e := range pending {
		if _, ok := placing(pending, e.EE); !ok {
			continue
		}
		ids := e.SelectedMachineIDs()
		share := splitHC(e.HC, len(ids))
		for _, id := range ids {
			if m, ok := d.machines.byID[id]; ok {
				members[m.ID] = append(members[m.ID], MemberView{EE: e.EE, Name: e.Name, HC: share, Level: d.levelOn(e.EE, m), Saved: false, Photo: s.photo(e.EE), Scaned: true, From: d.foreign(e.Process)})
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
		case e.Source == SourceNone || !e.HasMachine():
			bv.Totals.Unassigned++
		case e.Source == SourceAuto || e.Source == SourceManual:
			bv.Totals.Assigned++ // a line of an existing record has nothing to save: scanning such a person only shows their task
		}
		if v.Warn {
			bv.Totals.Warnings++
		}
	}
	sort.SliceStable(bv.Entries, func(i, j int) bool { return bv.Entries[i].ScannedAt.After(bv.Entries[j].ScannedAt) })

	s.shiftPeople(d, pending, bv)

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
	return bv
}

// shiftPeople looks at the members of the roster who belong to the shift and says what each of them has: a task
// (counted in PlacedHC), a leave (LeaveHC, listed in Leave), or nothing yet (ToAssignHC, listed in NotAssigned). The
// counters follow the Daily Attn Summary of the Python application: Scheduled is what its "Schedule HC" is, PlacedHC its
// "Actual HC", LeaveHC its "MC HC" plus "Other Leave HC", ToAssignHC its "To Be Recorded HC".
//
// A member belongs to the shift of their record when it has a machine, or of their pending line; otherwise to the shift
// the calendar gives them. The scan only helps to assign: a person who already has a task, made on another page of
// the Python application or earlier by this one, needs no scan and is never in NotAssigned.
func (s *Service) shiftPeople(d *dayData, pending map[string]Entry, bv *BoardView) {
	var placedHC, leaveHC, toAssignHC float64
	for _, p := range d.dir.Roster(d.key.Process) {
		var rec *DayRecord
		var work, leave []HCEntry
		var leaveOfDay float64
		if r, ok := d.recByEE[p.EE]; ok {
			rec = &r
			work, leave, leaveOfDay = s.splitEntries(r.Entries)
		}
		line, isPlacing := placing(pending, p.EE)

		shift := ""
		switch {
		case isPlacing:
			shift = d.key.Shift
		case len(work) > 0:
			shift = NormShift(firstNonEmpty(rec.Actual, rec.Schedule))
		}
		if shift == "" {
			shift = d.calendarShift(p, rec)
		}
		if shift != d.key.Shift {
			continue
		}
		bv.Totals.Scheduled++

		if isPlacing && leaveOfDay >= 1-eps {
			leave, leaveOfDay = nil, 0 // Confirm replaces a full-day leave that the shift leader overrides
		}
		switch {
		case isPlacing:
			placedHC += line.HC
		case len(work) > 0:
			placedHC += sumHC(work)
		case leaveOfDay >= 1-eps:
			// on leave for the whole day
		default:
			left := availHC(leaveOfDay)
			toAssignHC += left
			bv.NotAssigned = append(bv.NotAssigned, PersonView{EE: p.EE, Name: p.Name, Shift: p.Shift, Schedule: d.schedule(p.Shift), Photo: s.photo(p.EE), HC: left})
		}

		if leaveOfDay > 0 {
			leaveHC += leaveOfDay
			v := LeaveView{EE: p.EE, Name: p.Name, Photo: s.photo(p.EE), HC: leaveOfDay}
			for _, h := range leave {
				v.Items = append(v.Items, LeaveItem{Code: shortLeave(h.Machine), Name: strings.TrimSpace(h.Machine), HC: h.HC})
			}
			bv.Leave = append(bv.Leave, v)
		}

		if !isPlacing {
			var where []string
			var awayHC float64
			for _, h := range work {
				if _, ok := d.resolve(h, p.Process); !ok {
					where = append(where, entryLabel(h))
					awayHC += h.HC
				}
			}
			if len(where) > 0 {
				bv.Elsewhere = append(bv.Elsewhere, ElsewhereView{EE: p.EE, Name: p.Name, Photo: s.photo(p.EE), HC: round2(awayHC), Where: where})
			}
		}
	}
	bv.Totals.PlacedHC, bv.Totals.LeaveHC, bv.Totals.ToAssignHC = round2(placedHC), round2(leaveHC), round2(toAssignHC)
	if bv.NotAssigned == nil {
		bv.NotAssigned = []PersonView{}
	}
	if bv.Leave == nil {
		bv.Leave = []LeaveView{}
	}
	if bv.Elsewhere == nil {
		bv.Elsewhere = []ElsewhereView{}
	}
}

// boardCandidates populates the two supplementary unassigned candidate lists of this process:
//   - OtherShift: roster members scheduled for the opposite AM/PM shift today who have no task yet;
//   - OffDay: roster members whose calendar day type today is a non-empty non-AM/PM code (O, R, PHO, PHR, PH, ...)
//     who have no task yet; those who worked all 6 previous consecutive days in t_daily_attn_data are marked Blocked
//     and listed after the selectable members so leaders cannot assign a 7th consecutive work day.
func (s *Service) boardCandidates(ctx context.Context, d *dayData, pending, otherPending map[string]Entry, bv *BoardView) error {
	other := oppositeShift(d.key.Shift)
	var offPending []CandidateView
	for _, p := range d.dir.Roster(d.key.Process) {
		var rec *DayRecord
		var work []HCEntry
		var leaveOfDay float64
		if r, ok := d.recByEE[p.EE]; ok {
			rec = &r
			work, _, leaveOfDay = s.splitEntries(r.Entries)
		}
		if len(work) > 0 {
			continue
		}
		if _, inDraft := pending[p.EE]; inDraft {
			continue
		}
		if _, inOther := placing(otherPending, p.EE); inOther {
			continue
		}
		if leaveOfDay >= 1-eps {
			continue
		}
		sched := strings.TrimSpace(d.schedule(p.Shift))
		if sched == "" && rec != nil {
			sched = strings.TrimSpace(rec.Schedule)
		}
		if sched == "" {
			continue
		}
		calShift := NormShift(sched)
		if calShift == d.key.Shift {
			continue
		}
		cand := CandidateView{EE: p.EE, Name: p.Name, Shift: p.Shift, Schedule: sched, Photo: s.photo(p.EE), HC: availHC(leaveOfDay)}
		if calShift == other {
			bv.OtherShift = append(bv.OtherShift, cand)
		} else {
			offPending = append(offPending, cand)
		}
	}
	if len(offPending) > 0 {
		streaks, err := s.rosterStreaks(ctx, d)
		if err != nil {
			return err
		}
		var eligible, blocked []CandidateView
		for _, c := range offPending {
			c.ConsecutiveDays = streaks[c.EE]
			if c.ConsecutiveDays >= maxConsecutiveWorkDays {
				c.Blocked = true
				blocked = append(blocked, c)
			} else {
				eligible = append(eligible, c)
			}
		}
		bv.OffDay = append(eligible, blocked...)
	}
	if bv.OtherShift == nil {
		bv.OtherShift = []CandidateView{}
	}
	if bv.OffDay == nil {
		bv.OffDay = []CandidateView{}
	}
	return nil
}

// calendarShift is the AM/PM session the calendar gives the person on the date. When the calendar has no day type for
// their shift code, the day type stored in their record is used. It is "" for a day off and for an unknown shift code.
func (d *dayData) calendarShift(p Person, rec *DayRecord) string {
	if sc := strings.TrimSpace(d.schedule(p.Shift)); sc != "" {
		return NormShift(sc)
	}
	if rec != nil {
		return NormShift(rec.Schedule)
	}
	return ""
}

func sumHC(entries []HCEntry) float64 {
	var sum float64
	for _, h := range entries {
		sum += h.HC
	}
	return sum
}

// entryLabel says where a share of the day is: "<process> / <machine>", or only the machine when the entry has no
// process.
func entryLabel(h HCEntry) string {
	machine := firstNonEmpty(strings.TrimSpace(h.Machine), strings.TrimSpace(h.MachineID))
	if p := strings.TrimSpace(h.Process); p != "" {
		return p + " / " + machine
	}
	return machine
}
