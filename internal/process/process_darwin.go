//go:build darwin

package process

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/user"
	"slices"
	"strconv"
	"sync"

	"golang.org/x/sys/unix"
)

type darwinProcArgs struct {
	exec       string
	execBase   string
	argv0      string
	argv0Base  string
	script     string
	scriptBase string
}

const darwinUserCacheCap = 8

// darwinCurrentUser caches the effective uid and its resolved login name for
// the lifetime of the process. The first Open Directory / getpwuid_r round
// trip is the single most expensive cgo call in the -l path; lifting it out
// of Find keeps every per-Find user cache primed for the overwhelmingly
// common "matches belong to the calling user" case.
var darwinCurrentUser = sync.OnceValues(func() (uint32, string) {
	uid := os.Geteuid()
	if uid < 0 {
		return 0, ""
	}

	uidU := uint32(uid)

	return uidU, resolveDarwinUserName(uidU)
})

func resolveDarwinUserName(uid uint32) string {
	name := strconv.FormatUint(uint64(uid), 10)

	entry, err := user.LookupId(name)
	if err == nil && entry.Username != "" {
		return entry.Username
	}

	return name
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

	// Without -l the User field is never printed, so skip the entire login
	// name resolution path (cgo into opendirectoryd on macOS).
	var userNames map[uint32]string
	if opt.LongNames {
		userNames = make(map[uint32]string, darwinUserCacheCap)

		if uid, name := darwinCurrentUser(); name != "" {
			userNames[uid] = name
		}
	}

	for _, v := range slices.Backward(kprocs) {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("find processes: %w", err)
		}

		kp := &v

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

		// Path and script matching require a slower kern.procargs2 lookup, so
		// keep it lazy and pay for it only once per candidate PID.
		var (
			procArgs   darwinProcArgs
			argsLoaded bool

			userName     string
			userResolved bool
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

			if opt.LongNames && !userResolved {
				userResolved = true
				userName = darwinUserName(kp.Eproc.Ucred.Uid, userNames)
			}

			matches = append(matches, Match{
				Query: query.raw,
				PID:   pid,
				Name:  name,
				User:  userName,
			})
			if opt.Single {
				return matches, nil
			}
		}
	}

	return matches, nil
}

// darwinMatch resolves a query against the short name first, then lazily consults
// argv data only when the short-name fast path is insufficient.
func darwinMatch(short []byte, query query, opt FindOptions, loadArgs func() darwinProcArgs) (bool, string) {
	if !query.fullPath && cStringEqual(short, query.base) {
		return true, string(short)
	}

	if !query.fullPath && cStringContainsFold(short, query.base) {
		return true, string(short)
	}

	if !query.fullPath && !opt.ScriptsToo && (len(short) >= len(query.base) || !cStringPrefix(short, query.base)) {
		return false, ""
	}

	procArgs := loadArgs()

	if query.fullPath {
		switch {
		case samePath(procArgs.exec, query.raw):
			return true, string(short)
		case samePath(procArgs.argv0, query.raw):
			return true, string(short)
		case opt.ScriptsToo && samePath(procArgs.script, query.raw):
			return true, string(short)
		default:
			return false, ""
		}
	}

	switch {
	case stringContainsFold(procArgs.execBase, query.base):
		return true, string(short)
	case stringContainsFold(procArgs.argv0Base, query.base):
		return true, string(short)
	case opt.ScriptsToo && stringContainsFold(procArgs.scriptBase, query.base):
		return true, string(short)
	default:
		return false, ""
	}
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

	procArgs.execBase = baseName(procArgs.exec)
	procArgs.argv0Base = baseName(procArgs.argv0)

	if argc > 1 {
		procArgs.script = firstScriptArgN(procArgs.argv0Base, rest, argc-1)
		procArgs.scriptBase = baseName(procArgs.script)
	}

	return procArgs
}

// darwinUserName resolves a numeric uid to the long-output login name format.
func darwinUserName(uid uint32, cache map[uint32]string) string {
	if name, ok := cache[uid]; ok {
		return name
	}

	name := resolveDarwinUserName(uid)
	cache[uid] = name

	return name
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

// cStringContainsFold reports whether the C-style byte slice contains s under
// an ASCII-only case fold.
func cStringContainsFold(p []byte, s string) bool {
	if len(s) == 0 || len(p) < len(s) {
		return false
	}

	first := foldASCII(s[0])

	limit := len(p) - len(s)
	for i := 0; i <= limit; i++ {
		if foldASCII(p[i]) != first {
			continue
		}

		if cStringEqualFold(p[i:i+len(s)], s) {
			return true
		}
	}

	return false
}

// cStringEqualFold compares a C-style byte slice and string with ASCII folding.
func cStringEqualFold(p []byte, s string) bool {
	if len(p) != len(s) {
		return false
	}

	for i := range p {
		if foldASCII(p[i]) != foldASCII(s[i]) {
			return false
		}
	}

	return true
}

// stringContainsFold reports whether p contains s under an ASCII-only case fold.
func stringContainsFold(p, s string) bool {
	if len(s) == 0 || len(p) < len(s) {
		return false
	}

	first := foldASCII(s[0])

	limit := len(p) - len(s)
	for i := 0; i <= limit; i++ {
		if foldASCII(p[i]) != first {
			continue
		}

		if stringEqualASCIIFold(p[i:i+len(s)], s) {
			return true
		}
	}

	return false
}
