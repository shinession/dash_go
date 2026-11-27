package hcscan

import (
	"context"
	"fmt"
	"strings"

	"dash_go/internal/core/i18n"
)

// say turns a message into the sentence of the language of the request. A line that a version 0.1.0 draft stored
// has no message, only its English sentence, which is shown as it is.
func say(ctx context.Context, m Msg, legacy string) string {
	if m.Key == "" {
		return legacy
	}
	p := make(i18n.P, len(m.Args))
	for k, v := range m.Args {
		p[k] = v
	}
	return i18n.T(ctx, m.Key, p)
}

// flagViews translates the flags of a line for the page.
func flagViews(ctx context.Context, flags []Flag) []FlagView {
	out := make([]FlagView, 0, len(flags))
	for _, f := range flags {
		out = append(out, FlagView{Code: f.Code, Severity: f.Severity, Text: say(ctx, f.Msg, f.Text)})
	}
	return out
}

// staticFlags are the facts found when a badge is scanned: the person is not on the roster, is not scheduled for
// the session, is on leave or already has a record. They are shown in yellow so the shift leader can decide. The
// texts are in the catalog (hc.flag.*), so they follow the language of the page.
func (s *Service) staticFlags(d *dayData, p Person, rec *DayRecord, schedule string, leave []HCEntry, leaveHC float64, work []HCEntry) []Flag {
	var flags []Flag
	date := d.key.Date.Format("2006-01-02")

	if ok, why := d.dir.InRoster(p, d.key.Process); !ok {
		flags = append(flags, Flag{Code: FlagNotInRoster, Severity: SeverityWarn, Msg: why.with("process", d.key.Process).with("date", date)})
	}

	effective, scheduled := schedule, true
	if rec != nil && NormShift(rec.Actual) != "" {
		effective, scheduled = rec.Actual, false
	}
	switch norm := NormShift(effective); {
	case strings.TrimSpace(effective) == "":
		if strings.TrimSpace(p.Shift) == "" {
			flags = append(flags, Flag{Code: FlagShiftMismatch, Severity: SeverityWarn, Msg: msg("hc.flag.shift_no_schedule_unknown")})
		} else {
			flags = append(flags, Flag{Code: FlagShiftMismatch, Severity: SeverityWarn, Msg: msg("hc.flag.shift_no_schedule", "shift", p.Shift)})
		}
	case norm == "":
		flags = append(flags, Flag{Code: FlagShiftMismatch, Severity: SeverityWarn, Msg: msg("hc.flag.shift_not_scheduled", "day_type", effective)})
	case norm != d.key.Shift:
		key := "hc.flag.shift_recorded"
		if scheduled {
			key = "hc.flag.shift_scheduled"
		}
		flags = append(flags, Flag{Code: FlagShiftMismatch, Severity: SeverityWarn, Msg: msg(key, "norm", norm, "day_type", effective, "shift", d.key.Shift)})
	}

	if leaveHC > 0 {
		names := make([]string, 0, len(leave))
		for _, h := range leave {
			names = append(names, shortLeave(h.Machine)+" "+trimFloat(h.HC))
		}
		if leaveHC >= 1-eps {
			flags = append(flags, Flag{Code: FlagFullDayLeave, Severity: SeverityWarn, Msg: msg("hc.flag.leave_full", "leave", strings.Join(names, ", "))})
		} else {
			flags = append(flags, Flag{Code: FlagPartDayLeave, Severity: SeverityInfo, Msg: msg("hc.flag.leave_part", "leave", strings.Join(names, ", "), "hc", trimFloat(availHC(leaveHC)))})
		}
	}

	if len(work) > 0 {
		parts := make([]string, 0, len(work))
		for _, h := range work {
			name := firstNonEmpty(strings.TrimSpace(h.Machine), strings.TrimSpace(h.MachineID))
			if m, ok := d.resolve(h, p.Process); ok {
				name = strings.TrimSpace(m.Name)
			}
			parts = append(parts, name+" "+trimFloat(h.HC))
		}
		sev := SeverityInfo
		if NormShift(rec.Actual) != "" && NormShift(rec.Actual) != d.key.Shift {
			sev = SeverityWarn
		}
		flags = append(flags, Flag{Code: FlagHasRecord, Severity: sev, Msg: msg("hc.flag.has_record", "records", strings.Join(parts, ", "))})
	}
	return flags
}

// dynamicFlags check the current assignment of a draft line, so they follow every manual change.
func (s *Service) dynamicFlags(d *dayData, e Entry, used map[string]float64) []Flag {
	ids := e.SelectedMachineIDs()
	if e.Source == SourceNone || len(ids) == 0 {
		f := Flag{Code: FlagNoMachine, Severity: SeverityWarn, Msg: e.ReasonMsg, Text: e.Reason}
		if f.Key == "" && strings.TrimSpace(f.Text) == "" {
			f.Msg = msg("hc.flag.no_machine")
		}
		return []Flag{f}
	}
	var flags []Flag
	for _, id := range ids {
		if _, isLeave := s.resolveLeaveOptionID(id); isLeave {
			continue
		}
		m, ok := d.machines.byID[id]
		if !ok {
			continue
		}
		if e.Source != SourceExisting {
			switch {
			case m.Budget > 0 && used[m.ID] > m.Budget+eps:
				flags = append(flags, Flag{Code: FlagOverBudget, Severity: SeverityWarn, Msg: msg("hc.flag.over_budget", "machine", m.Name, "used", trimFloat(used[m.ID]), "budget", trimFloat(m.Budget))})
			case m.Budget <= 0:
				flags = append(flags, Flag{Code: FlagOverBudget, Severity: SeverityInfo, Msg: msg("hc.flag.no_budget", "machine", m.Name)})
			}
		}
		if e.Source == SourceManual && m.Routine() {
			if m.CIMID == "" {
				flags = append(flags, Flag{Code: FlagNoMTR, Severity: SeverityWarn, Msg: msg("hc.flag.no_mtr_mapping", "machine", m.Name)})
			} else if d.levelOn(e.EE, m) < 1 {
				flags = append(flags, Flag{Code: FlagNoMTR, Severity: SeverityWarn, Msg: msg("hc.flag.no_mtr_record", "name", e.Name, "machine", m.Name)})
			}
		}
	}
	return flags
}

func shortLeave(name string) string {
	if i := strings.Index(name, " - "); i > 0 {
		return name[:i]
	}
	return name
}

func trimFloat(v float64) string {
	s := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
	if s == "" {
		return "0"
	}
	return s
}

func hasWarn(flags []Flag) bool {
	for _, f := range flags {
		if f.Severity == SeverityWarn {
			return true
		}
	}
	return false
}
