//go:build !darwin

package pidof

import (
	"bytes"
	"fmt"
	"io"
	"strconv"

	"github.com/zerospiel/pidof/internal/process"
)

// longMatchLineEstimateSize is the per-line buffer hint for the generic
// tab-separated long output. Sized to cover query+pid+name plus separators
// for the typical short binary name.
const longMatchLineEstimateSize = 32

// writeLongMatches uses the generic "<query>\t<pid>\t<name>" format on every
// platform other than darwin. The buffer is sized up-front to absorb the
// usual match list in a single write syscall.
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
