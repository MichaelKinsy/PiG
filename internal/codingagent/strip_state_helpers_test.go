package codingagent

import (
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// strippedFeatureCommand reports whether name is a built-in command that only shows a feature this build strips (/changelog in a build that
// strips the changelog): the build does not offer it, so the tests that hold Pi's list of commands hold it without that command.
func strippedFeatureCommand(name string) bool {
	feature, owned := featureCommands[name]
	return owned && pigstrip.Has(pigstrip.ListFeatures, feature)
}

// withoutStrippedSettingsRows is Pi's roster rows ("id (Label)") without the rows of a built-in this build strips: /settings does not offer them.
func withoutStrippedSettingsRows(rows []string) []string {
	return slices.DeleteFunc(slices.Clone(rows), func(row string) bool {
		id, _, _ := strings.Cut(row, " (")
		return settingStripped(id)
	})
}

// piBuiltinCommandCount is the number of Pi's built-in slash commands this build offers.
func piBuiltinCommandCount() int {
	count := 24 // BUILTIN_SLASH_COMMANDS (core/slash-commands.ts:19-44)
	if strippedFeatureCommand("changelog") {
		count--
	}
	return count
}
