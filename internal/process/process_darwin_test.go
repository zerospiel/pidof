//go:build darwin

package process

import (
	"context"
	"errors"
	"os"
	"testing"
)

func Test_darwinMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		short       []byte
		query       query
		opt         FindOptions
		procArgs    darwinProcArgs
		wantMatched bool
		wantName    string
		wantCalls   int
	}{
		{
			name:        "fast path short name match",
			short:       []byte("bash"),
			query:       query{raw: "bash", base: "bash"},
			wantMatched: true,
			wantName:    "bash",
			wantCalls:   0,
		},
		{
			name:  "script match",
			short: []byte("python3"),
			query: query{raw: "tool.py", base: "tool.py"},
			opt:   FindOptions{ScriptsToo: true},
			procArgs: darwinProcArgs{
				exec:   "/usr/bin/python3",
				argv0:  "/usr/bin/python3",
				script: "/tmp/tool.py",
			},
			wantMatched: true,
			wantName:    "tool.py",
			wantCalls:   1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			loadArgs := func() darwinProcArgs {
				calls++

				return test.procArgs
			}

			matched, name := darwinMatch(test.short, test.query, test.opt, loadArgs)
			if matched != test.wantMatched {
				t.Fatalf("matched = %v, want %v", matched, test.wantMatched)
			}

			if name != test.wantName {
				t.Fatalf("name = %q, want %q", name, test.wantName)
			}

			if calls != test.wantCalls {
				t.Fatalf("loadArgs() calls = %d, want %d", calls, test.wantCalls)
			}
		})
	}
}

func TestList(t *testing.T) {
	t.Parallel()

	processes, err := List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	pid := os.Getpid()
	for _, process := range processes {
		if process.PID == pid {
			return
		}
	}

	t.Fatal(errors.New("current pid not found"))
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
