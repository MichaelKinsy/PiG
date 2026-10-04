package packagecontent

import (
	"path/filepath"
	"slices"
	"testing"
)

const pythonFactoryModule = "import pig_sdk\n\n\ndef new_extension() -> pig_sdk.Extension:\n    return pig_sdk.Extension(\"hello-python\")\n"

// PiG's Python SDK has no Pi counterpart (D19). A Python extension directory is an entry wherever a Go or Rust one is (TestNativeBuildDirectoriesRemainExtensionEntries): a pyproject.toml marker, or the new_extension factory module or executable main.py that extsource.Resolve accepts, as `pig install DIR --validate-only` does.
func TestPythonExtensionDirectoryDiscovery(t *testing.T) {
	t.Run("factory directory in the global extensions directory", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "hello-python", "hello_python.py"), pythonFactoryModule)
		if got, want := DiscoverAutomatic(root, Extensions), []string{filepath.Join(root, "hello-python")}; !slices.Equal(got, want) {
			t.Fatalf("entries=%q, want %q", got, want)
		}
	})
	t.Run("pyproject marker", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "tool", "pyproject.toml"), "[project]\nname = \"tool\"\n")
		if got, want := DiscoverAutomatic(root, Extensions), []string{filepath.Join(root, "tool")}; !slices.Equal(got, want) {
			t.Fatalf("entries=%q, want %q", got, want)
		}
	})
	t.Run("settings entry or local Package naming the directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "hello-python")
		writeTestFile(t, filepath.Join(dir, "hello_python.py"), pythonFactoryModule)
		if got, want := Collect([]string{dir}, Extensions), []string{dir}; !slices.Equal(got, want) {
			t.Fatalf("entries=%q, want %q", got, want)
		}
	})
	t.Run("Package extensions directory", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "extensions", "hello-python", "hello_python.py"), pythonFactoryModule)
		resources, err := Discover(root)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{filepath.Join(root, "extensions", "hello-python")}; !slices.Equal(resources.ExtensionEntries, want) {
			t.Fatalf("ExtensionEntries=%q, want %q", resources.ExtensionEntries, want)
		}
	})
	t.Run("helper modules without a factory are not an extension", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "scripts", "helper.py"), "def main():\n    pass\n")
		if got := DiscoverAutomatic(root, Extensions); len(got) != 0 {
			t.Fatalf("entries=%q, want none", got)
		}
	})
	t.Run("an extensions directory is never itself one Python extension", func(t *testing.T) {
		// Pi discovers only .ts and .js files at the top of an extensions directory; a factory module there would make the whole directory one extension named after it.
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "tool.py"), pythonFactoryModule)
		if got := DiscoverAutomatic(root, Extensions); len(got) != 0 {
			t.Fatalf("entries=%q, want none", got)
		}
		writeTestFile(t, filepath.Join(root, "hello-go", "go.mod"), "module example.com/hello-go\n\ngo 1.26\n")
		if got, want := DiscoverAutomatic(root, Extensions), []string{filepath.Join(root, "hello-go")}; !slices.Equal(got, want) {
			t.Fatalf("entries=%q, want %q", got, want)
		}
	})
	t.Run("a factory directory keeps its child folders", func(t *testing.T) {
		// As go.mod and Cargo.toml do: tests/main.py and web/index.js belong to the extension and are not extensions of their own.
		root := t.TempDir()
		ext := filepath.Join(root, "hello-python")
		writeTestFile(t, filepath.Join(ext, "hello_python.py"), pythonFactoryModule)
		writeTestFile(t, filepath.Join(ext, "tests", "main.py"), "#!/usr/bin/env python3\nprint('test')\n")
		writeTestFile(t, filepath.Join(ext, "web", "index.js"), "export default function() {}\n")
		if got, want := DiscoverAutomatic(root, Extensions), []string{ext}; !slices.Equal(got, want) {
			t.Fatalf("DiscoverAutomatic=%q, want %q", got, want)
		}
		if got, want := Collect([]string{ext}, Extensions), []string{ext}; !slices.Equal(got, want) {
			t.Fatalf("Collect=%q, want %q", got, want)
		}
	})
	t.Run("a Package extensions directory is conventional", func(t *testing.T) {
		// Upstream collectPackageResources reads a Package's extensions/ with collectAutoExtensionEntries, as it reads the global directory.
		for _, module := range []string{"tool.py", "main.py"} {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, "extensions", module), "#!/usr/bin/env python3\n"+pythonFactoryModule)
			resources, err := Discover(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(resources.ExtensionEntries) != 0 {
				t.Fatalf("%s: ExtensionEntries=%q, want none", module, resources.ExtensionEntries)
			}
		}
	})
}
