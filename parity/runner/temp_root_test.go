//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi's loadProjectContextFiles walks every ancestor, even above a git root.
// These reproduce the review's checkout-TMPDIR overlay through the shared
// snapshot path, including aliases whose spelling is outside the checkout.
func TestSnapshotCWDRejectsCheckoutTempRoot(t *testing.T) {
	checkout, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	nested, err := os.MkdirTemp(checkout, "parity-boundary-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(nested) })
	alias := filepath.Join(t.TempDir(), "checkout-link")
	if err := os.Symlink(nested, alias); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{checkout, nested, alias} {
		t.Run(filepath.Base(root), func(t *testing.T) {
			t.Setenv("TMPDIR", root)
			_, err := defaultCWD(t)
			if err == nil || !strings.Contains(err.Error(), "checkout") || !strings.Contains(err.Error(), "TMPDIR") {
				t.Fatalf("defaultCWD with TMPDIR=%s: want actionable checkout rejection, got %v", root, err)
			}
		})
	}
}

func TestSnapshotCWDRejectsAncestorContext(t *testing.T) {
	// Independently copied from resource-loader.ts:loadContextFileFromDir.
	for _, name := range []string{"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"} {
		t.Run(name, func(t *testing.T) {
			ancestor := t.TempDir()
			contextFile := filepath.Join(ancestor, name)
			if err := os.WriteFile(contextFile, []byte("ANCESTOR_CONTEXT_SENTINEL\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			canonicalAncestor, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				t.Fatal(err)
			}
			canonicalContextFile := filepath.Join(canonicalAncestor, name)
			root := filepath.Join(ancestor, "nested", "temp")
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(t.TempDir(), "temp-link")
			if err := os.Symlink(root, alias); err != nil {
				t.Fatal(err)
			}
			for _, temp := range []string{ancestor, root, alias} {
				t.Run(filepath.Base(temp), func(t *testing.T) {
					t.Setenv("TMPDIR", temp)
					_, err := snapshotCWD(t, defaultCWDFixture())
					errorText := ""
					if err != nil {
						errorText = strings.ToLower(err.Error())
					}
					if err == nil || !strings.Contains(errorText, strings.ToLower(canonicalContextFile)) || !strings.Contains(err.Error(), "TMPDIR") {
						t.Fatalf("snapshotCWD: want actionable ancestor-context rejection naming %s, got %v", canonicalContextFile, err)
					}
				})
			}
		})
	}
}

func TestSnapshotCWDCanonicalCleanRootAndCleanup(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "temp-link")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", alias)
	var snapshot string
	t.Run("copy", func(t *testing.T) {
		var err error
		snapshot, err = defaultCWD(t)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := filepath.EvalSymlinks(snapshot)
		if err != nil || snapshot != canonical {
			t.Fatalf("snapshot = %s, canonical = %s, err = %v", snapshot, canonical, err)
		}
		want, err := os.ReadFile(filepath.Join(defaultCWDFixture(), "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(snapshot, "AGENTS.md"))
		if err != nil || string(got) != string(want) {
			t.Fatalf("fixture context changed: %q, %v", got, err)
		}
	})
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("snapshot survived cleanup: %v", err)
	}
}

func TestDriversRejectAncestorContextBeforeLaunch(t *testing.T) {
	installFakeHT(t)
	ancestor := t.TempDir()
	if err := os.WriteFile(filepath.Join(ancestor, "AGENTS.md"), []byte("ANCESTOR_CONTEXT_SENTINEL\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(ancestor, "temp")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	for name, driver := range DriverRegistry {
		for _, label := range []string{"pig", "pi"} {
			t.Run(name+"/"+label, func(t *testing.T) {
				sc := &Scenario{Name: "reject-context", SourcePath: filepath.Join(defaultCWDFixture(), "scenario.toml")}
				sc.Tmux.Width, sc.Tmux.Height = 100, 35
				sc.ExtensionHost.Sources = []string{"unused"}
				// A missing executable must never be reached. With the broken
				// harness, process drivers return a launch error instead.
				bin := BinaryRef{Label: label, Path: filepath.Join(root, "must-not-launch")}
				got := driver.Run(t.Context(), t, bin, sc)
				if name == "extension-host" && label == "pi" {
					// This driver has no Pi subprocess by design.
					if got.Err != nil {
						t.Fatal(got.Err)
					}
					return
				}
				if got.Err == nil || !strings.Contains(got.Err.Error(), "AGENTS.md") || !strings.Contains(got.Err.Error(), "TMPDIR") {
					t.Fatalf("driver must reject context before attempting launch, got %v", got.Err)
				}
			})
		}
	}
}
