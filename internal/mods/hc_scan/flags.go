package hcscan

import (
	"fmt"
	"strings"
)

// staticFlags are the facts found when a badge is scanned: the person is not on the roster, is not scheduled for
// the session, is on leave or already has a record. They are shown in yellow so the shift leader can decide.
func (s *Service) staticFlags(d *dayData, p Person, rec *DayRecord, schedule string, leave []HCEntry, leaveHC float64, work []HCEntry) []Flag {
	var flags []Flag

	if ok, why := d.dir.InRoster(p, d.key.Process); !ok {
		flags = append(flags, Flag{FlagNotInRoster, SeverityWarn, fmt.Sprintf("Not on the %s roster on %s: %s", d.key.Process, d.key.Date.Format("2006-01-02"), why)})
	}

	effective, source := schedule, "Scheduled"
	if rec != nil && NormShift(rec.Actual) != "" {
		effective, source = rec.Actual, "Recorded"
	}
	switch norm := NormShift(effective); {
	case strings.TrimSpace(effective) == "":
		flags = append(flags, Flag{FlagShiftMismatch, SeverityWarn, fmt.Sprintf("No schedule found in the calendar for shift %s on this date", displayOr(p.Shift, "(unknown)"))})
	case norm == "":
		flags = append(flags, Flag{FlagShiftMismatch, SeverityWarn, fmt.Sprintf("Not scheduled to work on this date (day type %s)", effective)})
	case norm != d.key.Shift:
		flags = append(flags, Flag{FlagShiftMismatch, SeverityWarn, fmt.Sprintf("%s %s (%s), but scanning in the %s shift", source, norm, effective, d.key.Shift)})
	}

	if leaveHC > 0 {
		names := make([]string, 0, len(leave))
		for _, h := range leave {
			names = append(names, fmt.Sprintf("%s %s", shortLeave(h.Machine), trimFloat(h.HC)))
		}
		if leaveHC >= 1-eps {
			flags = append(flags, Flag{FlagFullDayLeave, SeverityWarn, fmt.Sprintf("Full-day leave (%s). Saving replaces the leave record with the machine.", strings.Join(names, ", "))})
		} else {
			flags = append(flags, Flag{FlagPartDayLeave, SeverityInfo, fmt.Sprintf("Part-day leave (%s), %s HC is left for the machine", strings.Join(names, ", "), trimFloat(availHC(leaveHC)))})
		}
	}

	if len(work) > 0 {
		parts := make([]string, 0, len(work))
		for _, h := range work {
			parts = append(parts, fmt.Sprintf("%s %s", h.Machine, trimFloat(h.HC)))
		}
		sev := SeverityInfo
		if NormShift(rec.Actual) != "" && NormShift(rec.Actual) != d.key.Shift {
			sev = SeverityWarn
		}
		flags = append(flags, Flag{FlagHasRecord, sev, "Already has a record in the database: " + strings.Join(parts, ", ")})
	}
	return flags
}

// dynamicFlags check the current assignment of a draft line, so they follow every manual change.
func (s *Service) dynamicFlags(d *dayData, e Entry, used map[string]float64) []Flag {
	switch {
	case e.Source == SourceNone || e.MachineID == "":
		return []Flag{{FlagNoMachine, SeverityWarn, firstNonEmpty(e.Reason, "No machine assigned")}}
	default:
		m, ok := d.machines.byID[e.MachineID]
		if !ok {
			return nil
		}
		var flags []Flag
		if e.Source != SourceExisting {
			switch {
			case m.Budget > 0 && used[m.ID] > m.Budget+eps:
				flags = append(flags, Flag{FlagOverBudget, SeverityWarn, fmt.Sprintf("%s goes over its budget head count: %s / %s HC", m.Name, trimFloat(used[m.ID]), trimFloat(m.Budget))})
			case m.Budget <= 0:
				flags = append(flags, Flag{FlagOverBudget, SeverityInfo, fmt.Sprintf("No budget head count is maintained for %s", m.Name)})
			}
		}
		if e.Source == SourceManual && m.Routine() {
			if m.CIMID == "" {
				flags = append(flags, Flag{FlagNoMTR, SeverityWarn, fmt.Sprintf("%s has no MTR mapping, so the skill level cannot be checked", m.Name)})
			} else if d.levelOn(e.EE, m) < 1 {
				flags = append(flags, Flag{FlagNoMTR, SeverityWarn, fmt.Sprintf("No MTR record (Lv1 or higher) of %s on %s", e.Name, m.Name)})
			}
		}
		return flags
	}
}

func shortLeave(name string) string {
	if i := strings.Index(name, " - "); i > 0 {
		return name[:i]
	}
	return name
}

func trimFloat(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
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
