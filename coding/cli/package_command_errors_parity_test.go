package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const updateUsageLine = "Usage: pig update [source|self|pig] [--self|--extensions|--models|--all] [--extension <source>] [--approve|--no-approve] [--force]\n"

// Pi 0.87.1 package-manager-cli.ts parsePackageCommand keeps the first conflict it records: --all is checked before
// --models, then --extension, then a positional target, and a repeated --extension is recorded while parsing.
// handlePackageCommand prints the conflict and the usage line. Expected text was captured from Pi 0.87.1 on Windows.
func TestUpdateConflictMessagesMatchPi(t *testing.T) {
	cases := []struct{ args, want string }{
		{"--all --self", "--all cannot be combined with --self, --extensions, --models, or --extension"},
		{"--all --extensions", "--all cannot be combined with --self, --extensions, --models, or --extension"},
		{"--all --models", "--all cannot be combined with --self, --extensions, --models, or --extension"},
		{"--all --extension ./nope-a", "--all cannot be combined with --self, --extensions, --models, or --extension"},
		{"--all ./nope-a", "--all cannot be combined with a positional source"},
		{"--all pig", "--all cannot be combined with a positional source"},
		{"--models --self", "--models cannot be combined with --self, --extensions, --all, or --extension"},
		{"--models --extensions", "--models cannot be combined with --self, --extensions, --all, or --extension"},
		{"--models --all", "--all cannot be combined with --self, --extensions, --models, or --extension"},
		{"--models --extension ./nope-a", "--models cannot be combined with --self, --extensions, --all, or --extension"},
		{"--models ./nope-a", "--models cannot be combined with a positional source"},
		{"--models pig", "--models cannot be combined with a positional source"},
		{"--extension ./nope-a --self", "--extension cannot be combined with --self, --extensions, or --all"},
		{"--extension ./nope-a --extensions", "--extension cannot be combined with --self, --extensions, or --all"},
		{"--extension ./nope-a --all", "--all cannot be combined with --self, --extensions, --models, or --extension"},
		{"--extension ./nope-a ./nope-b", "--extension cannot be combined with a positional source"},
		{"--extension ./nope-a --extension ./nope-b", "--extension can only be provided once"},
		{"--extension ./nope-a --extension ./nope-b --self", "--extension can only be provided once"},
		{"--extension ./nope-a --extension ./nope-b ./nope-c", "--extension can only be provided once"},
		{"./nope-a --self", "positional update targets cannot be combined with --self, --extensions, or --all"},
		{"./nope-a --extensions", "positional update targets cannot be combined with --self, --extensions, or --all"},
		{"--self ./nope-a", "positional update targets cannot be combined with --self, --extensions, or --all"},
		{"--models --all --self", "--all cannot be combined with --self, --extensions, --models, or --extension"},
		{"--all --models ./nope-a", "--all cannot be combined with --self, --extensions, --models, or --extension"},
		{"--extension ./nope-a --models", "--models cannot be combined with --self, --extensions, --all, or --extension"},
		{"--self --extensions ./nope-a", "positional update targets cannot be combined with --self, --extensions, or --all"},
	}
	for _, tc := range cases {
		t.Run(tc.args, func(t *testing.T) {
			newPackageCommandPathsFixture(t)
			stdout, stderr, code := capturePackageCommand(t, append([]string{"update"}, strings.Fields(tc.args)...)...)
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.Equal(t, tc.want+"\n"+updateUsageLine, stderr)
		})
	}
}

// Pi 0.87.1 parsePackageCommand records the second positional argument of every package command as invalidArgument,
// and handlePackageCommand reports it after an unknown option and a missing option value but before a conflict.
func TestPackageCommandExtraArgumentMatchesPi(t *testing.T) {
	installUsage := "Usage: pig install <source> [-l] [--approve|--no-approve]\n"
	cases := []struct{ args, want string }{
		{"install ./nope-a ./nope-b", "Unexpected argument ./nope-b.\n" + installUsage},
		{"install -l ./nope-a ./nope-b ./nope-c", "Unexpected argument ./nope-b.\n" + installUsage},
		{"remove ./nope-a ./nope-b", "Unexpected argument ./nope-b.\nUsage: pig remove <source> [-l] [--approve|--no-approve]\n"},
		{"uninstall ./nope-a ./nope-b", "Unexpected argument ./nope-b.\nUsage: pig remove <source> [-l] [--approve|--no-approve]\n"},
		{"list ./nope-a ./nope-b", "Unexpected argument ./nope-b.\nUsage: pig list [--approve|--no-approve]\n"},
		{"update ./nope-a ./nope-b", "Unexpected argument ./nope-b.\n" + updateUsageLine},
		{"update ./nope-a ./nope-b --all", "Unexpected argument ./nope-b.\n" + updateUsageLine},
		{"update --all ./nope-a ./nope-b", "Unexpected argument ./nope-b.\n" + updateUsageLine},
		{"update --extension ./nope-b ./nope-c", "--extension cannot be combined with a positional source\n" + updateUsageLine},
		{"update ./nope-a ./nope-b --extension", "Missing value for --extension.\n" + updateUsageLine},
		{"install --bogus ./nope-a ./nope-b", "Unknown option --bogus for \"install\".\nUse \"pig --help\" or \"pig install <source> [-l] [--approve|--no-approve]\".\n"},
	}
	for _, tc := range cases {
		t.Run(tc.args, func(t *testing.T) {
			newPackageCommandPathsFixture(t)
			stdout, stderr, code := capturePackageCommand(t, strings.Fields(tc.args)...)
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.Equal(t, tc.want, stderr)
		})
	}
}

// pig additive (D28): --validate-only accepts several install sources; without it the extra source is Pi's unexpected argument.
func TestValidateOnlyInstallKeepsSeveralSources(t *testing.T) {
	for _, args := range [][]string{
		{"install", "--validate-only", "--json", "./nope-a", "./nope-b"},
		{"install", "./nope-a", "./nope-b", "--validate-only", "--json"},
	} {
		newPackageCommandPathsFixture(t)
		stdout, stderr, _ := capturePackageCommand(t, args...)
		assert.NotContains(t, stderr, "Unexpected argument")
		assert.Contains(t, stdout, "nope-a")
		assert.Contains(t, stdout, "nope-b")
	}
}

// Pi 0.87.1 printPackageCommandHelp("update"), with the app name and self alias rendered for pig.
func TestUpdateHelpMatchesPi(t *testing.T) {
	want := "Usage:\n" +
		"  pig update [source|self|pig] [--self|--extensions|--models|--all] [--extension <source>] [--approve|--no-approve] [--force]\n" +
		"\n" +
		"Update pig, installed packages, or model catalogs.\n" +
		"\n" +
		"Options:\n" +
		"  --self                  Update pig only (default when no target is given)\n" +
		"  --extensions            Update installed packages only\n" +
		"  --models                Refresh model catalogs only\n" +
		"  --all                   Update pig and installed packages\n" +
		"  --extension <source>    Update one package only\n" +
		"  -a, --approve           Trust project-local files for this command\n" +
		"  -na, --no-approve       Ignore project-local files for this command\n" +
		"  --force                 Reinstall pig even if the current version is latest\n" +
		"\n" +
		"Short forms:\n" +
		"  pig update                Update pig only\n" +
		"  pig update --all          Update pig and all extensions\n" +
		"  pig update --models       Refresh model catalogs only\n" +
		"  pig update <source>       Update one package\n" +
		"  pig update pig            Update pig only (self works as alias to pig)\n" +
		"\n"
	for _, args := range [][]string{{"update", "--help"}, {"update", "-h"}} {
		stdout, stderr, code := capturePackageCommand(t, args...)
		assert.Equal(t, 0, code)
		assert.Empty(t, stderr)
		assert.Equal(t, want, stdout)
		assert.False(t, regexp.MustCompile(`\bupdate pi\b`).MatchString(stdout), "pi is not a pig self target")
	}
}

// Pi 0.87.1 core/package-manager.ts installs a local source only when it exists: "Path does not exist: <resolved>".
func TestInstallMissingLocalPathMatchesPi(t *testing.T) {
	f := newPackageCommandPathsFixture(t)
	missing := filepath.Join(f.root, "does-not-exist")
	stdout, stderr, code := capturePackageCommand(t, "install", missing)
	assert.Equal(t, 1, code)
	assert.Equal(t, "Installing "+missing+"...\n", stdout)
	assert.Equal(t, "Error: Path does not exist: "+missing+"\n", stderr)
	_, err := os.Stat(filepath.Join(f.agentDir, "settings.json"))
	if err == nil {
		settings, readErr := os.ReadFile(filepath.Join(f.agentDir, "settings.json"))
		require.NoError(t, readErr)
		assert.NotContains(t, string(settings), "does-not-exist")
	}
}
