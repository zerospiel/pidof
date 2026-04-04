package process

import "bytes"

const (
	deletedSuffix        = " (deleted)"
	initialMatchCapacity = 16
)

// query stores one normalized lookup target together with its basename fast path.
// shouldOmit reports whether pid was explicitly excluded by the caller.
func shouldOmit(pid int, omit map[int]struct{}) bool {
	if len(omit) == 0 {
		return false
	}

	_, ok := omit[pid]

	return ok
}

// nextNULField splits a NUL-delimited byte sequence without allocating.
func nextNULField(b []byte) (field, rest []byte, ok bool) {
	for len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}

	if len(b) == 0 {
		return nil, nil, false
	}

	if before, after, ok := bytes.Cut(b, []byte{0}); ok {
		return before, after, true
	}

	return b, nil, true
}
