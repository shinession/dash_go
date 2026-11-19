package hcscan

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// changeLogName is the file the Python readers watch (read_daily_attn_data compares its modification time).
const changeLogName = "DailyAttnData.log"

type fileChangeLog struct {
	dirs []string
}

// NewFileChangeLog appends a line to DailyAttnData.log in every directory, like submit_mqpd_hc_daily_attn_data
// does for the data folder of the app and for cdn_data_folder. Empty directories are ignored.
func NewFileChangeLog(dirs ...string) ChangeLog {
	var d []string
	for _, x := range dirs {
		if x != "" {
			d = append(d, x)
		}
	}
	return &fileChangeLog{dirs: d}
}

type changeLine struct {
	UID      string    `json:"uid"`
	EE       string    `json:"ee_code"`
	Date     string    `json:"prodn_date"`
	Actual   string    `json:"actual_shift"`
	Machines []HCEntry `json:"machine_hc_json"`
}

func (c *fileChangeLog) Append(user string, rows []DayRecord) error {
	if len(c.dirs) == 0 {
		return errors.New("no change log folder is configured (dash_data_dir, cdn_data_folder): the Python pages will not notice the change before their next refresh")
	}
	lines := make([]changeLine, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, changeLine{UID: r.UID, EE: r.EE, Date: r.Date.Format("2006-01-02"), Actual: r.Actual, Machines: r.Entries})
	}
	payload, err := json.Marshal(lines)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("%s %s submit hc daily attn data successfully (dash_go hc_scan): %s\n", time.Now().Format("2006-01-02 15:04:05.000000"), user, payload)
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
