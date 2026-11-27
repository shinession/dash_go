// Package version holds the build identity of the dash_go binary.
package version

// Version is the project version. scripts/build.ps1 overrides it with -ldflags "-X".
var Version = "0.2.3"

// Modules is the comma separated list of modules compiled into the binary.
// A plain "go build" includes every module and leaves the value as "all".
var Modules = "all"
