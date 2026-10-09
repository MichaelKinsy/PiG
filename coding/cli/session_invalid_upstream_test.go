// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Upstream 0.99.1 changes only how its harness starts the CLI: `--import` receives the source resolver as a file URL (pathToFileURL) instead of a path
// (.upstream/v0.99.1/packages/coding-agent/test/session-file-invalid.test.ts:10-11). The Go test starts the compiled binary and has no Node loader flag, so that substitution has no Go
// counterpart: the inputs and expectations of every case are unchanged.
// .upstream/v0.99.1/packages/coding-agent/test/session-file-invalid.test.ts:49
func TestInvalidSessionFilePrintsFriendlyErrorAndPreservesContent(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	agentDir := filepath.Join(root, "agent")
	project := filepath.Join(root, "project")
	for _, path := range []string{home, agentDir, project} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "not-a-session.log")
	original := "{\"type\":\"event\",\"data\":\"not a session\"}\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	run := runPigStartup(t, buildPigBinaryForSignalTest(t), home, agentDir, project, "", "--session", path, "-p", "hi")
	var exit *exec.ExitError
	if !errors.As(run.err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("error=%v stderr=%s", run.err, run.stderr)
	}
	if !strings.Contains(run.stderr, "Error: Session file is not a valid pi session: "+path) {
		t.Fatal(run.stderr)
	}
	for _, unexpected := range []string{"SessionManager.open", "at ", "goroutine ", "panic:"} {
		if strings.Contains(run.stderr, unexpected) {
			t.Fatalf("unexpected %q in %s", unexpected, run.stderr)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("file=%q error=%v", data, err)
	}
}
