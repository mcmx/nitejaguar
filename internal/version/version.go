// Package version holds the application version, stamped at release time
// via ldflags (-X github.com/mcmx/nitejaguar/internal/version.Version=...).
// Both binaries (nitejaguar-server, nitejaguar) share it so --version stays
// in sync across the two entrypoints.
package version

// Version is the application version. It defaults to "dev" for local builds.
var Version = "dev"
