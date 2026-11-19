package hcscan

import "sort"

// Ranked is one machine a person may be assigned to, with the facts the ranking used.
type Ranked struct {
	Machine   Machine
	Level     int     // MTR level of the person on the machine (>= 1)
	Recent    int     // days of the last N days the person worked on the machine
	Remaining float64 // budget left on the machine
}

// RankMachines returns the machines one scanned person may be assigned to, best first.
//
// A machine is eligible when it is a routine machine that has an MTR mapping, the person holds MTR level 1 or
// higher on it, and the budget head count left on it has room for the HC the person brings (need, 1 for a full
// day), so an automatic assignment never creates an over-budget line. A person without an MTR record on a machine
// is never assigned to it automatically: the MTR data is used by other systems, so an automatic assignment must
// not hide a missing training record (a shift leader can still choose any machine by hand).
//
// Order: MTR level (3 before 2 before 1), then the days the person worked on the machine in the last days,
// then the larger remaining budget, then the display order and the id (so the result is reproducible).
func RankMachines(machines []Machine, level func(Machine) int, recent map[string]int, remaining map[string]float64, need float64) []Ranked {
	var out []Ranked
	for _, m := range machines {
		if !m.Routine() || m.CIMID == "" {
			continue
		}
		lv := level(m)
		if lv < 1 {
			continue
		}
		rem := remaining[m.ID]
		if rem <= eps || rem+eps < need {
			continue
		}
		out = append(out, Ranked{Machine: m, Level: lv, Recent: recent[m.ID], Remaining: rem})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Level != b.Level {
			return a.Level > b.Level
		}
		if a.Recent != b.Recent {
			return a.Recent > b.Recent
		}
		if a.Remaining != b.Remaining {
			return a.Remaining > b.Remaining
		}
		if a.Machine.Seq != b.Machine.Seq {
			return a.Machine.Seq < b.Machine.Seq
		}
		return a.Machine.ID < b.Machine.ID
	})
	return out
}
