package codingagent

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
		{"kernel release names Microsoft", nil, release("Linux version 5.15.153.1-microsoft-standard-WSL2"), true},
		{"kernel release names WSL", nil, release("Linux version 6.6.36.3-WSL2-STABLE"), true},
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
