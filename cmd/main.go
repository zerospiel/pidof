// Package main provides the pidof command-line entrypoint.
package main

import (
	"os"

	"github.com/zerospiel/pidof/internal/pidof"
)

func main() {
	os.Exit(pidof.Main(os.Args[1:], os.Stdout, os.Stderr))
}
