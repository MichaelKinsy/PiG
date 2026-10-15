package packagemanager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// packages/coding-agent/src/core/package-manager.ts:1360-1376 resolveLocalExtensionSource: a local Package source that is a file is
// itself an extension resource (addResource(accumulator.extensions, resolved, metadata, true)) whose base directory is the file's
// directory.
func TestLocalFilePackageSourceIsAnExtensionResource(t *testing.T) {
	cwd, sm := trustedSettings(t)
	extension := filepath.Join(cwd, "single", "hello.ts")
	if err := os.MkdirAll(filepath.Dir(extension), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extension, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: extension}}); err != nil {
		t.Fatal(err)
	}
	items, err := CollectResolvedPackageResourceItems(cwd, sm.AgentDir(), sm, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var found int
	for _, item := range items {
		if item.Path == extension {
			found++
			if string(item.ResourceType) != "extensions" || !item.Enabled || item.BaseDir != filepath.Dir(extension) || item.PackageRoot != "" {
				t.Fatalf("item = %+v, want an enabled extension with base dir %s and no package root", item, filepath.Dir(extension))
			}
		}
	}
	if found != 1 {
		t.Fatalf("items = %+v, want exactly one extension for %s", items, extension)
	}
}

// package-manager.ts:1360-1376: a local Package source that is a directory is a package root (metadata.packageRoot = resolved).
func TestLocalDirectoryPackageSourceHasAPackageRoot(t *testing.T) {
	cwd, sm := trustedSettings(t)
	root := filepath.Join(cwd, "pkg")
	if err := os.MkdirAll(filepath.Join(root, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "extensions", "hello.ts")
	if err := os.WriteFile(entry, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: root}}); err != nil {
		t.Fatal(err)
	}
	items, err := CollectResolvedPackageResourceItems(cwd, sm.AgentDir(), sm, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Path == entry {
			if item.PackageRoot != root || item.BaseDir != root {
				t.Fatalf("item = %+v, want package root and base dir %s", item, root)
			}
			return
		}
	}
	t.Fatalf("items = %+v, want the extension %s", items, entry)
}

// package-manager.ts:1377-1384 resolveLocalExtensionSource: a local Package directory that collectPackageResources reports as
// describing no resources (no filter, no "pi" manifest, no conventional resource directory, :2214-2262) is itself one enabled
// extension whose base directory and package root are the directory, as `pi install ./my-extension` loads it.
func TestLocalDirectoryPackageSourceWithoutResourcesIsItselfAnExtension(t *testing.T) {
	cwd, sm := trustedSettings(t)
	root := filepath.Join(cwd, "notes")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.mjs"), []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: root}}); err != nil {
		t.Fatal(err)
	}
	items, err := CollectResolvedPackageResourceItems(cwd, sm.AgentDir(), sm, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v, want exactly the directory as one extension", items)
	}
	item := items[0]
	if item.Path != root || string(item.ResourceType) != "extensions" || !item.Enabled || item.BaseDir != root || item.PackageRoot != root || item.Origin != "package" || item.Source != root {
		t.Fatalf("item = %+v, want an enabled package extension at %s with base dir and package root %[2]s", item, root)
	}
}

// package-manager.ts:2214-2262 collectPackageResources returns true for an object-form filter, a "pi" manifest, or any
// conventional resource directory, even an empty one; resolveLocalExtensionSource then does not add the directory itself.
func TestLocalDirectoryPackageSourceThatDescribesResourcesIsNotItselfAnExtension(t *testing.T) {
	cases := map[string]struct {
		source codingagent.PackageSource
		setup  func(t *testing.T, root string)
	}{
		"object-form filter": {source: codingagent.PackageSource{WasObject: true}},
		"pi manifest": {setup: func(t *testing.T, root string) {
			writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"notes","pi":{}}`)
		}},
		"empty conventional directory": {setup: func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cwd, sm := trustedSettings(t)
			root := filepath.Join(cwd, "notes")
			writeTestFile(t, filepath.Join(root, "index.mjs"), "export default function () {}\n")
			if tc.setup != nil {
				tc.setup(t, root)
			}
			source := tc.source
			source.Source = root
			if err := sm.SetPackages([]codingagent.PackageSource{source}); err != nil {
				t.Fatal(err)
			}
			items, err := CollectResolvedPackageResourceItems(cwd, sm.AgentDir(), sm, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range items {
				if item.Path == root {
					t.Fatalf("items = %+v, want no extension for the package directory itself", items)
				}
			}
		})
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
