//go:build linux

package process

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	linuxDirentBufferSize   = 64 << 10
	linuxStatBufferSize     = 512
	linuxCmdlineBufferSize  = 4096
	linuxReadlinkBufferSize = 4096
	// linuxCommMaxLen is TASK_COMM_LEN: the kernel silently truncates comm to
	// 16 bytes including the terminating NUL, which makes 15 the largest comm
	// length we can ever see in /proc/<pid>/stat. We round to 16 to give the
	// copy loop a power-of-two byte count.
	linuxCommMaxLen = 16
	// linuxMaxProcPathSize covers "<pid>/<longest-file>\x00" with margin.
	// "<pid>" is at most 10 digits on 64-bit Linux (PIDs are 32-bit), the longest
	// suffix we use is "/cmdline\x00" (9 bytes), so 24 fits comfortably; we round
	// up to 32 to keep the buffer 4-word aligned and absorb future growth.
	linuxMaxProcPathSize    = 32
	linuxMaxWorkers         = 8
	linuxMinWorkers         = 2
	linuxJobQueueFactor     = 2
	linuxPerProcessMatchCap = 2
	linuxDecimalBase        = 10
	procStatStateOffset     = 2
)

// Per-suffix NUL-terminated string literals, so the path builder can append
// them verbatim and the raw openat/readlinkat helpers can pass &path[0]
// straight to the kernel without a stringification + BytePtrFromString round
// trip. As consts they live in the binary's rodata, which is immutable and
// adds zero per-call work.
const (
	procSuffixStat    = "/stat\x00"
	procSuffixCmdline = "/cmdline\x00"
	procSuffixExe     = "/exe\x00"
	procSuffixRoot    = "/root\x00"
)

type cmdlineInfo struct {
	argv0      string
	argv0Base  string
	script     string
	scriptBase string
}

type matchKind uint8

const (
	regularMatch matchKind = iota
	scriptMatch
)

type displayMode uint8

const (
	shortDisplay displayMode = iota
	longDisplay
)

type cmdlineReadMode uint8

const (
	cmdlineArgv0Only cmdlineReadMode = iota
	cmdlineWithScript
)

// lazyProc carries the per-PID state used by linuxMatch. It owns the lazy reads
// of exe and cmdline so the hot fast path that resolves a query against
// /proc/<pid>/stat alone never triggers the extra syscalls.
//
// The struct is intentionally referenced via *lazyProc from a stack-local
// instance inside matchLinuxProcessAt so escape analysis keeps it on the
// caller's stack. The comm bytes are copied into a fixed-size array (rather
// than referenced as a slice into the caller's stat buffer) so the stat buffer
// stays on the stack; comm is bounded to TASK_COMM_LEN (16) bytes by the
// kernel. The string form of the comm is materialised lazily only when a
// query actually matches.
type lazyProc struct {
	name     string
	nameBase string
	exe      string
	exeBase  string

	cmd cmdlineInfo

	procRootFD int
	pid        int

	nameBytes [linuxCommMaxLen]byte
	nameLen   uint8

	cmdMode cmdlineReadMode

	nameLoaded     bool
	nameBaseLoaded bool
	exeLoaded      bool
	cmdLoaded      bool
}

// NameEquals reports whether the comm name equals s without materialising the
// comm bytes as a Go string. The compiler rewrites string(b) == s to a
// byte-by-byte compare with no allocation.
func (l *lazyProc) NameEquals(s string) bool {
	return string(l.nameSlice()) == s
}

// Name returns the comm name as a Go string, materialising it on first use.
func (l *lazyProc) Name() string {
	if !l.nameLoaded {
		l.nameLoaded = true
		l.name = string(l.nameSlice())
	}

	return l.name
}

// NameBase returns baseName(Name()) memoised across queries.
func (l *lazyProc) NameBase() string {
	if !l.nameBaseLoaded {
		l.nameBaseLoaded = true
		l.nameBase = baseName(l.Name())
	}

	return l.nameBase
}

// Exe returns the resolved /proc/<pid>/exe symlink target, loading it on first
// use. The base name is memoized in the same step to avoid re-scanning the
// path for every query against the same pid.
func (l *lazyProc) Exe() string {
	if !l.exeLoaded {
		l.exeLoaded = true
		l.exe = procReadlink(l.procRootFD, l.pid, procSuffixExe)
		l.exeBase = baseName(l.exe)
	}

	return l.exe
}

// ExeBase returns baseName(Exe()) without recomputing the basename.
func (l *lazyProc) ExeBase() string {
	l.Exe()

	return l.exeBase
}

// Cmd returns the parsed /proc/<pid>/cmdline, loading it on first use.
func (l *lazyProc) Cmd() cmdlineInfo {
	if !l.cmdLoaded {
		l.cmdLoaded = true
		l.cmd = procReadCmdline(l.procRootFD, l.pid, l.cmdMode)
	}

	return l.cmd
}

// nameSlice returns the comm bytes as a slice. The slice aliases the on-stack
// array, so callers must not retain it beyond the lazyProc's lifetime.
func (l *lazyProc) nameSlice() []byte {
	return l.nameBytes[:l.nameLen]
}

type linuxPIDJob struct {
	pid   int
	order int
}

type linuxMatchChunk struct {
	matches []Match
	order   int
}

// nativeFind uses a sequential fast path for single-shot lookups and a bounded
// worker pool for broader scans, where overlapping /proc syscalls tend to win
// on Linux.
func nativeFind(ctx context.Context, names []string, opt FindOptions) ([]Match, error) {
	queries := compileQueries(names)
	if len(queries) == 0 {
		return nil, nil
	}

	sameRoot := ""
	if opt.SameRoot {
		sameRoot = linuxSameRoot()
	}

	if opt.Single || runtime.GOMAXPROCS(0) == 1 {
		return findLinuxSequential(ctx, queries, opt, sameRoot)
	}

	return findLinuxParallel(ctx, queries, opt, sameRoot)
}

// linuxSameRoot resolves the caller root path only when -c is both requested
// and meaningful. This matches pidof's root-only behavior without penalizing
// the common case.
func linuxSameRoot() string {
	if linuxEuidOnce() != 0 {
		return ""
	}

	root, err := os.Readlink("/proc/self/root")
	if err != nil || root == "" {
		return ""
	}

	return root
}

// linuxEuidOnce caches the process effective uid; it never changes for the
// lifetime of the process so we avoid the per-Find geteuid syscall.
var linuxEuidOnce = sync.OnceValue(os.Geteuid)

// findLinuxSequential keeps procfs encounter order intact and avoids extra
// goroutine setup for single-shot queries.
func findLinuxSequential(ctx context.Context, queries []query, opt FindOptions, sameRoot string) ([]Match, error) {
	procRootFD, err := openProcRoot()
	if err != nil {
		return nil, fmt.Errorf("scan /proc: %w", err)
	}
	defer closeFD(procRootFD)

	matches := make([]Match, 0, initialMatchCapacity)

	walkErr := walkLinuxPIDs(ctx, procRootFD, func(pid int) bool {
		processMatches := matchLinuxProcessAt(procRootFD, pid, queries, opt, sameRoot)
		if len(processMatches) == 0 {
			return true
		}

		matches = append(matches, processMatches...)

		return !opt.Single
	})
	if walkErr != nil {
		return nil, fmt.Errorf("scan /proc: %w", walkErr)
	}

	return matches, nil
}

// findLinuxParallel overlaps per-PID /proc reads with a small worker pool while
// restoring procfs encounter order after the parallel scan completes.
func findLinuxParallel(ctx context.Context, queries []query, opt FindOptions, sameRoot string) ([]Match, error) {
	procRootFD, err := openProcRoot()
	if err != nil {
		return nil, fmt.Errorf("scan /proc: %w", err)
	}
	defer closeFD(procRootFD)

	workerCount := min(max(runtime.GOMAXPROCS(0), linuxMinWorkers), linuxMaxWorkers)
	jobs := make(chan linuxPIDJob, workerCount*linuxJobQueueFactor)
	results := make(chan linuxMatchChunk, workerCount)

	var workers sync.WaitGroup
	for range workerCount {
		workers.Go(func() {
			for job := range jobs {
				processMatches := matchLinuxProcessAt(procRootFD, job.pid, queries, opt, sameRoot)
				if len(processMatches) == 0 {
					continue
				}

				results <- linuxMatchChunk{
					order:   job.order,
					matches: processMatches,
				}
			}
		})
	}

	var collected []linuxMatchChunk

	collectorDone := make(chan struct{})

	go func() {
		for chunk := range results {
			collected = append(collected, chunk)
		}

		close(collectorDone)
	}()

	stoppedByContext := false
	jobCount := 0
	produceErr := walkLinuxPIDs(ctx, procRootFD, func(pid int) bool {
		job := linuxPIDJob{
			pid:   pid,
			order: jobCount,
		}

		jobCount++

		select {
		case jobs <- job:
			return true
		case <-ctx.Done():
			stoppedByContext = true

			return false
		}
	})

	close(jobs)
	workers.Wait()
	close(results)
	<-collectorDone

	if produceErr == nil && stoppedByContext {
		produceErr = ctx.Err()
	}

	if produceErr != nil {
		return nil, fmt.Errorf("scan /proc: %w", produceErr)
	}

	slices.SortFunc(collected, func(a, b linuxMatchChunk) int {
		return cmp.Compare(a.order, b.order)
	})

	totalMatches := 0
	for _, chunk := range collected {
		totalMatches += len(chunk.matches)
	}

	if totalMatches == 0 {
		return nil, nil
	}

	matches := make([]Match, 0, totalMatches)
	for _, chunk := range collected {
		matches = append(matches, chunk.matches...)
	}

	return matches, nil
}

// openProcRoot opens /proc as a read-anchored directory descriptor. The same
// fd is reused for getdents64 to enumerate <pid> entries and as the dirfd
// anchor for every per-PID openat/readlinkat call, so we never re-resolve
// "/proc" inside the hot loop. We cannot use O_PATH here: O_PATH descriptors
// fail getdents64 with EBADF.
func openProcRoot() (int, error) {
	fd, err := unix.Open("/proc", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, fmt.Errorf("open /proc: %w", err)
	}

	return fd, nil
}

// matchLinuxProcessAt resolves all queries against /proc/<pid> using path-based
// openat/readlinkat off procRootFD. There is intentionally no per-PID directory
// open: the kernel performs the same path resolution either way, and skipping
// the extra openat+close pair saves two syscalls per scanned PID. The stat
// buffer is allocated here (on the stack) so the comm byte slice we get back
// stays valid for the entire query loop, deferring the string materialisation
// to the first query that actually matches.
func matchLinuxProcessAt(procRootFD, pid int, queries []query, opt FindOptions, sameRoot string) []Match {
	if shouldOmit(pid, opt.Omit) {
		return nil
	}

	var statBuf [linuxStatBufferSize]byte

	commBytes, state, ok := readLinuxStat(procRootFD, pid, statBuf[:])
	if !ok {
		return nil
	}

	if state == 'D' && !opt.IncludeDState {
		return nil
	}

	if state == 'Z' && !opt.IncludeZombie {
		return nil
	}

	if sameRoot != "" && procReadlink(procRootFD, pid, procSuffixRoot) != sameRoot {
		return nil
	}

	proc := lazyProc{
		procRootFD: procRootFD,
		pid:        pid,
	}

	// copyLen is bounded by linuxCommMaxLen (16), which fits uint8 trivially.
	copyLen := min(len(commBytes), linuxCommMaxLen)
	proc.nameLen = uint8(copy(proc.nameBytes[:], commBytes[:copyLen])) //nolint:gosec // bounded above by linuxCommMaxLen=16.

	if opt.ScriptsToo {
		proc.cmdMode = cmdlineWithScript
	}

	// matches stays nil until the first query actually matches, so the
	// overwhelming majority of scanned PIDs incur zero match-slice allocations.
	var matches []Match

	for _, q := range queries {
		matched, displayName := linuxMatch(&proc, q, opt)
		if !matched {
			continue
		}

		if matches == nil {
			matches = make([]Match, 0, linuxPerProcessMatchCap)
		}

		matches = append(matches, Match{
			Query: q.raw,
			PID:   pid,
			Name:  displayName,
		})
	}

	return matches
}

// linuxMatch checks the cheap process name first and only loads exe/cmdline
// when a query needs more detail.
func linuxMatch(proc *lazyProc, q query, opt FindOptions) (bool, string) {
	mode := shortDisplay
	if opt.LongNames {
		mode = longDisplay
	}

	if !q.fullPath && proc.NameEquals(q.base) {
		if !opt.LongNames {
			return true, proc.Name()
		}

		return true, linuxDisplayName(proc, cmdlineInfo{}, mode, regularMatch)
	}

	exe := proc.Exe()
	if q.fullPath {
		if samePath(exe, q.raw) {
			return true, linuxDisplayName(proc, cmdlineInfo{}, mode, regularMatch)
		}

		cmd := proc.Cmd()
		switch {
		case samePath(cmd.argv0, q.raw):
			return true, linuxDisplayName(proc, cmd, mode, regularMatch)
		case opt.ScriptsToo && samePath(cmd.script, q.raw):
			return true, linuxDisplayName(proc, cmd, mode, scriptMatch)
		default:
			return false, ""
		}
	}

	if proc.ExeBase() == q.base {
		return true, linuxDisplayName(proc, cmdlineInfo{}, mode, regularMatch)
	}

	var cmd cmdlineInfo
	if exe == "" || opt.ScriptsToo {
		cmd = proc.Cmd()
		if cmd.argv0Base == q.base {
			return true, linuxDisplayName(proc, cmd, mode, regularMatch)
		}
	}

	if opt.ScriptsToo && cmd.scriptBase == q.base {
		return true, linuxDisplayName(proc, cmd, mode, scriptMatch)
	}

	return false, ""
}

// linuxDisplayName chooses the most informative printable name with the data
// that has already been loaded for the current query.
func linuxDisplayName(proc *lazyProc, cmd cmdlineInfo, mode displayMode, kind matchKind) string {
	if kind == scriptMatch && cmd.scriptBase != "" {
		return cmd.scriptBase
	}

	if mode == longDisplay {
		if exec := proc.ExeBase(); exec != "" {
			return exec
		}

		if cmd.argv0Base != "" {
			return cmd.argv0Base
		}
	}

	if name := proc.NameBase(); name != "" {
		return name
	}

	if exec := proc.ExeBase(); exec != "" {
		return exec
	}

	return cmd.argv0Base
}

// walkLinuxPIDs streams numeric /proc entries without allocating per directory
// name.
func walkLinuxPIDs(ctx context.Context, procRootFD int, yield func(pid int) bool) error {
	var dirbuf [linuxDirentBufferSize]byte

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("walk /proc pids: %w", err)
		}

		n, err := unix.ReadDirent(procRootFD, dirbuf[:])
		if err != nil {
			return fmt.Errorf("read /proc entries: %w", err)
		}

		if n == 0 {
			return nil
		}

		for off := 0; off < n; {
			typ, name, ok := nextLinuxDirent(dirbuf[:n], &off)
			if !ok {
				break
			}

			if len(name) == 0 || name[0] == '.' {
				continue
			}

			if typ != unix.DT_DIR && typ != unix.DT_UNKNOWN {
				continue
			}

			pid, ok := parseUint(name)
			if !ok || pid <= 0 {
				continue
			}

			if !yield(pid) {
				return nil
			}
		}
	}
}

// readLinuxStat opens, reads, and parses /proc/<pid>/stat in a single openat
// against procRootFD. The comm bytes are returned as a slice into statBuf
// (which must outlive the returned slice); the caller decides whether/when to
// materialise it as a Go string. Returns ok=false on any error so the caller
// skips the pid without paying for fmt.Errorf wrapping on the discard path.
func readLinuxStat(procRootFD, pid int, statBuf []byte) (comm []byte, state byte, ok bool) {
	var pathBuf [linuxMaxProcPathSize]byte

	fd, err := procOpenat(procRootFD, buildProcPath(pathBuf[:0], pid, procSuffixStat))
	if err != nil {
		return nil, 0, false
	}

	n, err := unix.Read(fd, statBuf)
	closeFD(fd)

	if err != nil || n <= 0 {
		return nil, 0, false
	}

	return parseProcStatFields(statBuf[:n])
}

// procReadCmdline reads /proc/<pid>/cmdline and extracts argv[0] and,
// optionally, the first script-like argument.
func procReadCmdline(procRootFD, pid int, mode cmdlineReadMode) cmdlineInfo {
	var pathBuf [linuxMaxProcPathSize]byte

	fd, err := procOpenat(procRootFD, buildProcPath(pathBuf[:0], pid, procSuffixCmdline))
	if err != nil {
		return cmdlineInfo{}
	}

	var buf [linuxCmdlineBufferSize]byte

	n, err := unix.Read(fd, buf[:])
	closeFD(fd)

	if err != nil || n <= 0 {
		return cmdlineInfo{}
	}

	field, rest, ok := nextNULField(buf[:n])
	if !ok {
		return cmdlineInfo{}
	}

	cmd := cmdlineInfo{argv0: string(field)}
	cmd.argv0Base = baseName(cmd.argv0)

	if mode == cmdlineArgv0Only {
		return cmd
	}

	cmd.script = firstScriptArgN(cmd.argv0Base, rest, -1)
	cmd.scriptBase = baseName(cmd.script)

	return cmd
}

// procReadlink reads the procfs symlink /proc/<pid>/<suffix> with a
// stack-backed scratch buffer. Errors collapse to an empty string because the
// caller never distinguishes failure modes.
func procReadlink(procRootFD, pid int, suffix string) string {
	var (
		pathBuf [linuxMaxProcPathSize]byte
		readBuf [linuxReadlinkBufferSize]byte
	)

	n, err := procReadlinkat(procRootFD, buildProcPath(pathBuf[:0], pid, suffix), readBuf[:])
	if err != nil || n <= 0 {
		return ""
	}

	return string(readBuf[:n])
}

// buildProcPath writes "<pid>" followed by suffix (which must already include a
// trailing NUL) into buf and returns the resulting slice. The returned slice is
// safe to pass to procOpenat/procReadlinkat.
func buildProcPath(buf []byte, pid int, suffix string) []byte {
	out := strconv.AppendInt(buf[:0], int64(pid), linuxDecimalBase)

	return append(out, suffix...)
}

// procOpenat is openat(dirfd, path, O_RDONLY|O_CLOEXEC) using a raw Syscall6
// against an already NUL-terminated path. This skips the BytePtrFromString
// alloc that unix.Openat performs (one ~16-byte slice per call), which adds up
// to two skipped allocs per matched PID. The path is a stack-allocated,
// NUL-terminated buffer that the kernel reads synchronously; int<->uintptr
// widths match on every supported arch.
//
//nolint:gosec // raw syscall by design; see comment above.
func procOpenat(dirfd int, path []byte) (int, error) {
	r1, _, errno := unix.Syscall6(
		unix.SYS_OPENAT,
		uintptr(dirfd),
		uintptr(unsafe.Pointer(&path[0])),
		uintptr(unix.O_RDONLY|unix.O_CLOEXEC),
		0,
		0,
		0,
	)
	if errno != 0 {
		return 0, errno
	}

	return int(r1), nil
}

// procReadlinkat is readlinkat(dirfd, path, buf) via raw Syscall6 with a
// pre-NUL-terminated path. path and buf are stack-allocated; the syscall is
// synchronous so the Go runtime keeps both alive; int<->uintptr widths match
// on amd64/arm64. See procOpenat for the broader rationale.
//
//nolint:gosec // raw syscall by design; see comment above.
func procReadlinkat(dirfd int, path, buf []byte) (int, error) {
	r1, _, errno := unix.Syscall6(
		unix.SYS_READLINKAT,
		uintptr(dirfd),
		uintptr(unsafe.Pointer(&path[0])),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0,
		0,
	)
	if errno != 0 {
		return 0, errno
	}

	return int(r1), nil
}

// parseProcStatFields parses /proc/<pid>/stat into comm (as a slice of the
// input buffer) and state. It deliberately returns the comm slice without
// allocating; the caller materialises a string only when it intends to keep it.
func parseProcStatFields(b []byte) (comm []byte, state byte, ok bool) {
	open := bytes.IndexByte(b, '(')

	closeParen := bytes.LastIndexByte(b, ')')
	if open < 0 || closeParen <= open || closeParen+procStatStateOffset >= len(b) {
		return nil, 0, false
	}

	return b[open+1 : closeParen], b[closeParen+procStatStateOffset], true
}

// parseUint parses a decimal pid from a /proc directory name.
func parseUint(b []byte) (int, bool) {
	if len(b) == 0 {
		return 0, false
	}

	n := 0

	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}

		n = n*linuxDecimalBase + int(c-'0')
	}

	return n, true
}

// nextLinuxDirent decodes one linux_dirent64 entry from buf.
func nextLinuxDirent(buf []byte, off *int) (typ uint8, name []byte, ok bool) {
	if *off+19 > len(buf) {
		return 0, nil, false
	}

	reclen := int(binary.NativeEndian.Uint16(buf[*off+16:]))
	if reclen < 19 || *off+reclen > len(buf) {
		return 0, nil, false
	}

	typ = buf[*off+18]

	name = buf[*off+19 : *off+reclen]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}

	*off += reclen

	return typ, name, true
}

// closeFD closes a procfs descriptor on a best-effort basis.
func closeFD(fd int) {
	_ = unix.Close(fd)
}
