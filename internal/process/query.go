package process

import "strings"

// query stores one normalized lookup target together with its basename fast path.
type query struct {
	raw      string
	base     string
	fullPath bool
}

// compileQueries normalizes user input once so the hot matching loops can stay simple.
func compileQueries(input []string) []query {
	seen := make(map[string]struct{}, len(input))
	queries := make([]query, 0, len(input))

	for _, raw := range input {
		raw = normalizeInput(raw)
		if raw == "" {
			continue
		}

		if _, ok := seen[raw]; ok {
			continue
		}

		seen[raw] = struct{}{}

		queries = append(queries, query{
			raw:      raw,
			base:     baseName(raw),
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

// baseName extracts the last path component using slash semantics on all targets.
func baseName(s string) string {
	s = normalizeInput(s)
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
