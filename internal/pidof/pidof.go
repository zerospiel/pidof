// Package pidof implements process lookup by executable name.
package pidof

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

// Process models one process row from `ps` output.
type Process struct {
	PID     int
	Command string
	Args    string
}

// ProcessLister returns a snapshot of running processes.
type ProcessLister interface {
	List(ctx context.Context) ([]Process, error)
}

// Finder resolves process IDs by executable name.
type Finder struct {
	Lister ProcessLister
}

type psLister struct{}

// Main implements the pidof command behavior and returns a process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if isVersionRequest(args) {
		_, _ = fmt.Fprintln(stdout, BuildInfo())
		return 0
	}

	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: pidof <name> [name ...]")
		return 1
	}

	finder := Finder{Lister: psLister{}}
	pids, err := finder.Find(context.Background(), args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pidof: %v\n", err)
		return 1
	}

	if len(pids) == 0 {
		return 1
	}

	parts := make([]string, 0, len(pids))
	for _, pid := range pids {
		parts = append(parts, strconv.Itoa(pid))
	}
	_, _ = fmt.Fprintln(stdout, strings.Join(parts, " "))
	return 0
}

// BuildInfo returns the current binary version and build time.
func BuildInfo() string {
	return fmt.Sprintf("version=%s buildTime=%s", version, buildTime)
}

// isVersionRequest reports whether CLI args request version output.
func isVersionRequest(args []string) bool {
	if len(args) != 1 {
		return false
	}

	switch args[0] {
	case "--version", "-version", "-v":
		return true
	default:
		return false
	}
}

// Find returns sorted unique process IDs matching any requested executable name.
func (f Finder) Find(ctx context.Context, names []string) ([]int, error) {
	if f.Lister == nil {
		f.Lister = psLister{}
	}

	processes, err := f.Lister.List(ctx)
	if err != nil {
		return nil, err
	}

	queries := make([]string, 0, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed != "" {
			queries = append(queries, filepath.Base(trimmed))
		}
	}
	if len(queries) == 0 {
		return nil, nil
	}

	seen := map[int]struct{}{}
	pids := make([]int, 0)
	for _, process := range processes {
		for _, query := range queries {
			if matchProcess(process, query) {
				if _, ok := seen[process.PID]; !ok {
					seen[process.PID] = struct{}{}
					pids = append(pids, process.PID)
				}
				break
			}
		}
	}

	sort.Ints(pids)
	return pids, nil
}

// matchProcess checks command and argv[0] executable names against one query.
func matchProcess(process Process, query string) bool {
	if filepath.Base(process.Command) == query {
		return true
	}

	arg0 := firstField(process.Args)
	if arg0 != "" && filepath.Base(arg0) == query {
		return true
	}

	return false
}

// firstField returns the first whitespace-separated token from input.
func firstField(input string) string {
	field, _ := splitField(input)
	return field
}

// List executes `ps` and returns parsed process rows.
func (psLister) List(ctx context.Context) ([]Process, error) {
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,comm=,args=")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing processes: %w", err)
	}

	return parsePSOutput(output)
}

// parsePSOutput parses `ps` raw output into process structs.
func parsePSOutput(raw []byte) ([]Process, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	processes := make([]Process, 0)
	for scanner.Scan() {
		line := scanner.Text()
		process, ok := parsePSLine(line)
		if !ok {
			continue
		}
		processes = append(processes, process)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading ps output: %w", err)
	}

	return processes, nil
}

// parsePSLine parses one `ps` output line.
func parsePSLine(line string) (Process, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Process{}, false
	}

	pidField, rest := splitField(line)
	if pidField == "" {
		return Process{}, false
	}

	pid, err := strconv.Atoi(pidField)
	if err != nil {
		return Process{}, false
	}

	commandField, argsField := splitField(rest)
	if commandField == "" {
		return Process{}, false
	}

	return Process{
		PID:     pid,
		Command: commandField,
		Args:    strings.TrimSpace(argsField),
	}, true
}

// splitField returns the first token and the remaining string.
func splitField(input string) (string, string) {
	normalized := strings.TrimSpace(strings.ReplaceAll(input, "\t", " "))
	if normalized == "" {
		return "", ""
	}

	field, rest, found := strings.Cut(normalized, " ")
	if !found {
		return field, ""
	}

	return field, strings.TrimSpace(rest)
}
