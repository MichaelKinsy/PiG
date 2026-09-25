package runtimecell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
)

// writeLegacySDKFactory writes a factory module shaped like a Pig 0.84
// extension: it imports the legacy SDK module path and replaces it with a
// sibling SDK directory, which here is an old SDK whose API cannot build the
// factory. Only aliasing the legacy path to the current SDK can compile it.
func writeLegacySDKFactory(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	oldSDK := filepath.Join(root, ".sdk")
	extensionRoot := filepath.Join(root, "ask")
	for path, content := range map[string]string{
		filepath.Join(oldSDK, "go.mod"):        "module " + extsource.LegacyGoSDKModulePath + "\n\ngo 1.26\n",
		filepath.Join(oldSDK, "sdk.go"):        "package sdk\n\ntype Extension struct{}\n\nfunc New(string) *Extension { return &Extension{} }\n",
		filepath.Join(extensionRoot, "go.mod"): "module example.com/legacy-ask\n\ngo 1.26\n\nrequire " + extsource.LegacyGoSDKModulePath + " v0.0.0\n\nreplace " + extsource.LegacyGoSDKModulePath + " => ../.sdk\n",
		filepath.Join(extensionRoot, "main.go"): `package ask

import "github.com/mainstai/pig/extensions/sdk"

func Extension() *sdk.Extension {
	ext := sdk.New("legacy-ask")
	ext.Command("ask", "legacy ask", func(sdk.Context, string) error { return nil })
	return ext
}
`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return extensionRoot
}

func TestRenderGoModAliasesLegacySDKToCurrentSDK(t *testing.T) {
	extensionRoot := writeLegacySDKFactory(t)
	sdkRoot := filepath.Join(t.TempDir(), "current-sdk")
	result := renderGoMod([]GoExtension{{Name: "legacy-ask", Root: extensionRoot, ModulePath: "example.com/legacy-ask", Package: "example.com/legacy-ask", Factory: "Extension"}}, sdkRoot)
	for _, want := range []string{
		"require " + extsource.LegacyGoSDKModulePath + " v0.0.0 // indirect\n",
		"replace " + extsource.LegacyGoSDKModulePath + " => ./" + legacyGoSDKDir + "\n",
		"replace " + extsource.GoSDKModulePath + " => " + modfile.AutoQuote(filepath.ToSlash(sdkRoot)) + "\n",
	} {
		if strings.Count(result, want) != 1 {
			t.Fatalf("generated go.mod does not contain %q exactly once:\n%s", want, result)
		}
	}
	if count := strings.Count(result, extsource.LegacyGoSDKModulePath); count != 2 {
		t.Fatalf("legacy SDK lines = %d, want only the generated require and replace:\n%s", count, result)
	}
	if strings.Contains(result, ".sdk") {
		t.Fatalf("generated go.mod kept the extension's legacy SDK replace:\n%s", result)
	}

	current := t.TempDir()
	if err := os.WriteFile(filepath.Join(current, "go.mod"), []byte("module example.com/current\n\ngo 1.26\n\nrequire "+extsource.GoSDKModulePath+" v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result := renderGoMod([]GoExtension{{Name: "current", Root: current, ModulePath: "example.com/current"}}, sdkRoot); strings.Contains(result, extsource.LegacyGoSDKModulePath) {
		t.Fatalf("current-SDK extension gained a legacy SDK alias:\n%s", result)
	}
}

func TestGoPackedCellHashCoversLegacySDKAlias(t *testing.T) {
	sdkRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sdkRoot, "go.mod"), []byte("module "+extsource.GoSDKModulePath+"\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ext := []GoExtension{{Name: "ask", Root: root, ModulePath: "example.com/ask", Package: "example.com/ask", Factory: "Extension", Hash: "same-source"}}
	write := func(sdkPath string) string {
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/ask\n\ngo 1.26\n\nrequire "+sdkPath+" v0.0.0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return goPackedCellHash(t.TempDir(), "cell", ext, sdkRoot)
	}
	current, legacy := write(extsource.GoSDKModulePath), write(extsource.LegacyGoSDKModulePath)
	if current == legacy || strings.HasPrefix(current, "error:") {
		t.Fatalf("hash current=%q legacy=%q; the generated legacy alias must be a cell input", current, legacy)
	}
}

func TestBuildGoPackedCellBuildsLegacySDKFactoryAgainstCurrentSDK(t *testing.T) {
	currentSDK, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PIG_HOME", filepath.Join(home, ".pig"))
	t.Setenv("PIG_SDK_GO_ROOT", currentSDK)
	extensionRoot := writeLegacySDKFactory(t)
	cell, err := BuildGoPackedCell(context.Background(), t.TempDir(), "legacy-ask", []GoExtension{{
		Name: "legacy-ask", Root: extensionRoot, ModulePath: "example.com/legacy-ask", Package: "example.com/legacy-ask", Factory: "Extension", Hash: "legacy",
	}})
	if err != nil {
		t.Fatalf("build legacy-SDK factory: %v", err)
	}
	if _, err := os.Stat(cell.BinaryPath); err != nil {
		t.Fatalf("packed runner missing: %v", err)
	}
}
