package process

import "context"

// Process describes one running process.
type Process struct {
	Name   string
	Exe    string
	Argv0  string
	Script string
	PID    int
	PPID   int
	State  byte
}

// Match describes one successful query match.
type Match struct {
	Query string
	Name  string // Printed process name for the active platform mode.
	User  string // Owning login name when the backend can provide it.
	PID   int
}

// FindOptions tunes process matching. On Darwin, LongNames enables slower
// compatibility checks against argv and exec paths; on Linux it also influences
// the richer display name used by long output.
type FindOptions struct {
	Omit          map[int]struct{}
	LongNames     bool
	Single        bool
	SameRoot      bool
	ScriptsToo    bool
	IncludeZombie bool
	IncludeDState bool
}

// List returns a snapshot of the currently running processes.
func List(ctx context.Context) ([]Process, error) {
	return nativeList(ctx)
}

// Find resolves the provided queries to matching processes.
func Find(ctx context.Context, names []string, opt FindOptions) ([]Match, error) {
	return nativeFind(ctx, names, opt)
}
