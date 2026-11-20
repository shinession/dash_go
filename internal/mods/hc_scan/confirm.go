package hcscan

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/i18n"
)

// Confirm saves the pending lines to t_daily_attn_data. It is the only place where the module writes to the
// database, and it follows the write rules of the Python Area Assign page: leave entries of the record are kept,
// the remaining HC goes to the machine, the built-in OT is recalculated and the additional OT is kept.
//
// Lines with warnings (not on the roster, shift mismatch, full-day leave, over budget, no MTR record) are only
// saved when the caller acknowledges them; without ack the result lists them in NeedsAck and nothing is written.
func (s *Service) Confirm(ctx context.Context, u auth.User, k Key, ack bool) (*ConfirmResult, error) {
	if err := s.check(ctx, u, k, true); err != nil { // fresh permission check before a write
		return nil, err
	}
	today := ProductionDate(s.Now(), s.Settings.ProductionDayOffsetHours)
	if MonthLocked(k.Date, today) {
		return nil, newError(409, "month_locked")
	}
	unlock := s.locks.lock(k.id())
	defer unlock()

	pending, err := s.Drafts.Entries(ctx, k)
	if err != nil {
		return nil, s.internal("read draft", err)
	}
	d, err := s.loadDay(ctx, k)
	if err != nil {
		return nil, s.internal("load session data", err)
	}
	used := s.usage(d, pending)

	res := &ConfirmResult{Written: []WrittenView{}, Skipped: []SkippedView{}, NeedsAck: []AckView{}}
	var toWrite []Entry
	for _, e := range pending {
		switch {
		case (e.Source == SourceAuto || e.Source == SourceManual) && e.MachineID != "":
			toWrite = append(toWrite, e)
		case e.Source == SourceNone || e.MachineID == "":
			res.Unassigned++
		}
		// existing lines have nothing to save: the database already holds their record
	}
	sort.Slice(toWrite, func(i, j int) bool { return toWrite[i].EE < toWrite[j].EE })
	if len(toWrite) == 0 {
		return nil, newError(409, "nothing_to_save")
	}
	for _, e := range toWrite {
		v := s.entryView(ctx, d, e, used)
		if v.Warn {
			res.NeedsAck = append(res.NeedsAck, AckView{EE: e.EE, Name: e.Name, Machine: e.MachineName, Flags: warnOnly(v.Flags)})
		}
	}
	if len(res.NeedsAck) > 0 && !ack {
		return res, nil
	}
	res.NeedsAck = []AckView{}

	ees := make([]string, 0, len(toWrite))
	for _, e := range toWrite {
		ees = append(ees, e.EE)
	}
	written := map[string]DayRecord{}
	skipped := map[string]string{}
	rows, err := s.Repo.WriteDay(ctx, k.Date, ees, u.Name, func(current map[string]DayRecord) ([]DayRecord, error) {
		var out []DayRecord
		for _, e := range toWrite {
			row, why := s.buildRow(d, k, e, current)
			if why.Key != "" {
				skipped[e.EE] = say(ctx, why, "")
				continue
			}
			out = append(out, row)
		}
		return out, nil
	})
	if err != nil {
		s.Log.Error("hc_scan save failed", "process", k.Process, "date", k.Date.Format("2006-01-02"), "shift", k.Shift, "lines", len(toWrite), "error", err)
		return nil, newError(500, "save_failed")
	}
	for _, r := range rows {
		written[r.EE] = r
	}

	now := s.Now()
	var confirmedEEs []string
	for _, e := range toWrite {
		if r, ok := written[e.EE]; ok {
			res.Written = append(res.Written, WrittenView{EE: e.EE, Name: e.Name, Machine: e.MachineName, HC: workHC(r, s)})
			confirmedEEs = append(confirmedEEs, e.EE)
			continue
		}
		res.Skipped = append(res.Skipped, SkippedView{EE: e.EE, Name: e.Name, Reason: skipped[e.EE]})
	}
	if len(rows) > 0 && s.ChangeLog != nil {
		if err := s.ChangeLog.Append(u.Name, rows); err != nil {
			// the rows are committed; only the Python caches may show the change late
			s.Log.Warn("hc_scan change log failed", "error", err)
			detail := err.Error()
			if errors.Is(err, ErrNoChangeLogFolder) {
				detail = i18n.T(ctx, "hc.msg.log_no_folder", nil)
			}
			res.LogWarning = i18n.T(ctx, "hc.msg.log_warning", i18n.P{"detail": detail})
		}
	}
	scans, _ := s.Drafts.Scans(ctx, k)
	if err := s.Drafts.DeleteEntries(ctx, k, confirmedEEs...); err != nil {
		s.Log.Error("hc_scan draft cleanup failed", "error", err)
	}
	for _, ee := range confirmedEEs {
		rec := scans[ee]
		if rec.ScannedAt.IsZero() {
			rec.ScannedAt = now
		}
		rec.ConfirmedAt = &now
		if err := s.Drafts.PutScan(ctx, k, ee, rec); err != nil {
			s.Log.Error("hc_scan scan log update failed", "ee", ee, "error", err)
		}
	}
	s.Log.Info("hc_scan saved", "process", k.Process, "date", k.Date.Format("2006-01-02"), "shift", k.Shift, "by", u.EE, "written", len(res.Written), "skipped", len(res.Skipped))
	return res, nil
}

// buildRow creates the t_daily_attn_data row of one pending line from the freshly locked database state. It
// returns a reason (a message of the catalog) instead of a row when the line must not be saved.
func (s *Service) buildRow(d *dayData, k Key, e Entry, current map[string]DayRecord) (DayRecord, Msg) {
	m, ok := d.machines.byID[e.MachineID]
	if !ok {
		return DayRecord{}, msg("hc.skip.machine_gone")
	}
	cur, hasCur := current[e.EE]
	var work, leave []HCEntry
	var leaveHC float64
	if hasCur {
		work, leave, leaveHC = s.splitEntries(cur.Entries)
	}
	if e.Source == SourceAuto && len(work) > 0 {
		return DayRecord{}, msg("hc.skip.changed_record", "machine", work[0].Machine)
	}
	workHC := availHC(leaveHC)
	if leaveHC >= 1-eps {
		if !hasFlag(e.Flags, FlagFullDayLeave) {
			return DayRecord{}, msg("hc.skip.changed_leave")
		}
		leave = nil // the shift leader confirmed that the person is working
	}

	shift, schedule := e.Shift, e.Schedule
	if p, ok := d.dir.Lookup(e.EE); ok && p.Shift != "" {
		shift = p.Shift
		if sc := d.schedule(p.Shift); sc != "" {
			schedule = sc
		}
	}
	if shift == "" && hasCur {
		shift = cur.Shift
	}
	if schedule == "" && hasCur {
		schedule = cur.Schedule
	}
	if shift == "" || schedule == "" {
		return DayRecord{}, msg("hc.skip.no_shift")
	}

	entries := make([]HCEntry, 0, len(leave)+1)
	entries = append(entries, leave...)
	entries = append(entries, HCEntry{Process: k.Process, Machine: m.Name, HC: workHC})
	row := DayRecord{
		UID: fmt.Sprintf("%s_%s", e.EE, k.Date.Format("20060102")), EE: e.EE, Date: k.Date,
		Shift: shift, Schedule: schedule, Actual: k.Shift, Entries: entries,
	}
	if hasCur {
		row.Remark, row.AddtOT = cur.Remark, cur.AddtOT
	}
	row.BuildIn = CalBuildInOT(shift, schedule, workHC)
	row.TotalOT = round2(row.BuildIn + row.AddtOT)
	return row, Msg{}
}

func workHC(r DayRecord, s *Service) float64 {
	work, _, _ := s.splitEntries(r.Entries)
	var sum float64
	for _, h := range work {
		sum += h.HC
	}
	return round2(sum)
}

func warnOnly(flags []FlagView) []FlagView {
	out := []FlagView{}
	for _, f := range flags {
		if f.Severity == SeverityWarn {
			out = append(out, f)
		}
	}
	return out
}

func hasFlag(flags []Flag, code string) bool {
	for _, f := range flags {
		if f.Code == code {
			return true
		}
	}
	return false
}
