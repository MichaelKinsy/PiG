package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// workingDirectoryErrorMessage is the line printed when the working directory cannot be read. Pi catches only ENOENT from process.cwd() and rethrows anything else.
// pig divergence (D217): Pi 88ffcdc7 (#11509) names a deleted working directory and tells the user what to do; pinned Pi crashes with an uncaught uv_cwd error.
func workingDirectoryErrorMessage(err error) string {
	if errors.Is(err, fs.ErrNotExist) {
		return "The current working directory no longer exists. Change to an existing directory and try again."
	}
	return "get cwd: " + err.Error()
}
