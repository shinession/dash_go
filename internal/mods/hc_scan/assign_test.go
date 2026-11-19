package hcscan

import "testing"

func rankFixture() ([]Machine, map[string]int) {
	machines := []Machine{
		{ID: "M1", Name: "M1", Type: "Routine", Budget: 1, Seq: 1, CIMID: "C1"},
		{ID: "M2", Name: "M2", Type: "Routine", Budget: 2, Seq: 2, CIMID: "C2"},
		{ID: "M3", Name: "M3", Type: "Routine", Budget: 1, Seq: 3, CIMID: "C3"},
		{ID: "A1", Name: "Activity", Type: "Non-routine", Budget: 5, Seq: 4},
		{ID: "U1", Name: "Unmapped", Type: "Routine", Budget: 1, Seq: 5},
	}
	return machines, map[string]int{"C1": 3, "C2": 2, "C3": 1}
}

func ids(r []Ranked) []string {
	var out []string
	for _, x := range r {
		out = append(out, x.Machine.ID)
	}
	return out
}

func TestRankMachinesLevelFirst(t *testing.T) {
	machines, levels := rankFixture()
	remaining := map[string]float64{"M1": 1, "M2": 2, "M3": 1, "A1": 5, "U1": 1}
	// the person worked M3 five days, but Lv3 on M1 beats it: the MTR level decides first
	got := RankMachines(machines, func(m Machine) int { return levels[m.CIMID] }, map[string]int{"M3": 5}, remaining, 1)
	if want := []string{"M1", "M2", "M3"}; !equal(ids(got), want) {
		t.Fatalf("order = %v, want %v", ids(got), want)
	}
}

func TestRankMachinesRecentDaysBreakLevelTies(t *testing.T) {
	machines := []Machine{
		{ID: "M1", Type: "Routine", Budget: 1, Seq: 1, CIMID: "C1"},
		{ID: "M2", Type: "Routine", Budget: 1, Seq: 2, CIMID: "C2"},
	}
	level := func(Machine) int { return 2 }
	remaining := map[string]float64{"M1": 1, "M2": 1}
	got := RankMachines(machines, level, map[string]int{"M2": 3, "M1": 1}, remaining, 1)
	if want := []string{"M2", "M1"}; !equal(ids(got), want) {
		t.Fatalf("recent days must decide between equal levels: %v, want %v", ids(got), want)
	}
	// without history the larger remaining budget wins, then the display order
	got = RankMachines(machines, level, nil, map[string]float64{"M1": 1, "M2": 2}, 1)
	if want := []string{"M2", "M1"}; !equal(ids(got), want) {
		t.Fatalf("remaining budget tie-break: %v, want %v", ids(got), want)
	}
	got = RankMachines(machines, level, nil, remaining, 1)
	if want := []string{"M1", "M2"}; !equal(ids(got), want) {
		t.Fatalf("display order tie-break: %v, want %v", ids(got), want)
	}
}

func TestRankMachinesGates(t *testing.T) {
	machines, levels := rankFixture()
	level := func(m Machine) int { return levels[m.CIMID] }
	// a full machine is skipped
	got := RankMachines(machines, level, nil, map[string]float64{"M1": 0, "M2": 1, "M3": 1, "A1": 5, "U1": 1}, 1)
	if want := []string{"M2", "M3"}; !equal(ids(got), want) {
		t.Fatalf("a machine without vacancy must be skipped: %v, want %v", ids(got), want)
	}
	// no MTR record (level 0) never qualifies; activities and unmapped machines are never automatic
	got = RankMachines(machines, func(Machine) int { return 0 }, nil, map[string]float64{"M1": 1, "M2": 1, "M3": 1, "A1": 5, "U1": 1}, 1)
	if len(got) != 0 {
		t.Fatalf("Lv0 must never be assigned automatically, got %v", ids(got))
	}
	got = RankMachines(machines, func(Machine) int { return 3 }, nil, map[string]float64{"M1": 1, "M2": 1, "M3": 1, "A1": 5, "U1": 1}, 1)
	if want := []string{"M1", "M2", "M3"}; !equal(ids(got), want) {
		t.Fatalf("activity and unmapped machine must not be ranked: %v, want %v", ids(got), want)
	}
	// the room left must fit the HC of the person: 0.25 HC left does not take a full day, but a quarter day
	got = RankMachines(machines[:1], level, nil, map[string]float64{"M1": 0.25}, 1)
	if len(got) != 0 {
		t.Fatalf("0.25 HC left must not take a full day, got %v", ids(got))
	}
	got = RankMachines(machines[:1], level, nil, map[string]float64{"M1": 0.25}, 0.25)
	if len(got) != 1 {
		t.Fatalf("0.25 HC left takes a quarter day, got %v", ids(got))
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
