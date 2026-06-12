package process

import (
	"bytes"
	"slices"
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

// shellRuntimeNames is the static set of POSIX-shell argv0 base names
// recognised for -x script detection. The list is sorted strictly by length so
// detectScriptRuntime can bail out the instant it sees a name longer than the
// candidate base.
var shellRuntimeNames = [...]string{
	"sh",
	"ash", "ksh", "zsh",
	"bash", "dash", "lksh", "mksh", "oksh", "yash",
	"rbash",
}

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

// firstScriptArgN returns the first argv field that should be treated as a
// script path for -x matching, while skipping runtime modes such as `sh -c`
// and `python -m` that do not execute a script file at all. argv0Base must be
// baseName(argv0); the caller passes it in so the function doesn't pay for a
// second normalisation pass when the caller already needs the basename. When
// maxFields is non-negative the scan stops after that many argv entries.
func firstScriptArgN(argv0Base string, rest []byte, maxFields int) string {
	runtime := detectScriptRuntime(argv0Base)
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

		// "--" disables option parsing without consuming a positional.
		if allowOptions && len(field) == 2 && field[0] == '-' && field[1] == '-' {
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

// detectScriptRuntime classifies an argv0 basename against the runtimes
// recognised by -x. The input must already be baseName'd; it uses
// shellRuntimeNames' length ordering to bail out before any byte comparison
// once the candidate runs out of matchable names.
func detectScriptRuntime(argv0Base string) scriptRuntime {
	if argv0Base == "" {
		return scriptRuntimeUnknown
	}

	baseLen := len(argv0Base)

	for _, name := range shellRuntimeNames {
		if len(name) > baseLen {
			break
		}

		if len(name) == baseLen && stringEqualASCIIFold(argv0Base, name) {
			return scriptRuntimeShell
		}
	}

	if stringHasASCIIPrefixFold(argv0Base, "python") || stringHasASCIIPrefixFold(argv0Base, "pypy") {
		return scriptRuntimePython
	}

	return scriptRuntimeUnknown
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

// stringEqualASCIIFold reports whether two strings are equal under ASCII-only
// case folding. It avoids the Unicode-aware strings.EqualFold cost since every
// runtime name we compare is plain ASCII.
func stringEqualASCIIFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range len(a) {
		if foldASCII(a[i]) != foldASCII(b[i]) {
			return false
		}
	}

	return true
}

// stringHasASCIIPrefixFold reports whether s starts with prefix under ASCII
// case folding.
func stringHasASCIIPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}

	for i := range len(prefix) {
		if foldASCII(s[i]) != foldASCII(prefix[i]) {
			return false
		}
	}

	return true
}

// foldASCII lowercases a single ASCII byte without paying Unicode case-fold
// costs. It is the single source of truth for ASCII folding across the package.
func foldASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}

	return b
}
