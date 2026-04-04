package process

import (
	"bytes"
	"slices"
	"strings"
)

const (
	deletedSuffix        = " (deleted)"
	initialMatchCapacity = 16
)

type scriptRuntime uint8

const (
	scriptRuntimeUnknown scriptRuntime = iota
	scriptRuntimeShell
	scriptRuntimePython
)

// query stores one normalized lookup target together with its basename fast path.
// shouldOmit reports whether pid was explicitly excluded by the caller.
func shouldOmit(pid int, omit map[int]struct{}) bool {
	if len(omit) == 0 {
		return false
	}

	_, ok := omit[pid]

	return ok
}

// nextNULField splits a NUL-delimited byte sequence without allocating.
func nextNULField(b []byte) (field, rest []byte, ok bool) {
	for len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}

	if len(b) == 0 {
		return nil, nil, false
	}

	if before, after, ok := bytes.Cut(b, []byte{0}); ok {
		return before, after, true
	}

	return b, nil, true
}

// firstScriptArg returns the first argv field that should be treated as a script
// path for -x matching, while skipping runtime modes such as `sh -c` and
// `python -m` that do not execute a script file at all.
func firstScriptArg(argv0 string, rest []byte) string {
	return firstScriptArgN(argv0, rest, -1)
}

// firstScriptArgN is like firstScriptArg, but scans at most maxFields argv
// entries when maxFields is non-negative.
func firstScriptArgN(argv0 string, rest []byte, maxFields int) string {
	runtime := detectScriptRuntime(argv0)
	allowOptions := true
	scanned := 0

	for {
		if maxFields >= 0 && scanned >= maxFields {
			return ""
		}

		field, next, ok := nextNULField(rest)
		if !ok {
			return ""
		}

		rest = next
		scanned++

		if len(field) == 0 {
			continue
		}

		if allowOptions && bytes.Equal(field, []byte("--")) {
			allowOptions = false

			continue
		}

		if allowOptions && scriptSearchStops(runtime, field) {
			return ""
		}

		if allowOptions && field[0] == '-' {
			continue
		}

		return string(field)
	}
}

func detectScriptRuntime(argv0 string) scriptRuntime {
	base := strings.ToLower(baseName(argv0))

	switch {
	case isShellRuntime(base):
		return scriptRuntimeShell
	case strings.HasPrefix(base, "python"), strings.HasPrefix(base, "pypy"):
		return scriptRuntimePython
	default:
		return scriptRuntimeUnknown
	}
}

func isShellRuntime(base string) bool {
	switch base {
	case "sh", "ash", "bash", "dash", "ksh", "lksh", "mksh", "oksh", "rbash", "yash", "zsh":
		return true
	default:
		return false
	}
}

func scriptSearchStops(runtime scriptRuntime, field []byte) bool {
	switch runtime {
	case scriptRuntimeShell:
		return shortOptionClusterContains(field, 'c')
	case scriptRuntimePython:
		return shortOptionClusterContains(field, 'c') || shortOptionClusterContains(field, 'm')
	case scriptRuntimeUnknown:
		return false
	default:
		return false
	}
}

func shortOptionClusterContains(field []byte, want byte) bool {
	if len(field) < 2 || field[0] != '-' || field[1] == '-' {
		return false
	}

	return slices.Contains(field[1:], want)
}
