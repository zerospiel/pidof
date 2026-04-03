// Package version reports build and release metadata for the binary.
package version

import (
	"runtime/debug"
)

// VersionOverride overrides the version of the application at the link time.
var VersionOverride = ""

// PathVersion returns the module path and version derived from build metadata.
// VersionOverride, when set by linker flags, takes precedence over build info.
func PathVersion() (path, ver string) {
	path, ver = "pidof", "(unknown)"

	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Path != "" {
			path = info.Main.Path
		}

		if info.Main.Version != "" {
			ver = info.Main.Version
		}
	}

	if VersionOverride != "" {
		ver = VersionOverride
	}

	return path, ver
}
