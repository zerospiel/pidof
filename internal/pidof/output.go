package pidof

import (
	"bytes"
	"fmt"
	"io"
	"runtime"
	"strconv"

	"github.com/zerospiel/pidof/internal/process"
)

const (
	maxIntTextSize            = 20
	longMatchLineEstimateSize = 32
	darwinLongLineEstimate    = 48
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

// writeLongMatches prints matches using the current platform's compatibility
// format.
func writeLongMatches(w io.Writer, matches []process.Match) error {
	if runtime.GOOS == "darwin" {
		return writeDarwinLongMatches(w, matches)
	}

	return writeGenericLongMatches(w, matches)
}

// writeDarwinLongMatches prints the legacy macOS' pidof long output.
func writeDarwinLongMatches(w io.Writer, matches []process.Match) error {
	var buf bytes.Buffer
	buf.Grow(len(matches) * darwinLongLineEstimate)

	var scratch [maxIntTextSize]byte

	for _, match := range matches {
		_, _ = buf.WriteString("PID for ")
		_, _ = buf.WriteString(match.Name)
		_, _ = buf.WriteString(" is ")
		_, _ = buf.Write(strconv.AppendInt(scratch[:0], int64(match.PID), decimalBase))
		_, _ = buf.WriteString(" (")
		_, _ = buf.WriteString(match.User)
		_, _ = buf.WriteString(")\n")
	}

	_, err := w.Write(buf.Bytes())
	if err != nil {
		return fmt.Errorf("write darwin long match list: %w", err)
	}

	return nil
}

// writeGenericLongMatches prints one match per line in query, pid, name form.
func writeGenericLongMatches(w io.Writer, matches []process.Match) error {
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

// uniquePIDs de-duplicates the pid set while preserving the first-seen order.
func uniquePIDs(matches []process.Match) []int {
	if len(matches) == 0 {
		return nil
	}

	if len(matches) == 1 {
		return []int{matches[0].PID}
	}

	pids := make([]int, 0, len(matches))
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
