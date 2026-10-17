package pigdocs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Prepare writes nothing: a missing bundle is named when the config root lets this process create it and stays pending until EnsureSynced runs.
func TestPrepareDefersTheWriteOfAMissingBundle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "pig")
	present, pending, err := Prepare(root)
	if err != nil || !present || !pending {
		t.Fatalf("Prepare(fresh root) = present %v, pending %v, err %v; want present and pending", present, pending, err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("Prepare created %s (err %v)", root, err)
	}
	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	present, pending, err = Prepare(root)
	if err != nil || !present || pending {
		t.Fatalf("Prepare(synced root) = present %v, pending %v, err %v; want present, nothing pending", present, pending, err)
	}
}

// A config root this user cannot create the bundle in names no bundle, the outcome EnsureSynced's failed write had at every start.
func TestPrepareNamesNoBundleItCannotWrite(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop this user from writing")
	}
	for _, test := range []struct {
		name string
		// root returns a config root whose nearest existing ancestor is read-only.
		root func(t *testing.T) string
	}{
		{"root exists", func(t *testing.T) string {
			root := t.TempDir()
			readOnly(t, root)
			return root
		}},
		{"only an ancestor exists", func(t *testing.T) string {
			parent := t.TempDir()
			readOnly(t, parent)
			return filepath.Join(parent, "pig", "nested")
		}},
		{"docs exists", func(t *testing.T) string {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, SubDir), 0o755); err != nil {
				t.Fatal(err)
			}
			readOnly(t, filepath.Join(root, SubDir))
			return root
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := test.root(t)
			present, _, err := Prepare(root)
			if err != nil || present {
				t.Fatalf("Prepare(read-only) = present %v, err %v; want absent", present, err)
			}
			if EnsureSynced(root) == nil {
				t.Fatal("EnsureSynced wrote a bundle Prepare said it could not create")
			}
		})
	}
}

func readOnly(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
}
