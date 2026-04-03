// Package main provides the pidof command-line entrypoint.
package main

import (
	"context"
	"os"

	"github.com/zerospiel/pidof/internal/pidof"
	versionpkg "github.com/zerospiel/pidof/internal/version"
)

var version = "" // overrides the version, if set by the linker flags

func main() {
	versionpkg.VersionOverride = version

	pidof.Main(context.Background(), os.Args[1:])
}
