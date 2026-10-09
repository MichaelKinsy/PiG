package cli

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/tui"
)

// package-manager.ts resolveLocalExtensionSource (:1364-1393): a local package source that is a file is one extension (baseDir is its directory, no packageRoot); a directory is a package whose baseDir and packageRoot are the directory.
func TestLocalPackageSourceThatIsAFileLoadsAsOneExtension(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "solo.ts")
	writePackageResource(t, file, "export default function() {}")
	cwd, agent := t.TempDir(), t.TempDir()
	settings := codingagent.NewSettingsManager(cwd, agent)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: file}}); err != nil {
		t.Fatal(err)
	}
	items, err := packagemanager.CollectResolvedPackageResourceItems(cwd, settings.AgentDir(), settings, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Path != file || items[0].ResourceType != tui.ResourceExtensions || !items[0].Enabled {
		t.Fatalf("single-file package source resolved to %+v, want the file as one enabled extension", items)
	}
	if items[0].BaseDir != root {
		t.Fatalf("baseDir=%q, want the file's directory %q (package-manager.ts:1375)", items[0].BaseDir, root)
	}
	if items[0].PackageRoot != "" {
		t.Fatalf("packageRoot=%q, want none for a single file (package-manager.ts:1375-1376)", items[0].PackageRoot)
	}
}

func TestLocalPackageSourceThatIsADirectorySetsPackageRootToBaseDir(t *testing.T) {
	root := t.TempDir()
	writePackageResource(t, filepath.Join(root, "extensions", "a.ts"), "export default function() {}")
	cwd, agent := t.TempDir(), t.TempDir()
	settings := codingagent.NewSettingsManager(cwd, agent)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: root}}); err != nil {
		t.Fatal(err)
	}
	items, err := packagemanager.CollectResolvedPackageResourceItems(cwd, settings.AgentDir(), settings, nil, false)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if items[0].BaseDir != root || items[0].PackageRoot != root {
		t.Fatalf("baseDir=%q packageRoot=%q, want both %q (package-manager.ts:1380-1381)", items[0].BaseDir, items[0].PackageRoot, root)
	}
}

// resource-loader.ts collectExtensionPackageWarnings (:66-93) reads package.json only from PathMetadata.packageRoot, which package-manager.ts leaves unset for a single-file local source whose baseDir is the file's directory.
func TestExtensionPackageWarningsSkipASingleFileLocalSource(t *testing.T) {
	root := t.TempDir()
	writeResourceTestFiles(t, root, map[string]string{"package.json": `{"dependencies":{"typebox":"1"}}`, "solo.ts": "export default function() {}"})
	file := filepath.Join(root, "solo.ts")
	single := []subprocess.ExtConfig{{Source: file, SourceInfo: codingagent.PiSourceInfo{Path: file, Source: file, Scope: "user", Origin: "package", BaseDir: root}}}
	if got, err := extensionPackageWarnings(single); err != nil || len(got) != 0 {
		t.Fatalf("single-file source warned: %+v err=%v", got, err)
	}
	pkg := packageExtensionConfigsFixture(root)
	got, err := extensionPackageWarnings(pkg)
	if err != nil || len(got) != 1 || got[0].Path != filepath.Join(root, "package.json") {
		t.Fatalf("directory package warnings=%+v err=%v, want one for its package.json", got, err)
	}
}
