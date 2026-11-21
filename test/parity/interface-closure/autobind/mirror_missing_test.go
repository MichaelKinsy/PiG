package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
