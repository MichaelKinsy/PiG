package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// A read-only run (-check, ledger-check, ledger-frontier, -closed-check) derives rows from Pi's source under .upstream/current.
// When the mirror is absent it cannot derive anything and must fail immediately naming the missing mirror and `make upstream-mirror`,
// with no gap table printed.
func TestReadOnlyRunsFailFastWithoutMirror(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"check without mirror", []string{"-check"}},
		{"default report without mirror", nil},
		{"closed-check without mirror", []string{"-closed-check"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, reach := mirrorFixture(t)
			if err := os.RemoveAll(filepath.Join(root, ".upstream")); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"-root", root, "-reach", reach, "-out", filepath.Join(root, "build/interface-gaps")}, c.args...)
			code, out := runAutobind(t, args...)
			if code == 0 {
				t.Fatalf("autobind %v exited 0 without a mirror; output:\n%s", c.args, out)
			}
			if !strings.Contains(out, "make upstream-mirror") {
				t.Errorf("output does not name `make upstream-mirror`; output:\n%s", out)
			}
			if !strings.Contains(out, ".upstream") {
				t.Errorf("output does not name the missing mirror path; output:\n%s", out)
			}
			if strings.Contains(out, "| reason |") || strings.Contains(out, "total IDs:") {
				t.Errorf("output printed a gap table despite missing mirror; output:\n%s", out)
			}
		})
	}
}

// A read-only run over another Pi's mirror is the same failure class as no mirror: deriving against the wrong Pi produces a gap
// table with false reasons. It must fail fast with upstreammirror.Check's sentence and print no gap table.
func TestReadOnlyRunsFailFastOverWrongMirror(t *testing.T) {
	otherCommit := strings.Repeat("3", 40)
	cases := []struct {
		name     string
		mirror   func(t *testing.T, root string)
		args     []string
		wantText string
	}{
		{"check over another Pi version", func(t *testing.T, root string) {
			writeMirror(t, root, "1.0.3", "v1.0.3", otherCommit, "1.0.3")
		}, []string{"-check"}, "is Pi 1.0.3"},
		{"check over pinned version at another commit", func(t *testing.T, root string) {
			writeMirror(t, root, pigversion.UpstreamVersion, pigversion.UpstreamRef, otherCommit, pigversion.UpstreamPackageVersion)
		}, []string{"-check"}, otherCommit},
		{"default report over another Pi version", func(t *testing.T, root string) {
			writeMirror(t, root, "1.0.3", "v1.0.3", otherCommit, "1.0.3")
		}, nil, "is Pi 1.0.3"},
		{"closed-check over another Pi version", func(t *testing.T, root string) {
			writeMirror(t, root, "1.0.3", "v1.0.3", otherCommit, "1.0.3")
		}, []string{"-closed-check"}, "is Pi 1.0.3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, reach := mirrorFixture(t)
			c.mirror(t, root)
			args := append([]string{"-root", root, "-reach", reach, "-out", filepath.Join(root, "build/interface-gaps")}, c.args...)
			code, out := runAutobind(t, args...)
			if code == 0 {
				t.Fatalf("autobind %v exited 0 over another Pi's mirror; output:\n%s", c.args, out)
			}
			if !strings.Contains(out, c.wantText) {
				t.Errorf("output does not name the wrong mirror %q; output:\n%s", c.wantText, out)
			}
			if !strings.Contains(out, "make upstream-mirror") {
				t.Errorf("output does not name `make upstream-mirror`; output:\n%s", out)
			}
			if !strings.Contains(out, ".upstream") {
				t.Errorf("output does not name the mirror path; output:\n%s", out)
			}
			if strings.Contains(out, "| reason |") || strings.Contains(out, "total IDs:") {
				t.Errorf("output printed a gap table despite wrong mirror; output:\n%s", out)
			}
		})
	}
}
