package hcscan

import (
	"math"
	"regexp"
	"strings"
	"time"
)

// Shift values of the page.
const (
	ShiftAM = "AM"
	ShiftPM = "PM"
)

const eps = 1e-7

func oppositeShift(shift string) string {
	if shift == ShiftAM {
		return ShiftPM
	}
	return ShiftAM
}

// NormShift maps a day type or an actual shift to the AM/PM session it belongs to. The map is the one the Python
// Area Assign page uses (shift_norm_map); every other value (days off, public holidays, leave names) gives "".
func NormShift(code string) string {
	switch strings.TrimSpace(code) {
	case "W", "AM", "PH_AM":
		return ShiftAM
	case "PM", "PH_PM":
		return ShiftPM
	}
	return ""
}

var shiftColumnRe = regexp.MustCompile(`^[A-Z]{1,3}$`)

// ShiftColumn returns the column of t_daily_attn_calendar that holds the day type of a shift code: every night
// shift N0..N5 uses the column N, the others use their own code. It returns "" for a code that is no column name.
func ShiftColumn(shift string) string {
	shift = strings.ToUpper(strings.TrimSpace(shift))
	if shift == "" {
		return ""
	}
	if shift[0] == 'N' {
		return "N"
	}
	if !shiftColumnRe.MatchString(shift) {
		return ""
	}
	return shift
}

// hcCalendarColumns are the shift columns of t_daily_attn_calendar that the Python HC grid reads. The table also has
// KA..KD and LA..LD, which the grid ignores (its "known quirks" list says so); this module ignores them too, so the
// roster of a shift is the one the HC Input page shows.
var hcCalendarColumns = map[string]bool{
	"N": true, "PA": true, "PB": true, "PC": true, "PD": true,
	"RA": true, "RB": true, "RC": true, "RD": true, "RE": true, "RF": true, "RG": true, "RH": true, "RI": true, "RJ": true, "RK": true, "RL": true,
	"TA": true, "TB": true, "TC": true,
}

func hcCalendarColumn(col string) bool { return hcCalendarColumns[col] }

// CalBuildInOT is the port of cal_build_in_ot in LocalListV3.py: the built-in overtime hours of a day for the
// non-leave HC of the record.
func CalBuildInOT(shift, schedule string, hc float64) float64 {
	if hc < 0 || hc > 1 {
		return 0
	}
	var ratio float64
	switch {
	case hc < 0.25:
		ratio = 0
	case hc < 0.5:
		ratio = 0.25
	case hc < 0.75:
		ratio = 0.5
	case hc < 1:
		ratio = 0.75
	default:
		ratio = 1
	}
	switch schedule {
	case "W", "AM", "PM":
		switch shift {
		case "PA", "PB", "PC", "PD":
			return ratio
		case "TA", "TB", "TC":
			return round2(2.75 * ratio)
		}
		return 0
	case "PH", "PH_AM", "PH_PM":
		if shift != "" && shift[0] != 'N' {
			return round2(11.08 * ratio)
		}
	}
	return 0
}

// MonthLocked is the port of check_mqpd_hc_daily_attn_last_month_locked: on days 1 to 7 the previous month can
// still be edited, from day 8 the previous month and everything older is locked.
func MonthLocked(from, today time.Time) bool {
	currentMonthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthEnd := currentMonthStart.AddDate(0, 0, -1)
	previousMonthStart := time.Date(previousMonthEnd.Year(), previousMonthEnd.Month(), 1, 0, 0, 0, 0, time.UTC)
	from = DateOnly(from)
	if today.Day() <= 7 {
		return from.Before(previousMonthStart)
	}
	return !from.After(previousMonthEnd)
}

// DateOnly returns the calendar date at 00:00 UTC, the representation used for production dates.
func DateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ProductionDate is the production date of a moment: the day starts at offsetHours (7 in the Python code).
func ProductionDate(now time.Time, offsetHours int) time.Time {
	return DateOnly(now.Add(-time.Duration(offsetHours) * time.Hour))
}

// CleanName removes the "(...)" suffix the HR names may carry, like the Python grid does.
func CleanName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = fallback
	}
	if i := strings.Index(name, "("); i > 0 {
		name = strings.TrimSpace(name[:i])
	}
	return name
}

// MachineProcesses lists the processes whose machines belong to the page process. CSV and NWC share their
// machines (see resolve_mqpd_hc_machine_id).
func MachineProcesses(process string) []string {
	if process == "CSV" || process == "NWC" {
		return []string{"CSV", "NWC"}
	}
	return []string{process}
}

// RosterProcesses lists the member processes shown on the page of a process. CSV and NWC also include the
// combined "CSV & NWC" members.
func RosterProcesses(process string) []string {
	if process == "CSV" || process == "NWC" {
		return []string{"CSV", "NWC", "CSV & NWC"}
	}
	return []string{process}
}

// AllProcess is the process value of an entry that was allocated on the "All Process" view of the Python application:
// it names no process of its own.
const AllProcess = "All Process"

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// inProcesses is contains for process names: case and surrounding spaces do not matter.
func inProcesses(list []string, process string) bool {
	process = strings.TrimSpace(process)
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), process) {
			return true
		}
	}
	return false
}

func round2(v float64) float64  { return math.Round(v*100) / 100 }
func round10(v float64) float64 { return math.Round(v*1e10) / 1e10 }

// splitHC divides a person's available working HC evenly across count selected machines (10-decimal precision so
// e.g. 3 machines get 0.3333333333 each and sum back to 1).
func splitHC(total float64, count int) float64 {
	if count <= 1 {
		return round10(total)
	}
	return round10(total / float64(count))
}
