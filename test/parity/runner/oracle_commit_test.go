//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// oracleCommitMismatch accepts only the Pi build of the pinned commit: a
// published package cannot stand in for a commit pin, and a build stamp that
// names another commit never passes, though both report the same package version.
func TestOracleCommitMismatch(t *testing.T) {
	stamped := func(t *testing.T, commit string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".pig-pi-oracle.json"), []byte(`{"commit":"`+commit+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dir, "node_modules", ".bin", "pi")
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return bin
	}
	t.Run("the stamped pinned commit passes", func(t *testing.T) {
		if got := oracleCommitMismatch(stamped(t, coding.UpstreamCommit)); got != "" {
			t.Fatalf("rejected the pinned build: %s", got)
		}
	})
	t.Run("another commit's build is rejected", func(t *testing.T) {
		got := oracleCommitMismatch(stamped(t, strings.Repeat("0", 40)))
		if !strings.Contains(got, "want "+coding.UpstreamCommit) {
			t.Fatalf("accepted or mis-reported another commit's build: %q", got)
		}
	})
	t.Run("an unstamped (published) oracle passes only on an explicit opt-in for a release tag pin", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "pi")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PIG_PARITY_PI_PUBLISHED", "")
		if got := oracleCommitMismatch(bin); !strings.Contains(got, "published package") {
			t.Fatalf("an unstamped oracle passed without the opt-in: %q", got)
		}
		t.Setenv("PIG_PARITY_PI_PUBLISHED", "1")
		tagPin := coding.UpstreamVersion == coding.UpstreamPackageVersion
		if got := oracleCommitMismatch(bin); tagPin != (got == "") {
			t.Fatalf("tag pin %v, opted-in unstamped oracle verdict %q", tagPin, got)
		}
	})
}

// Without PIG_PARITY_PI_BIN a run takes only the source-built oracle linked into extensions/sdk-ts, however many mise
// installs of that version exist; the exact published mise versions are candidates only when the caller says so.
func TestUpstreamPiCandidates(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "extensions", "sdk-ts", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extensions", "sdk-ts", "package.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(root, "extensions", "sdk-ts", "sub")
	got := upstreamPiCandidates("/home/x", "1.1.0", true, cwd)
	want := filepath.Join(root, "extensions", "sdk-ts", "node_modules", ".bin", "pi")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("commit pin candidates = %v, want only %s", got, want)
	}
	for _, candidate := range upstreamPiCandidates("/home/x", "1.1.0", false, cwd) {
		if !strings.Contains(candidate, filepath.Join("mise", "installs")) || !strings.Contains(candidate, "1.1.0") {
			t.Fatalf("published candidate %q is not an exact mise install", candidate)
		}
	}
	t.Setenv("PIG_PARITY_PI_PUBLISHED", "")
	if publishedOracleAllowed() {
		t.Fatal("the published oracle is allowed without the opt-in")
	}
	if got := upstreamPiCandidates("/home/x", "1.1.0", true, t.TempDir()); len(got) != 0 {
		t.Fatalf("a tree without extensions/sdk-ts yielded %v", got)
	}
}
