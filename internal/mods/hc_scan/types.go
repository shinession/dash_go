package hcscan

import (
	"strings"
	"time"
)

// Key identifies one scanning session: a process, a production date and an AM/PM shift. The draft of the
// session is stored under this key.
type Key struct {
	Process string
	Date    time.Time // production date, 00:00 UTC
	Shift   string    // ShiftAM or ShiftPM
}

// Machine is one machine or activity of a process (a row of t_mqpd_hc_table).
type Machine struct {
	ID      string
	Name    string
	Process string
	Type    string  // "Routine", "Non-routine", ...
	Budget  float64 // budget_head_count, 0 when none is maintained
	Seq     float64 // display_seq
	CIMID   string  // MTR machine id, empty when the MTR data has no machine of that name
}

// Routine reports whether the machine needs an MTR proficiency record. Non-routine activities do not.
func (m Machine) Routine() bool { return !strings.EqualFold(strings.TrimSpace(m.Type), "Non-routine") }

// Flag is a message about an entry. Severity "warn" is shown in yellow and must be acknowledged before the
// entry is written; "info" is only shown.
type Flag struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Text     string `json:"text"`
}

// Flag codes.
const (
	FlagNotInRoster   = "NOT_IN_ROSTER"
	FlagShiftMismatch = "SHIFT_MISMATCH"
	FlagFullDayLeave  = "FULL_DAY_LEAVE"
	FlagPartDayLeave  = "PART_DAY_LEAVE"
	FlagHasRecord     = "HAS_RECORD"
	FlagNoMachine     = "NO_MACHINE"
	FlagNoMTR         = "NO_MTR"
	FlagOverBudget    = "OVER_BUDGET"
)

// Severities.
const (
	SeverityWarn = "warn"
	SeverityInfo = "info"
)

// Entry sources.
const (
	SourceAuto     = "auto"     // chosen by the assignment rules
	SourceManual   = "manual"   // chosen or changed by the shift leader
	SourceExisting = "existing" // the person already has a record with a machine
	SourceNone     = "none"     // no machine could be assigned
)

// HCEntry is one item of machine_hc_json: a machine or leave option and its share of the day.
type HCEntry struct {
	Process string  `json:"process,omitempty"`
	Machine string  `json:"machine"`
	HC      float64 `json:"hc"`
}

// Entry is the draft line of one scanned person. It is stored as JSON in the draft store.
type Entry struct {
	EE          string    `json:"ee"`
	Name        string    `json:"name"`
	Process     string    `json:"process"` // member process on the date
	Source      string    `json:"source"`
	MachineID   string    `json:"machine_id"`
	MachineName string    `json:"machine_name"`
	HC          float64   `json:"hc"` // HC the person puts on the machine (1 minus part-day leave)
	Level       int       `json:"level"`
	RecentDays  int       `json:"recent_days"`
	Reason      string    `json:"reason"`
	Shift       string    `json:"shift"`    // shift code of the member, for example PA
	Schedule    string    `json:"schedule"` // day type from the calendar, for example W
	Existing    []HCEntry `json:"existing,omitempty"`
	LeaveHC     float64   `json:"leave_hc"`
	Flags       []Flag    `json:"flags"` // facts found at scan time (roster, shift, leave)
	ScannedAt   time.Time `json:"scanned_at"`
	ScannedBy   string    `json:"scanned_by"`
}

// ScanRecord remembers that a person scanned in a session, also after the entry was saved.
type ScanRecord struct {
	ScannedAt   time.Time  `json:"scanned_at"`
	ScannedBy   string     `json:"scanned_by"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
}

// DayRecord is a row of t_daily_attn_data.
type DayRecord struct {
	UID      string
	EE       string
	Date     time.Time
	Shift    string
	Schedule string
	Actual   string
	Entries  []HCEntry
	Remark   *string
	BuildIn  float64
	AddtOT   float64
	TotalOT  float64
}

// MESRow is a row of the monthly MES employee snapshot t_mes_ee.
type MESRow struct {
	EE           string
	Name         string
	CostCenter   string
	Section      string
	Division     string
	DirectType   string
	Shift        string
	ResignDate   *time.Time
	EffectiveMth time.Time
}

// MemberRow is a membership range of t_daily_attn_member.
type MemberRow struct {
	EE        string
	Name      string
	Process   string
	Direct    string
	Shift     string
	Effective time.Time
	End       time.Time
}

// Person is what the service knows about an employee on a date: the membership range valid on that date
// overrides the MES snapshot.
type Person struct {
	EE         string
	Name       string
	Process    string
	Direct     string
	Shift      string
	ResignDate *time.Time
	FromMember bool
}

// MTRData is the proficiency matrix read from t_mtr_checkpoint.
type MTRData struct {
	Levels    map[string]map[string]int // employee code -> MTR machine id -> level
	NameToCIM map[string]string         // machine name -> MTR machine id
	DataTime  time.Time                 // newest updated_at of the rows
}
