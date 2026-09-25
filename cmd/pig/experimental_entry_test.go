package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestStableEntryDoesNotDispatchExperimentalCommands ports upstream
// test/experimental-cli-entry.test.ts "does not dispatch experimental commands
// from the stable entrypoint" (#9132: enabling experiments must not pull
// remote-server dependencies into the published CLI).
//
// Pi's published `pi` binary is built from src/cli.ts, which calls main()
// directly; only the source-only development entrypoint
// src/experimental/cli.ts routes `server` and `client` to
// runExperimentalCommand. tsconfig.build.json and the package "files" list
// exclude src/experimental, src/cli/experimental, and src/client, so
// PI_EXPERIMENTAL=1 leaves both words to the stable CLI, where --version wins.
// commands.ts gates `server` and `client` identically, so both are pinned.
func TestStableEntryDoesNotDispatchExperimentalCommands(t *testing.T) {
	bin := testExecutable(filepath.Join(t.TempDir(), "pig-experimental-entry"))
	build := exec.Command("go", "build", "-o", bin, ".")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pig: %v\n%s", err, data)
	}

	for _, command := range []string{"server", "client"} {
		t.Run(command, func(t *testing.T) {
			directory := t.TempDir()
			cmd := exec.Command(bin, command, "--server-id", "invalid", "--version")
			cmd.Dir = directory
			cmd.Env = append(os.Environ(),
				"HOME="+directory,
				"USERPROFILE="+directory,
				"PI_CODING_AGENT_DIR="+filepath.Join(directory, "agent"),
				"PI_OFFLINE=1",
				"PI_EXPERIMENTAL=1",
			)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			stdout, err := cmd.Output()
			if err != nil {
				t.Fatalf("pig %s --server-id invalid --version: %v\nstderr: %s", command, err, stderr.String())
			}
			if got := strings.TrimSpace(string(stdout)); got != cliVersionString() {
				t.Fatalf("stdout = %q, want the stable CLI version %q", got, cliVersionString())
			}
			if strings.Contains(stderr.String(), "Invalid --server-id") {
				t.Fatalf("stable entry parsed the experimental command: %s", stderr.String())
			}
		})
	}
}
