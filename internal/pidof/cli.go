// Package pidof implements the pidof command-line interface.
package pidof

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/zerospiel/pidof/internal/process"
	versionpkg "github.com/zerospiel/pidof/internal/version"
)

const (
	defaultSeparator = " "
	exitSuccess      = 0
	exitNoMatch      = 1
	exitUsage        = 2
)

const usageText = `usage: pidof [-k] [-l] [-h|-?] [-v] [-s] [-c] [-x] [-q] [-z] [-d sep] [-o omitpid[,omitpid...]] name [name ...]

	-k	Kill processes for given pid name
		  (Note: You must have sufficient privileges!)
	-l	List long output
	-h -?	This help screen
	-v	Print out the version info

	-s	Single shot - this instructs the program to only return one pid
	-c	Only return process ids that are running with the same root directory. This option is ignored for non-root users, as they will be unable to check the current root directory of processes they do not own
	-x	Scripts too - this causes the program to also return process id's of shells running the named scripts
	-o pid	Tells pidof to omit processes with that process id. The special pid %PPID can be used to name the parent process of the pidof program, in other words the calling shell or shell script
	-q	Quiet mode, suppress any output and only sets the exit status accordingly
	-d sep	Tells pidof to use sep as an output separator if more than one PID is shown. The default separator is a space
	-z	Include zombie and D-state processes
`

type killFunc func(int) error

// cliOptions keeps the parsed flag state together so Main can stay straight-line
// without dragging a long var list around. The bools live at the end to keep
// the layout tight on 64-bit targets.
type cliOptions struct {
	separator            string
	queries              []string
	omitSpecs            omitSpecList
	kill                 bool
	long                 bool
	showHelp             bool
	showVersion          bool
	single               bool
	sameRoot             bool
	scriptsToo           bool
	quiet                bool
	includeSpecialStates bool
}

// omitSpecList stores the raw -o values exactly as accepted by pidof(8):
// repeated -o flags, each optionally containing a comma-separated pid list, and
// the special token %PPID.
type omitSpecList []string

// Main runs the pidof CLI and exits the process for non-zero status codes.
func Main(ctx context.Context, args []string) { //nolint:revive // Main is the process-facing CLI entrypoint and intentionally exits on non-zero status codes.
	if code := run(ctx, args); code != exitSuccess {
		os.Exit(code)
	}
}

// run runs the pidof CLI with process-local I/O and returns its exit code.
func run(ctx context.Context, args []string) int {
	opts, code, ok := loadRunOptions(args)
	if !ok {
		return code
	}

	matches, err := findMatches(ctx, opts)
	if err != nil {
		return usageError(err)
	}

	return finishRun(opts, matches)
}

func loadRunOptions(args []string) (cliOptions, int, bool) {
	opts, err := parseOptions(args)
	if err != nil {
		return cliOptions{}, usageError(err), false
	}

	switch {
	case opts.showHelp:
		writeUsage(os.Stderr)

		return cliOptions{}, exitSuccess, false
	case opts.showVersion:
		_, _ = io.WriteString(os.Stdout, versionString()+"\n")

		return cliOptions{}, exitSuccess, false
	case len(opts.queries) == 0:
		writeUsage(os.Stderr)

		return cliOptions{}, exitUsage, false
	default:
		return opts, exitSuccess, true
	}
}

func findMatches(ctx context.Context, opts cliOptions) ([]process.Match, error) {
	omit, err := opts.omitSpecs.Resolve(unix.Getppid())
	if err != nil {
		return nil, fmt.Errorf("resolve omitted pids: %w", err)
	}

	matches, err := process.Find(ctx, opts.queries, process.FindOptions{
		LongNames:     opts.long,
		Single:        opts.single,
		SameRoot:      opts.sameRoot,
		ScriptsToo:    opts.scriptsToo,
		IncludeZombie: opts.includeSpecialStates,
		IncludeDState: opts.includeSpecialStates,
		Omit:          omit,
	})
	if err != nil {
		return nil, fmt.Errorf("find matching processes: %w", err)
	}

	return matches, nil
}

func finishRun(opts cliOptions, matches []process.Match) int {
	if len(matches) == 0 {
		return exitNoMatch
	}

	if opts.kill {
		if err := killMatches(matches, killUnixProcess); err != nil {
			return usageError(err)
		}

		return exitSuccess
	}

	if opts.quiet {
		return exitSuccess
	}

	if err := writeMatches(opts, matches); err != nil {
		return usageError(err)
	}

	return exitSuccess
}

func writeMatches(opts cliOptions, matches []process.Match) error {
	if opts.long {
		return writeLongMatches(os.Stdout, matches)
	}

	return writePIDList(os.Stdout, matches, opts.separator)
}

func usageError(err error) int {
	_, _ = fmt.Fprintf(os.Stderr, "pidof: %v\n", err)

	return exitUsage
}

// Resolve converts the raw omit specs into the PID set used by process.Find.
func (specs omitSpecList) Resolve(parentPID int) (map[int]struct{}, error) {
	if len(specs) == 0 {
		return nil, nil //nolint:nilnil // A nil map is the no-omit fast path and avoids an unnecessary allocation.
	}

	omit := make(map[int]struct{}, len(specs))
	for _, spec := range specs {
		if spec == "%PPID" {
			if parentPID <= 0 {
				return nil, errors.New("cannot resolve %PPID")
			}

			omit[parentPID] = struct{}{}

			continue
		}

		pid, err := strconv.Atoi(spec)
		if err != nil || pid <= 0 {
			return nil, fmt.Errorf("invalid omit pid %q", spec)
		}

		omit[pid] = struct{}{}
	}

	return omit, nil
}

// appendOmitSpec parses one -o flag value. pidof accepts repeated -o flags, and
// each value may contain a comma-separated list.
func appendOmitSpec(specs *omitSpecList, value string) error {
	for item := range strings.SplitSeq(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		*specs = append(*specs, item)
	}

	return nil
}

// parseOptions parses pidof flags without performing process lookup.
func parseOptions(args []string) (cliOptions, error) {
	var opts cliOptions

	opts.separator = defaultSeparator

	fs := flag.NewFlagSet("pidof", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.BoolVar(&opts.kill, "k", false, "kill processes for the given names")
	fs.BoolVar(&opts.long, "l", false, "print long output")
	fs.BoolVar(&opts.showHelp, "h", false, "show help")
	fs.BoolVar(&opts.showHelp, "?", false, "show help")
	fs.BoolVar(&opts.showVersion, "v", false, "print version information")
	fs.BoolVar(&opts.single, "s", false, "single shot")
	fs.BoolVar(&opts.sameRoot, "c", false, "same root directory only")
	fs.BoolVar(&opts.scriptsToo, "x", false, "include scripts")
	fs.BoolVar(&opts.quiet, "q", false, "quiet mode")
	fs.BoolVar(&opts.includeSpecialStates, "z", false, "include zombie and D-state processes")
	fs.StringVar(&opts.separator, "d", defaultSeparator, "output separator")
	fs.Func("o", "omit pid", func(value string) error {
		return appendOmitSpec(&opts.omitSpecs, value)
	})

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, fmt.Errorf("parse flags: %w", err)
	}

	opts.queries = fs.Args()

	return opts, nil
}

// writeUsage prints the pidof help text.
func writeUsage(w io.Writer) {
	_, _ = io.WriteString(w, usageText)
}

// killUnixProcess sends the default pidof signal to pid.
func killUnixProcess(pid int) error {
	if err := unix.Kill(pid, unix.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM: %w", err)
	}

	return nil
}

// versionString formats the build metadata used by --version.
func versionString() string {
	path, version := versionpkg.PathVersion()
	if path == "" {
		path = "pidof"
	}

	if version == "" {
		version = "dev"
	}

	return path + " " + version
}

// killMatches sends the default signal to each unique pid and tolerates races
// where the process exits before the signal is delivered.
func killMatches(matches []process.Match, kill killFunc) error {
	var firstErr error

	for _, pid := range uniquePIDs(matches) {
		if err := kill(pid); err != nil && !errors.Is(err, unix.ESRCH) && firstErr == nil {
			firstErr = fmt.Errorf("kill %d: %w", pid, err)
		}
	}

	return firstErr
}
