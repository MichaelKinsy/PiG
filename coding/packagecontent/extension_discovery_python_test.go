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
	t.Run("child extensions win over a Python directory", func(t *testing.T) {
		// As for go.work: a directory whose children are extensions is a collection of them.
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "tool.py"), pythonFactoryModule)
		writeTestFile(t, filepath.Join(dir, "hello-python", "hello_python.py"), pythonFactoryModule)
		if got, want := Collect([]string{dir}, Extensions), []string{filepath.Join(dir, "hello-python")}; !slices.Equal(got, want) {
			t.Fatalf("entries=%q, want %q", got, want)
		}
	})
}
