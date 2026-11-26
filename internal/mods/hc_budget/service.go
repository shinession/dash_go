package hcbudget

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/hc"
	"dash_go/internal/core/i18n"
	"dash_go/internal/version"
)

// Error is returned by the service; the HTTP layer maps it to a status code and localized JSON message.
type Error struct {
	Status int
	Code   string
	Key    string
	Args   i18n.P
}

func (e *Error) Error() string { return e.Code }

func newError(status int, code string, args ...i18n.P) *Error {
	e := &Error{Status: status, Code: code, Key: "hb.err." + code}
	if len(args) > 0 {
		e.Args = args[0]
	}
	return e
}

// Settings are the optional module settings (config key modules.hc_budget).
type Settings struct {
	DashAPIURL  string `json:"dash_api_url"`  // default: top-level dash_api_url / api_server
	DashDataDir string `json:"dash_data_dir"` // default: top-level dash_data_dir (holds MQPDHeadCount.log)
}

// DefaultSettings returns the default module settings.
func DefaultSettings() Settings { return Settings{} }

// Deps are the collaborators of Service.
type Deps struct {
	Repo      Repository
	ChangeLog ChangeLog
	Perms     hc.Permissions
	Processes []string // HC_PROCESS_LIST
	Log       *slog.Logger
	Now       func() time.Time
}

// Service implements the business logic of hc_budget.
type Service struct {
	Deps
}

// NewService validates dependencies and returns a Service.
func NewService(d Deps) (*Service, error) {
	if d.Repo == nil || d.Perms == nil {
		return nil, fmt.Errorf("hc_budget: repository and permissions are required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Service{Deps: d}, nil
}

// budgetRegex matches IE.py's numeric validation rule: up to 8 integer digits, optional decimal up to 12 digits.
var budgetRegex = regexp.MustCompile(`^\d{1,8}(\.\d{1,12})?$`)

const (
	minStationCode = 1
	maxSmallInt    = 32767
	minSmallInt    = -32768
	maxCostCenter  = 4
	maxMachineLen  = 120
	maxCategoryLen = 80
)

// Me returns the signed-in user, permitted processes (in HC_PROCESS_LIST order), and admin flag.
func (s *Service) Me(ctx context.Context, u auth.User) (MeView, error) {
	acc, err := s.Perms.Access(ctx, u, false)
	if err != nil {
		s.Log.Error("hc_budget: permission check failed", "user", u.EE, "error", err)
		return MeView{}, newError(http.StatusBadGateway, "permission_check_failed")
	}
	return MeView{
		User:      UserView{EE: u.EE, Name: u.Name},
		Processes: hc.OrderProcesses(acc.Processes, s.Processes),
		IsAdmin:   acc.IsAdmin,
		Version:   version.Version,
	}, nil
}

// Table loads the budget rows and process metadata for one process.
// Hidden rows are included only when includeHidden is true AND the user is an admin.
func (s *Service) Table(ctx context.Context, u auth.User, process string, includeHidden bool) (TableView, error) {
	process = strings.TrimSpace(process)
	acc, err := s.requireProcess(ctx, u, process, false)
	if err != nil {
		return TableView{}, err
	}
	if !acc.IsAdmin {
		includeHidden = false
	}
	allRows, err := s.Repo.ProcessRows(ctx, process)
	if err != nil {
		s.Log.Error("hc_budget: read process rows failed", "process", process, "error", err)
		return TableView{}, s.internal("read rows")
	}

	visible := make([]Row, 0, len(allRows))
	var totals Totals
	for _, r := range allRows {
		if r.Hidden {
			totals.HiddenCount++
		} else {
			totals.VisibleCount++
			totals.TotalBudgetHC += r.BudgetHC
			visible = append(visible, r)
		}
	}
	totals.TotalBudgetHC = round4(totals.TotalBudgetHC)

	shown := visible
	if includeHidden {
		shown = allRows
		if shown == nil {
			shown = []Row{}
		}
	}
	return TableView{
		Process:       process,
		IsAdmin:       acc.IsAdmin,
		IncludeHidden: includeHidden,
		Rows:          shown,
		Defaults:      buildDefaults(allRows),
		Totals:        totals,
	}, nil
}

// Save validates and commits staged inserts, updates, hidden toggles, and display_seq reorders in one transaction.
func (s *Service) Save(ctx context.Context, u auth.User, req SaveRequest) (SaveResult, error) {
	process := strings.TrimSpace(req.Process)
	acc, err := s.requireProcess(ctx, u, process, true)
	if err != nil {
		return SaveResult{}, err
	}
	if !acc.IsAdmin && (len(req.Creates) > 0 || len(req.Order) > 0) {
		return SaveResult{}, newError(http.StatusForbidden, "forbidden_admin")
	}

	// Validate request payload before opening a DB transaction.
	preparedCreates, candidateIDs, err := validateCreates(process, req.Creates)
	if err != nil {
		return SaveResult{}, err
	}
	updatesByID, err := validateUpdatePayloads(req.Updates)
	if err != nil {
		return SaveResult{}, err
	}
	orderList, err := validateOrderList(req.Order)
	if err != nil {
		return SaveResult{}, err
	}

	now := s.Now()
	actor := strings.TrimSpace(u.Name)
	if actor == "" {
		actor = u.EE
	}

	var insertedCount, updatedCount int
	written, err := s.Repo.Apply(ctx, process, candidateIDs, func(existing []Row, takenIDs map[string]bool) ([]Row, []Row, error) {
		existingByID := make(map[string]Row, len(existing))
		usedStations := make(map[int]string, len(existing))
		maxSeq := 0
		for _, r := range existing {
			existingByID[r.ID] = r
			if r.StationCode != nil {
				usedStations[*r.StationCode] = r.ID
			}
			if r.DisplaySeq != nil && *r.DisplaySeq > maxSeq {
				maxSeq = *r.DisplaySeq
			}
		}

		// Verify new rows against existing station codes and global IDs.
		inserts := make([]Row, 0, len(preparedCreates))
		newByID := make(map[string]int, len(preparedCreates))
		nextSeq := maxSeq
		for _, c := range preparedCreates {
			sc := *c.StationCode
			if _, exists := usedStations[sc]; exists {
				return nil, nil, newError(http.StatusConflict, "duplicate_station_code", i18n.P{"process": process, "code": sc})
			}
			if _, exists := existingByID[c.ID]; exists || takenIDs[c.ID] {
				return nil, nil, newError(http.StatusConflict, "duplicate_id", i18n.P{"id": c.ID})
			}
			usedStations[sc] = c.ID
			nextSeq++
			seq := nextSeq
			c.DisplaySeq = &seq
			c.UpdateTime = now
			c.UpdateAt = now.Format("2006-01-02 15:04:05")
			c.UpdateUser = actor
			newByID[c.ID] = len(inserts)
			inserts = append(inserts, c)
		}

		// Verify updates target existing rows of this process and obey role rules.
		mergedByID := make(map[string]Row, len(existing))
		for _, r := range existing {
			mergedByID[r.ID] = r
		}
		for id, up := range updatesByID {
			cur, ok := existingByID[id]
			if !ok {
				return nil, nil, newError(http.StatusNotFound, "unknown_row", i18n.P{"id": id})
			}
			if !acc.IsAdmin && cur.Hidden {
				return nil, nil, newError(http.StatusForbidden, "forbidden_admin")
			}
			if up.Hidden != nil && *up.Hidden != cur.Hidden && !acc.IsAdmin {
				return nil, nil, newError(http.StatusForbidden, "forbidden_admin")
			}
			// Require Routine / Non-routine when Type changes (allow legacy value if unchanged).
			if up.Type != cur.Type && !isValidType(up.Type) {
				return nil, nil, newError(http.StatusBadRequest, "invalid_type", i18n.P{"machine": up.Machine})
			}
			next := cur
			next.Machine = up.Machine
			next.Type = up.Type
			next.Category = up.Category
			next.BudgetHC = up.BudgetHC
			if up.Hidden != nil {
				next.Hidden = *up.Hidden
			}
			mergedByID[id] = next
		}

		// Compute per-process display_seq when admin supplied a sequence order.
		if len(orderList) > 0 {
			seenOrder := make(map[string]bool, len(orderList))
			seq := 0
			assignSeq := func(id string) error {
				seq++
				if seq > maxSmallInt {
					return newError(http.StatusBadRequest, "too_many_rows")
				}
				v := seq
				if idx, isNew := newByID[id]; isNew {
					inserts[idx].DisplaySeq = &v
					return nil
				}
				cur, ok := mergedByID[id]
				if !ok {
					return newError(http.StatusBadRequest, "invalid_order_id", i18n.P{"id": id})
				}
				cur.DisplaySeq = &v
				mergedByID[id] = cur
				return nil
			}
			for _, id := range orderList {
				if err := assignSeq(id); err != nil {
					return nil, nil, err
				}
				seenOrder[id] = true
			}
			// Keep any rows not included in orderList (e.g. hidden rows when Show Hidden is off) after the ordered rows.
			for _, r := range existing {
				if !seenOrder[r.ID] {
					if err := assignSeq(r.ID); err != nil {
						return nil, nil, err
					}
					seenOrder[r.ID] = true
				}
			}
			for _, c := range inserts {
				if !seenOrder[c.ID] {
					if err := assignSeq(c.ID); err != nil {
						return nil, nil, err
					}
					seenOrder[c.ID] = true
				}
			}
		}

		// Collect only existing rows that actually changed.
		updates := make([]Row, 0, len(existing))
		for _, cur := range existing {
			next := mergedByID[cur.ID]
			if !rowChanged(cur, next) {
				continue
			}
			next.UpdateTime = now
			next.UpdateAt = now.Format("2006-01-02 15:04:05")
			next.UpdateUser = actor
			updates = append(updates, next)
		}

		if len(inserts) == 0 && len(updates) == 0 {
			return nil, nil, newError(http.StatusConflict, "nothing_to_save")
		}
		insertedCount = len(inserts)
		updatedCount = len(updates)
		return inserts, updates, nil
	})
	if err != nil {
		var se *Error
		if errors.As(err, &se) {
			return SaveResult{}, se
		}
		s.Log.Error("hc_budget: save failed", "process", process, "user", u.EE, "error", err)
		return SaveResult{}, newError(http.StatusInternalServerError, "save_failed")
	}

	res := SaveResult{Inserted: insertedCount, Updated: updatedCount}
	if s.ChangeLog != nil {
		if err := s.ChangeLog.Append(actor, written); err != nil {
			s.Log.Warn("hc_budget: saved to database, but failed to write MQPDHeadCount.log", "process", process, "user", u.EE, "error", err)
			res.LogWarning = i18n.T(ctx, "hb.msg.log_warning", nil)
		}
	}
	s.Log.Info("hc_budget: saved budget changes", "process", process, "user", u.EE, "inserted", insertedCount, "updated", updatedCount)
	return res, nil
}

func (s *Service) requireProcess(ctx context.Context, u auth.User, process string, fresh bool) (hc.Access, error) {
	if process == "" || !hc.ContainsProcess(s.Processes, process) {
		return hc.Access{}, newError(http.StatusBadRequest, "unknown_process", i18n.P{"process": process})
	}
	acc, err := s.Perms.Access(ctx, u, fresh)
	if err != nil {
		s.Log.Error("hc_budget: permission check failed", "user", u.EE, "process", process, "error", err)
		return hc.Access{}, newError(http.StatusBadGateway, "permission_check_failed")
	}
	ordered := hc.OrderProcesses(acc.Processes, s.Processes)
	if !hc.ContainsProcess(ordered, process) {
		return hc.Access{}, newError(http.StatusForbidden, "no_process_permission", i18n.P{"process": process})
	}
	acc.Processes = ordered
	return acc, nil
}

func (s *Service) internal(step string) *Error {
	return newError(http.StatusInternalServerError, "internal", i18n.P{"step": stepKey(step)})
}

func stepKey(step string) string {
	return "hb.step." + strings.ReplaceAll(strings.ToLower(step), " ", "_")
}

type validatedUpdate struct {
	ID       string
	Machine  string
	Type     string
	Category string
	BudgetHC float64
	Hidden   *bool
}

func validateCreates(process string, creates []CreateRow) ([]Row, []string, error) {
	out := make([]Row, 0, len(creates))
	candidateIDs := make([]string, 0, len(creates))
	seenStations := map[int]bool{}
	seenIDs := map[string]bool{}
	for _, c := range creates {
		machine := strings.TrimSpace(c.Machine)
		if machine == "" || utf8.RuneCountInString(machine) > maxMachineLen {
			return nil, nil, newError(http.StatusBadRequest, "invalid_machine")
		}
		if c.StationCode == nil || *c.StationCode < minStationCode || *c.StationCode > maxSmallInt {
			return nil, nil, newError(http.StatusBadRequest, "invalid_station_code", i18n.P{"machine": machine})
		}
		sc := *c.StationCode
		if seenStations[sc] {
			return nil, nil, newError(http.StatusConflict, "duplicate_station_code", i18n.P{"process": process, "code": sc})
		}
		seenStations[sc] = true

		id := fmt.Sprintf("%s_%d", process, sc)
		if seenIDs[id] {
			return nil, nil, newError(http.StatusConflict, "duplicate_id", i18n.P{"id": id})
		}
		seenIDs[id] = true

		typ := strings.TrimSpace(c.Type)
		if !isValidType(typ) {
			return nil, nil, newError(http.StatusBadRequest, "invalid_type", i18n.P{"machine": machine})
		}
		cc := strings.TrimSpace(c.CostCenter)
		if utf8.RuneCountInString(cc) > maxCostCenter {
			return nil, nil, newError(http.StatusBadRequest, "invalid_cost_center", i18n.P{"machine": machine})
		}
		var procCode *int
		if c.ProcessCode != nil {
			if *c.ProcessCode < minSmallInt || *c.ProcessCode > maxSmallInt {
				return nil, nil, newError(http.StatusBadRequest, "invalid_process_code", i18n.P{"machine": machine})
			}
			v := *c.ProcessCode
			procCode = &v
		}
		cat := strings.TrimSpace(c.Category)
		if utf8.RuneCountInString(cat) > maxCategoryLen {
			return nil, nil, newError(http.StatusBadRequest, "invalid_category", i18n.P{"machine": machine})
		}
		budget, err := parseBudget(c.BudgetHC)
		if err != nil {
			return nil, nil, newError(http.StatusBadRequest, "invalid_budget", i18n.P{"machine": machine})
		}
		scVal := sc
		out = append(out, Row{
			ID:          id,
			Process:     process,
			CostCenter:  cc,
			ProcessCode: procCode,
			Type:        typ,
			StationCode: &scVal,
			Machine:     machine,
			BudgetHC:    budget,
			Category:    cat,
			Hidden:      c.Hidden,
		})
		candidateIDs = append(candidateIDs, id)
	}
	return out, candidateIDs, nil
}

func validateUpdatePayloads(updates []UpdateRow) (map[string]validatedUpdate, error) {
	out := make(map[string]validatedUpdate, len(updates))
	for _, u := range updates {
		id := strings.TrimSpace(u.ID)
		if id == "" {
			return nil, newError(http.StatusBadRequest, "bad_request")
		}
		if _, dup := out[id]; dup {
			return nil, newError(http.StatusBadRequest, "bad_request")
		}
		machine := strings.TrimSpace(u.Machine)
		if machine == "" || utf8.RuneCountInString(machine) > maxMachineLen {
			return nil, newError(http.StatusBadRequest, "invalid_machine")
		}
		typ := strings.TrimSpace(u.Type)
		if typ == "" {
			return nil, newError(http.StatusBadRequest, "invalid_type", i18n.P{"machine": machine})
		}
		cat := strings.TrimSpace(u.Category)
		if utf8.RuneCountInString(cat) > maxCategoryLen {
			return nil, newError(http.StatusBadRequest, "invalid_category", i18n.P{"machine": machine})
		}
		budget, err := parseBudget(u.BudgetHC)
		if err != nil {
			return nil, newError(http.StatusBadRequest, "invalid_budget", i18n.P{"machine": machine})
		}
		out[id] = validatedUpdate{
			ID:       id,
			Machine:  machine,
			Type:     typ,
			Category: cat,
			BudgetHC: budget,
			Hidden:   u.Hidden,
		}
	}
	return out, nil
}

func validateOrderList(order []string) ([]string, error) {
	if len(order) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(order))
	out := make([]string, 0, len(order))
	for _, raw := range order {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			return nil, newError(http.StatusBadRequest, "bad_request")
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func parseBudget(raw string) (float64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, nil
	}
	if !budgetRegex.MatchString(s) {
		return 0, errors.New("budget does not match numeric format")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0, errors.New("invalid budget")
	}
	return v, nil
}

func isValidType(typ string) bool {
	return typ == TypeRoutine || typ == TypeNonRoutine
}

func rowChanged(a, b Row) bool {
	if a.Machine != b.Machine || a.Type != b.Type || a.Category != b.Category || a.Hidden != b.Hidden {
		return true
	}
	if float32(a.BudgetHC) != float32(b.BudgetHC) {
		return true
	}
	return !intPtrEqual(a.DisplaySeq, b.DisplaySeq)
}

func intPtrEqual(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func buildDefaults(rows []Row) ProcessDefaults {
	ccCounts := map[string]int{}
	pcCounts := map[int]int{}
	catSet := map[string]bool{}
	stationSet := map[int]bool{}
	maxSeq := 0

	for _, r := range rows {
		if r.CostCenter != "" {
			ccCounts[r.CostCenter]++
		}
		if r.ProcessCode != nil {
			pcCounts[*r.ProcessCode]++
		}
		if r.Category != "" {
			catSet[r.Category] = true
		}
		if r.StationCode != nil {
			stationSet[*r.StationCode] = true
		}
		if r.DisplaySeq != nil && *r.DisplaySeq > maxSeq {
			maxSeq = *r.DisplaySeq
		}
	}

	var bestCC string
	bestCCCount := 0
	for cc, n := range ccCounts {
		if n > bestCCCount || (n == bestCCCount && cc < bestCC) {
			bestCC, bestCCCount = cc, n
		}
	}

	var bestPC *int
	bestPCCount := 0
	for pc, n := range pcCounts {
		if n > bestPCCount || (n == bestPCCount && (bestPC == nil || pc < *bestPC)) {
			v := pc
			bestPC, bestPCCount = &v, n
		}
	}

	cats := make([]string, 0, len(catSet))
	for c := range catSet {
		cats = append(cats, c)
	}
	sort.Strings(cats)

	codes := make([]int, 0, len(stationSet))
	for sc := range stationSet {
		codes = append(codes, sc)
	}
	sort.Ints(codes)

	return ProcessDefaults{
		CostCenter:       bestCC,
		ProcessCode:      bestPC,
		Categories:       cats,
		UsedStationCodes: codes,
		NextSeq:          maxSeq + 1,
	}
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
