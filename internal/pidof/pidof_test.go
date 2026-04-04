package pidof

import (
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/zerospiel/pidof/internal/process"
	versionpkg "github.com/zerospiel/pidof/internal/version"
)

func captureRun(t *testing.T, args []string) (code int, stdout, stderr string) {
	t.Helper()

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(stdout) error = %v", err)
	}

	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(stderr) error = %v", err)
	}

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter

	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	code = run(context.Background(), args)

	if err := stdoutWriter.Close(); err != nil {
		t.Fatalf("stdout close error = %v", err)
	}

	if err := stderrWriter.Close(); err != nil {
		t.Fatalf("stderr close error = %v", err)
	}

	stdoutBytes, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatalf("io.ReadAll(stdout) error = %v", err)
	}

	if err := stdoutReader.Close(); err != nil {
		t.Fatalf("stdout close error = %v", err)
	}

	stderrBytes, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatalf("io.ReadAll(stderr) error = %v", err)
	}

	if err := stderrReader.Close(); err != nil {
		t.Fatalf("stderr close error = %v", err)
	}

	return code, string(stdoutBytes), string(stderrBytes)
}

func Test_parseOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    cliOptions
		wantErr string
	}{
		{
			name: "full flag set",
			args: []string{
				"-l",
				"-s",
				"-x",
				"-c",
				"-q",
				"-z",
				"-d",
				",",
				"-o",
				"1,%PPID",
				"-o",
				"99",
				"python",
			},
			want: cliOptions{
				queries:              []string{"python"},
				omitSpecs:            omitSpecList{"1", "%PPID", "99"},
				separator:            ",",
				long:                 true,
				single:               true,
				sameRoot:             true,
				scriptsToo:           true,
				quiet:                true,
				includeSpecialStates: true,
			},
		},
		{
			name: "short help alias",
			args: []string{"-?", "bash"},
			want: cliOptions{showHelp: true, separator: defaultSeparator, queries: []string{"bash"}},
		},
		{
			name: "short version alias",
			args: []string{"-v", "bash"},
			want: cliOptions{showVersion: true, separator: defaultSeparator, queries: []string{"bash"}},
		},
		{
			name:    "invalid flag",
			args:    []string{"--definitely-not-a-flag"},
			wantErr: "flag provided but not defined",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			opts, err := parseOptions(test.args)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseOptions() error = %v, want %q", err, test.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseOptions() error = %v", err)
			}

			if got, want := opts.separator, test.want.separator; got != want {
				t.Fatalf("separator = %q, want %q", got, want)
			}

			if got, want := []string(opts.omitSpecs), []string(test.want.omitSpecs); !slices.Equal(got, want) {
				t.Fatalf("omit specs = %v, want %v", got, want)
			}

			if got, want := opts.queries, test.want.queries; !slices.Equal(got, want) {
				t.Fatalf("queries = %v, want %v", got, want)
			}

			if opts.kill != test.want.kill || opts.long != test.want.long ||
				opts.showHelp != test.want.showHelp || opts.showVersion != test.want.showVersion ||
				opts.single != test.want.single || opts.sameRoot != test.want.sameRoot ||
				opts.scriptsToo != test.want.scriptsToo || opts.quiet != test.want.quiet ||
				opts.includeSpecialStates != test.want.includeSpecialStates {
				t.Fatalf("parseOptions(%v) = %#v, want %#v", test.args, opts, test.want)
			}
		})
	}
}

//nolint:paralleltest // The test captures process-global stdout/stderr and is clearer as one table.
func Test_run(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		versionOverride  string
		wantCode         int
		wantStdout       string
		wantStderr       string
		wantStdoutSubstr string
		wantStderrSubstr string
	}{
		{
			name:             "help",
			args:             []string{"-?"},
			wantCode:         exitSuccess,
			wantStderrSubstr: "usage: pidof",
		},
		{
			name:             "version",
			args:             []string{"-v"},
			versionOverride:  "test-version",
			wantCode:         exitSuccess,
			wantStdoutSubstr: "test-version",
		},
		{
			name:       "missing arguments",
			wantCode:   exitUsage,
			wantStderr: usageText,
		},
		{
			name:     "no match",
			args:     []string{"pidof-test-process-that-should-not-exist"},
			wantCode: exitNoMatch,
		},
		{
			name:             "invalid omit pid",
			args:             []string{"-o", "abc", "bash"},
			wantCode:         exitUsage,
			wantStderrSubstr: "invalid omit pid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			old := versionpkg.VersionOverride
			versionpkg.VersionOverride = test.versionOverride

			t.Cleanup(func() {
				versionpkg.VersionOverride = old
			})

			code, stdout, stderr := captureRun(t, test.args)
			if code != test.wantCode {
				t.Fatalf("exit code = %d, want %d", code, test.wantCode)
			}

			if test.wantStdout != "" && stdout != test.wantStdout {
				t.Fatalf("stdout = %q, want %q", stdout, test.wantStdout)
			}

			if test.wantStderr != "" && stderr != test.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr, test.wantStderr)
			}

			if test.wantStdout == "" && test.wantStdoutSubstr == "" && stdout != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}

			if test.wantStderr == "" && test.wantStderrSubstr == "" && stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}

			if test.wantStdoutSubstr != "" && !strings.Contains(stdout, test.wantStdoutSubstr) {
				t.Fatalf("stdout = %q, want substring %q", stdout, test.wantStdoutSubstr)
			}

			if test.wantStderrSubstr != "" && !strings.Contains(stderr, test.wantStderrSubstr) {
				t.Fatalf("stderr = %q, want substring %q", stderr, test.wantStderrSubstr)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		specs     omitSpecList
		parentPID int
		want      map[int]struct{}
		wantErr   string
	}{
		{
			name:      "resolves explicit and parent pid",
			specs:     omitSpecList{"10", "%PPID", "11"},
			parentPID: 99,
			want: map[int]struct{}{
				10: {},
				11: {},
				99: {},
			},
		},
		{
			name:      "rejects invalid pid",
			specs:     omitSpecList{"abc"},
			parentPID: 99,
			wantErr:   `invalid omit pid "abc"`,
		},
		{
			name:      "rejects unresolved parent pid",
			specs:     omitSpecList{"%PPID"},
			parentPID: 0,
			wantErr:   "cannot resolve %PPID",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := test.specs.Resolve(test.parentPID)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("Resolve() error = %v, want %q", err, test.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}

			if !maps.Equal(got, test.want) {
				t.Fatalf("Resolve() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func Test_writePIDList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		matches []process.Match
		sep     string
		want    string
	}{
		{
			name: "deduplicates and preserves first seen order",
			matches: []process.Match{
				{PID: 44},
				{PID: 12},
				{PID: 44},
			},
			sep:  ",",
			want: "44,12\n",
		},
		{
			name: "empty match list",
			sep:  " ",
			want: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout strings.Builder

			err := writePIDList(&stdout, test.matches, test.sep)
			if err != nil {
				t.Fatalf("writePIDList() error = %v", err)
			}

			if got := stdout.String(); got != test.want {
				t.Fatalf("stdout = %q, want %q", got, test.want)
			}
		})
	}
}

func Test_writeLongMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		matches []process.Match
		want    string
	}{
		{
			name: "single match",
			matches: []process.Match{
				{Query: "code", PID: 12, Name: "Code", User: "uname"},
			},
			want: map[bool]string{
				true:  "PID for Code is 12 (uname)\n",
				false: "code\t12\tCode\n",
			}[runtime.GOOS == "darwin"],
		},
		{
			name: "empty match list",
			want: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout strings.Builder

			err := writeLongMatches(&stdout, test.matches)
			if err != nil {
				t.Fatalf("writeLongMatches() error = %v", err)
			}

			if got := stdout.String(); got != test.want {
				t.Fatalf("stdout = %q, want %q", got, test.want)
			}
		})
	}
}

func Test_killMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		matches     []process.Match
		killErrs    map[int]error
		wantKilled  []int
		wantErrText string
	}{
		{
			name:       "ignores missing process races",
			matches:    []process.Match{{PID: 1}, {PID: 1}},
			killErrs:   map[int]error{1: unix.ESRCH},
			wantKilled: []int{1},
		},
		{
			name:        "returns first real error",
			matches:     []process.Match{{PID: 9}, {PID: 7}, {PID: 9}},
			killErrs:    map[int]error{9: errors.New("boom")},
			wantKilled:  []int{9, 7},
			wantErrText: "kill 9: boom",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var killed []int

			err := killMatches(test.matches, func(pid int) error {
				killed = append(killed, pid)

				return test.killErrs[pid]
			})

			if !slices.Equal(killed, test.wantKilled) {
				t.Fatalf("killed = %v, want %v", killed, test.wantKilled)
			}

			if test.wantErrText == "" {
				if err != nil {
					t.Fatalf("killMatches() error = %v, want nil", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), test.wantErrText) {
				t.Fatalf("killMatches() error = %v, want %q", err, test.wantErrText)
			}
		})
	}
}
