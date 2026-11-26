package hcscan

import (
	"time"

	"dash_go/internal/core/hc"
)

// ProfileSource is the part of the Dash API client the permission check needs.
type ProfileSource = hc.ProfileSource

// NewDashPermissions returns the Permissions implementation backed by the Python API.
func NewDashPermissions(src ProfileSource, ttl time.Duration) Permissions {
	return hc.NewDashPermissions(src, ttl)
}
