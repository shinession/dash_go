//go:build !nomod_web_log

package main

// Pulls the web_log module into the binary. scripts/build.ps1 adds the build tag nomod_web_log when the module
// was not selected for the package.
import _ "dash_go/internal/mods/web_log"
