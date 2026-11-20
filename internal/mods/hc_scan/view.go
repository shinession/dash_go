package hcscan

import "time"

// The types below are the JSON documents of the HTTP API.

// UserView is the logged in person.
type UserView struct {
	EE   string `json:"ee"`
	Name string `json:"name"`
}

// ClientConfig is what the browser needs to scan: the employee code pattern, the photo location and the poll rate.
type ClientConfig struct {
	IDPattern      string `json:"id_pattern"`
	PhotoURL       string `json:"photo_url"`
	PollMS         int    `json:"poll_ms"`
	ProductionDate string `json:"production_date"`
	DefaultShift   string `json:"default_shift"`
	RecentDays     int    `json:"recent_days"`
}

// MeView answers GET api/me.
type MeView struct {
	User      UserView     `json:"user"`
	Processes []string     `json:"processes"`
	Config    ClientConfig `json:"config"`
	Version   string       `json:"version"`
}

// EntryView is a draft line with its current warnings. Reason and the texts of the flags are in the language of
// the request.
type EntryView struct {
	Entry
	Reason string         `json:"reason"`
	Photo  string         `json:"photo"`
	Flags  []FlagView     `json:"flags"` // facts found at scan time plus the checks of the current assignment
	Levels map[string]int `json:"levels"`
	Warn   bool           `json:"warn"`
}

// MemberView is a person on a machine card.
type MemberView struct {
	EE     string  `json:"ee"`
	Name   string  `json:"name"`
	HC     float64 `json:"hc"`
	Level  int     `json:"level"`
	Saved  bool    `json:"saved"` // true: already in the database, false: pending in the draft
	Photo  string  `json:"photo"`
	Scaned bool    `json:"scanned"`
	// From is the process of a person who is not a member of this process and works on one of its machines (support
	// from another process, for example the "Others" group). Empty for a member.
	From string `json:"from,omitempty"`
}

// MachineView is a machine card of the board.
type MachineView struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Type      string       `json:"type"`
	Routine   bool         `json:"routine"`
	MTRMapped bool         `json:"mtr_mapped"`
	Budget    float64      `json:"budget"`
	Used      float64      `json:"used"`
	Over      bool         `json:"over"`
	Members   []MemberView `json:"members"`
}

// PersonView is a member of the shift who has no task yet: nothing in the database and no pending line with a machine.
type PersonView struct {
	EE       string  `json:"ee"`
	Name     string  `json:"name"`
	Shift    string  `json:"shift"`
	Schedule string  `json:"schedule"`
	Photo    string  `json:"photo"`
	HC       float64 `json:"hc"` // HC still to place: 1, or what a part-day leave leaves
}

// LeaveItem is one leave option of a person on the day.
type LeaveItem struct {
	Code string  `json:"code"` // the short code, for example MCL
	Name string  `json:"name"` // the full name of the leave option, for example "MCL - Medical Leave"
	HC   float64 `json:"hc"`
}

// LeaveView is a member of the shift who is on leave, whatever the kind of leave. A part-day leave also shows the
// person on their machine (or in the list of people without a task) for the rest of the day.
type LeaveView struct {
	EE    string      `json:"ee"`
	Name  string      `json:"name"`
	Photo string      `json:"photo"`
	HC    float64     `json:"hc"`
	Items []LeaveItem `json:"items"`
}

// ElsewhereView is a member of the shift who is assigned only to machines of another process. The person has a task, so
// they are not in the list of people without one, and they are on no machine card of this page.
type ElsewhereView struct {
	EE    string   `json:"ee"`
	Name  string   `json:"name"`
	Photo string   `json:"photo"`
	HC    float64  `json:"hc"`
	Where []string `json:"where"` // "<process> / <machine>" of every share of the day that is elsewhere
}

// Totals are the counters of the page header. The first four describe the people of the shift in HC, the way the Daily
// Attn Summary of the Python application does; the others describe the draft.
type Totals struct {
	Scheduled  int     `json:"scheduled"`    // members who belong to the shift
	PlacedHC   float64 `json:"placed_hc"`    // HC of them on a machine: saved records and pending lines
	LeaveHC    float64 `json:"leave_hc"`     // HC of them on leave, all kinds together
	ToAssignHC float64 `json:"to_assign_hc"` // HC of them still without a task
	Scanned    int     `json:"scanned"`      // everybody who scanned in this session
	Saved      int     `json:"saved"`
	Pending    int     `json:"pending"`
	Assigned   int     `json:"assigned"` // pending lines that Confirm would write: they have a machine and no record yet
	Unassigned int     `json:"unassigned"`
	Warnings   int     `json:"warnings"`
}

// MTRInfo tells how fresh the proficiency data is.
type MTRInfo struct {
	DataTime  string `json:"data_time"`
	Employees int    `json:"employees"`
	Unmapped  int    `json:"unmapped_machines"` // routine machines without an MTR mapping
}

// BoardView is the whole state of a scanning session.
type BoardView struct {
	Process     string          `json:"process"`
	Date        string          `json:"date"`
	Shift       string          `json:"shift"`
	ServerTime  time.Time       `json:"server_time"`
	Locked      bool            `json:"locked"` // the month is locked, nothing can be saved
	Machines    []MachineView   `json:"machines"`
	Entries     []EntryView     `json:"entries"`
	NotAssigned []PersonView    `json:"not_assigned"`
	Leave       []LeaveView     `json:"leave"`
	Elsewhere   []ElsewhereView `json:"elsewhere"`
	Totals      Totals          `json:"totals"`
	MTR         MTRInfo         `json:"mtr"`
}

// ScanResult answers POST api/scan.
type ScanResult struct {
	Status  string    `json:"status"` // assigned, unassigned, existing, duplicate, saved
	Message string    `json:"message"`
	Entry   EntryView `json:"entry"`
}

// WrittenView is a saved line.
type WrittenView struct {
	EE      string  `json:"ee"`
	Name    string  `json:"name"`
	Machine string  `json:"machine"`
	HC      float64 `json:"hc"`
}

// SkippedView is a line that was not saved, with the reason.
type SkippedView struct {
	EE     string `json:"ee"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// AckView is a line with warnings the shift leader has to acknowledge.
type AckView struct {
	EE      string     `json:"ee"`
	Name    string     `json:"name"`
	Machine string     `json:"machine"`
	Flags   []FlagView `json:"flags"`
}

// ConfirmResult answers POST api/confirm.
type ConfirmResult struct {
	Written    []WrittenView `json:"written"`
	Skipped    []SkippedView `json:"skipped"`
	NeedsAck   []AckView     `json:"needs_ack"`
	Unassigned int           `json:"unassigned"` // pending lines without a machine, which are not saved
	LogWarning string        `json:"log_warning,omitempty"`
}
