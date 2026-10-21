package rssdk

import (
	"bytes"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
)

// Every source file Cargo can consume in a build must ship with the staged SDK, including newly added modules. The unit-test modules (src/*tests.rs) do not: lib.rs and the modules that declare them do so under #[cfg(test)].
func TestBundledFilesMatchRustSourceTree(t *testing.T) {
	want := []string{"LICENSE", "Cargo.toml", "Cargo.lock"}
	err := fs.WalkDir(os.DirFS("."), "src", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !isTestModule(path) {
			want = append(want, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	got := slices.Clone(BundledFiles())
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("staged Rust sources=%v, want filesystem inventory=%v; run go generate in extensions/sdk-rs", got, want)
	}
	for _, path := range want {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		embedded, err := Source.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(source, embedded) {
			t.Fatalf("staged Rust source %s differs from its current source", path)
		}
	}
}

func isTestModule(path string) bool { return strings.HasSuffix(path, "tests.rs") }

// A test module is left out of the staged crate only if every declaration of it is behind #[cfg(test)], which rustc strips before it looks for the file.
func TestOmittedTestModulesAreDeclaredUnderCfgTest(t *testing.T) {
	var sources []string
	omitted := 0
	err := fs.WalkDir(os.DirFS("."), "src", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if isTestModule(path) {
			omitted++
			return nil
		}
		data, err := os.ReadFile(path)
		sources = append(sources, string(data))
		return err
	})
	if err != nil || omitted == 0 {
		t.Fatalf("walk src: %v (%d test modules)", err, omitted)
	}
	err = fs.WalkDir(os.DirFS("."), "src", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !isTestModule(path) {
			return err
		}
		module := strings.TrimSuffix(path[strings.LastIndex(path, "/")+1:], ".rs")
		declared := 0
		for _, source := range sources {
			lines := strings.Split(source, "\n")
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				pathAttr := "#[path = \"" + module + ".rs\"]"
				if trimmed == pathAttr || module == "tests" && trimmed == "mod tests;" || trimmed == "mod "+module+";" {
					declared++
					// The attributes above the declaration must hold a cfg that needs test.
					cfgTest := false
					for j := i - 1; j >= 0 && strings.HasPrefix(strings.TrimSpace(lines[j]), "#["); j-- {
						attribute := strings.TrimSpace(lines[j])
						cfgTest = cfgTest || attribute == "#[cfg(test)]" || attribute == "#[cfg(all(test, unix))]"
					}
					if !cfgTest {
						t.Errorf("%s is declared at %q without #[cfg(test)]", path, trimmed)
					}
				}
			}
		}
		if declared == 0 {
			t.Errorf("%s is not declared by any module: it is not a test module", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
