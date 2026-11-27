package hcbudget

import "context"

// CommitPlan computes inserts and updates from the locked snapshot of a process inside a single transaction.
// existing holds every row of the process (visible + hidden); takenIDs reports whether any candidate new ID
// already exists anywhere in t_mqpd_hc_table (across all processes).
type CommitPlan func(existing []Row, takenIDs map[string]bool) (inserts []Row, updates []Row, err error)

// Repository reads and writes t_mqpd_hc_table and checks user role in "T_USER_ALL" in the Auth PostgreSQL database.
type Repository interface {
	// IsUserAdmin reports whether the user's latest row in "T_USER_ALL" is activated and has "Type" = 'admin'
	// (case-insensitive). Admin capabilities in hc_budget come strictly from "T_USER_ALL", not t_auth_group.
	IsUserAdmin(ctx context.Context, eeCode string) (bool, error)
	// ProcessRows returns all rows (both visible and hidden) of a process ordered by display_seq ASC NULLS LAST, id ASC.
	ProcessRows(ctx context.Context, process string) ([]Row, error)
	// Apply runs plan inside a single database transaction with the process's rows locked for update, checks
	// candidateIDs for global uniqueness across t_mqpd_hc_table, inserts and updates the returned rows, and commits.
	Apply(ctx context.Context, process string, candidateIDs []string, plan CommitPlan) (written []Row, err error)
}

// ChangeLog appends saved changes to MQPDHeadCount.log so Python readers invalidate their Redis cache.
type ChangeLog interface {
	Append(user string, rows []Row) error
}
