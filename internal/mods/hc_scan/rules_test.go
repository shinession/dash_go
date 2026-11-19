package hcscan

import (
	"testing"
	"time"
)

func TestIDParserExtract(t *testing.T) {
	p, err := NewIDParser("")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"AB12C34", "AB12C34", true},
		{"abc12d34", "ABC12D34", true},     // three letters, lower case from the scanner
		{"  AB12C34\r\n", "AB12C34", true}, // whitespace and line end
		{"*AB12C34*", "AB12C34", true},     // Code 39 start/stop characters
		{"9AB12C34", "AB12C34", true},      // a stray digit in front is skipped by the search (a stray letter would look like a 3-letter code)
		{"AB12C3", "", false},              // incomplete read
		{"1234567", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := p.Extract(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("Extract(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
	if _, err := NewIDParser("("); err == nil {
		t.Error("an invalid pattern must be rejected")
	}
}

func TestNormShift(t *testing.T) {
	for in, want := range map[string]string{"W": "AM", "AM": "AM", "PH_AM": "AM", "PM": "PM", "PH_PM": "PM", "O": "", "R": "", "PH": "", "MCL - Medical Leave": "", "": ""} {
		if got := NormShift(in); got != want {
			t.Errorf("NormShift(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShiftColumn(t *testing.T) {
	for in, want := range map[string]string{"N0": "N", "N5": "N", "PA": "PA", "rl": "RL", "TC": "TC", "": "", "P'; DROP": "", "A1": ""} {
		if got := ShiftColumn(in); got != want {
			t.Errorf("ShiftColumn(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCalBuildInOTMatchesPython compares with the table generated from cal_build_in_ot of LocalListV3.py.
func TestCalBuildInOTMatchesPython(t *testing.T) {
	for _, c := range buildInOTCases {
		if got := CalBuildInOT(c.shift, c.schedule, c.hc); got != c.want {
			t.Errorf("CalBuildInOT(%q, %q, %v) = %v, want %v", c.shift, c.schedule, c.hc, got, c.want)
		}
	}
	if len(buildInOTCases) < 100 {
		t.Fatalf("the parity table is too small: %d", len(buildInOTCases))
	}
}

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

func TestMonthLocked(t *testing.T) {
	cases := []struct {
		name        string
		today, from time.Time
		want        bool
	}{
		{"day 19, this month", d(2026, 11, 19), d(2026, 11, 1), false},
		{"day 19, last month end", d(2026, 11, 19), d(2026, 10, 31), true},
		{"day 7, last month still open", d(2026, 11, 7), d(2026, 10, 15), false},
		{"day 7, two months back", d(2026, 11, 7), d(2026, 9, 30), true},
		{"day 8, last month locked", d(2026, 11, 8), d(2026, 10, 1), true},
		{"january wraps to december", d(2027, 1, 5), d(2026, 12, 20), false},
		{"january, november is locked", d(2027, 1, 5), d(2026, 11, 30), true},
	}
	for _, c := range cases {
		if got := MonthLocked(c.from, c.today); got != c.want {
			t.Errorf("%s: MonthLocked = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestProductionDate(t *testing.T) {
	loc := time.UTC
	if got := ProductionDate(time.Date(2026, 11, 19, 6, 59, 0, 0, loc), 7); !got.Equal(d(2026, 11, 18)) {
		t.Errorf("before 07:00 the production date is still yesterday, got %v", got)
	}
	if got := ProductionDate(time.Date(2026, 11, 19, 7, 0, 0, 0, loc), 7); !got.Equal(d(2026, 11, 19)) {
		t.Errorf("at 07:00 the new production date starts, got %v", got)
	}
}

func TestCleanName(t *testing.T) {
	if got := CleanName("Li Yi (Winder)", "X"); got != "Li Yi" {
		t.Errorf("got %q", got)
	}
	if got := CleanName("", "AB12C34"); got != "AB12C34" {
		t.Errorf("empty name must fall back to the code, got %q", got)
	}
}

func TestMachineMatchesRole(t *testing.T) {
	cases := []struct {
		machine, role string
		want          bool
	}{
		{"Winder_515", "winder_515", true},
		{"CSV-1/2 Packing", "CSV-1", true},  // substring
		{"CSV-2", "CSV-1/2", true},          // the role lists several lines
		{"CSV-3", "CSV-1/2", false},         //
		{"XCSV-2", "CSV-1/2", false},        // lookbehind rule: CSV must not follow a letter
		{"CSV-2 SET", "CSV-1/2 SET", false}, // SET roles never match loosely
		{"Anything", "", false},
		{"Anything", "-", false},
	}
	for _, c := range cases {
		if got := machineMatchesRole(c.machine, c.role); got != c.want {
			t.Errorf("machineMatchesRole(%q, %q) = %v, want %v", c.machine, c.role, got, c.want)
		}
	}
}

func TestHCCalendarColumns(t *testing.T) {
	for col, want := range map[string]bool{"N": true, "PA": true, "RL": true, "TC": true, "KA": false, "LD": false, "PRODN_DATE": false, "UPDATE_TIME": false, "WEEK_DAY": false, "": false} {
		if got := hcCalendarColumn(col); got != want {
			t.Errorf("hcCalendarColumn(%q) = %v, want %v", col, got, want)
		}
	}
}
