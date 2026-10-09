package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// cliInventoryRow is one entry of test/parity/interfaces/cli-v<pinned version>.json, the compiler-derived list of the pinned Pi's
// command-line flags.
type cliInventoryRow struct {
	ID      string   `json:"id"`
	Command string   `json:"command"`
	Flag    string   `json:"flag"`
	Aliases []string `json:"aliases"`
	Value   string   `json:"value"`
}

func loadCLIInventory(t *testing.T) []cliInventoryRow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "test", "parity", "interfaces", "cli-v"+pigversion.UpstreamVersion+".json"))
	require.NoError(t, err)
	var doc struct {
		Interfaces []cliInventoryRow `json:"interfaces"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	return doc.Interfaces
}

// spellings is the flag followed by its aliases: the forms a Pi user can type.
func (r cliInventoryRow) spellings() []string { return append([]string{r.Flag}, r.Aliases...) }

// argsFor is the command line that gives the flag its value, as args.ts reads it.
func (r cliInventoryRow) argsFor(spelling string) []string {
	if r.Value == "" {
		return []string{spelling}
	}
	return []string{spelling, "value"}
}

// Pi 1.0.4 cli/args.ts parseArgs, one row per flag of the `pi` command: every spelling the inventory lists sets the
// field Pi sets (args.ts line in each case's comment).
func TestCLIInventoryTopLevelFlagsParseAsPiParses(t *testing.T) {
	type check func(t *testing.T, got Args)
	list := func(want ...string) []string { return want }
	cases := map[string]check{
		"--api-key":              func(t *testing.T, a Args) { assert.Equal(t, "value", a.APIKey) },                   // args.ts:119
		"--append-system-prompt": func(t *testing.T, a Args) { assert.Equal(t, list("value"), a.AppendSystemPrompt) }, // args.ts:123
		"--approve": func(t *testing.T, a Args) {
			require.NotNil(t, a.ProjectTrustOverride)
			assert.True(t, *a.ProjectTrustOverride)
		}, // args.ts:235
		"--continue":      func(t *testing.T, a Args) { assert.True(t, a.Continue) },                                      // args.ts:111
		"--exclude-tools": func(t *testing.T, a Args) { assert.Equal(t, list("value"), a.ExcludeTools) },                  // args.ts:156
		"--export":        func(t *testing.T, a Args) { assert.Equal(t, "value", a.Export) },                              // args.ts:178
		"--extension":     func(t *testing.T, a Args) { assert.Equal(t, list("value"), a.Extensions) },                    // args.ts:180
		"--fork":          func(t *testing.T, a Args) { assert.Equal(t, "value", a.Fork) },                                // args.ts:138
		"--help":          func(t *testing.T, a Args) { assert.True(t, a.Help) },                                          // args.ts:92
		"--list-models":   func(t *testing.T, a Args) { assert.True(t, a.ListModelsAll); assert.Empty(t, a.ListModels) },  // args.ts:212
		"--mode":          func(t *testing.T, a Args) { assert.Equal(t, "json", a.Mode); assert.Empty(t, a.Diagnostics) }, // args.ts:96
		"--model":         func(t *testing.T, a Args) { assert.Equal(t, "value", a.Model) },                               // args.ts:117
		"--models":        func(t *testing.T, a Args) { assert.Equal(t, list("value"), a.Models) },                        // args.ts:142
		"--name":          func(t *testing.T, a Args) { assert.Equal(t, "value", a.Name); assert.True(t, a.NameSet) },     // args.ts:126
		"--no-approve": func(t *testing.T, a Args) {
			require.NotNil(t, a.ProjectTrustOverride)
			assert.False(t, *a.ProjectTrustOverride)
		}, // args.ts:237
		"--no-builtin-tools":    func(t *testing.T, a Args) { assert.True(t, a.NoBuiltinTools) },                                         // args.ts:149
		"--no-context-files":    func(t *testing.T, a Args) { assert.True(t, a.NoContextFiles) },                                         // args.ts:210
		"--no-extensions":       func(t *testing.T, a Args) { assert.True(t, a.NoExtensions) },                                           // args.ts:183
		"--no-mcp":              func(t *testing.T, a Args) { assert.True(t, a.NoMcp) },                                                  // args.ts:185
		"--no-prompt-templates": func(t *testing.T, a Args) { assert.True(t, a.NoPromptTemplates) },                                      // args.ts:206
		"--no-session":          func(t *testing.T, a Args) { assert.True(t, a.NoSession) },                                              // args.ts:132
		"--no-skills":           func(t *testing.T, a Args) { assert.True(t, a.NoSkills) },                                               // args.ts:204
		"--no-themes":           func(t *testing.T, a Args) { assert.True(t, a.NoThemes) },                                               // args.ts:208
		"--no-tools":            func(t *testing.T, a Args) { assert.True(t, a.NoTools) },                                                // args.ts:147
		"--offline":             func(t *testing.T, a Args) { assert.True(t, a.Offline) },                                                // args.ts:239
		"--print":               func(t *testing.T, a Args) { assert.True(t, a.Print) },                                                  // args.ts:171
		"--prompt-template":     func(t *testing.T, a Args) { assert.Equal(t, list("value"), a.PromptTemplates) },                        // args.ts:190
		"--provider":            func(t *testing.T, a Args) { assert.Equal(t, "value", a.Provider) },                                     // args.ts:115
		"--resume":              func(t *testing.T, a Args) { assert.True(t, a.Resume) },                                                 // args.ts:113
		"--session":             func(t *testing.T, a Args) { assert.Equal(t, "value", a.Session) },                                      // args.ts:134
		"--session-dir":         func(t *testing.T, a Args) { assert.Equal(t, "value", a.SessionDir) },                                   // args.ts:140
		"--session-id":          func(t *testing.T, a Args) { assert.Equal(t, "value", a.SessionID) },                                    // args.ts:136
		"--skill":               func(t *testing.T, a Args) { assert.Equal(t, list("value"), a.Skills) },                                 // args.ts:187
		"--system-prompt":       func(t *testing.T, a Args) { assert.Equal(t, "value", a.SystemPrompt) },                                 // args.ts:121
		"--theme":               func(t *testing.T, a Args) { assert.Equal(t, list("value"), a.Themes) },                                 // args.ts:193
		"--thinking":            func(t *testing.T, a Args) { assert.Equal(t, "high", a.Thinking) },                                      // args.ts:161
		"--tools":               func(t *testing.T, a Args) { assert.Equal(t, list("value"), a.Tools) },                                  // args.ts:151
		"--tui-mode":            func(t *testing.T, a Args) { assert.Equal(t, "fullscreen", a.TuiMode); assert.Empty(t, a.Diagnostics) }, // args.ts:219
		"--use-theme":           func(t *testing.T, a Args) { require.NotNil(t, a.UseTheme); assert.Equal(t, "value", *a.UseTheme) },     // args.ts:196
		"--verbose":             func(t *testing.T, a Args) { assert.True(t, a.Verbose) },                                                // args.ts:233
		"--version":             func(t *testing.T, a Args) { assert.True(t, a.Version) },                                                // args.ts:94
	}
	var help bytes.Buffer
	printHelp(&help, false)
	seen := 0
	for _, row := range loadCLIInventory(t) {
		if row.Command != "pi" {
			continue
		}
		seen++
		check, ok := cases[row.Flag]
		require.True(t, ok, "%s has no case", row.ID)
		for _, spelling := range row.spellings() {
			args := row.argsFor(spelling)
			switch row.Flag {
			case "--mode":
				args = []string{spelling, "json"}
			case "--tui-mode":
				args = []string{spelling, "fullscreen"}
			case "--thinking":
				args = []string{spelling, "high"}
			case "--list-models":
				args = []string{spelling}
			}
			t.Run(row.Flag+" "+spelling, func(t *testing.T) { check(t, parseArgs(args)) })
		}
		// printHelp lists the flag with every alias on one line (args.ts:262-330), except --help's own `-h`.
		line := helpLine(help.String(), row.Flag)
		if row.Flag == "--list-models" {
			// args.ts:212-218: a following argument that is not a flag or @file is the search pattern.
			assert.Equal(t, "fable", parseArgs([]string{"--list-models", "fable"}).ListModels)
		}
		assert.NotEmpty(t, line, "%s is missing from --help", row.ID)
		for _, alias := range row.Aliases {
			assert.Contains(t, line, alias, "%s alias on its --help line", row.ID)
		}
	}
	assert.Len(t, cases, seen, "every case names an inventory row")
}

// helpLine is the line of text that starts the entry for flag.
func helpLine(text, flag string) string {
	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == flag || strings.HasPrefix(trimmed, flag+" ") || strings.HasPrefix(trimmed, flag+",") {
			return line
		}
	}
	return ""
}

// Pi 1.0.4 package-manager-cli.ts parsePackageCommand (install, remove, list, update) and handleConfigCommand: every
// flag row of those commands sets the option Pi sets, and the command's help lists the flag.
func TestCLIInventoryPackageCommandFlagsParseAsPiParses(t *testing.T) {
	type check func(t *testing.T, got *packageCLIOptions)
	trust := func(want bool) check {
		return func(t *testing.T, got *packageCLIOptions) {
			require.NotNil(t, got.projectTrustOverride)
			assert.Equal(t, want, *got.projectTrustOverride)
		}
	}
	// One check per flag; the line is where parsePackageCommand reads it.
	cases := map[string]check{
		"--approve":    trust(true),                                                                       // package-manager-cli.ts:454
		"--no-approve": trust(false),                                                                      // package-manager-cli.ts:459
		"--help":       func(t *testing.T, got *packageCLIOptions) { assert.True(t, got.help) },           // package-manager-cli.ts:404
		"--local":      func(t *testing.T, got *packageCLIOptions) { assert.True(t, got.local) },          // package-manager-cli.ts:409
		"--all":        func(t *testing.T, got *packageCLIOptions) { assert.True(t, got.allPackages) },    // package-manager-cli.ts:445
		"--extensions": func(t *testing.T, got *packageCLIOptions) { assert.True(t, got.extensionsOnly) }, // package-manager-cli.ts:427
		"--force":      func(t *testing.T, got *packageCLIOptions) { assert.True(t, got.force) },          // package-manager-cli.ts:464
		"--models":     func(t *testing.T, got *packageCLIOptions) { assert.True(t, got.modelsOnly) },     // package-manager-cli.ts:436
		"--self":       func(t *testing.T, got *packageCLIOptions) { assert.True(t, got.selfOnly) },       // package-manager-cli.ts:418
		"--extension": func(t *testing.T, got *packageCLIOptions) { // package-manager-cli.ts:473
			assert.Equal(t, "./pkg", got.extensionSource)
			assert.Empty(t, got.invalidOption)
		},
	}
	commandOf := func(row cliInventoryRow) string { return strings.TrimPrefix(row.Command, "pi-") }
	seen := 0
	for _, row := range loadCLIInventory(t) {
		if row.Command == "pi" || commandOf(row) == "config" {
			continue
		}
		seen++
		check, ok := cases[row.Flag]
		require.True(t, ok, "%s has no case", row.ID)
		for _, spelling := range row.spellings() {
			args := []string{commandOf(row), spelling}
			if row.Value != "" {
				args = append(args, "./pkg")
			}
			t.Run(commandOf(row)+" "+spelling, func(t *testing.T) {
				got, handled := parsePackageCommand(args)
				require.True(t, handled)
				assert.Empty(t, got.invalidOption, "%s is a flag of %s", spelling, commandOf(row))
				check(t, got)
			})
		}
		if row.Flag == "--help" {
			continue
		}
		// printPackageCommandHelp names the flag and each alias in the command's Options (package-manager-cli.ts:295-376).
		newPackageCommandPathsFixture(t)
		help, _, code := capturePackageCommand(t, commandOf(row), "--help")
		require.Equal(t, 0, code)
		line := helpLine(help, row.Flag)
		if row.Flag == "--local" || row.Flag == "--approve" || row.Flag == "--no-approve" {
			line = helpLine(help, row.Aliases[0]+", "+row.Flag)
		}
		assert.NotEmpty(t, line, "%s is missing from `%s --help`", row.ID, commandOf(row))
	}
	assert.Equal(t, 20, seen, "install 4, remove 4, list 3 and update 9 rows of the inventory")

	// A flag outside a command's rows is Pi's unknown option for that command (package-manager-cli.ts:883).
	for _, args := range [][]string{{"list", "--local"}, {"update", "--local"}, {"install", "--all"}, {"remove", "--force"}} {
		got, handled := parsePackageCommand(args)
		require.True(t, handled)
		assert.Equal(t, args[1], got.invalidOption, "%v", args)
	}
}

// Pi 1.0.4 handleConfigCommand (package-manager-cli.ts:790-870): `-h`/`--help` anywhere prints the help,
// `-l`, `-a` and `-na` are the options, another dash argument is an unknown option and a bare word an unexpected
// argument. printConfigCommandHelp names the same three options.
func TestCLIInventoryConfigCommandFlagsAsPi(t *testing.T) {
	configRows := 0
	for _, row := range loadCLIInventory(t) {
		if row.Command == "pi-config" {
			configRows++
		}
	}
	assert.Equal(t, 4, configRows, "pi-config rows: --approve, --help, --local, --no-approve")

	wantHelp := "Usage:\n  pig config [-l] [--approve|--no-approve]\n\n" +
		"Open the resource configuration TUI to enable or disable package resources.\n" +
		"Without -l, starts in global settings (~/.pig/agent/settings.json).\n" +
		"Press Tab in the TUI to switch between global and project-local modes.\n\n" +
		"Options:\n" +
		"  -l, --local       Edit project overrides (.pig/settings.json)\n" +
		"  -a, --approve     Trust project-local files for this command with -l\n" +
		"  -na, --no-approve Ignore project-local files for this command with -l\n\n"
	for _, args := range [][]string{{"--help"}, {"-h"}, {"-l", "--bogus", "-h"}} {
		stdout, stderr, code := captureStdoutStderr(t, func() int { return runConfigCommand(args) })
		assert.Equal(t, 0, code, "%v", args)
		assert.Empty(t, stderr)
		assert.Equal(t, wantHelp, stdout, "%v", args)
	}
	usage := "pig config [-l] [--approve|--no-approve]"
	for _, tc := range []struct{ arg, want string }{
		{"--bogus", "Unknown option --bogus for \"config\".\nUse \"pig --help\" or \"" + usage + "\".\n"},
		{"-x", "Unknown option -x for \"config\".\nUse \"pig --help\" or \"" + usage + "\".\n"},
		{"extra", "Unexpected argument extra.\nUsage: " + usage + "\n"},
	} {
		stdout, stderr, code := captureStdoutStderr(t, func() int { return runConfigCommand([]string{tc.arg}) })
		assert.Equal(t, 1, code, tc.arg)
		assert.Empty(t, stdout)
		assert.Equal(t, tc.want, stderr, tc.arg)
	}
}

// Pi 1.0.4 printPackageCommandHelp("list") and ("remove") (package-manager-cli.ts:332-376) with the app and config
// directory names; the option columns are each command's own width. (`install` adds the --validate-only options of D28.)
func TestCLIInventoryListAndRemoveHelpMatchPi(t *testing.T) {
	want := map[string]string{
		"list": "Usage:\n  pig list [--approve|--no-approve]\n\nList installed packages from user and project settings.\n\n" +
			"Options:\n" +
			"  -a, --approve      Trust project-local files for this command\n" +
			"  -na, --no-approve  Ignore project-local files for this command\n\n",
		"remove": "Usage:\n  pig remove <source> [-l] [--approve|--no-approve]\n\nRemove a package and its source from settings.\nAlias: pig uninstall <source> [-l]\n\n" +
			"Options:\n" +
			"  -l, --local       Remove from project settings (.pig/settings.json)\n" +
			"  -a, --approve     Trust project-local files for this command\n" +
			"  -na, --no-approve Ignore project-local files for this command\n\n" +
			"Examples:\n  pig remove npm:@foo/bar\n  pig uninstall npm:@foo/bar\n\n",
	}
	for command, text := range want {
		newPackageCommandPathsFixture(t)
		stdout, stderr, code := capturePackageCommand(t, command, "--help")
		assert.Equal(t, 0, code, command)
		assert.Empty(t, stderr)
		assert.Equal(t, text, stdout, command)
	}
}
