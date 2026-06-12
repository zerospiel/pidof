package process

import "context"

// Match describes one successful query match.
type Match struct {
	Query string
	Name  string // Printed process name for the active platform mode.
	User  string // Owning login name when the backend can provide it.
	PID   int
}

// FindOptions tunes process matching. LongNames currently only affects the
// Linux backend's display-name selection; the Darwin backend always falls back
// to argv/exec inspection when the short comm name doesn't match.
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
