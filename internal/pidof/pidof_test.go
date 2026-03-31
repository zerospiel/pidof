package pidof

import (
	"context"
	"reflect"
	"testing"
)

type staticLister struct {
	processes []Process
	err       error
}

// List returns the preconfigured process set for tests.
func (s staticLister) List(context.Context) ([]Process, error) {
	return s.processes, s.err
}

// TestFindMatchesByCommandAndArg0 verifies matching by command and argv[0].
func TestFindMatchesByCommandAndArg0(t *testing.T) {
	finder := Finder{
		Lister: staticLister{processes: []Process{
			{PID: 100, Command: "bash", Args: "/bin/bash -l"},
			{PID: 200, Command: "/usr/bin/python3", Args: "/usr/bin/python3 app.py"},
			{PID: 300, Command: "zsh", Args: "/bin/zsh"},
			{PID: 200, Command: "/usr/bin/python3", Args: "/usr/bin/python3 duplicate.py"},
		}},
	}

	got, err := finder.Find(context.Background(), []string{"python3", "/bin/bash"})
	if err != nil {
		t.Fatalf("Find returned error: %v", err)
	}

	want := []int{100, 200}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected PIDs, got %v want %v", got, want)
	}
}

// TestParsePSOutput verifies parsing valid lines and skipping malformed rows.
func TestParsePSOutput(t *testing.T) {
	raw := []byte("   12 launchd /sbin/launchd\n  333 /usr/bin/python3 /usr/bin/python3 app.py\n\nmalformed\n")

	got, err := parsePSOutput(raw)
	if err != nil {
		t.Fatalf("parsePSOutput returned error: %v", err)
	}

	want := []Process{
		{PID: 12, Command: "launchd", Args: "/sbin/launchd"},
		{PID: 333, Command: "/usr/bin/python3", Args: "/usr/bin/python3 app.py"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected parsed processes, got %#v want %#v", got, want)
	}
}
