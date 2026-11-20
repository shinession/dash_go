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

// PersonView is a member who is expected on the shift but has not scanned yet.
type PersonView struct {
	EE       string `json:"ee"`
	Name     string `json:"name"`
	Shift    string `json:"shift"`
	Schedule string `json:"schedule"`
	Assigned string `json:"assigned,omitempty"` // machine of an existing record
	Photo    string `json:"photo"`
}

// Totals are the counters of the page header.
type Totals struct {
	Expected        int `json:"expected"`         // roster members expected on the shift
	ExpectedScanned int `json:"expected_scanned"` // of them, how many scanned
	Scanned         int `json:"scanned"`          // everybody who scanned in this session
	Saved           int `json:"saved"`
	Pending         int `json:"pending"`
	Assigned        int `json:"assigned"`
	Unassigned      int `json:"unassigned"`
	Warnings        int `json:"warnings"`
}

// MTRInfo tells how fresh the proficiency data is.
type MTRInfo struct {
	DataTime  string `json:"data_time"`
	Employees int    `json:"employees"`
	Unmapped  int    `json:"unmapped_machines"` // routine machines without an MTR mapping
}

// BoardView is the whole state of a scanning session.
type BoardView struct {
	Process    string        `json:"process"`
	Date       string        `json:"date"`
	Shift      string        `json:"shift"`
	ServerTime time.Time     `json:"server_time"`
	Locked     bool          `json:"locked"` // the month is locked, nothing can be saved
	Machines   []MachineView `json:"machines"`
	Entries    []EntryView   `json:"entries"`
	NotScanned []PersonView  `json:"not_scanned"`
	Totals     Totals        `json:"totals"`
	MTR        MTRInfo       `json:"mtr"`
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
