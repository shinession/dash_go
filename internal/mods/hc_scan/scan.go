package hcscan

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/i18n"
)

const prevWorkDayLookbackDays = 30

// Scan handles one badge read: it finds the employee, assigns a machine by the MTR rules and stores the result in
// the draft. Nothing is written to the database.
func (s *Service) Scan(ctx context.Context, u auth.User, k Key, input string) (*ScanResult, error) {
	return s.ScanTo(ctx, u, k, input, "")
}

// ScanTo handles one badge read, optionally assigning directly to targetMachineID when the shift leader selected a
// target machine on the board. Nothing is written to the database.
func (s *Service) ScanTo(ctx context.Context, u auth.User, k Key, input, targetMachineID string) (*ScanResult, error) {
	if err := s.check(ctx, u, k, false); err != nil {
		return nil, err
	}
	ee, ok := s.ids.Extract(input)
	if !ok {
		return nil, newError(400, "invalid_id", i18n.P{"input": clip(strings.TrimSpace(input), 40)})
	}
	unlock, err := s.Locker.Lock(ctx, k.id())
	if err != nil {
		return nil, s.internal("lock session", err)
	}
	defer unlock()

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
	var targetMachine Machine
	hasTarget := false
	if tid := strings.TrimSpace(targetMachineID); tid != "" {
		m, ok := d.machines.ByID(tid)
		if !ok {
			return nil, newError(400, "bad_machine", i18n.P{"machine": clip(tid, 40), "process": k.Process})
		}
		targetMachine, hasTarget = m, true
	}
	used := s.usage(d, pending)

	if e, ok := pending[ee]; ok {
		if hasTarget && (e.Source == SourceNone || !e.HasMachine()) {
			now := s.Now()
			e.HC = availHC(e.LeaveHC)
			e.Source = SourceManual
			e.MachineIDs, e.MachineNames = []string{targetMachine.ID}, []string{targetMachine.Name}
			e.MachineID, e.MachineName = targetMachine.ID, targetMachine.Name
			e.Level, e.RecentDays = d.levelOn(e.EE, targetMachine), 0
			e.ReasonMsg = msg("hc.reason.target_machine", "machine", targetMachine.Name, "name", u.Name)
			e.ScannedAt, e.ScannedBy = now, u.Name
			if err := s.Drafts.PutEntry(ctx, k, e); err != nil {
				return nil, s.internal("save draft", err)
			}
			pending[ee] = e
			used = s.usage(d, pending)
			return &ScanResult{Status: "assigned", Message: s.scanMessage(ctx, e, "assigned"), Entry: s.entryView(ctx, d, e, used)}, nil
		}
		return &ScanResult{Status: "duplicate", Message: i18n.T(ctx, "hc.msg.scan_duplicate", i18n.P{"name": e.Name, "time": e.ScannedAt.Local().Format("15:04")}), Entry: s.entryView(ctx, d, e, used)}, nil
	}

	person, found := d.dir.Lookup(ee)
	if !found {
		row, err := s.Repo.MESPerson(ctx, ee)
		if err != nil {
			return nil, s.internal("look up employee", err)
		}
		if row == nil {
			return nil, newError(404, "unknown_employee", i18n.P{"ee": ee})
		}
		person, found = MESPerson(*row), true
	}
	var rec *DayRecord
	if r, ok := d.recByEE[ee]; ok {
		rec = &r
	}
	now := s.Now()
	e, err := s.baseEntry(u, d, person, rec, now)
	if err != nil {
		return nil, err
	}

	status := "assigned"
	switch {
	case scans[ee].ConfirmedAt != nil && len(e.Existing) > 0:
		e.Source, e.ReasonMsg = SourceExisting, msg("hc.reason.saved", "time", scans[ee].ConfirmedAt.Local().Format("15:04"))
		s.useExisting(d, &e)
		status = "saved"
	case len(e.Existing) > 0:
		e.Source, e.ReasonMsg = SourceExisting, msg("hc.reason.existing")
		s.useExisting(d, &e)
		status = "existing"
	case hasTarget:
		e.HC = availHC(e.LeaveHC)
		e.Source = SourceManual
		e.MachineIDs, e.MachineNames = []string{targetMachine.ID}, []string{targetMachine.Name}
		e.MachineID, e.MachineName = targetMachine.ID, targetMachine.Name
		e.Level, e.RecentDays = d.levelOn(e.EE, targetMachine), 0
		e.ReasonMsg = msg("hc.reason.target_machine", "machine", targetMachine.Name, "name", u.Name)
	default:
		if err := s.autoAssign(ctx, d, &e, person, used); err != nil {
			return nil, s.internal("read assignment history", err)
		}
		if e.Source == SourceNone {
			status = "unassigned"
		}
	}

	if status == "saved" {
		return &ScanResult{Status: status, Message: i18n.T(ctx, "hc.msg.scan_saved", i18n.P{"name": e.Name}), Entry: s.entryView(ctx, d, e, used)}, nil
	}
	if err := s.Drafts.PutEntry(ctx, k, e); err != nil {
		return nil, s.internal("save draft", err)
	}
	if err := s.Drafts.PutScan(ctx, k, ee, ScanRecord{ScannedAt: now, ScannedBy: u.Name}); err != nil {
		return nil, s.internal("save scan log", err)
	}
	pending[ee] = e
	used = s.usage(d, pending)
	return &ScanResult{Status: status, Message: s.scanMessage(ctx, e, status), Entry: s.entryView(ctx, d, e, used)}, nil
}

// baseEntry fills everything about a scanned person that does not depend on the machine choice.
func (s *Service) baseEntry(u auth.User, d *dayData, p Person, rec *DayRecord, now time.Time) (Entry, error) {
	shiftCode := p.Shift
	if shiftCode == "" && rec != nil {
		shiftCode = rec.Shift
	}
	if shiftCode == "" {
		return Entry{}, newError(422, "no_shift", i18n.P{"name": p.Name, "ee": p.EE})
	}
	schedule := d.schedule(shiftCode)
	if schedule == "" && rec != nil {
		schedule = rec.Schedule
	}
	e := Entry{
		EE: p.EE, Name: p.Name, Process: p.Process, Source: SourceNone, Shift: shiftCode, Schedule: schedule,
		ScannedAt: now, ScannedBy: u.Name,
	}
	var work, leave []HCEntry
	if rec != nil {
		work, leave, e.LeaveHC = s.splitEntries(rec.Entries)
		e.Existing = work
	}
	e.Flags = s.staticFlags(d, p, rec, schedule, leave, e.LeaveHC, work)
	return e, nil
}

// useExisting shows the machine(s) of an existing record: all machines of this process in the record, or,
// when the person works only on machines of other processes, the name of the biggest share.
func (s *Service) useExisting(d *dayData, e *Entry) {
	var best HCEntry
	var ids, names []string
	seen := map[string]bool{}
	bestHereHC := -1.0
	var bestHere Machine
	for _, h := range e.Existing {
		e.HC += h.HC
		if h.HC > best.HC {
			best = h
		}
		if m, ok := d.resolve(h, e.Process); ok {
			if !seen[m.ID] {
				seen[m.ID] = true
				ids = append(ids, m.ID)
				names = append(names, m.Name)
			}
			if h.HC > bestHereHC {
				bestHereHC, bestHere = h.HC, m
			}
		}
	}
	e.HC = round2(e.HC)
	if len(ids) > 0 {
		e.MachineIDs, e.MachineNames = ids, names
		e.MachineID, e.MachineName, e.Level = ids[0], strings.Join(names, ", "), d.levelOn(e.EE, bestHere)
	} else {
		e.MachineIDs, e.MachineNames = nil, nil
		e.MachineName = firstNonEmpty(best.Machine, best.MachineID) // a machine outside this process
	}
}

// autoAssign picks the best machine: leaders go to the leader row; otherwise the employee's previous working day's
// machine (requiring at least MTR Lv1 and remaining budget) takes priority, falling back to RankMachines (at least
// MTR Lv1) for newcomers or when the previous working day's machine is not eligible.
func (s *Service) autoAssign(ctx context.Context, d *dayData, e *Entry, p Person, used map[string]float64) error {
	e.HC = availHC(e.LeaveHC)
	lookback := s.Settings.RecentDays
	if lookback < prevWorkDayLookbackDays {
		lookback = prevWorkDayLookbackDays
	}
	history, err := s.Repo.History(ctx, []string{e.EE}, d.key.Date.AddDate(0, 0, -lookback), d.key.Date)
	if err != nil {
		return err
	}
	recentCutoff := d.key.Date.AddDate(0, 0, -s.Settings.RecentDays)
	var recentHistory []DayRecord
	for _, r := range history {
		if !r.Date.Before(recentCutoff) {
			recentHistory = append(recentHistory, r)
		}
	}
	recent := s.recentDays(d, recentHistory, p.Process)
	if row, why, isLeader := s.leaderPlacement(d, p, recentHistory); isLeader {
		return s.placeLeader(d, e, row, why, recent, used)
	}
	remaining := make(map[string]float64, len(d.machines.machines))
	for _, m := range d.machines.machines {
		remaining[m.ID] = m.Budget - used[m.ID]
	}
	levelOf := func(m Machine) int { return d.levelOn(e.EE, m) }
	if prevDate, prevCands := s.prevWorkDay(d, history, p.Process); len(prevCands) > 0 {
		if best, ok := PickPrevWorkMachine(prevCands, levelOf, recent, remaining, e.HC); ok {
			e.Source, e.MachineID, e.MachineName, e.Level, e.RecentDays = SourceAuto, best.Machine.ID, best.Machine.Name, best.Level, best.Recent
			e.MachineIDs, e.MachineNames = []string{best.Machine.ID}, []string{best.Machine.Name}
			e.ReasonMsg = msg("hc.reason.auto_prev_workday", "date", prevDate.Format("2006-01-02"), "machine", best.Machine.Name, "level", strconv.Itoa(best.Level))
			return nil
		}
	}
	ranked := RankMachines(d.machines.machines, levelOf, recent, remaining, e.HC)
	if len(ranked) == 0 {
		e.Source, e.MachineID, e.MachineName, e.MachineIDs, e.MachineNames, e.Level, e.RecentDays = SourceNone, "", "", nil, nil, 0, 0
		e.ReasonMsg = s.noMachineReason(d, e.EE, e.HC)
		return nil
	}
	best := ranked[0]
	e.Source, e.MachineID, e.MachineName, e.Level, e.RecentDays = SourceAuto, best.Machine.ID, best.Machine.Name, best.Level, best.Recent
	e.MachineIDs, e.MachineNames = []string{best.Machine.ID}, []string{best.Machine.Name}
	level := strconv.Itoa(best.Level)
	if best.Recent > 0 {
		e.ReasonMsg = msg("hc.reason.auto_recent", "level", level, "machine", best.Machine.Name, "days", strconv.Itoa(best.Recent), "window", strconv.Itoa(s.Settings.RecentDays))
	} else {
		e.ReasonMsg = msg("hc.reason.auto", "level", level, "machine", best.Machine.Name)
	}
	return nil
}

// prevWorkDay finds the employee's most recent working day in history (skipping leave-only or empty records) and
// returns the machines of this process worked on that day with their HC shares.
func (s *Service) prevWorkDay(d *dayData, history []DayRecord, personProcess string) (time.Time, []PrevWorkCandidate) {
	byDate := append([]DayRecord(nil), history...)
	sort.SliceStable(byDate, func(i, j int) bool { return byDate[i].Date.After(byDate[j].Date) })
	for _, rec := range byDate {
		work, _, _ := s.splitEntries(rec.Entries)
		if len(work) == 0 {
			continue // leave-only day is not a working day
		}
		var out []PrevWorkCandidate
		idxByID := map[string]int{}
		for _, h := range work {
			m, ok := d.resolve(h, personProcess)
			if !ok {
				continue
			}
			if idx, seen := idxByID[m.ID]; seen {
				out[idx].Share += h.HC
			} else {
				idxByID[m.ID] = len(out)
				out = append(out, PrevWorkCandidate{Machine: m, Share: h.HC})
			}
		}
		return rec.Date, out
	}
	return time.Time{}, nil
}

// placeLeader puts a leader on the leader row, or explains why that is not possible.
func (s *Service) placeLeader(d *dayData, e *Entry, row Machine, why Msg, recent map[string]int, used map[string]float64) error {
	if row.Budget > 0 && row.Budget-used[row.ID]+eps < e.HC {
		e.Source, e.MachineID, e.MachineName, e.MachineIDs, e.MachineNames, e.Level, e.RecentDays = SourceNone, "", "", nil, nil, 0, 0
		full := Msg{Key: why.Key + "_full", Args: map[string]string{"row": row.Name, "used": trimFloat(used[row.ID]), "budget": trimFloat(row.Budget)}}
		for k, v := range why.Args {
			full.Args[k] = v
		}
		e.ReasonMsg = full
		return nil
	}
	e.Source, e.MachineID, e.MachineName, e.Level, e.RecentDays = SourceAuto, row.ID, row.Name, d.levelOn(e.EE, row), recent[row.ID]
	e.MachineIDs, e.MachineNames = []string{row.ID}, []string{row.Name}
	e.ReasonMsg = why
	return nil
}

func (s *Service) noMachineReason(d *dayData, ee string, need float64) Msg {
	mapped, qualified := 0, 0
	for _, m := range d.machines.machines {
		if !m.Routine() || m.CIMID == "" {
			continue
		}
		mapped++
		if d.levelOn(ee, m) >= 1 {
			qualified++
		}
	}
	switch {
	case mapped == 0:
		return msg("hc.reason.no_mapping", "process", d.key.Process)
	case qualified == 0:
		return msg("hc.reason.no_mtr", "process", d.key.Process)
	default:
		return msg("hc.reason.all_full", "count", strconv.Itoa(qualified), "need", trimFloat(need))
	}
}

func (s *Service) scanMessage(ctx context.Context, e Entry, status string) string {
	switch status {
	case "assigned":
		return i18n.T(ctx, "hc.msg.scan_assigned", i18n.P{"name": e.Name, "machine": e.MachineName})
	case "existing":
		if strings.TrimSpace(e.MachineName) == "" {
			return i18n.T(ctx, "hc.msg.scan_existing_record", i18n.P{"name": e.Name})
		}
		return i18n.T(ctx, "hc.msg.scan_existing", i18n.P{"name": e.Name, "machine": e.MachineName})
	default:
		return i18n.T(ctx, "hc.msg.scan_unassigned", i18n.P{"name": e.Name})
	}
}

// Assign lets the shift leader set a single machine on a pending line. An empty machine id removes the assignment.
func (s *Service) Assign(ctx context.Context, u auth.User, k Key, ee, machineID string) (*EntryView, error) {
	if strings.TrimSpace(machineID) == "" {
		return s.AssignMany(ctx, u, k, ee, nil)
	}
	return s.AssignMany(ctx, u, k, ee, []string{machineID})
}

// AssignMany lets the shift leader set zero, one or multiple machines, activities and/or leave options on a pending
// line. When multiple are selected, the person's available HC is split evenly across them (e.g. 0.5 each for 2,
// 0.3333333333 for 3). An empty slice removes the assignment.
func (s *Service) AssignMany(ctx context.Context, u auth.User, k Key, ee string, machineIDs []string) (*EntryView, error) {
	if err := s.check(ctx, u, k, false); err != nil {
		return nil, err
	}
	ee = strings.ToUpper(strings.TrimSpace(ee))
	unlock, err := s.Locker.Lock(ctx, k.id())
	if err != nil {
		return nil, s.internal("lock session", err)
	}
	defer unlock()
	pending, err := s.Drafts.Entries(ctx, k)
	if err != nil {
		return nil, s.internal("read draft", err)
	}
	e, ok := pending[ee]
	if !ok {
		return nil, newError(404, "not_in_draft", i18n.P{"ee": ee})
	}
	d, err := s.loadDay(ctx, k)
	if err != nil {
		return nil, s.internal("load session data", err)
	}
	wantedMachines := map[string]bool{}
	wantedLeaves := map[string]bool{}
	for _, raw := range machineIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := d.machines.byID[id]; ok {
			wantedMachines[id] = true
			continue
		}
		if opt, ok := s.resolveLeaveOptionID(id); ok {
			wantedLeaves[opt] = true
			continue
		}
		return nil, newError(400, "bad_machine", i18n.P{"machine": clip(id, 40), "process": k.Process})
	}
	// keep selected machines in display_seq order and leave options in config order for deterministic labels and storage
	var chosenMachines []Machine
	for _, m := range d.machines.machines {
		if wantedMachines[m.ID] {
			chosenMachines = append(chosenMachines, m)
		}
	}
	var chosenLeaves []string
	for _, opt := range s.leaveOptions() {
		if wantedLeaves[opt] {
			chosenLeaves = append(chosenLeaves, opt)
		}
	}
	if len(chosenMachines) == 0 && len(chosenLeaves) == 0 {
		e.Source, e.MachineID, e.MachineName, e.MachineIDs, e.MachineNames, e.Level, e.RecentDays, e.HC = SourceNone, "", "", nil, nil, 0, 0, availHC(e.LeaveHC)
		e.ReasonMsg = msg("hc.reason.removed")
	} else {
		ids := make([]string, 0, len(chosenMachines)+len(chosenLeaves))
		names := make([]string, 0, len(chosenMachines)+len(chosenLeaves))
		for _, m := range chosenMachines {
			ids = append(ids, m.ID)
			names = append(names, m.Name)
		}
		for _, opt := range chosenLeaves {
			ids = append(ids, leaveOptionID(opt))
			names = append(names, opt)
		}
		e.Source = SourceManual
		e.MachineIDs, e.MachineNames = ids, names
		e.MachineID, e.MachineName = ids[0], strings.Join(names, ", ")
		e.HC = availHC(e.LeaveHC)
		if len(chosenMachines) > 0 {
			history, err := s.Repo.History(ctx, []string{e.EE}, k.Date.AddDate(0, 0, -s.Settings.RecentDays), k.Date)
			if err != nil {
				return nil, s.internal("read assignment history", err)
			}
			e.Level, e.RecentDays = d.levelOn(e.EE, chosenMachines[0]), s.recentDays(d, history, e.Process)[chosenMachines[0].ID]
		} else {
			e.Level, e.RecentDays = -2, 0
		}
		e.ReasonMsg = msg("hc.reason.chosen", "name", u.Name)
	}
	if err := s.Drafts.PutEntry(ctx, k, e); err != nil {
		return nil, s.internal("save draft", err)
	}
	pending[ee] = e
	v := s.entryView(ctx, d, e, s.usage(d, pending))
	return &v, nil
}

// Remove deletes a pending line and forgets the scan, so the person is not assigned again (unless the database has a
// task for them).
func (s *Service) Remove(ctx context.Context, u auth.User, k Key, ee string) error {
	if err := s.check(ctx, u, k, false); err != nil {
		return err
	}
	ee = strings.ToUpper(strings.TrimSpace(ee))
	unlock, err := s.Locker.Lock(ctx, k.id())
	if err != nil {
		return s.internal("lock session", err)
	}
	defer unlock()
	if err := s.Drafts.DeleteEntries(ctx, k, ee); err != nil {
		return s.internal("delete draft line", err)
	}
	if scans, err := s.Drafts.Scans(ctx, k); err == nil {
		if r, ok := scans[ee]; ok && r.ConfirmedAt == nil {
			if err := s.Drafts.DeleteScans(ctx, k, ee); err != nil {
				return s.internal("delete scan record", err)
			}
		}
	}
	return nil
}

// Reset clears every pending line of the session. Lines that were saved stay saved.
func (s *Service) Reset(ctx context.Context, u auth.User, k Key) error {
	if err := s.check(ctx, u, k, false); err != nil {
		return err
	}
	unlock, err := s.Locker.Lock(ctx, k.id())
	if err != nil {
		return s.internal("lock session", err)
	}
	defer unlock()
	pending, err := s.Drafts.Entries(ctx, k)
	if err != nil {
		return s.internal("read draft", err)
	}
	scans, err := s.Drafts.Scans(ctx, k)
	if err != nil {
		return s.internal("read scan log", err)
	}
	var ees, unconfirmed []string
	for ee := range pending {
		ees = append(ees, ee)
	}
	for ee, r := range scans {
		if r.ConfirmedAt == nil {
			unconfirmed = append(unconfirmed, ee)
		}
	}
	if err := s.Drafts.DeleteEntries(ctx, k, ees...); err != nil {
		return s.internal("clear draft", err)
	}
	if err := s.Drafts.DeleteScans(ctx, k, unconfirmed...); err != nil {
		return s.internal("clear scan log", err)
	}
	return nil
}

// internal logs the technical error and returns a message without technical details for the browser.
func (s *Service) internal(what string, err error) *Error {
	s.Log.Error("hc_scan failure", "step", what, "error", err)
	return newError(500, "internal", i18n.P{"step": stepKey(what)})
}

// stepKey is the catalog key that names a step of the service ("read draft" is hc.step.read_draft), so the message
// of a failed request can say what was being done in the language of the page.
func stepKey(what string) string {
	return "hc.step." + strings.ReplaceAll(what, " ", "_")
}
