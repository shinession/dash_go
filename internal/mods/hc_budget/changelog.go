package hcbudget

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// changeLogName is the file Python's read_mqpd_hc_data watches for mtime changes to invalidate its Redis cache.
const changeLogName = "MQPDHeadCount.log"

// ErrNoChangeLogFolder means neither dash_data_dir nor cdn_data_folder is set: the database was updated,
// but the Python Dash pages will not refresh their cache until the next cache cycle.
var ErrNoChangeLogFolder = errors.New("no change log folder is configured (dash_data_dir, cdn_data_folder)")

type fileChangeLog struct {
	dirs []string
}

// NewFileChangeLog appends a line to MQPDHeadCount.log in every non-empty directory (dash_data_dir and cdn_data_folder),
// matching LocalListV3.py mqpd_hc_budget_update_sql.
func NewFileChangeLog(dirs ...string) ChangeLog {
	var d []string
	seen := map[string]bool{}
	for _, x := range dirs {
		if x != "" && !seen[x] {
			seen[x] = true
			d = append(d, x)
		}
	}
	return &fileChangeLog{dirs: d}
}

type changeLine struct {
	ID          string  `json:"id"`
	Process     string  `json:"process"`
	Machine     string  `json:"machine"`
	Type        string  `json:"type"`
	Category    string  `json:"category,omitempty"`
	BudgetHC    float64 `json:"budget_head_count"`
	DisplaySeq  *int    `json:"display_seq,omitempty"`
	Hidden      bool    `json:"hidden"`
	StationCode *int    `json:"station_code,omitempty"`
}

func (c *fileChangeLog) Append(user string, rows []Row) error {
	if len(c.dirs) == 0 {
		return ErrNoChangeLogFolder
	}
	lines := make([]changeLine, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, changeLine{
			ID:          r.ID,
			Process:     r.Process,
			Machine:     r.Machine,
			Type:        r.Type,
			Category:    r.Category,
			BudgetHC:    r.BudgetHC,
			DisplaySeq:  r.DisplaySeq,
			Hidden:      r.Hidden,
			StationCode: r.StationCode,
		})
	}
	payload, err := json.Marshal(lines)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("%s %s update mqpd hc budget data successfully (dash_go hc_budget): %s\n",
		time.Now().Format("2006-01-02 15:04:05.000000"), user, payload)
	var errs []error
	for _, dir := range c.dirs {
		if err := appendFile(filepath.Join(dir, changeLogName), text); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func appendFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
