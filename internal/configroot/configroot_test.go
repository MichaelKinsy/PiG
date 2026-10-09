package configroot_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/configroot"
	"github.com/MichaelKinsy/PiG/internal/pigdocs"
)

// matrixCase is one row of the env matrix every PiG config root getter (host, Go, Python, Rust and Node SDKs) must answer identically.
type matrixCase struct {
	Name string             `json:"name"`
	Env  map[string]*string `json:"env"`
	Want *string            `json:"want"`
}

func loadMatrix(t *testing.T) []matrixCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "test", "extension-conformance", "testdata", "configroot-matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	var matrix struct {
		Cases []matrixCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatal(err)
	}
	if len(matrix.Cases) == 0 {
		t.Fatal("empty matrix")
	}
	return matrix.Cases
}

// The shared matrix: PIG_HOME, else XDG_CONFIG_HOME/pig, else ~/.pig; an empty override falls through; a leading ~ or ~/ expands; a non-tilde value is literal;
// an unavailable home directory is an error, never a relative path.
func TestResolveMatchesTheSharedMatrix(t *testing.T) {
	for _, tc := range loadMatrix(t) {
		t.Run(tc.Name, func(t *testing.T) {
			home := t.TempDir()
			for _, name := range []string{"PIG_HOME", "XDG_CONFIG_HOME"} {
				t.Setenv(name, "")
				_ = os.Unsetenv(name)
			}
			homeValue := home
			if v, ok := tc.Env["HOME"]; ok {
				homeValue = *v
			}
			t.Setenv("HOME", homeValue)
			t.Setenv("USERPROFILE", homeValue)
			for name, v := range tc.Env {
				if name != "HOME" && v != nil {
					t.Setenv(name, *v)
				}
			}
			got, err := configroot.Resolve()
			if tc.Want == nil {
				if err == nil {
					t.Fatalf("Resolve() = %q, want an error", got)
				}
				if got != "" {
					t.Fatalf("Resolve() on failure returned %q, want no path", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error: %v", err)
			}
			want := strings.ReplaceAll(*tc.Want, "<HOME>", filepath.ToSlash(home))
			if filepath.ToSlash(got) != want {
				t.Fatalf("Resolve() = %q, want %q", got, want)
			}
		})
	}
}

// ExpandHome fails, rather than yielding a relative path, when a leading ~ needs a home directory that is unavailable, and needs none otherwise.
func TestExpandHomeFailsWithoutAHomeDirectoryOnlyWhenItIsNeeded(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	for _, in := range []string{"~", "~/x"} {
		if got, err := configroot.ExpandHome(in); err == nil {
			t.Errorf("ExpandHome(%q) = %q, want an error", in, got)
		}
	}
	for _, in := range []string{"~user/x", "a/~/b", "/abs", "rel", ""} {
		if got, err := configroot.ExpandHome(in); err != nil || got != in {
			t.Errorf("ExpandHome(%q) = %q, %v; want it unchanged", in, got, err)
		}
	}
}

// Dir cannot report a failure, so it panics instead of returning a path relative to the working directory.
func TestDirPanicsInsteadOfReturningARelativePath(t *testing.T) {
	t.Setenv("PIG_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	defer func() {
		var unresolved *configroot.UnresolvedError
		if err, _ := recover().(error); !errors.As(err, &unresolved) {
			t.Fatalf("Dir() panicked with %v, want an *UnresolvedError", err)
		}
	}()
	t.Fatalf("Dir() = %q, want a panic", configroot.Dir())
}

// expandTildePath (config.ts / utils): "~" is the home directory, "~/x" is under it, and any other path, including "~user" and a ~ elsewhere, is unchanged.
func TestExpandTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for in, want := range map[string]string{"~": home, "~/x": filepath.Join(home, "x"), "~user/x": "~user/x", "a/~/b": "a/~/b", "/abs": "/abs", "": ""} {
		if got := configroot.ExpandTilde(in); got != want {
			t.Errorf("ExpandTilde(%q) = %q, want %q", in, got, want)
		}
	}
}

// The config root is PIG_HOME, else XDG_CONFIG_HOME/pig, else ~/.pig, and a leading ~ in the variables is expanded.
func TestDirPrefersPigHomeThenXDGThenTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_HOME", "~/p")
	t.Setenv("XDG_CONFIG_HOME", "~/x")
	if got, want := configroot.Dir(), filepath.Join(home, "p"); got != want {
		t.Fatalf("PIG_HOME wins: %q, want %q", got, want)
	}
	t.Setenv("PIG_HOME", "")
	if got, want := configroot.Dir(), filepath.Join(home, "x", "pig"); got != want {
		t.Fatalf("XDG_CONFIG_HOME next: %q, want %q", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := configroot.Dir(), filepath.Join(home, ".pig"); got != want {
		t.Fatalf("the home directory last: %q, want %q", got, want)
	}
}

// DocsDir spells the docs directory name itself so that a Binary that strips docs does not link pigdocs; the literal is pigdocs.SubDir.
func TestDocsDirIsThePigdocsDirectoryUnderTheConfigRoot(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	if got, want := configroot.DocsDir(), pigdocs.DocsDir(configroot.Dir()); got != want {
		t.Fatalf("DocsDir() = %q, want %q", got, want)
	}
}
