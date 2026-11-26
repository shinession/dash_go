package hcbudget

import "time"

// UserView is the signed-in user as returned to the browser.
type UserView struct {
	EE   string `json:"ee"`
	Name string `json:"name"`
}

// Machine/activity types allowed in t_mqpd_hc_table.
const (
	TypeRoutine    = "Routine"
	TypeNonRoutine = "Non-routine"
)

// Row is one machine or activity record in t_mqpd_hc_table.
type Row struct {
	ID          string    `json:"id"`
	Process     string    `json:"process"`
	CostCenter  string    `json:"cost_center"`
	ProcessCode *int      `json:"process_code"`
	Type        string    `json:"type"`
	StationCode *int      `json:"station_code"`
	Machine     string    `json:"machine"`
	BudgetHC    float64   `json:"budget_head_count"`
	Category    string    `json:"category"`
	DisplaySeq  *int      `json:"display_seq"`
	Hidden      bool      `json:"hidden"`
	UpdateTime  time.Time `json:"-"`
	UpdateAt    string    `json:"update_time"`
	UpdateUser  string    `json:"update_user"`
}

// ProcessDefaults summarizes existing metadata of a process for pre-filling the Add modal and datalist suggestions.
type ProcessDefaults struct {
	CostCenter       string   `json:"cost_center"`
	ProcessCode      *int     `json:"process_code"`
	Categories       []string `json:"categories"`
	UsedStationCodes []int    `json:"used_station_codes"`
	NextSeq          int      `json:"next_seq"`
}

// Totals summarizes row counts and visible head count budget for a process.
type Totals struct {
	VisibleCount  int     `json:"visible_count"`
	HiddenCount   int     `json:"hidden_count"`
	TotalBudgetHC float64 `json:"total_budget_hc"`
}

// MeView is the response of GET api/me.
type MeView struct {
	User      UserView `json:"user"`
	Processes []string `json:"processes"`
	IsAdmin   bool     `json:"is_admin"`
	Version   string   `json:"version"`
}

// TableView is the response of GET api/table.
type TableView struct {
	Process       string          `json:"process"`
	IsAdmin       bool            `json:"is_admin"`
	IncludeHidden bool            `json:"include_hidden"`
	Rows          []Row           `json:"rows"`
	Defaults      ProcessDefaults `json:"defaults"`
	Totals        Totals          `json:"totals"`
}

// CreateRow is a newly added machine/activity staged on the client (admin only).
type CreateRow struct {
	StationCode *int   `json:"station_code"`
	CostCenter  string `json:"cost_center"`
	ProcessCode *int   `json:"process_code"`
	Type        string `json:"type"`
	Machine     string `json:"machine"`
	Category    string `json:"category"`
	BudgetHC    string `json:"budget_head_count"`
	Hidden      bool   `json:"hidden"`
}

// UpdateRow is a modification to an existing row of t_mqpd_hc_table.
type UpdateRow struct {
	ID       string `json:"id"`
	Machine  string `json:"machine"`
	Type     string `json:"type"`
	Category string `json:"category"`
	BudgetHC string `json:"budget_head_count"`
	Hidden   *bool  `json:"hidden,omitempty"`
}

// SaveRequest is the batch payload sent by POST api/save when the user clicks Confirm.
type SaveRequest struct {
	Process string      `json:"process"`
	Creates []CreateRow `json:"creates"`
	Updates []UpdateRow `json:"updates"`
	Order   []string    `json:"order,omitempty"`
}

// SaveResult is the response of POST api/save.
type SaveResult struct {
	Inserted   int    `json:"inserted"`
	Updated    int    `json:"updated"`
	LogWarning string `json:"log_warning,omitempty"`
}
