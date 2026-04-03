//go:build darwin

package process

import (
	"context"
	"encoding/binary"
	"fmt"

	"golang.org/x/sys/unix"
)

type darwinProcArgs struct {
	exec   string
	argv0  string
	script string
}

// nativeList returns a full Darwin process snapshot from the kernel process table.
func nativeList(ctx context.Context) ([]Process, error) {
	kprocs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("read kern.proc.all: %w", err)
	}

	processes := make([]Process, 0, len(kprocs))
	for i := range kprocs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("list processes: %w", err)
		}

		kp := &kprocs[i]

		pid := int(kp.Proc.P_pid)
		if pid <= 0 {
			continue
		}

		short := kp.Proc.P_comm[:]

		shortLen := cStringLen(short)
		if shortLen == 0 {
			continue
		}

		processes = append(processes, Process{
			PID:  pid,
			PPID: int(kp.Eproc.Ppid),
			Name: string(short[:shortLen]),
		})
	}

	return processes, nil
}

// nativeFind scans the Darwin kinfo table sequentially because the source data is already
// in one contiguous kernel buffer, so extra goroutines would mostly add scheduling
// overhead and reduce cache locality.
func nativeFind(ctx context.Context, names []string, opt FindOptions) ([]Match, error) {
	queries := compileQueries(names)
	if len(queries) == 0 {
		return nil, nil
	}

	kprocs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("read kern.proc.all: %w", err)
	}

	matches := make([]Match, 0, initialMatchCapacity)

	for i := range kprocs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("find processes: %w", err)
		}

		kp := &kprocs[i]

		pid := int(kp.Proc.P_pid)
		if pid <= 0 || shouldOmit(pid, opt.Omit) {
			continue
		}

		short := kp.Proc.P_comm[:]

		shortLen := cStringLen(short)
		if shortLen == 0 {
			continue
		}

		short = short[:shortLen]

		// Long-name and script matching require a slower kern.procargs2 lookup, so
		// keep it lazy and pay for it only once per candidate PID.
		var (
			procArgs   darwinProcArgs
			argsLoaded bool
		)

		loadArgs := func() darwinProcArgs {
			if argsLoaded {
				return procArgs
			}

			argsLoaded = true
			procArgs = readDarwinProcArgs(pid)

			return procArgs
		}

		for _, query := range queries {
			matched, name := darwinMatch(short, query, opt, loadArgs)
			if !matched {
				continue
			}

			matches = append(matches, Match{
				Query: query.raw,
				PID:   pid,
				Name:  name,
			})
			if opt.Single {
				return matches, nil
			}
		}
	}

	sortMatches(matches)

	return matches, nil
}

// darwinMatch resolves a query against the short name first, then lazily consults
// argv data only when the short-name fast path is insufficient.
func darwinMatch(short []byte, query query, opt FindOptions, loadArgs func() darwinProcArgs) (bool, string) {
	mode := shortDisplay
	if opt.LongNames {
		mode = longDisplay
	}

	if !query.fullPath && cStringEqual(short, query.base) {
		if !opt.LongNames {
			return true, query.base
		}

		procArgs := loadArgs()

		return true, darwinDisplayName(short, procArgs, mode, regularMatch)
	}

	if !query.fullPath && !opt.ScriptsToo && (len(short) >= len(query.base) || !cStringPrefix(short, query.base)) {
		return false, ""
	}

	procArgs := loadArgs()

	if query.fullPath {
		switch {
		case samePath(procArgs.exec, query.raw):
			return true, darwinDisplayName(short, procArgs, mode, regularMatch)
		case samePath(procArgs.argv0, query.raw):
			return true, darwinDisplayName(short, procArgs, mode, regularMatch)
		case opt.ScriptsToo && samePath(procArgs.script, query.raw):
			return true, darwinDisplayName(short, procArgs, mode, scriptMatch)
		default:
			return false, ""
		}
	}

	switch {
	case baseName(procArgs.exec) == query.base:
		return true, darwinDisplayName(short, procArgs, mode, regularMatch)
	case baseName(procArgs.argv0) == query.base:
		return true, darwinDisplayName(short, procArgs, mode, regularMatch)
	case opt.ScriptsToo && baseName(procArgs.script) == query.base:
		return true, darwinDisplayName(short, procArgs, mode, scriptMatch)
	default:
		return false, ""
	}
}

// darwinDisplayName picks the most informative printable name without extra work.
func darwinDisplayName(short []byte, procArgs darwinProcArgs, mode displayMode, kind matchKind) string {
	if kind == scriptMatch {
		if script := baseName(procArgs.script); script != "" {
			return script
		}
	}

	if mode == longDisplay {
		if argv0 := baseName(procArgs.argv0); argv0 != "" {
			return argv0
		}

		if exec := baseName(procArgs.exec); exec != "" {
			return exec
		}
	}

	return string(short)
}

// readDarwinProcArgs extracts exec, argv0, and the first script argument from
// kern.procargs2. The buffer layout is argc, exec path, NUL padding, then argv.
func readDarwinProcArgs(pid int) darwinProcArgs {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return darwinProcArgs{}
	}

	argc := int(binary.NativeEndian.Uint32(raw[:4]))
	if argc <= 0 {
		return darwinProcArgs{}
	}

	execPath, rest, ok := nextNULField(raw[4:])
	if !ok {
		return darwinProcArgs{}
	}

	argv0, rest, ok := nextNULField(rest)
	if !ok {
		return darwinProcArgs{exec: string(execPath)}
	}

	procArgs := darwinProcArgs{
		exec:  string(execPath),
		argv0: string(argv0),
	}

	for argc--; argc > 0; argc-- {
		field, next, ok := nextNULField(rest)
		if !ok {
			break
		}

		rest = next

		if len(field) == 0 || field[0] == '-' {
			continue
		}

		procArgs.script = string(field)

		break
	}

	return procArgs
}

// cStringLen reports the length up to the first NUL byte.
func cStringLen(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}

	return len(b)
}

// cStringEqual compares a C-style byte slice against a Go string.
func cStringEqual(p []byte, s string) bool {
	if len(p) != len(s) {
		return false
	}

	for i := range p {
		if p[i] != s[i] {
			return false
		}
	}

	return true
}

// cStringPrefix reports whether the C-style byte slice is a prefix of s.
func cStringPrefix(p []byte, s string) bool {
	if len(p) == 0 || len(p) > len(s) {
		return false
	}

	for i := range p {
		if p[i] != s[i] {
			return false
		}
	}

	return true
}
