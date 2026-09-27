package generate_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run the actual Make recipe against a tiny generator fixture. This checks failure status and the contributor's repair command without changing the tree.
func TestGoDriftGateNamesRepairCommand(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	put := func(name, body string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(fixture, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	put("bin/go", "#!/bin/sh\nprintf 'fresh\\n' > \"$4\"\n", 0o700)
	comparator := filepath.Join(root, "automation/ci/check-generated.sh")
	data, err := os.ReadFile(comparator)
	if err != nil {
		t.Fatal(err)
	}
	put("automation/ci/check-generated.sh", string(data), 0o700)
	for _, state := range []string{"stale", "fresh", "missing"} {
		t.Run(state, func(t *testing.T) {
			if state == "missing" {
				if err := os.Remove(filepath.Join(fixture, "parity/interfaces/pig-go.json")); err != nil {
					t.Fatal(err)
				}
			} else {
				put("parity/interfaces/pig-go.json", state+"\n", 0o600)
			}
			cmd := exec.CommandContext(t.Context(), "make", "--no-print-directory", "-f", filepath.Join(root, "automation/make/parity.mk"), "interface-go-drift", "MKTEMP=mktemp "+filepath.Join(fixture, "generated.XXXXXXXX"))
			cmd.Dir = fixture
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(fixture, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := cmd.CombinedOutput()
			if state == "fresh" {
				if err != nil {
					t.Fatalf("clean gate failed: %v\n%s", err, output)
				}
				return
			}
			if err == nil {
				t.Fatal("stale inventory passed")
			}
			for _, want := range []string{"run: make generate", "make interface-go", "parity/interfaces/pig-go.json"} {
				if !strings.Contains(string(output), want) {
					t.Errorf("missing %q in failure:\n%s", want, output)
				}
			}
		})
	}
}
