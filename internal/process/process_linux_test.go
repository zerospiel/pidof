//go:build linux

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func Test_parseProcStatFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     []byte
		wantComm  string
		wantState byte
		wantOK    bool
	}{
		{
			name:      "valid stat line",
			input:     []byte("1234 (python3.12) S 44 1 1 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 123 456"),
			wantComm:  "python3.12",
			wantState: 'S',
			wantOK:    true,
		},
		{
			name:   "rejects malformed input",
			input:  []byte("broken"),
			wantOK: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			comm, state, ok := parseProcStatFields(test.input)
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v", ok, test.wantOK)
			}

			if string(comm) != test.wantComm {
				t.Fatalf("comm = %q, want %q", comm, test.wantComm)
			}

			if state != test.wantState {
				t.Fatalf("state = %q, want %q", state, test.wantState)
			}
		})
	}
}

// newPreloadedLazyProc builds a *lazyProc with its lazy loaders short-circuited
// so tests run without touching procfs. cmd basenames are derived from the
// raw fields to mirror what procReadCmdline produces in production.
func newPreloadedLazyProc(name, exe string, cmd cmdlineInfo) *lazyProc {
	if cmd.argv0Base == "" {
		cmd.argv0Base = baseName(cmd.argv0)
	}

	if cmd.scriptBase == "" {
		cmd.scriptBase = baseName(cmd.script)
	}

	proc := &lazyProc{
		name:           name,
		nameBase:       baseName(name),
		exe:            exe,
		exeBase:        baseName(exe),
		cmd:            cmd,
		nameLoaded:     true,
		nameBaseLoaded: true,
		exeLoaded:      true,
		cmdLoaded:      true,
	}

	copyLen := min(len(name), linuxCommMaxLen)
	proc.nameLen = uint8(copy(proc.nameBytes[:], name[:copyLen])) //nolint:gosec // bounded above by linuxCommMaxLen=16.

	return proc
}

func Test_linuxDisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		proc *lazyProc
		cmd  cmdlineInfo
		mode displayMode
		kind matchKind
		want string
	}{
		{
			name: "prefers script when it matched",
			proc: newPreloadedLazyProc("python3", "/usr/bin/python3", cmdlineInfo{argv0: "/usr/bin/python3", script: "/tmp/tool.py"}),
			cmd:  cmdlineInfo{argv0: "/usr/bin/python3", argv0Base: "python3", script: "/tmp/tool.py", scriptBase: "tool.py"},
			mode: longDisplay,
			kind: scriptMatch,
			want: "tool.py",
		},
		{
			name: "falls back to process name",
			proc: newPreloadedLazyProc("bash", "", cmdlineInfo{}),
			want: "bash",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := linuxDisplayName(test.proc, test.cmd, test.mode, test.kind)
			if got != test.want {
				t.Fatalf("linuxDisplayName() = %q, want %q", got, test.want)
			}
		})
	}
}

func Test_linuxMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		proc        *lazyProc
		query       query
		opt         FindOptions
		wantMatched bool
		wantName    string
	}{
		{
			name:        "script match",
			proc:        newPreloadedLazyProc("python3", "/usr/bin/python3", cmdlineInfo{argv0: "/usr/bin/python3", script: "/tmp/tool.py"}),
			query:       query{raw: "tool.py", base: "tool.py"},
			opt:         FindOptions{ScriptsToo: true},
			wantMatched: true,
			wantName:    "tool.py",
		},
		{
			name:        "plain process name fast path",
			proc:        newPreloadedLazyProc("bash", "", cmdlineInfo{}),
			query:       query{raw: "bash", base: "bash"},
			wantMatched: true,
			wantName:    "bash",
		},
		{
			name:        "matches interpreter executable exactly",
			proc:        newPreloadedLazyProc("bashlike.sh", "/bin/bash", cmdlineInfo{argv0: "/tmp/bashlike.sh"}),
			query:       query{raw: "bash", base: "bash"},
			wantMatched: true,
			wantName:    "bashlike.sh",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			matched, name := linuxMatch(test.proc, test.query, test.opt)
			if matched != test.wantMatched {
				t.Fatalf("matched = %v, want %v", matched, test.wantMatched)
			}

			if name != test.wantName {
				t.Fatalf("name = %q, want %q", name, test.wantName)
			}
		})
	}
}

func TestFind(t *testing.T) {
	t.Parallel()

	query := baseName(os.Args[0])

	matches, err := Find(context.Background(), []string{query}, FindOptions{Single: true})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if len(matches) == 0 {
		t.Fatal(errors.New("current process query returned no matches"))
	}
}

func TestFind_manyMatchesDoesNotDeadlock(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(oldProcs)

	const (
		matchCount    = linuxMaxWorkers + 2
		followerCount = linuxMaxWorkers + linuxJobQueueFactor
	)

	children := make([]*exec.Cmd, 0, matchCount+followerCount)
	for range matchCount {
		cmd := exec.CommandContext(t.Context(), "sleep", "10")
		if err := cmd.Start(); err != nil {
			t.Fatalf("sleep start error = %v", err)
		}

		children = append(children, cmd)
	}

	for range followerCount {
		cmd := exec.CommandContext(t.Context(), "tail", "-f", "/dev/null")
		if err := cmd.Start(); err != nil {
			t.Fatalf("tail start error = %v", err)
		}

		children = append(children, cmd)
	}

	t.Cleanup(func() {
		for _, child := range children {
			if child.Process == nil {
				continue
			}

			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})

	type result struct {
		matches []Match
		err     error
	}

	done := make(chan result, 1)

	go func() {
		matches, err := Find(context.Background(), []string{"sleep"}, FindOptions{})
		done <- result{matches: matches, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Find() error = %v", got.err)
		}

		if len(got.matches) < matchCount {
			t.Fatalf("len(matches) = %d, want at least %d", len(got.matches), matchCount)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Find() timed out with many matching processes")
	}
}
