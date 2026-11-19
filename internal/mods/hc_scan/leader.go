package hcscan

import (
	"fmt"
	"sort"
	"strings"
)

// Leaders have no MTR record: the activity they work on is the "Group Leader" row of the process, which is a
// non-routine row and therefore never part of the ranking by MTR level. They are placed on that row in two ways:
//
//  1. Their direct type is "Shift Leader" (maintained with HC Input > Tools > Member). This is the target.
//  2. Until that is maintained for everybody (setting leader_by_history, on by default): the most recent record of the
//     person within the history window that has a machine is on a leader row. "Same as last time" separates the
//     leaders from the operators who covered the row once and went back to their machine: on the development copy the
//     rule finds 2 people per shift on the Winder row, whose budget is 2.
//
// A leader is only ever placed on the leader row. When that row is full the line stays without machine, so the shift
// leader decides; the budget rule of the automatic assignment does not change for leaders.

// leaderRows returns the leader activity rows of the process, the row of the page process first (CSV and NWC share
// their machines and each has its own row).
func (s *Service) leaderRows(d *dayData) []Machine {
	var rows []Machine
	for _, m := range d.machines.machines {
		if m.Routine() {
			continue
		}
		name := strings.TrimSpace(m.Name)
		for _, want := range s.Settings.LeaderActivityNames {
			if strings.EqualFold(name, strings.TrimSpace(want)) {
				rows = append(rows, m)
				break
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Process == d.key.Process && rows[j].Process != d.key.Process
	})
	return rows
}

// leaderPlacement tells whether the person is a leader and on which row to place them, with the reason shown on the
// line. history holds the records of the person before the date of the session.
func (s *Service) leaderPlacement(d *dayData, p Person, history []DayRecord) (Machine, string, bool) {
	rows := s.leaderRows(d)
	if len(rows) == 0 {
		return Machine{}, "", false
	}
	if p.Direct == "Shift Leader" {
		return rows[0], "Shift Leader (direct type)", true
	}
	if !s.Settings.LeaderByHistory {
		return Machine{}, "", false
	}
	leaderIDs := make(map[string]Machine, len(rows))
	for _, r := range rows {
		leaderIDs[r.ID] = r
	}
	byDate := append([]DayRecord(nil), history...)
	sort.SliceStable(byDate, func(i, j int) bool { return byDate[i].Date.After(byDate[j].Date) })
	for _, rec := range byDate {
		work, _, _ := s.splitEntries(rec.Entries)
		if len(work) == 0 {
			continue // a leave day says nothing about the work
		}
		for _, h := range work {
			if m, ok := d.machines.Resolve(h.Machine); ok {
				if row, isLeader := leaderIDs[m.ID]; isLeader {
					return row, fmt.Sprintf("%s at the last record (%s)", row.Name, rec.Date.Format("2 Jan")), true
				}
			}
		}
		return Machine{}, "", false // the most recent record is a machine: not a leader (any more)
	}
	return Machine{}, "", false
}
