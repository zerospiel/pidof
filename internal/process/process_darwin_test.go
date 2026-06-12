//go:build darwin

package process

import (
	"context"
	"errors"
	"os"
	"os/user"
	"strconv"
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
			name:        "case insensitive short name match",
			short:       []byte("Code"),
			query:       query{raw: "code", base: "code"},
			wantMatched: true,
			wantName:    "Code",
			wantCalls:   0,
		},
		{
			name:        "case insensitive substring short name match",
			short:       []byte("Code Helper"),
			query:       query{raw: "code", base: "code"},
			wantMatched: true,
			wantName:    "Code Helper",
			wantCalls:   0,
		},
		{
			name:  "script match",
			short: []byte("python3"),
			query: query{raw: "tool.py", base: "tool.py"},
			opt:   FindOptions{ScriptsToo: true},
			procArgs: darwinProcArgs{
				exec:       "/usr/bin/python3",
				execBase:   "python3",
				argv0:      "/usr/bin/python3",
				argv0Base:  "python3",
				script:     "/tmp/tool.py",
				scriptBase: "tool.py",
			},
			wantMatched: true,
			wantName:    "python3",
			wantCalls:   1,
		},
		{
			name:        "short name miss without scripts does not load argv",
			short:       []byte("sshd"),
			query:       query{raw: "bash", base: "bash"},
			wantMatched: false,
			wantCalls:   0,
		},
		{
			name:  "full path exec match",
			short: []byte("python3"),
			query: query{raw: "/usr/bin/python3", base: "python3", fullPath: true},
			procArgs: darwinProcArgs{
				exec:      "/usr/bin/python3",
				execBase:  "python3",
				argv0:     "/usr/bin/python3",
				argv0Base: "python3",
			},
			wantMatched: true,
			wantName:    "python3",
			wantCalls:   1,
		},
		{
			name:        "full path with empty argv data is a miss",
			short:       []byte("python3"),
			query:       query{raw: "/usr/bin/python3", base: "python3", fullPath: true},
			wantMatched: false,
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

func TestFind_longNamesPopulatesUser(t *testing.T) {
	t.Parallel()

	query := baseName(os.Args[0])

	matches, err := Find(context.Background(), []string{query}, FindOptions{Single: true, LongNames: true})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if len(matches) == 0 {
		t.Fatal("current process query returned no matches")
	}

	if matches[0].User == "" {
		t.Fatal("Find(LongNames=true) returned empty User on match")
	}
}

func Test_readDarwinProcArgs(t *testing.T) {
	t.Parallel()

	got := readDarwinProcArgs(os.Getpid())
	if got.exec == "" {
		t.Fatal("readDarwinProcArgs(self).exec is empty")
	}

	if got.execBase == "" {
		t.Fatal("readDarwinProcArgs(self).execBase is empty")
	}

	if got.argv0 == "" {
		t.Fatal("readDarwinProcArgs(self).argv0 is empty")
	}

	if got.argv0Base == "" {
		t.Fatal("readDarwinProcArgs(self).argv0Base is empty")
	}

	// PID 0 (kernel_task) is not introspectable by ordinary users; the helper
	// must collapse the failure path into a zero value without panicking.
	if got := readDarwinProcArgs(0); got != (darwinProcArgs{}) {
		t.Fatalf("readDarwinProcArgs(0) = %#v, want zero value", got)
	}
}

func Test_darwinUserName(t *testing.T) {
	t.Parallel()

	uid := uint32(os.Geteuid()) //nolint:gosec // Geteuid is non-negative on macOS
	cache := map[uint32]string{uid: "primed"}

	if got := darwinUserName(uid, cache); got != "primed" {
		t.Fatalf("darwinUserName(cache hit) = %q, want %q", got, "primed")
	}

	cache = map[uint32]string{}

	got := darwinUserName(uid, cache)
	if got == "" {
		t.Fatal("darwinUserName(cache miss) returned empty string")
	}

	if cached, ok := cache[uid]; !ok || cached != got {
		t.Fatalf("cache[%d] = %q, ok = %v; want %q populated", uid, cached, ok, got)
	}
}

func Test_resolveDarwinUserName(t *testing.T) {
	t.Parallel()

	uid := uint32(os.Geteuid()) //nolint:gosec // Geteuid is non-negative on macOS

	got := resolveDarwinUserName(uid)
	if got == "" {
		t.Fatal("resolveDarwinUserName returned empty string")
	}

	// On success the result is the login name; otherwise it falls back to the
	// numeric uid. Either is acceptable; verify it matches one of those.
	numeric := strconv.FormatUint(uint64(uid), 10)
	if got == numeric {
		return
	}

	entry, err := user.LookupId(numeric)
	if err == nil && entry.Username == got {
		return
	}

	t.Fatalf("resolveDarwinUserName(%d) = %q; expected login name or %q", uid, got, numeric)
}
