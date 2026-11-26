// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func gitleaksCheckFromConfig(t *testing.T) check {
	t.Helper()
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	for _, c := range cfg.Checks {
		if c.Name == "gitleaks" {
			return c
		}
	}
	t.Fatal("gitleaks check not found in config.yaml")
	return check{}
}

// Always runs: verifies gitleaks executes from inside {input} with relative path "." so ^-anchored paths in .gitleaks.toml
// and relative fingerprints in .gitleaksignore match repository-relative paths instead of temporary runner directories.
func TestGitleaksScanCommandRunsInsideAddedTree(t *testing.T) {
	c := gitleaksCheckFromConfig(t)
	ws := workspace{
		dir:   filepath.Join(t.TempDir(), "ws"),
		added: filepath.Join(t.TempDir(), "added"),
		main:  filepath.Join(t.TempDir(), "main"),
		bin:   filepath.Join(t.TempDir(), "bin"),
	}
	out := filepath.Join(ws.dir, "gitleaks.report")
	cmd, name := scanCommand(context.Background(), c.Scan, nil, ws, ws.added, out)
	if name != "gitleaks" {
		t.Fatalf("program name = %q, want gitleaks", name)
	}
	if cmd.Dir != ws.added {
		t.Fatalf("cmd.Dir = %q, want %q ({input})", cmd.Dir, ws.added)
	}
	// cmd.Args[0] is the resolved executable; cmd.Args[1:] are the arguments.
	if len(cmd.Args) < 3 || cmd.Args[1] != "dir" || cmd.Args[2] != "." {
		t.Fatalf("cmd.Args = %q, want [gitleaks dir . ...]", cmd.Args)
	}
	if slices.Contains(cmd.Args[1:], ws.added) {
		t.Fatalf("cmd.Args still passes absolute input path %q: %q", ws.added, cmd.Args)
	}
	wantCfg := filepath.Join(ws.main, ".gitleaks.toml")
	if idx := slices.Index(cmd.Args, "--config"); idx < 0 || idx+1 >= len(cmd.Args) || cmd.Args[idx+1] != wantCfg {
		t.Fatalf("--config arg missing or wrong in %q, want %q", cmd.Args, wantCfg)
	}
	if idx := slices.Index(cmd.Args, "--gitleaks-ignore-path"); idx < 0 || idx+1 >= len(cmd.Args) || cmd.Args[idx+1] != ws.main {
		t.Fatalf("--gitleaks-ignore-path arg missing or wrong in %q, want %q", cmd.Args, ws.main)
	}
}

// Runs with the real gitleaks binary when available on PATH; skips otherwise.
// Proves: (1) an allowlisted ^-anchored path in main's .gitleaks.toml passes;
// (2) a secret in a non-allowlisted path still fails with relative path:head-line mapped through toHeadLines;
// (3) --gitleaks-ignore-path {main} honors main's .gitleaksignore using relative fingerprints.
func TestGitleaksScanAllowlistAndRealFindings(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not on PATH")
	}
	c := gitleaksCheckFromConfig(t)
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	realConfig, err := os.ReadFile(filepath.Join(root, ".gitleaks.toml"))
	if err != nil {
		t.Fatalf("read repo .gitleaks.toml: %v", err)
	}

	// Build a synthetic secret at runtime so the test file itself never trips gitleaks.
	syntheticSecret := strings.Join([]string{"sk", "live", "9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"}, "_")
	allowlistedPath := "coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-ai/providers/data/.manifest.json"
	unlistedPath := "pkg/example/secret.go"

	newWorkspace := func(t *testing.T, ignoreLine string) workspace {
		t.Helper()
		dir := t.TempDir()
		ws := workspace{
			dir:   dir,
			added: filepath.Join(dir, "added"),
			main:  filepath.Join(dir, "main"),
			bin:   filepath.Join(dir, "bin"),
		}
		for _, d := range []string{ws.added, ws.main, ws.bin} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(ws.main, ".gitleaks.toml"), realConfig, 0o600); err != nil {
			t.Fatal(err)
		}
		if ignoreLine != "" {
			if err := os.WriteFile(filepath.Join(ws.main, ".gitleaksignore"), []byte(ignoreLine+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return ws
	}

	t.Run("allowlisted ^-anchored path passes with real digests and synthetic key", func(t *testing.T) {
		ws := newWorkspace(t, "")
		patch := "@@ -1,1 +1,1 @@\n-old\n+{\"google.json\":\"a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90\",\"k\":\"" + syntheticSecret + "\"}"
		d := &data{Files: []prFile{{Filename: allowlistedPath, Status: "modified", Patch: patch}}}
		if _, _, err := writeAdded(d, ws, true); err != nil {
			t.Fatal(err)
		}
		o := runScan(context.Background(), c, d, ws, io.Discard)
		if o.result != resultPass {
			t.Fatalf("allowlisted path %s failed: %+v", allowlistedPath, o)
		}
	})

	t.Run("non-allowlisted path still fails and maps relative path to PR head line", func(t *testing.T) {
		ws := newWorkspace(t, "")
		patch := "@@ -40,2 +40,3 @@\n ctx\n+const token = \"" + syntheticSecret + "\"\n ctx"
		d := &data{Files: []prFile{{Filename: unlistedPath, Status: "modified", Patch: patch}}}
		if _, _, err := writeAdded(d, ws, true); err != nil {
			t.Fatal(err)
		}
		o := runScan(context.Background(), c, d, ws, io.Discard)
		if o.result != resultFail {
			t.Fatalf("non-allowlisted secret passed: %+v", o)
		}
		wantRef := unlistedPath + ":41 "
		if !strings.Contains(o.reason, wantRef) {
			t.Fatalf("finding reason = %q, want relative path and mapped head line %q", o.reason, wantRef)
		}
		if strings.Contains(o.reason, ws.added) {
			t.Fatalf("finding reason leaked temp added dir %q: %q", ws.added, o.reason)
		}
	})

	t.Run("gitleaks-ignore-path on main matches relative fingerprint", func(t *testing.T) {
		ws := newWorkspace(t, unlistedPath+":stripe-access-token:1")
		patch := "@@ -40,2 +40,3 @@\n ctx\n+const token = \"" + syntheticSecret + "\"\n ctx"
		d := &data{Files: []prFile{{Filename: unlistedPath, Status: "modified", Patch: patch}}}
		if _, _, err := writeAdded(d, ws, true); err != nil {
			t.Fatal(err)
		}
		o := runScan(context.Background(), c, d, ws, io.Discard)
		if o.result != resultPass {
			t.Fatalf("main .gitleaksignore relative fingerprint did not suppress finding: %+v", o)
		}
	})
}
