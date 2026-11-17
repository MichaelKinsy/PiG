package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi b513b64d8ac8 (#11264): interactive mode installs and refreshes packages while the TUI draws, so main.ts captures git and npm output and
// announces an install on stderr until the TUI starts. These tests drive PiG's counterpart, the package run the runtime builder hands every load.

// fakeNpmOnPath puts an npm on PATH whose install prints npm's summary lines and creates the package, or fails after printing when the
// package is named fail-pkg.
func fakeNpmOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	writeStubScript(t, filepath.Join(bin, "npm"), `#!/bin/sh
cmd=$1; shift
prefix=; spec=
while [ $# -gt 0 ]; do case $1 in --prefix) prefix=$2; shift 2;; --*) shift;; *) [ -z "$spec" ] && spec=$1; shift;; esac; done
case $cmd in
 install)
  echo "npm warn deprecated left-pad@1.0.0: use something else"
  case $spec in
   fail-pkg*) echo "npm ERR! code ELIFECYCLE" >&2; echo "npm ERR! fail-pkg postinstall: exit 1" >&2; exit 1;;
  esac
  name=${spec%@*}
  mkdir -p "$prefix/node_modules/$name/prompts"
  printf '{"name":"%s","version":"1.0.0","pi":{"prompts":["prompts"]}}\n' "$name" > "$prefix/node_modules/$name/package.json"
  printf 'hello\n' > "$prefix/node_modules/$name/prompts/hello.md"
  echo "added 1 package in 1s";;
 view) echo '"1.0.0"';;
esac
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func packageRunFixture(t *testing.T) (cwd, agentDir string, sm *codingagent.SettingsManager) {
	t.Helper()
	cwd, agentDir = t.TempDir(), t.TempDir()
	t.Chdir(cwd)
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	fakeNpmOnPath(t)
	return cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir)
}

func TestInteractivePackageRunKeepsNpmOutputOffTheTerminalAndAnnouncesTheInstallUntilTheTUIStarts(t *testing.T) {
	cwd, agentDir, sm := packageRunFixture(t)
	run := newPackageRun(appModeInteractive)
	var set cliExtensionSet
	stdout, stderr, _ := captureStdoutStderr(t, func() int {
		set = collectCLIExtensionConfigs(cwd, agentDir, sm, run, []string{"npm:probe-npm"})
		return 0
	})
	if len(set.missing) != 0 || len(set.installFailures) != 0 {
		t.Fatalf("set = %+v, want the install to succeed", set)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing: npm's output is captured", stdout)
	}
	if stderr != "Installing npm:probe-npm...\n" {
		t.Errorf("stderr = %q, want only the install announcement", stderr)
	}

	// The TUI draws from here: a refresh announces nothing.
	run.stopAnnouncing()
	resetCLIPackageResolutions()
	stdout, stderr, _ = captureStdoutStderr(t, func() int {
		collectCLIExtensionConfigs(cwd, agentDir, sm, run, []string{"npm:probe-npm"})
		return 0
	})
	if stdout != "" || stderr != "" {
		t.Errorf("after the TUI started stdout = %q stderr = %q, want nothing", stdout, stderr)
	}
}

func TestOtherModesInheritNpmOutputAndAnnounceNothing(t *testing.T) {
	for _, mode := range []appMode{appModePrint, appModeJSON, appModeRPC} {
		t.Run(string(mode), func(t *testing.T) {
			cwd, agentDir, sm := packageRunFixture(t)
			run := newPackageRun(mode)
			if run != nil {
				t.Fatalf("newPackageRun(%s) = %+v, want nil: inherit and no announcement", mode, run)
			}
			stdout, stderr, _ := captureStdoutStderr(t, func() int {
				collectCLIExtensionConfigs(cwd, agentDir, sm, run, []string{"npm:probe-npm"})
				return 0
			})
			// An install prints its child's output on the terminal, as Pi's inherited stdio does; headless modes keep stdout for the protocol.
			if !strings.Contains(stdout+stderr, "added 1 package in 1s") {
				t.Errorf("stdout = %q stderr = %q, want npm's output on the terminal", stdout, stderr)
			}
		})
	}
}

func TestInteractivePackageRunReportsTheEndOfAFailingInstallsOutput(t *testing.T) {
	cwd, agentDir, sm := packageRunFixture(t)
	run := newPackageRun(appModeInteractive)
	run.stopAnnouncing()
	var set cliExtensionSet
	stdout, stderr, _ := captureStdoutStderr(t, func() int {
		set = collectCLIExtensionConfigs(cwd, agentDir, sm, run, []string{"npm:fail-pkg"})
		return 0
	})
	if stdout != "" || stderr != "" {
		t.Errorf("stdout = %q stderr = %q, want nothing over the TUI", stdout, stderr)
	}
	if len(set.installFailures) != 1 || !strings.Contains(set.installFailures[0].Message, "failed with code 1:\nnpm warn deprecated left-pad@1.0.0: use something else\nnpm ERR! code ELIFECYCLE\nnpm ERR! fail-pkg postinstall: exit 1") {
		t.Fatalf("install failures = %+v, want one that carries npm's output", set.installFailures)
	}
}
