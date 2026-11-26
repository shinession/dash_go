package hcscan

import (
	"regexp"
	"strings"
)

// machineIndex resolves the machine ids and names stored in t_daily_attn_data to the machines of t_mqpd_hc_table.
type machineIndex struct {
	machines []Machine
	norm     []string // normName of the name of every machine, in the order of machines
	byID     map[string]Machine
	byName   map[string]Machine // normalised name -> machine
}

// normName makes two spellings of a machine name comparable: no space at the ends and one space for every run of spaces.
// The machine table has "CSV-10/13 Set2  (SV#9/10|SD#12/13/15|TCW#12/13/14/18)" and "Fix-Shipment/ Logistic  - 4pax"
// with two spaces where the records have one, and the people on them were on no card.
func normName(s string) string { return strings.Join(strings.Fields(s), " ") }

func newMachineIndex(machines []Machine) *machineIndex {
	idx := &machineIndex{machines: machines, norm: make([]string, len(machines)), byID: map[string]Machine{}, byName: map[string]Machine{}}
	for i, m := range machines {
		idx.byID[m.ID] = m
		if tid := strings.TrimSpace(m.ID); tid != m.ID && tid != "" {
			idx.byID[tid] = m
		}
		name := normName(m.Name)
		idx.norm[i] = name
		if _, dup := idx.byName[name]; !dup {
			idx.byName[name] = m
		}
	}
	return idx
}

// ByID looks up a machine by its exact t_mqpd_hc_table.id (trimmed).
func (x *machineIndex) ByID(id string) (Machine, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Machine{}, false
	}
	m, ok := x.byID[id]
	return m, ok
}

// Resolve is the port of resolve_mqpd_hc_machine_id: an exact id, then the exact name, then the loose name match
// the Python page uses for records that were typed with a shortened machine name. Names are compared with normName.
// It looks at the names only: whether an entry belongs to the process of the page is for dayData.resolve to say.
func (x *machineIndex) Resolve(name string) (Machine, bool) {
	name = normName(name)
	if name == "" {
		return Machine{}, false
	}
	if m, ok := x.byID[name]; ok {
		return m, true
	}
	if m, ok := x.byName[name]; ok {
		return m, true
	}
	for i, m := range x.machines {
		if machineMatchesRole(x.norm[i], name) {
			return m, true
		}
	}
	return Machine{}, false
}

var (
	csvRoleRe    = regexp.MustCompile(`CSV-([0-9/]+)`)
	csvMachineRe = regexp.MustCompile(`CSV-(\d+)`)
)

// machineMatchesRole is the port of machine_matches_allocation_role: does the table machine name contain the
// recorded name, ignoring case, or do both name the same CSV line number.
func machineMatchesRole(machine, role string) bool {
	if role == "" || role == "-" {
		return false
	}
	if strings.Contains(strings.ToLower(machine), strings.ToLower(role)) {
		return true
	}
	upperRole := strings.ToUpper(role)
	if strings.Contains(upperRole, "SET") {
		return false
	}
	roleNums := firstUnprefixed(csvRoleRe, upperRole)
	machineNum := firstUnprefixed(csvMachineRe, strings.ToUpper(machine))
	if roleNums == "" || machineNum == "" {
		return false
	}
	for _, n := range strings.Split(roleNums, "/") {
		if n == machineNum {
			return true
		}
	}
	return false
}

// firstUnprefixed returns the first submatch whose match is not directly preceded by a letter or digit (the
// lookbehind (?<![A-Z0-9]) of the Python expression, which Go regular expressions do not have).
func firstUnprefixed(re *regexp.Regexp, s string) string {
	for _, loc := range re.FindAllStringSubmatchIndex(s, -1) {
		if loc[0] > 0 {
			prev := s[loc[0]-1]
			if (prev >= 'A' && prev <= 'Z') || (prev >= '0' && prev <= '9') {
				continue
			}
		}
		return s[loc[2]:loc[3]]
	}
	return ""
}
