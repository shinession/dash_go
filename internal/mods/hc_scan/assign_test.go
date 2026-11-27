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

func TestPickPrevWorkMachine(t *testing.T) {
	machines, levels := rankFixture()
	level := func(m Machine) int { return levels[m.CIMID] }
	remaining := map[string]float64{"M1": 1, "M2": 2, "M3": 1, "A1": 5, "U1": 1}

	// Previous day on M3 (Lv1) is chosen even when M1 has Lv3.
	pick, ok := PickPrevWorkMachine([]PrevWorkCandidate{{Machine: machines[2], Share: 1}}, level, map[string]int{"M3": 1}, remaining, 1)
	if !ok || pick.Machine.ID != "M3" || pick.Level != 1 {
		t.Fatalf("prev day M3 must win: ok=%v pick=%+v", ok, pick)
	}
	// Multiple previous-day machines: larger HC share wins first.
	pick, ok = PickPrevWorkMachine([]PrevWorkCandidate{
		{Machine: machines[0], Share: 0.25},
		{Machine: machines[2], Share: 0.75},
	}, level, nil, remaining, 1)
	if !ok || pick.Machine.ID != "M3" {
		t.Fatalf("larger previous-day share must win: ok=%v pick=%+v", ok, pick)
	}
	// Equal share breaks ties by higher MTR level.
	pick, ok = PickPrevWorkMachine([]PrevWorkCandidate{
		{Machine: machines[2], Share: 0.5},
		{Machine: machines[1], Share: 0.5},
	}, level, nil, remaining, 1)
	if !ok || pick.Machine.ID != "M2" {
		t.Fatalf("equal share must prefer higher MTR level: ok=%v pick=%+v", ok, pick)
	}
	// Ineligible previous-day machine (no MTR Lv1, activity, unmapped, or full) returns false so caller falls back to RankMachines.
	if _, ok := PickPrevWorkMachine([]PrevWorkCandidate{{Machine: machines[2], Share: 1}}, func(Machine) int { return 0 }, nil, remaining, 1); ok {
		t.Fatal("Lv0 on previous-day machine must not qualify")
	}
	if _, ok := PickPrevWorkMachine([]PrevWorkCandidate{{Machine: machines[3], Share: 1}, {Machine: machines[4], Share: 1}}, level, nil, remaining, 1); ok {
		t.Fatal("activity and unmapped machines must not qualify")
	}
	if _, ok := PickPrevWorkMachine([]PrevWorkCandidate{{Machine: machines[2], Share: 1}}, level, nil, map[string]float64{"M3": 0}, 1); ok {
		t.Fatal("full previous-day machine must not qualify")
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
