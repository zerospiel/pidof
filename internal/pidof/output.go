package pidof

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/zerospiel/pidof/internal/process"
)

const (
	maxIntTextSize = 20
	decimalBase    = 10
	// uniquePIDLinearLimit gates the linear-scan dedup. For typical pidof use
	// (a few matched PIDs) the linear scan beats map allocation + hashing on
	// both wall time and allocs. Above the threshold we switch to a map.
	uniquePIDLinearLimit = 32
)

// writePIDList prints unique pids using the requested separator, preserving
// the order in which the backend reported each match. The exact ordering is a
// backend detail; callers should only rely on "first match wins" semantics.
func writePIDList(w io.Writer, matches []process.Match, sep string) error {
	pids := uniquePIDs(matches)
	if len(pids) == 0 {
		return nil
	}

	var buf bytes.Buffer
	buf.Grow(len(pids)*(len(sep)+maxIntTextSize) + 1)

	var scratch [maxIntTextSize]byte

	for i, pid := range pids {
		if i > 0 {
			_, _ = buf.WriteString(sep)
		}

		_, _ = buf.Write(strconv.AppendInt(scratch[:0], int64(pid), decimalBase))
	}

	_ = buf.WriteByte('\n')

	_, err := w.Write(buf.Bytes())
	if err != nil {
		return fmt.Errorf("write pid list: %w", err)
	}

	return nil
}

// uniquePIDs de-duplicates the pid set while preserving the first-seen order.
// For the typical small input it does a linear scan to avoid the per-call map
// allocation and hashing overhead; the map fallback kicks in only when the
// match list is unusually large.
func uniquePIDs(matches []process.Match) []int {
	switch len(matches) {
	case 0:
		return nil
	case 1:
		return []int{matches[0].PID}
	}

	pids := make([]int, 0, len(matches))

	if len(matches) <= uniquePIDLinearLimit {
		for _, match := range matches {
			if slices.Contains(pids, match.PID) {
				continue
			}

			pids = append(pids, match.PID)
		}

		return pids
	}

	seen := make(map[int]struct{}, len(matches))

	for _, match := range matches {
		if _, ok := seen[match.PID]; ok {
			continue
		}

		seen[match.PID] = struct{}{}
		pids = append(pids, match.PID)
	}

	return pids
}
