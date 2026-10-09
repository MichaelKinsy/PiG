//go:build unix

package evals

// Tests for docker/entrypoint.ts. entrypoint.ts has no upstream test; the cases derive from reading it. The success
// path needs root and an image, and was run in the built evaluator image (see the gap-closure ledger).

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigdocs"
)

func TestParseEntrypointArgsReadsWhatTheHostPasses(t *testing.T) {
	discover, selection, err := ParseEntrypointArgs([]string{"--discover", "--project", "docs", "evals/a.docs.eval.go", "evals/b.docs.eval.go", "-t", "^A b$"})
	if err != nil || !discover || selection.Project != EvalProjectDocs || !reflect.DeepEqual(selection.Files, []string{"evals/a.docs.eval.go", "evals/b.docs.eval.go"}) ||
		selection.NamePattern == nil || selection.NamePattern.String() != "^A b$" {
		t.Fatalf("discover = %v, selection = %+v, err = %v", discover, selection, err)
	}
	// The host escapes a case name with docker.ts runTask's pattern; the filter must match exactly that name.
	_, selection, err = ParseEntrypointArgs([]string{"--project", "docs", "evals/a.docs.eval.go", "--testNamePattern", `^A \(b\) c\.d$`})
	if err != nil || !selection.NamePattern.MatchString("A (b) c.d") || selection.NamePattern.MatchString("A (b) cxd") {
		t.Fatalf("escaped pattern: %+v, %v", selection, err)
	}
	for args, want := range map[string]string{
		"--project":                          "Missing value for --project.",
		"-t":                                 "Missing value for -t.",
		"--bogus":                            "Unsupported container argument: --bogus",
		"evals/a.go":                         "Unsupported container argument: evals/a.go",
		"-t \"(\"":                           "error parsing regexp",
		"--project docs --testNamePattern=(": "error parsing regexp",
	} {
		fields := strings.Fields(args)
		for i := range fields {
			fields[i] = strings.Trim(fields[i], `"`)
		}
		if _, _, err := ParseEntrypointArgs(fields); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseEntrypointArgs(%v) error = %v, want %q", fields, err, want)
		}
	}
}

func TestRunEntrypointRefusesAnUnsafeImage(t *testing.T) {
	directory := t.TempDir()
	runner := filepath.Join(directory, "pig-evals")
	for _, name := range []string{"pig-evals", "pig", "pig-eval-extension"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	config := EntrypointConfig{Runner: runner, Binaries: []string{filepath.Join(directory, "pig"), filepath.Join(directory, "pig-eval-extension")},
		HostAgentDir: filepath.Join(directory, "agent"), ArtifactDir: directory}
	cases := []struct {
		name     string
		uid, gid string
		variant  string
		mutate   func()
		want     string
	}{
		{"bad uid", "0", "65532", "with_docs", nil, "PI_EVAL_SANDBOX_UID must be a positive integer."},
		{"bad gid", "65532", "abc", "with_docs", nil, "PI_EVAL_SANDBOX_GID must be a positive integer."},
		{"unknown variant", "65532", "65532", "other", nil, "Eval image has no valid PI_EVAL_VARIANT."},
		{"missing binary", "65532", "65532", "with_docs", func() { _ = os.Remove(filepath.Join(directory, "pig")) }, "Installed binary is missing or not executable: " + filepath.Join(directory, "pig")},
		{"evaluator readable beyond root", "65532", "65532", "with_docs", func() { _ = os.WriteFile(filepath.Join(directory, "pig"), []byte("#!/bin/sh\n"), 0o755) }, "Evaluator source must be readable only by root: " + runner},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("PI_EVAL_SANDBOX_UID", c.uid)
			t.Setenv("PI_EVAL_SANDBOX_GID", c.gid)
			t.Setenv("PI_EVAL_VARIANT", c.variant)
			if c.mutate != nil {
				c.mutate()
			}
			if _, err := RunEntrypoint(t.Context(), config, []string{"--discover", "evals/a.docs.eval.go"}, os.Stdout); err == nil || err.Error() != c.want {
				t.Errorf("error = %v, want %q", err, c.want)
			}
		})
	}
}

// TestCopyAuthFileKeepsTheSourceMode pins entrypoint.ts:109-110: the credential is copied only when the mount exists,
// and copyFileSync keeps the source's mode rather than applying the umask to 0666.
func TestCopyAuthFileKeepsTheSourceMode(t *testing.T) {
	directory := t.TempDir()
	source, target := filepath.Join(directory, "source.json"), filepath.Join(directory, "auth.json")
	if err := copyAuthFile(source, target); err != nil {
		t.Fatalf("missing source: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("a missing source produced a copy: %v", err)
	}
	// 0666 checks that the umask does not apply to the copy.
	for _, mode := range []os.FileMode{0o600, 0o666} {
		if err := os.WriteFile(source, []byte(`{"p":{}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(source, mode); err != nil {
			t.Fatal(err)
		}
		if err := copyAuthFile(source, target); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(target)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("copy mode = %v, %v; want %v", info.Mode().Perm(), err, mode)
		}
		if data, _ := os.ReadFile(target); string(data) != `{"p":{}}` {
			t.Errorf("copy = %q", data)
		}
	}
}

// TestAssertRootOnlyRejectsGroupOrOtherAccess pins entrypoint.ts:73-78: a root-owned path with any group or other
// permission bit is not root-only. "/" is root-owned and world-readable on every Unix host.
func TestAssertRootOnlyRejectsGroupOrOtherAccess(t *testing.T) {
	info, err := os.Stat("/")
	if err != nil || info.Mode().Perm()&0o077 == 0 {
		t.Skipf("/ is not world-accessible here: %v", err)
	}
	if err := assertRootOnly("/"); err == nil || err.Error() != "Evaluator source must be readable only by root: /" {
		t.Errorf("assertRootOnly(/) = %v", err)
	}
}

// TestAssertDocumentationFollowsTheVariant is entrypoint.ts:48-62: without_docs leaves no documentation on disk and
// with_docs leaves the required, non-empty pages.
func TestAssertDocumentationFollowsTheVariant(t *testing.T) {
	for _, tc := range []struct {
		variant string
		wantErr string
	}{
		{string(DocumentationVariantWithDocs), ""},
		{string(DocumentationVariantWithoutDocs), ""},
		{"other", "Eval image has no valid PI_EVAL_VARIANT."},
		{"", "Eval image has no valid PI_EVAL_VARIANT."},
	} {
		t.Run(tc.variant, func(t *testing.T) {
			err := assertDocumentation(tc.variant)
			if (err == nil) != (tc.wantErr == "") || (err != nil && err.Error() != tc.wantErr) {
				t.Fatalf("assertDocumentation(%q) = %v, want %q", tc.variant, err, tc.wantErr)
			}
		})
	}
}

// TestRemovePigDocumentationRemovesEveryNameEntrypointChecks pins the removal against the names the without_docs
// check requires absent, including the ones pig itself never materializes.
func TestRemovePigDocumentationRemovesEveryNameEntrypointChecks(t *testing.T) {
	root := t.TempDir()
	if _, err := pigdocs.Sync(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", "CHANGELOG.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "examples", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(root, "auth.json")
	if err := os.WriteFile(keep, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removePigDocumentation(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", "CHANGELOG.md", "docs", "examples"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			t.Errorf("%s survived the removal", name)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("the removal deleted an unrelated file: %v", err)
	}
	if err := removePigDocumentation(root); err != nil {
		t.Errorf("removing absent documentation failed: %v", err)
	}
}
