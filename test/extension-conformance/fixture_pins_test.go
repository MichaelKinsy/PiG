package extensionconformance

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/mod/modfile"
)

// TestInTreeGoModulesPinTheRepositorySDKVersion keeps every in-tree Go module that replaces the PiG or SDK module with local source on the version the root module requires. A stale fixture pin makes the generated packed-cell go.mod select an older SDK than the replaced root module needs, and `go build` stops with "updates to go.mod needed".
func TestInTreeGoModulesPinTheRepositorySDKVersion(t *testing.T) {
	const sdkPath = "github.com/MichaelKinsy/PiG/extensions/sdk"
	const rootPath = "github.com/MichaelKinsy/PiG"
	root := fixtureSourceRoot
	pinned := func(file *modfile.File, path string) string {
		for _, requirement := range file.Require {
			if requirement.Mod.Path == path {
				return requirement.Mod.Version
			}
		}
		return ""
	}
	parse := func(path string) *modfile.File {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := modfile.Parse(path, data, nil)
		if err != nil {
			t.Fatal(err)
		}
		return file
	}
	want := pinned(parse(filepath.Join(root, "go.mod")), sdkPath)
	if want == "" {
		t.Fatalf("root go.mod does not require %s", sdkPath)
	}
	var checked []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".upstream", "node_modules", "target":
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "go.mod" || path == filepath.Join(root, "go.mod") {
			return nil
		}
		file := parse(path)
		replaced := false
		for _, replacement := range file.Replace {
			if replacement.Old.Path == sdkPath || replacement.Old.Path == rootPath {
				replaced = true
			}
		}
		if !replaced {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		checked = append(checked, rel)
		for _, module := range []string{sdkPath, rootPath} {
			if got := pinned(file, module); got != "" && got != want {
				t.Errorf("%s requires %s %s, want the root module's %s (set-version -release-modules pins every in-tree module)", rel, module, got, want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"test/extension-conformance/testdata/extension-api-go/go.mod", "test/extension-conformance/testdata/model-types-go/go.mod"} {
		if !slices.Contains(checked, filepath.FromSlash(rel)) {
			t.Errorf("%s was not checked", rel)
		}
	}
}
