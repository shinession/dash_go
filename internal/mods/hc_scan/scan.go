package hcscan

import (
	"context"
	"strconv"
	"strings"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/i18n"
)

// Scan handles one badge read: it finds the employee, assigns a machine by the MTR rules and stores the result in
// the draft. Nothing is written to the database.
func (s *Service) Scan(ctx context.Context, u auth.User, k Key, input string) (*ScanResult, error) {
	if err := s.check(ctx, u, k, false); err != nil {
		return nil, err
	}
	ee, ok := s.ids.Extract(input)
	if !ok {
		return nil, newError(400, "invalid_id", i18n.P{"input": clip(strings.TrimSpace(input), 40)})
	}
	unlock := s.locks.lock(k.id())
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
	used := s.usage(d, pending)

	if e, ok := pending[ee]; ok {
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

// useExisting shows the machine of an existing record: the biggest share of the day on a machine of this process, or,
// when the person works only on machines of other processes, the name of the biggest share.
func (s *Service) useExisting(d *dayData, e *Entry) {
	var best, bestHere HCEntry
	var hereMachine Machine
	haveHere := false
	for _, h := range e.Existing {
		e.HC += h.HC
		if h.HC > best.HC {
			best = h
		}
		if m, ok := d.resolve(h, e.Process); ok && (!haveHere || h.HC > bestHere.HC) {
			bestHere, hereMachine, haveHere = h, m, true
		}
	}
	e.HC = round2(e.HC)
	if haveHere {
		e.MachineID, e.MachineName, e.Level = hereMachine.ID, hereMachine.Name, d.levelOn(e.EE, hereMachine)
	} else {
		e.MachineName = best.Machine // a machine outside this process
	}
}

// autoAssign picks the best machine by the MTR rules (see RankMachines).
func (s *Service) autoAssign(ctx context.Context, d *dayData, e *Entry, p Person, used map[string]float64) error {
	e.HC = availHC(e.LeaveHC)
	history, err := s.Repo.History(ctx, []string{e.EE}, d.key.Date.AddDate(0, 0, -s.Settings.RecentDays), d.key.Date)
	if err != nil {
		return err
	}
	recent := s.recentDays(d, history, p.Process)
	if row, why, isLeader := s.leaderPlacement(d, p, history); isLeader {
		return s.placeLeader(d, e, row, why, recent, used)
	}
	remaining := make(map[string]float64, len(d.machines.machines))
	for _, m := range d.machines.machines {
		remaining[m.ID] = m.Budget - used[m.ID]
	}
	ranked := RankMachines(d.machines.machines, func(m Machine) int { return d.levelOn(e.EE, m) }, recent, remaining, e.HC)
	if len(ranked) == 0 {
		e.Source, e.MachineID, e.MachineName, e.Level, e.RecentDays = SourceNone, "", "", 0, 0
		e.ReasonMsg = s.noMachineReason(d, e.EE, e.HC)
		return nil
	}
	best := ranked[0]
	e.Source, e.MachineID, e.MachineName, e.Level, e.RecentDays = SourceAuto, best.Machine.ID, best.Machine.Name, best.Level, best.Recent
	level := strconv.Itoa(best.Level)
	if best.Recent > 0 {
		e.ReasonMsg = msg("hc.reason.auto_recent", "level", level, "machine", best.Machine.Name, "days", strconv.Itoa(best.Recent), "window", strconv.Itoa(s.Settings.RecentDays))
	} else {
		e.ReasonMsg = msg("hc.reason.auto", "level", level, "machine", best.Machine.Name)
	}
	return nil
}

// placeLeader puts a leader on the leader row, or explains why that is not possible.
func (s *Service) placeLeader(d *dayData, e *Entry, row Machine, why Msg, recent map[string]int, used map[string]float64) error {
	if row.Budget > 0 && row.Budget-used[row.ID]+eps < e.HC {
		e.Source, e.MachineID, e.MachineName, e.Level, e.RecentDays = SourceNone, "", "", 0, 0
		full := Msg{Key: why.Key + "_full", Args: map[string]string{"row": row.Name, "used": trimFloat(used[row.ID]), "budget": trimFloat(row.Budget)}}
		for k, v := range why.Args {
			full.Args[k] = v
		}
		e.ReasonMsg = full
		return nil
	}
	e.Source, e.MachineID, e.MachineName, e.Level, e.RecentDays = SourceAuto, row.ID, row.Name, d.levelOn(e.EE, row), recent[row.ID]
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

// Assign lets the shift leader change the machine of a pending line. An empty machine id removes the assignment.
func (s *Service) Assign(ctx context.Context, u auth.User, k Key, ee, machineID string) (*EntryView, error) {
	if err := s.check(ctx, u, k, false); err != nil {
		return nil, err
	}
	ee = strings.ToUpper(strings.TrimSpace(ee))
	unlock := s.locks.lock(k.id())
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
	if machineID == "" {
		e.Source, e.MachineID, e.MachineName, e.Level, e.RecentDays, e.HC = SourceNone, "", "", 0, 0, availHC(e.LeaveHC)
		e.ReasonMsg = msg("hc.reason.removed")
	} else {
		m, ok := d.machines.byID[machineID]
		if !ok {
			return nil, newError(400, "bad_machine", i18n.P{"machine": clip(machineID, 40), "process": k.Process})
		}
		history, err := s.Repo.History(ctx, []string{e.EE}, k.Date.AddDate(0, 0, -s.Settings.RecentDays), k.Date)
		if err != nil {
			return nil, s.internal("read assignment history", err)
		}
		e.Source, e.MachineID, e.MachineName, e.HC = SourceManual, m.ID, m.Name, availHC(e.LeaveHC)
		e.Level, e.RecentDays = d.levelOn(e.EE, m), s.recentDays(d, history, e.Process)[m.ID]
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
	unlock := s.locks.lock(k.id())
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
	unlock := s.locks.lock(k.id())
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
