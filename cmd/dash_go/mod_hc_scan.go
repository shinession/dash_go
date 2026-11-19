//go:build !nomod_hc_scan

package main

// Pulls the hc_scan module into the binary. scripts/build.ps1 adds the build tag nomod_hc_scan when the module
// was not selected for the package.
import _ "dash_go/internal/mods/hc_scan"
