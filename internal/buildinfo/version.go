// Package buildinfo holds metadata supplied by release builds using -ldflags.
package buildinfo

import (
	"fmt"
	"strings"
)

// VersionPrefix makes the injected version identifiable in cross-built binaries
// even when -trimpath omits linker flags from Go's executable build metadata.
const VersionPrefix = "iptest-version:"

var (
	Version = VersionPrefix + "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String retains a short default while identifying versioned release builds.
func String() string {
	version := strings.TrimPrefix(Version, VersionPrefix)
	if Commit == "unknown" && Date == "unknown" {
		return "iptest " + version
	}
	return fmt.Sprintf("iptest %s (commit %s; built %s)", version, Commit, Date)
}
