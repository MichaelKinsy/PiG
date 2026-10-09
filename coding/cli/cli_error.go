package cli

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// writeCLIError mirrors Pi main.ts and package-manager-cli.ts: caught failures use a red Error label on stderr, after the failing operation finishes.
func writeCLIError(w io.Writer, message string, color bool) {
	text := "Error: " + message
	if color {
		text = "\x1b[31m" + text + "\x1b[39m"
	}
	_, _ = fmt.Fprintln(w, text) // Best effort: stderr has no fallback channel.
}

func printCLIError(format string, args ...any) {
	writeCLIError(os.Stderr, fmt.Sprintf(format, args...), term.IsTerminal(int(os.Stderr.Fd())))
}
