// SPDX-License-Identifier: MIT

package runtimecell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A local replacement changes the source location, not the required version.
// Go's readonly module build must retain the maximum declared requirement.
func TestBuildGoPackedCellKeepsRequiredWorkspaceVersions(t *testing.T) {
	root := t.TempDir()
	sdkRoot, err := filepath.Abs("../../../../extensions/sdk")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SDK_GO_ROOT", sdkRoot)
	write := func(path, source string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	shared := filepath.Join(root, "shared")
	write(filepath.Join(shared, "go.mod"), "module example.com/shared\n\ngo 1.26.0\n")
	write(filepath.Join(shared, "shared.go"), "package shared\nconst Name = \"workspace\"\n")
	var extensions []GoExtension
	for i, version := range []string{"v1.9.0", "v1.10.0"} {
		name := fmt.Sprintf("ext%d", i)
		dir := filepath.Join(root, name)
		modulePath := "example.com/" + name
		write(filepath.Join(dir, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26.0\n\nrequire (\n github.com/MichaelKinsy/PiG/extensions/sdk v0.3.0\n example.com/shared %s\n)\n", modulePath, version))
		write(filepath.Join(dir, "extension.go"), "package extension\nimport (\n sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n \"example.com/shared\"\n)\nfunc Extension() *sdk.Extension { return sdk.New(shared.Name) }\n")
		write(filepath.Join(dir, "go.sum"), fmt.Sprintf("example.com/checksum v1.%d.0 h1:fixture\n", i))
		extensions = append(extensions, GoExtension{Name: name, Root: dir, ModulePath: modulePath, Package: modulePath, Factory: "Extension", Hash: name, WorkspaceModules: []string{shared}})
	}
	check := func(t *testing.T) {
		sums := mergeGoSum(extensions)
		for i := range extensions {
			if want := fmt.Sprintf("example.com/checksum v1.%d.0 h1:fixture\n", i); !strings.Contains(sums, want) {
				t.Errorf("workspace checksum missing: %s", want)
			}
		}
		mod := renderGoMod(extensions, "/sdk")
		for _, want := range []string{"require example.com/shared v1.10.0\n", "require github.com/MichaelKinsy/PiG/extensions/sdk v0.3.0 // indirect\n"} {
			if !strings.Contains(mod, want) {
				t.Errorf("local replacement lost the maximum required version %q:\n%s", want, mod)
			}
		}
		cell, err := BuildGoPackedCell(t.Context(), t.TempDir(), "workspace-versions", extensions)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(cell.BinaryPath); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("extension-roots", check)
	workspace := []string{extensions[0].Root, extensions[1].Root}
	for i := range extensions {
		extensions[i].Root = shared
		extensions[i].ModulePath = "example.com/shared"
		extensions[i].WorkspaceModules = workspace
	}
	t.Run("workspace-roots", check)
}

func BenchmarkRenderGoModPorterWorkspace(b *testing.B) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		b.Fatal(err)
	}
	extensions := []GoExtension{{
		Name: "pig-porter", Root: root, ModulePath: "github.com/MichaelKinsy/PiG",
		Package:          "github.com/MichaelKinsy/PiG/piglets/porter/extensions/pig-porter",
		WorkspaceModules: []string{filepath.Join(root, "piglets", "porter", "extensions", "pig-porter")},
	}}
	sdkRoot := filepath.Join(root, "extensions", "sdk")
	b.ReportAllocs()
	for b.Loop() {
		renderGoMod(extensions, sdkRoot)
	}
}
