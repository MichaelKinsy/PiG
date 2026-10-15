package sdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type configHomeCase struct {
	Name string             `json:"name"`
	Env  map[string]*string `json:"env"`
	Want *string            `json:"want"`
}

// Context.ConfigHome answers the shared env matrix (test/extension-conformance/testdata/configroot-matrix.json) as the host's internal/configroot and
// the Python, Rust and Node SDKs do: PIG_HOME, else XDG_CONFIG_HOME/pig, else ~/.pig; empty falls through; ~ and ~/ expand; other values stay
// literal; an unavailable home directory is an error, never a relative path.
func TestConfigHomeMatchesTheSharedMatrix(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "test", "extension-conformance", "testdata", "configroot-matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	var matrix struct {
		Cases []configHomeCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatal(err)
	}
	if len(matrix.Cases) == 0 {
		t.Fatal("empty matrix")
	}
	for _, tc := range matrix.Cases {
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
			got, err := Context{}.ConfigHome()
			if tc.Want == nil {
				if err == nil || got != "" {
					t.Fatalf("ConfigHome() = %q, %v; want an error and no path", got, err)
				}
				return
			}
			want := strings.ReplaceAll(*tc.Want, "<HOME>", filepath.ToSlash(home))
			if err != nil || filepath.ToSlash(got) != want {
				t.Fatalf("ConfigHome() = %q, %v; want %q", got, err, want)
			}
		})
	}
}
