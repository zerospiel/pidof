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
	maxIntTextSize            = 20
	longMatchLineEstimateSize = 32
	decimalBase               = 10
)

// writePIDList prints unique pids in ascending order using the requested separator.
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

// writeLongMatches prints one match per line in query, pid, name form.
func writeLongMatches(w io.Writer, matches []process.Match) error {
	var buf bytes.Buffer
	buf.Grow(len(matches) * longMatchLineEstimateSize)

	var scratch [maxIntTextSize]byte

	for _, match := range matches {
		_, _ = buf.WriteString(match.Query)
		_ = buf.WriteByte('\t')
		_, _ = buf.Write(strconv.AppendInt(scratch[:0], int64(match.PID), decimalBase))
		_ = buf.WriteByte('\t')
		_, _ = buf.WriteString(match.Name)
		_ = buf.WriteByte('\n')
	}

	_, err := w.Write(buf.Bytes())
	if err != nil {
		return fmt.Errorf("write long match list: %w", err)
	}

	return nil
}

// uniquePIDs de-duplicates and sorts the pid set once before formatting or killing.
func uniquePIDs(matches []process.Match) []int {
	if len(matches) == 0 {
		return nil
	}

	pids := make([]int, len(matches))
	for i, match := range matches {
		pids[i] = match.PID
	}

	slices.Sort(pids)

	uniqueCount := 1
	for _, pid := range pids[1:] {
		if pid == pids[uniqueCount-1] {
			continue
		}

		pids[uniqueCount] = pid
		uniqueCount++
	}

	return pids[:uniqueCount]
}
