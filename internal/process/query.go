package process

import (
	"slices"
	"strings"
)

// query stores one normalized lookup target together with its basename fast path.
type query struct {
	raw      string
	base     string
	fullPath bool
}

// compileQueries normalizes user input once so the hot matching loops can stay
// simple. Inputs are tiny (a handful at most), so a linear scan beats the per
// call map allocation for dedup.
func compileQueries(input []string) []query {
	queries := make([]query, 0, len(input))

	for _, raw := range input {
		raw = normalizeInput(raw)
		if raw == "" {
			continue
		}

		if slices.ContainsFunc(queries, func(q query) bool { return q.raw == raw }) {
			continue
		}

		queries = append(queries, query{
			raw:      raw,
			base:     baseNameOf(raw),
			fullPath: strings.IndexByte(raw, '/') >= 0,
		})
	}

	return queries
}

// normalizeInput trims shell-like noise from query and process path strings.
func normalizeInput(s string) string {
	s = strings.TrimSpace(s)

	s, _ = strings.CutSuffix(s, deletedSuffix)
	if s == "" {
		return ""
	}

	if strings.IndexByte(s, '/') >= 0 {
		for len(s) > 1 && s[len(s)-1] == '/' {
			s = s[:len(s)-1]
		}
	}

	switch s {
	case "", ".", "/":
		return ""
	default:
		return s
	}
}

// baseName extracts the last path component using slash semantics on all
// targets. It is the safe entry point for callers that may receive untrimmed
// or shell-noisy strings (e.g. procfs cmdline data, kern.procargs2 fields).
func baseName(s string) string {
	return baseNameOf(normalizeInput(s))
}

// baseNameOf is the basename fast path for callers that have already passed
// the input through normalizeInput. It skips the redundant trim/cut work that
// dominates baseName when the input is known to be clean (compileQueries does
// exactly this once per query).
func baseNameOf(s string) string {
	if s == "" {
		return ""
	}

	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}

	switch s {
	case "", ".", "/":
		return ""
	default:
		return s
	}
}

// samePath compares paths after normalization.
func samePath(lhs, rhs string) bool {
	if lhs == "" || rhs == "" {
		return false
	}

	return normalizeInput(lhs) == normalizeInput(rhs)
}
