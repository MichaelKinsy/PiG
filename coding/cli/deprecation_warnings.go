package cli

// Ports packages/coding-agent/src/migrations.ts showDeprecationWarnings.

import (
	"fmt"
	"io"
	"os"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"

	"golang.org/x/term"
)

// showDeprecationWarnings prints the deprecated-extension-directory warnings and blocks until one key arrives on stdin, with stdin in raw mode when it is a terminal
// (migrations.ts:277-296). There is no timeout and no cancellation: the caller waits for the key. A stdin that ends or fails before a key arrives lets startup go on.
func showDeprecationWarnings(out io.Writer, stdin *os.File, warnings []string) error {
	var rawMode func() (restore func() error, err error)
	if term.IsTerminal(int(stdin.Fd())) {
		fd := int(stdin.Fd())
		rawMode = func() (func() error, error) {
			state, err := tui.MakeRaw(fd)
			if err != nil {
				return nil, err
			}
			return func() error { return tui.RestoreCooked(fd, state) }, nil
		}
	}
	return awaitDeprecationAcknowledgement(out, stdin, rawMode, warnings)
}

// awaitDeprecationAcknowledgement is showDeprecationWarnings over any input. rawMode, nil when the input has no raw mode (Node's setRawMode?.), switches the
// input to raw mode before the key is awaited and returns the function that restores it once the key arrived.
func awaitDeprecationAcknowledgement(out io.Writer, in io.Reader, rawMode func() (restore func() error, err error), warnings []string) error {
	if len(warnings) == 0 {
		return nil
	}
	for _, warning := range warnings {
		_, _ = fmt.Fprintln(out, chalkYellow("Warning: "+warning))
	}
	_, _ = fmt.Fprintln(out, chalkYellow("\nMove your extensions to the extensions/ directory."))
	_, _ = fmt.Fprintln(out, chalkYellow("Migration guide: "+codingagent.MigrationGuideURL))
	_, _ = fmt.Fprintln(out, chalkYellow("Documentation: "+codingagent.ExtensionsDocURL))
	_, _ = fmt.Fprintln(out, chalkDim("\nPress any key to continue..."))

	restore := func() error { return nil }
	if rawMode != nil {
		var err error
		if restore, err = rawMode(); err != nil {
			return err
		}
	}
	// process.stdin.once("data"): one chunk, whatever it holds.
	_, _ = in.Read(make([]byte, 4096))
	if err := restore(); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out)
	return nil
}
