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

func checkFromConfig(t *testing.T, name string) check {
	t.Helper()
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	for _, c := range cfg.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("%s check not found in config.yaml", name)
	return check{}
}

func gitleaksCheckFromConfig(t *testing.T) check {
	t.Helper()
	return checkFromConfig(t, "gitleaks")
}

// Always runs: verifies gitleaks executes from inside {input} with relative path "." so ^-anchored paths in .gitleaks.toml
// and relative fingerprints in main's .gitleaksignore match repository-relative paths, and quarantines PR-owned
// .gitleaksignore and .gitleaks.toml so gitleaks never loads PR content as configuration.
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
	for _, q := range []string{".gitleaksignore", ".gitleaks.toml"} {
		if !slices.Contains(c.Scan.Quarantine, q) {
			t.Fatalf("gitleaks scan quarantine = %q, want %q included", c.Scan.Quarantine, q)
		}
	}
}

// Always runs (no gitleaks binary needed): proves that when a PR touches .gitleaksignore or .gitleaks.toml,
// runScan stages them under neutral non-colliding names in a per-scan view (never leaving .gitleaksignore or
// .gitleaks.toml at the scanned root), keeps the original ws.added intact for other scans, and maps findings
// on the neutral names back to the real path and PR head line.
func TestGitleaksQuarantineStagesAndMapsFindingsBack(t *testing.T) {
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

	// Fake scanner checks that neither .gitleaksignore nor .gitleaks.toml exists in its cwd,
	// finds the quarantined content under neutral names, and reports line 2 of the quarantined ignore file.
	fakeTool(t, ws.bin, "fake-gl", `
set -e
if [ -e .gitleaksignore ] || [ -e .gitleaks.toml ]; then
  echo "PR config file leaked into scan dir" >&2
  exit 9
fi
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--report-path" ]; then out="$2"; shift 2; continue; fi
  shift
done
ignore_file=$(grep -l "SECRET_IN_IGNORE" _preflight_quarantine_*.txt || true)
if [ -z "$ignore_file" ]; then
  echo "quarantined .gitleaksignore content not scanned" >&2
  exit 8
fi
printf '[{"File":"%s","StartLine":2,"RuleID":"fake-rule"}]' "$ignore_file" > "$out"
exit 3
`)

	s := scanSpec{
		Input:      inputAdded,
		Quarantine: []string{".gitleaksignore", ".gitleaks.toml"},
		Dir:        "{input}",
		Run:        []string{"fake-gl", "dir", ".", "--report-path", "{out}"},
		Exit:       exitCodes{Clean: []int{0}, Findings: []int{3}},
		Report:     reportSpec{In: "out", Format: "json", File: "File", Line: "StartLine", Message: "RuleID"},
	}
	c := scanCheck(t, s)
	d := &data{Files: []prFile{
		{Filename: ".gitleaksignore", Status: "modified", Patch: "@@ -10,2 +10,4 @@\n # keep\n+pkg/a.go:stripe-access-token:1\n+SECRET_IN_IGNORE\n # end"},
		{Filename: ".gitleaks.toml", Status: "modified", Patch: "@@ -1,1 +1,2 @@\n title = \"x\"\n+# pr edit"},
		// Colliding filename in the PR must not overwrite or collide with the neutral quarantine name.
		{Filename: "_preflight_quarantine_1.txt", Status: "added", Patch: "@@ -0,0 +1,1 @@\n+harmless"},
	}}
	if _, _, err := writeAdded(d, ws, true); err != nil {
		t.Fatal(err)
	}
	o := runScan(context.Background(), c, d, ws, io.Discard)
	if o.result != resultFail {
		t.Fatalf("outcome = %+v, want fail mapped back to .gitleaksignore:12", o)
	}
	if !strings.Contains(o.reason, ".gitleaksignore:12 fake-rule") {
		t.Fatalf("reason = %q, want .gitleaksignore:12 fake-rule", o.reason)
	}
	if strings.Contains(o.reason, "_preflight_quarantine_") {
		t.Fatalf("reason leaked internal quarantine name: %q", o.reason)
	}
	// Original ws.added must still have the real files untouched for sibling scans (e.g. trufflehog).
	if _, err := os.Stat(filepath.Join(ws.added, ".gitleaksignore")); err != nil {
		t.Fatalf("ws.added/.gitleaksignore was disturbed: %v", err)
	}
}

// Always runs: verifies the shipped gitleaks-config check emits a warning row when a PR adds or changes
// .gitleaksignore or .gitleaks.toml, and stays quiet on ordinary code changes.
func TestShippedGitleaksConfigWarningCheck(t *testing.T) {
	c := checkFromConfig(t, "gitleaks-config")
	r := builtinRules[c.Rule]
	clean := &data{Files: []prFile{{Filename: "pkg/a.go", Status: "modified", SHA: "s1"}}}
	if o := r.run(clean, params(c.With), nil); o.result != resultPass || len(o.warnings) != 0 {
		t.Fatalf("clean PR outcome = %+v, want pass with 0 warnings", o)
	}
	touched := &data{Files: []prFile{
		{Filename: ".gitleaksignore", Status: "added", SHA: "sha-ign"},
		{Filename: ".gitleaks.toml", Status: "modified", SHA: "sha-toml"},
	}}
	o := r.run(touched, params(c.With), nil)
	if o.result != resultPass || len(o.warnings) != 2 {
		t.Fatalf("gitleaks-config outcome = %+v, want pass with 2 warnings", o)
	}
	if o.warnings[0].File != ".gitleaksignore" || o.warnings[1].File != ".gitleaks.toml" {
		t.Fatalf("warning files = %+v", o.warnings)
	}
}

// Runs with the real gitleaks binary when available on PATH; skips otherwise.
// Proves:
// (1) an allowlisted ^-anchored path in main's .gitleaks.toml passes;
// (2) a secret in a non-allowlisted path still fails with relative path:head-line mapped through toHeadLines;
// (3) --gitleaks-ignore-path {main} honors main's .gitleaksignore using relative fingerprints;
// (4) a two-file PR adding a secret and a PR-owned .gitleaksignore suppressing it FAILS;
// (5) a secret written inside a PR's .gitleaksignore is FOUND and reported at .gitleaksignore:<head-line>;
// (6) a secret written inside a PR's .gitleaks.toml is FOUND and reported at .gitleaks.toml:<head-line>;
// (7) a PR-owned .gitleaks.toml with a blanket allowlist cannot suppress a secret in the PR.
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

	t.Run("PR-owned .gitleaksignore cannot suppress a secret in the PR", func(t *testing.T) {
		ws := newWorkspace(t, "")
		secretPatch := "@@ -40,2 +40,3 @@\n ctx\n+const token = \"" + syntheticSecret + "\"\n ctx"
		ignorePatch := "@@ -0,0 +1,1 @@\n+" + unlistedPath + ":stripe-access-token:1"
		d := &data{Files: []prFile{
			{Filename: unlistedPath, Status: "modified", Patch: secretPatch},
			{Filename: ".gitleaksignore", Status: "added", Patch: ignorePatch},
		}}
		if _, _, err := writeAdded(d, ws, true); err != nil {
			t.Fatal(err)
		}
		o := runScan(context.Background(), c, d, ws, io.Discard)
		if o.result != resultFail {
			t.Fatalf("PR-owned .gitleaksignore bypassed gitleaks scan: %+v", o)
		}
		if !strings.Contains(o.reason, unlistedPath+":41 ") {
			t.Fatalf("finding reason = %q, want %s:41", o.reason, unlistedPath)
		}
	})

	t.Run("secret written inside PR .gitleaksignore is found at .gitleaksignore:head-line", func(t *testing.T) {
		ws := newWorkspace(t, "")
		ignorePatch := "@@ -5,2 +5,4 @@\n # comment\n+pkg/other.go:rule:1\n+# leaked " + syntheticSecret + "\n # end"
		d := &data{Files: []prFile{{Filename: ".gitleaksignore", Status: "modified", Patch: ignorePatch}}}
		if _, _, err := writeAdded(d, ws, true); err != nil {
			t.Fatal(err)
		}
		o := runScan(context.Background(), c, d, ws, io.Discard)
		if o.result != resultFail {
			t.Fatalf("secret inside PR .gitleaksignore passed: %+v", o)
		}
		if !strings.Contains(o.reason, ".gitleaksignore:7 ") {
			t.Fatalf("finding reason = %q, want .gitleaksignore:7", o.reason)
		}
	})

	t.Run("secret written inside PR .gitleaks.toml is found at .gitleaks.toml:head-line", func(t *testing.T) {
		ws := newWorkspace(t, "")
		tomlPatch := "@@ -12,1 +12,3 @@\n # header\n+# comment\n+# key = \"" + syntheticSecret + "\""
		d := &data{Files: []prFile{{Filename: ".gitleaks.toml", Status: "modified", Patch: tomlPatch}}}
		if _, _, err := writeAdded(d, ws, true); err != nil {
			t.Fatal(err)
		}
		o := runScan(context.Background(), c, d, ws, io.Discard)
		if o.result != resultFail {
			t.Fatalf("secret inside PR .gitleaks.toml passed: %+v", o)
		}
		if !strings.Contains(o.reason, ".gitleaks.toml:14 ") {
			t.Fatalf("finding reason = %q, want .gitleaks.toml:14", o.reason)
		}
	})

	t.Run("PR-owned .gitleaks.toml cannot weaken main's config", func(t *testing.T) {
		ws := newWorkspace(t, "")
		secretPatch := "@@ -40,2 +40,3 @@\n ctx\n+const token = \"" + syntheticSecret + "\"\n ctx"
		tomlPatch := "@@ -0,0 +1,3 @@\n+[allowlist]\n+paths = [\".*\"]\n+regexes = [\".*\"]"
		d := &data{Files: []prFile{
			{Filename: unlistedPath, Status: "modified", Patch: secretPatch},
			{Filename: ".gitleaks.toml", Status: "modified", Patch: tomlPatch},
		}}
		if _, _, err := writeAdded(d, ws, true); err != nil {
			t.Fatal(err)
		}
		o := runScan(context.Background(), c, d, ws, io.Discard)
		if o.result != resultFail {
			t.Fatalf("PR-owned .gitleaks.toml bypassed gitleaks scan: %+v", o)
		}
		if !strings.Contains(o.reason, unlistedPath+":41 ") {
			t.Fatalf("finding reason = %q, want %s:41", o.reason, unlistedPath)
		}
	})
}
