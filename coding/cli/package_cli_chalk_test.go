package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// package-manager-cli.ts styles its output with chalk: progress lines chalk.dim(`${message}\n`), results green, "No packages installed." dim, list headings bold and installed paths dim, argument errors red with a dim usage line. Under FORCE_COLOR=1 Pi 1.1.0 prints these bytes for remove, an empty list and an unknown option (APP_NAME aside).
func TestPackageCommandsStyleOutputLikeChalk(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	root := physicalTempDir(t)
	t.Chdir(root)
	agentDir := filepath.Join(root, "agent")
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"list"}) })
	if code != 0 || stdout != "\x1b[2mNo packages installed.\x1b[22m\n" || stderr != "" {
		t.Errorf("list: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = captureStdoutStderr(t, func() int { return runPackageCommand([]string{"remove", "npm:nope-pkg"}) })
	if code != 1 || stdout != "\x1b[2mRemoving npm:nope-pkg...\x1b[22m\n\x1b[2m\x1b[22m" || stderr != "\x1b[31mNo matching package found for npm:nope-pkg\x1b[39m\n" {
		t.Errorf("remove missing: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", "--bogus", "x"}) })
	wantErr := "\x1b[31mUnknown option --bogus for \"install\".\x1b[39m\n\x1b[2mUse \"pig --help\" or \"" + packageUsage(packageInstall) + "\".\x1b[22m\n"
	if code != 1 || stdout != "" || stderr != wantErr {
		t.Errorf("unknown option: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	sm := codingagent.NewSettingsManager(root, agentDir)
	if _, err := packageManagerFor(root, sm, nil).AddSourceToSettings("./fixture", false); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code = captureStdoutStderr(t, func() int { return runPackageCommand([]string{"remove", "./fixture"}) })
	if code != 0 || stdout != "\x1b[2mRemoving ./fixture...\x1b[22m\n\x1b[2m\x1b[22m\x1b[32mRemoved ./fixture\x1b[39m\n" || stderr != "" {
		t.Errorf("remove: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

// package-manager-cli.ts handleConfigCommand prints an unknown option or extra argument in red with a dim usage line.
func TestConfigCommandArgumentErrorsStyleLikeChalk(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	t.Chdir(t.TempDir())
	_, stderr, code := captureStdoutStderr(t, func() int { return runConfigCommand([]string{"--bogus"}) })
	if want := "\x1b[31mUnknown option --bogus for \"config\".\x1b[39m\n\x1b[2mUse \"pig --help\" or \"" + configCommandUsage + "\".\x1b[22m\n"; code != 1 || stderr != want {
		t.Errorf("unknown option: code=%d stderr=%q, want %q", code, stderr, want)
	}
	_, stderr, code = captureStdoutStderr(t, func() int { return runConfigCommand([]string{"extra"}) })
	if want := "\x1b[31mUnexpected argument extra.\x1b[39m\n\x1b[2mUsage: " + configCommandUsage + "\x1b[22m\n"; code != 1 || stderr != want {
		t.Errorf("extra argument: code=%d stderr=%q, want %q", code, stderr, want)
	}
}

// package-manager-cli.ts help text starts with chalk.bold("Usage:"), so under FORCE_COLOR the heading is bold even when stdout is not a terminal.
func TestPackageAndConfigHelpBoldUsageLikeChalk(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	stdout, _, _ := captureStdoutStderr(t, func() int { return runConfigCommand([]string{"--help"}) })
	if !strings.HasPrefix(stdout, "\x1b[1mUsage:\x1b[22m\n  pig config") {
		t.Errorf("config help = %q", stdout)
	}
	stdout, _, _ = captureStdoutStderr(t, func() int { return runPackageCommand([]string{"list", "--help"}) })
	if !strings.HasPrefix(stdout, "\x1b[1mUsage:\x1b[22m\n  pig list") {
		t.Errorf("list help = %q", stdout)
	}
}
