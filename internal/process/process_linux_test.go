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

func Test_parseProcStat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     []byte
		wantName  string
		wantState byte
		wantPPID  int
		wantOK    bool
	}{
		{
			name:      "valid stat line",
			input:     []byte("1234 (python3.12) S 44 1 1 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 123 456"),
			wantName:  "python3.12",
			wantState: 'S',
			wantPPID:  44,
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

			name, state, ppid, ok := parseProcStat(test.input)
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v", ok, test.wantOK)
			}

			if name != test.wantName {
				t.Fatalf("name = %q, want %q", name, test.wantName)
			}

			if state != test.wantState {
				t.Fatalf("state = %q, want %q", state, test.wantState)
			}

			if ppid != test.wantPPID {
				t.Fatalf("ppid = %d, want %d", ppid, test.wantPPID)
			}
		})
	}
}

func Test_linuxDisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		process processInfo
		exe     string
		cmd     cmdlineInfo
		mode    displayMode
		kind    matchKind
		want    string
	}{
		{
			name:    "prefers script when it matched",
			process: processInfo{Name: "python3"},
			exe:     "/usr/bin/python3",
			cmd:     cmdlineInfo{argv0: "/usr/bin/python3", script: "/tmp/tool.py"},
			mode:    longDisplay,
			kind:    scriptMatch,
			want:    "tool.py",
		},
		{
			name:    "falls back to process name",
			process: processInfo{Name: "bash"},
			want:    "bash",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := linuxDisplayName(test.process, test.exe, test.cmd, test.mode, test.kind)
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
		process     processInfo
		query       query
		opt         FindOptions
		exe         string
		cmd         cmdlineInfo
		wantMatched bool
		wantName    string
	}{
		{
			name:        "script match",
			process:     processInfo{Name: "python3"},
			query:       query{raw: "tool.py", base: "tool.py"},
			opt:         FindOptions{ScriptsToo: true},
			exe:         "/usr/bin/python3",
			cmd:         cmdlineInfo{argv0: "/usr/bin/python3", script: "/tmp/tool.py"},
			wantMatched: true,
			wantName:    "tool.py",
		},
		{
			name:        "plain process name fast path",
			process:     processInfo{Name: "bash"},
			query:       query{raw: "bash", base: "bash"},
			wantMatched: true,
			wantName:    "bash",
		},
		{
			name:        "matches interpreter executable exactly",
			process:     processInfo{Name: "bashlike.sh"},
			query:       query{raw: "bash", base: "bash"},
			exe:         "/bin/bash",
			cmd:         cmdlineInfo{argv0: "/tmp/bashlike.sh"},
			wantMatched: true,
			wantName:    "bashlike.sh",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			matched, name := linuxMatch(
				test.process,
				test.query,
				test.opt,
				func() string { return test.exe },
				func() cmdlineInfo { return test.cmd },
			)
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
