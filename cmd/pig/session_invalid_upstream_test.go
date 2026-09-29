// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// .upstream/v0.87.1/packages/coding-agent/test/session-file-invalid.test.ts:47
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
