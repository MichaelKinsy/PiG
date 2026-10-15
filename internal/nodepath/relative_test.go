package nodepath

import (
	"encoding/json"
	"os"
	"testing"
)

// TestRelativeMatchesNode runs on every OS: both flavors take their process state as input. The expectations are Node 24's path.posix.relative and path.win32.relative (testdata/generate_relative.mjs).
func TestRelativeMatchesNode(t *testing.T) {
	data, err := os.ReadFile("testdata/relative_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Relative []struct {
			Flavor, Cwd, From, To, Want string
		} `json:"relative"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Relative) < 1000 {
		t.Fatalf("case table is too small: %d", len(file.Relative))
	}
	failures := 0
	for _, c := range file.Relative {
		env := Env{Cwd: c.Cwd}
		var got string
		switch c.Flavor {
		case "win32":
			got, err = Win32Relative(env, c.From, c.To)
		case "posix":
			got, err = PosixRelative(env, c.From, c.To)
		default:
			t.Fatalf("unknown flavor %q", c.Flavor)
		}
		if err != nil || got != c.Want {
			if failures++; failures <= 20 {
				t.Errorf("%s relative(%q, %q) = %q, %v; Node gives %q", c.Flavor, c.From, c.To, got, err, c.Want)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d cases differ from Node", failures, len(file.Relative))
	}
}

func TestRelativeReturnsTheWorkingDirectoryError(t *testing.T) {
	env := Env{CwdErr: errCwd}
	if got, err := PosixRelative(env, "a", "/b"); err == nil {
		t.Errorf("posix relative with an unreadable working directory = %q, nil", got)
	}
	if got, err := Win32Relative(env, `C:\a`, "b"); err == nil {
		t.Errorf("win32 relative with an unreadable working directory = %q, nil", got)
	}
}
