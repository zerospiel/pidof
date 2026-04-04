package process

import "context"

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

// Find resolves the provided queries to matching processes.
func Find(ctx context.Context, names []string, opt FindOptions) ([]Match, error) {
	return nativeFind(ctx, names, opt)
}
