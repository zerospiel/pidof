//go:build darwin

package pidof

import (
	"bytes"
	"fmt"
	"io"
	"strconv"

	"github.com/zerospiel/pidof/internal/process"
)

// darwinLongLineEstimate is the per-line buffer hint for the macOS long
// output format "PID for <name> is <pid> (<user>)". It is kept on the
// generous side so buf.Grow lands a single allocation for typical match
// counts.
const darwinLongLineEstimate = 48

// writeLongMatches prints the legacy macOS pidof long output: one line per
// match in the form "PID for <name> is <pid> (<user>)".
func writeLongMatches(w io.Writer, matches []process.Match) error {
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
