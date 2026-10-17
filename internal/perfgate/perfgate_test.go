// SPDX-License-Identifier: MIT

package perfgate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestRatchetFileLoads(t *testing.T) {
	ratchet, err := Load(testenv.ModuleRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(ratchet.Ceilings) == 0 {
		t.Fatal("the ratchet file has no ceilings")
	}
}

func TestLoadRejectsNonFiniteAndNegativeCeilings(t *testing.T) {
	for _, value := range []string{"-1.0", "nan", "inf"} {
		root := t.TempDir()
		path := filepath.Join(root, RatchetPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("[ceilings]\n\"x.y\" = "+value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "x.y") {
			t.Errorf("ceiling %s: err = %v", value, err)
		}
	}
}

func TestFormat(t *testing.T) {
	for value, want := range map[float64]string{3: "3", 1_000_000: "1000000", 2.5: "2.50"} {
		if got := format(value); got != want {
			t.Errorf("format(%v) = %q, want %q", value, got, want)
		}
	}
}
