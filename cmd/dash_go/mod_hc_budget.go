//go:build !nomod_hc_budget

package main

// Pulls the hc_budget module into the binary. scripts/build.ps1 adds the build tag nomod_hc_budget when the module
// was not selected for the package.
import _ "dash_go/internal/mods/hc_budget"
