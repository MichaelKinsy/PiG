package testenv

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/mod/modfile"
)

// Pi's realpath and resource discovery follow directory junctions (paths.ts:28-33, package-manager.ts:336-342). Module and workspace builds must both retain Node's classification; checking only a CI environment variable does not protect installed binaries.
func TestJunctionGodebugInModuleAndWorkspace(t *testing.T) {
	for _, name := range []string{"go.mod", "go.work"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var settings []*modfile.Godebug
			if name == "go.mod" {
				file, err := modfile.Parse(path, data, nil)
				if err != nil {
					t.Fatal(err)
				}
				settings = file.Godebug
			} else {
				file, err := modfile.ParseWork(path, data, nil)
				if err != nil {
					t.Fatal(err)
				}
				settings = file.Godebug
			}
			found := false
			for _, setting := range settings {
				if setting.Key == "winsymlink" {
					found = true
					if setting.Value != "0" {
						t.Errorf("%s: godebug winsymlink=%s; want 0 for Node junction semantics", name, setting.Value)
					}
				}
			}
			if !found {
				t.Errorf("%s must set godebug winsymlink=0 independently of the build environment", name)
			}
		})
	}
}
