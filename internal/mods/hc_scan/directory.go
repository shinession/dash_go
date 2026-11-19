package hcscan

import (
	"sort"
	"strings"
	"time"
)

// The tables below are ported from read_mes_ee_data() in LocalListV3.py: the MES employee snapshot has no
// "process" column, the process follows from the cost center (and, for CDC, from the section).
const mesGroupCode = "SP9200"

var mesDirectCostCenters = map[string]bool{
	"1079": true, "1081": true, "1082": true, "1083": true, "1085": true, "1086": true, "1087": true, "2081": true,
	"2082": true, "2083": true, "2085": true, "2086": true, "2087": true, "2090": true, "2093": true, "8522": true,
}

var mesProcessByCostCenter = map[string]string{
	"1082": "Mobile", "1083": "Mobile", "1085": "Mobile", "1086": "Mobile", "SP92512": "Mobile", "SP92513": "Mobile", "SP92514": "Mobile", "SP92515": "Mobile",
	"SP92331": "QA", "8522": "QA", "1079": "Slitter", "1081": "Slitter", "2081": "Slitter", "SP92511": "Slitter", "SP92521": "Slitter",
	"2082": "Taping", "2085": "Winder", "2182": "Taping", "2185": "Winder", "2083": "CSV & NWC", "2183": "CSV & NWC", "2086": "LSL", "2186": "LSL",
	"2089": "Assy Support", "2090": "HSL", "2190": "HSL", "1087": "CDC", "2087": "CDC", "2187": "CDC", "2093": "Packing",
	"SP92531": "CDC", "SP92532": "Packing", "SP9253": "CDC",
}

var mesDivisionName = map[string]string{"SP9220": "PPE", "SP9230": "QAQC", "SP9250": "MFC", "SP9260": "PP", "SP9280": "ME"}

// MESProcess derives the process of an employee of the MES snapshot (cal_process in the Python code).
func MESProcess(costCenter, section, division string) string {
	p, ok := mesProcessByCostCenter[costCenter]
	if !ok {
		if d, ok := mesDivisionName[division]; ok {
			return d
		}
		return "Unknown"
	}
	if p != "CDC" {
		return p
	}
	if s, ok := mesProcessByCostCenter[section]; ok {
		return s
	}
	return "Unknown"
}

// MESDirectType derives the direct type when the snapshot does not store one (cal_direct_type).
func MESDirectType(costCenter, section, process, stored string) string {
	if stored != "" {
		return stored
	}
	if !mesDirectCostCenters[costCenter] {
		return "Indirect"
	}
	if process == "QA" {
		if mesProcessByCostCenter[section] == "QA" && mesProcessByCostCenter[costCenter] == "QA" {
			return "Direct"
		}
		return "Indirect"
	}
	return "Direct"
}

// MESPerson converts a snapshot row to a Person (without membership overrides).
func MESPerson(r MESRow) Person {
	process := MESProcess(r.CostCenter, r.Section, r.Division)
	return Person{
		EE:         strings.ToUpper(r.EE),
		Name:       CleanName(r.Name, r.EE),
		Process:    process,
		Direct:     MESDirectType(r.CostCenter, r.Section, process, r.DirectType),
		Shift:      r.Shift,
		ResignDate: r.ResignDate,
	}
}

// Directory answers "who is this employee on this date": the membership range valid on the date overrides the MES
// snapshot, exactly as update_member_daily_data does for the Python grid.
type Directory struct {
	date   time.Time
	people map[string]Person
}

// BuildDirectory merges the MES snapshot and the membership ranges valid on date.
func BuildDirectory(date time.Time, mes []MESRow, members []MemberRow) *Directory {
	date = DateOnly(date)
	d := &Directory{date: date, people: map[string]Person{}}
	for _, r := range mes {
		if strings.TrimSpace(r.EE) == "" {
			continue
		}
		p := MESPerson(r)
		d.people[p.EE] = p
	}
	best := map[string]MemberRow{}
	for _, m := range members {
		ee := strings.ToUpper(strings.TrimSpace(m.EE))
		if ee == "" || m.Effective.After(date) || m.End.Before(date) {
			continue
		}
		if cur, ok := best[ee]; !ok || m.Effective.After(cur.Effective) {
			best[ee] = m
		}
	}
	for ee, m := range best {
		p, ok := d.people[ee]
		if !ok {
			p = Person{EE: ee}
		}
		if strings.TrimSpace(m.Name) != "" {
			p.Name = CleanName(m.Name, ee)
		} else if p.Name == "" {
			p.Name = ee
		}
		p.Process, p.Direct, p.Shift, p.FromMember = m.Process, m.Direct, m.Shift, true
		d.people[ee] = p
	}
	return d
}

// Lookup returns the person with the employee code.
func (d *Directory) Lookup(ee string) (Person, bool) {
	p, ok := d.people[strings.ToUpper(ee)]
	return p, ok
}

// Resigned reports whether the person left before the date of the directory.
func (d *Directory) Resigned(p Person) bool {
	return p.ResignDate != nil && DateOnly(*p.ResignDate).Before(d.date)
}

// InRoster reports whether the person belongs to the roster of the process on the date, and if not, why.
func (d *Directory) InRoster(p Person, process string) (bool, string) {
	if d.Resigned(p) {
		return false, "resigned on " + p.ResignDate.Format("2006-01-02")
	}
	if !contains(RosterProcesses(process), p.Process) {
		return false, "member of " + displayOr(p.Process, "no process") + ", not " + process
	}
	if p.Direct != "Direct" && p.Direct != "Shift Leader" {
		return false, "direct type is " + displayOr(p.Direct, "unknown") + ", not Direct or Shift Leader"
	}
	return true, ""
}

// Roster lists the Direct and Shift Leader members of the process on the date, sorted by shift and name.
func (d *Directory) Roster(process string) []Person {
	var out []Person
	for _, p := range d.people {
		if ok, _ := d.InRoster(p, process); ok {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].EE < out[j].EE
	})
	return out
}

func displayOr(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
