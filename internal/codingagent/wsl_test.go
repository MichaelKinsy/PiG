package codingagent

// pi: packages/coding-agent/src/utils/wsl.ts

import (
	"errors"
	"testing"
)

func TestIsWSL(t *testing.T) {
	missing := func(string) ([]byte, error) { return nil, errors.New("no /proc") }
	release := func(text string) func(string) ([]byte, error) {
		return func(path string) ([]byte, error) {
			if path != "/proc/version" {
				t.Fatalf("read %q, want /proc/version", path)
			}
			return []byte(text), nil
		}
	}
	for _, c := range []struct {
		name     string
		env      map[string]string
		readFile func(string) ([]byte, error)
		want     bool
	}{
		{"distro name", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, missing, true},
		{"WSLENV", map[string]string{"WSLENV": "WT_SESSION::WT_PROFILE_ID"}, missing, true},
		// wsl.ts tests env.WSL_DISTRO_NAME for JavaScript truthiness, so an empty value falls through to /proc/version.
		{"empty distro name", map[string]string{"WSL_DISTRO_NAME": ""}, release("Linux version 6.1.0-generic"), false},
		{"kernel release names Microsoft", nil, release("Linux version 5.15.153.1-microsoft-standard-WSL2"), true},
		{"kernel release names WSL", nil, release("Linux version 6.6.36.3-WSL2-STABLE"), true},
		// wsl.ts reads /proc/version with /microsoft|wsl/i: ASCII letters fold, U+017F and U+212A do not (Node 24: /wsl/i.test("wſl") === false).
		{"kernel release in capitals", nil, release("Linux version 5.15-MICROSOFT-STANDARD-WSL2"), true},
		{"kernel release with a long s", nil, release("Linux version 5.15-microſoft-standard-wſl2"), false},
		{"plain Linux kernel", nil, release("Linux version 6.8.0-45-generic (buildd@lcy02)"), false},
		{"no /proc", nil, missing, false},
		{"WT_SESSION alone", map[string]string{"WT_SESSION": "x"}, missing, false},
	} {
		if got := IsWSL(fakeEnvLookup(c.env), c.readFile); got != c.want {
			t.Errorf("%s: IsWSL = %v, want %v", c.name, got, c.want)
		}
	}
}

func fakeEnvLookup(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
