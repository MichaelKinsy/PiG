package tools

import (
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

// Ports packages/agent/src/harness/utils/output-capture.ts.
// ApplyShellOutputUpdate folds one bounded update into its preceding view.
func ApplyShellOutputUpdate(current *harness.ShellOutputView, update harness.ShellOutputUpdate) harness.ShellOutputView {
	previous := ""
	if current != nil {
		previous = current.Text
	}
	switch update.Kind {
	case harness.ShellOutputUpdateReplace:
		return update.Output
	case harness.ShellOutputUpdateAppend:
		return harness.ShellOutputView{Text: previous + update.Text, ShellOutputMetadata: update.Metadata}
	case harness.ShellOutputUpdateSlide:
		units := utf16.Encode([]rune(previous))
		start := min(max(update.Drop, 0), len(units))
		return harness.ShellOutputView{Text: string(utf16.Decode(units[start:])) + update.Text, ShellOutputMetadata: update.Metadata}
	default:
		return harness.ShellOutputView{Text: previous, ShellOutputMetadata: update.Metadata}
	}
}
