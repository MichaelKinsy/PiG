package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// writeCLIError mirrors Pi main.ts and package-manager-cli.ts: caught failures use a red Error label on stderr, after the failing operation finishes.
func writeCLIError(w io.Writer, message string, color bool) {
	text := "Error: " + message
	if color {
		text = codingagent.ChalkApply("\x1b[31m", "\x1b[39m", text)
	}
	_, _ = fmt.Fprintln(w, text) // Best effort: stderr has no fallback channel.
}

func printCLIError(format string, args ...any) {
	writeCLIError(os.Stderr, fmt.Sprintf(format, args...), chalkStdoutColor())
}
