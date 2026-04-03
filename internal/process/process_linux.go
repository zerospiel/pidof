//go:build linux

package process

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"runtime"
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
	linuxPIDTextSize        = 20
	linuxMaxWorkers         = 8
	linuxJobQueueFactor     = 2
)

type cmdlineInfo struct {
	argv0  string
	script string
}

// nativeList walks /proc sequentially and materializes a full process snapshot.
func nativeList(ctx context.Context) ([]Process, error) {
	processes := make([]Process, 0, 256)

	err := walkLinuxProcDirs(ctx, func(pid, pidDirFD int) (bool, error) {
		process, err := readLinuxProcessAt(pid, pidDirFD)
		if err != nil {
			return false, nil
		}

		processes = append(processes, process)
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}

	return processes, nil
}

// nativeFind uses a sequential fast path for single-shot lookups and a bounded worker
// pool for broader scans, where overlapping /proc syscalls tends to win on Linux.
func nativeFind(ctx context.Context, names []string, opt FindOptions) ([]Match, error) {
	queries := compileQueries(names)
	if len(queries) == 0 {
		return nil, nil
	}

	sameRoot := linuxSameRoot(opt.SameRoot)

	if opt.Single || runtime.GOMAXPROCS(0) == 1 {
		return findLinuxSequential(ctx, queries, opt, sameRoot)
	}

	return findLinuxParallel(ctx, queries, opt, sameRoot)
}

// linuxSameRoot resolves the caller root path only when -c is both requested and
// meaningful. This matches pidof's root-only behavior without penalizing the
// common case.
func linuxSameRoot(enabled bool) string {
	if !enabled || os.Geteuid() != 0 {
		return ""
	}

	root, err := os.Readlink("/proc/self/root")
	if err != nil || root == "" {
		return ""
	}

	return root
}

// findLinuxSequential keeps deterministic traversal order and avoids extra
// goroutine setup for single-shot queries.
func findLinuxSequential(ctx context.Context, queries []query, opt FindOptions, sameRoot string) ([]Match, error) {
	matches := make([]Match, 0, initialMatchCapacity)

	err := walkLinuxProcDirs(ctx, func(pid, pidDirFD int) (bool, error) {
		processMatches, err := matchLinuxProcessAt(pid, pidDirFD, queries, opt, sameRoot)
		if err != nil {
			return false, nil
		}
		if len(processMatches) == 0 {
			return false, nil
		}

		matches = append(matches, processMatches...)
		return opt.Single, nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan /proc: %w", err)
	}

	sortMatches(matches)
	return matches, nil
}

// findLinuxParallel overlaps per-PID /proc reads with a small worker pool. The
// worker count is capped to avoid turning the scan into FD or scheduler churn.
func findLinuxParallel(ctx context.Context, queries []query, opt FindOptions, sameRoot string) ([]Match, error) {
	procRootFD, err := unix.Open("/proc", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /proc: %w", err)
	}
	defer unix.Close(procRootFD)

	workerCount := min(max(runtime.GOMAXPROCS(0), 2), linuxMaxWorkers)
	jobs := make(chan int, workerCount*linuxJobQueueFactor)
	results := make(chan []Match, workerCount)

	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()

			local := make([]Match, 0, 8)
			for pid := range jobs {
				processMatches, err := matchLinuxProcess(procRootFD, pid, queries, opt, sameRoot)
				if err != nil || len(processMatches) == 0 {
					continue
				}

				local = append(local, processMatches...)
			}

			if len(local) > 0 {
				results <- local
			}
		}()
	}

	stoppedByContext := false
	produceErr := walkLinuxPIDs(ctx, procRootFD, func(pid int) bool {
		select {
		case jobs <- pid:
			return true
		case <-ctx.Done():
			stoppedByContext = true
			return false
		}
	})

	close(jobs)
	workers.Wait()
	close(results)

	if produceErr == nil && stoppedByContext {
		produceErr = ctx.Err()
	}
	if produceErr != nil {
		return nil, fmt.Errorf("scan /proc: %w", produceErr)
	}

	matches := make([]Match, 0, initialMatchCapacity)
	for chunk := range results {
		matches = append(matches, chunk...)
	}

	sortMatches(matches)
	return matches, nil
}

// matchLinuxProcess opens /proc/<pid> and delegates to the per-directory matcher.
func matchLinuxProcess(procRootFD, pid int, queries []query, opt FindOptions, sameRoot string) ([]Match, error) {
	var pidText [linuxPIDTextSize]byte

	pidDirFD, err := unix.Openat(
		procRootFD,
		bytesToString(strconv.AppendInt(pidText[:0], int64(pid), 10)),
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("open /proc/%d: %w", pid, err)
	}
	defer unix.Close(pidDirFD)

	return matchLinuxProcessAt(pid, pidDirFD, queries, opt, sameRoot)
}

// matchLinuxProcessAt matches one already-open /proc/<pid> directory.
func matchLinuxProcessAt(pid, pidDirFD int, queries []query, opt FindOptions, sameRoot string) ([]Match, error) {
	if shouldOmit(pid, opt.Omit) {
		return nil, nil
	}

	name, state, ppid, ok := readLinuxStatAt(pidDirFD)
	if !ok {
		return nil, errors.New("parse stat")
	}

	if state == 'D' && !opt.IncludeDState {
		return nil, nil
	}
	if state == 'Z' && !opt.IncludeZombie {
		return nil, nil
	}

	if sameRoot != "" {
		root, err := readlinkAt(pidDirFD, "root")
		if err != nil || root != sameRoot {
			return nil, nil
		}
	}

	process := Process{
		PID:   pid,
		PPID:  ppid,
		State: state,
		Name:  name,
	}

	// exe and cmdline reads are relatively expensive, so keep them lazy and shared
	// across all query checks for this pid.
	var exe string
	var exeLoaded bool
	loadExe := func() string {
		if exeLoaded {
			return exe
		}
		exeLoaded = true
		exe, _ = readlinkAt(pidDirFD, "exe")
		return exe
	}

	var cmd cmdlineInfo
	var cmdLoaded bool
	loadCmd := func() cmdlineInfo {
		if cmdLoaded {
			return cmd
		}
		cmdLoaded = true
		cmd, _ = readCmdlineInfoAt(pidDirFD, opt.ScriptsToo)
		return cmd
	}

	matches := make([]Match, 0, 2)
	for _, query := range queries {
		matched, name := linuxMatch(process, query, opt, loadExe, loadCmd)
		if !matched {
			continue
		}

		matches = append(matches, Match{
			Query: query.raw,
			PID:   pid,
			Name:  name,
		})
	}

	return matches, nil
}

// linuxMatch checks the cheap process name first and only loads exe/cmdline when
// a query needs more detail.
func linuxMatch(process Process, query query, opt FindOptions, loadExe func() string, loadCmd func() cmdlineInfo) (bool, string) {
	mode := shortDisplay
	if opt.LongNames {
		mode = longDisplay
	}

	if !query.fullPath && process.Name == query.base {
		if !opt.LongNames {
			return true, process.Name
		}
		return true, linuxDisplayName(process, loadExe(), cmdlineInfo{}, mode, regularMatch)
	}

	exe := loadExe()
	if query.fullPath {
		if samePath(exe, query.raw) {
			return true, linuxDisplayName(process, exe, cmdlineInfo{}, mode, regularMatch)
		}

		cmd := loadCmd()
		switch {
		case samePath(cmd.argv0, query.raw):
			return true, linuxDisplayName(process, exe, cmd, mode, regularMatch)
		case opt.ScriptsToo && samePath(cmd.script, query.raw):
			return true, linuxDisplayName(process, exe, cmd, mode, scriptMatch)
		default:
			return false, ""
		}
	}

	if baseName(exe) == query.base {
		return true, linuxDisplayName(process, exe, cmdlineInfo{}, mode, regularMatch)
	}

	cmd := cmdlineInfo{}
	if exe == "" || opt.ScriptsToo {
		cmd = loadCmd()
		if baseName(cmd.argv0) == query.base {
			return true, linuxDisplayName(process, exe, cmd, mode, regularMatch)
		}
	}

	if opt.ScriptsToo && baseName(cmd.script) == query.base {
		return true, linuxDisplayName(process, exe, cmd, mode, scriptMatch)
	}

	return false, ""
}

// linuxDisplayName chooses the most informative printable name with the data that
// has already been loaded for the current query.
func linuxDisplayName(process Process, exe string, cmd cmdlineInfo, mode displayMode, kind matchKind) string {
	if kind == scriptMatch {
		if script := baseName(cmd.script); script != "" {
			return script
		}
	}

	if mode == longDisplay {
		if exec := baseName(exe); exec != "" {
			return exec
		}
		if argv0 := baseName(cmd.argv0); argv0 != "" {
			return argv0
		}
	}

	if name := baseName(process.Name); name != "" {
		return name
	}
	if exec := baseName(exe); exec != "" {
		return exec
	}
	return baseName(cmd.argv0)
}

// walkLinuxProcDirs sequentially iterates /proc numeric directories and opens
// each matching process directory for the callback.
func walkLinuxProcDirs(ctx context.Context, fn func(pid, pidDirFD int) (stop bool, err error)) error {
	procRootFD, err := unix.Open("/proc", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open /proc: %w", err)
	}
	defer unix.Close(procRootFD)

	var callbackErr error
	err = walkLinuxPIDs(ctx, procRootFD, func(pid int) bool {
		var pidText [linuxPIDTextSize]byte

		pidDirFD, err := unix.Openat(
			procRootFD,
			bytesToString(strconv.AppendInt(pidText[:0], int64(pid), 10)),
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC,
			0,
		)
		if err != nil {
			return true
		}

		stop, cbErr := fn(pid, pidDirFD)
		unix.Close(pidDirFD)
		if cbErr != nil {
			callbackErr = cbErr
			return false
		}
		return !stop
	})
	if err != nil {
		return fmt.Errorf("walk /proc directories: %w", err)
	}
	return callbackErr
}

// walkLinuxPIDs streams numeric /proc entries without allocating per directory name.
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

// readLinuxProcessAt loads the process details used by the snapshot API.
func readLinuxProcessAt(pid, pidDirFD int) (Process, error) {
	name, state, ppid, ok := readLinuxStatAt(pidDirFD)
	if !ok {
		return Process{}, errors.New("parse stat")
	}

	exe, _ := readlinkAt(pidDirFD, "exe")
	cmd, _ := readCmdlineInfoAt(pidDirFD, true)

	return Process{
		PID:    pid,
		PPID:   ppid,
		State:  state,
		Name:   name,
		Exe:    exe,
		Argv0:  cmd.argv0,
		Script: cmd.script,
	}, nil
}

// readLinuxStatAt parses /proc/<pid>/stat.
func readLinuxStatAt(pidDirFD int) (name string, state byte, ppid int, ok bool) {
	var statBuf [linuxStatBufferSize]byte

	n, err := readSmallFileAt(pidDirFD, "stat", statBuf[:])
	if err != nil || n == 0 {
		return "", 0, 0, false
	}

	return parseProcStat(statBuf[:n])
}

// readSmallFileAt reads small procfs files with one open and one read.
func readSmallFileAt(dirFD int, name string, dst []byte) (int, error) {
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", name, err)
	}
	defer unix.Close(fd)

	n, err := unix.Read(fd, dst)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", name, err)
	}

	return n, nil
}

// readCmdlineInfoAt reads argv0 and, optionally, the first script-like argument.
func readCmdlineInfoAt(dirFD int, wantScript bool) (cmdlineInfo, error) {
	var buf [linuxCmdlineBufferSize]byte

	n, err := readSmallFileAt(dirFD, "cmdline", buf[:])
	if err != nil {
		return cmdlineInfo{}, fmt.Errorf("read cmdline: %w", err)
	}
	if n == 0 {
		return cmdlineInfo{}, errors.New("read cmdline: empty file")
	}

	field, rest, ok := nextNULField(buf[:n])
	if !ok {
		return cmdlineInfo{}, nil
	}

	cmd := cmdlineInfo{
		argv0: string(field),
	}
	if !wantScript {
		return cmd, nil
	}

	for {
		field, next, ok := nextNULField(rest)
		if !ok {
			return cmd, nil
		}
		rest = next
		if len(field) == 0 || field[0] == '-' {
			continue
		}

		cmd.script = string(field)
		return cmd, nil
	}
}

// readlinkAt reads a procfs symlink into a stack-backed buffer.
func readlinkAt(dirFD int, name string) (string, error) {
	var buf [linuxReadlinkBufferSize]byte

	n, err := unix.Readlinkat(dirFD, name, buf[:])
	if err != nil {
		return "", fmt.Errorf("readlink %s: %w", name, err)
	}
	if n <= 0 {
		return "", fmt.Errorf("readlink %s: empty target", name)
	}

	return string(buf[:n]), nil
}

// parseProcStat extracts comm, state, and ppid from /proc/<pid>/stat.
func parseProcStat(b []byte) (name string, state byte, ppid int, ok bool) {
	open := bytes.IndexByte(b, '(')
	close := bytes.LastIndexByte(b, ')')
	if open < 0 || close <= open || close+4 > len(b) {
		return "", 0, 0, false
	}

	name = string(b[open+1 : close])

	stateIndex := close + 2
	if stateIndex >= len(b) {
		return "", 0, 0, false
	}
	state = b[stateIndex]

	ppidStart := stateIndex + 2
	if ppidStart >= len(b) {
		return "", 0, 0, false
	}

	ppid, ok = parseInt(b[ppidStart:])
	return name, state, ppid, ok
}

// parseInt parses the leading decimal integer from b.
func parseInt(b []byte) (int, bool) {
	if len(b) == 0 {
		return 0, false
	}

	sign := 1
	i := 0
	if b[0] == '-' {
		sign = -1
		i++
	}
	if i >= len(b) || b[i] < '0' || b[i] > '9' {
		return 0, false
	}

	n := 0
	for ; i < len(b); i++ {
		c := b[i]
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}

	return sign * n, true
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
		n = n*10 + int(c-'0')
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

// bytesToString converts a short-lived byte slice to string without allocation.
func bytesToString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}
