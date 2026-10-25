package nodehome

import (
	"errors"
	"os/user"
	"testing"
)

func TestDirIsNodesOSHomedir(t *testing.T) {
	entry := func() (*user.User, error) { return &user.User{HomeDir: "/home/entry"}, nil }
	none := func() (*user.User, error) { return nil, errors.New("user: unknown userid 1000") }
	env := func(values map[string]string) func(string) (string, bool) {
		return func(key string) (string, bool) { v, ok := values[key]; return v, ok }
	}
	for _, c := range []struct {
		name    string
		goos    string
		env     map[string]string
		current func() (*user.User, error)
		want    string
		wantErr bool
	}{
		{"HOME set", "linux", map[string]string{"HOME": "/h"}, entry, "/h", false},
		{"HOME set, no entry", "linux", map[string]string{"HOME": "/h"}, none, "/h", false},
		{"HOME empty is returned as it is", "linux", map[string]string{"HOME": ""}, entry, "", false},
		{"HOME empty, no entry", "linux", map[string]string{"HOME": ""}, none, "", false},
		{"HOME absent, the entry's home", "linux", nil, entry, "/home/entry", false},
		{"HOME absent, no entry throws", "linux", nil, none, "", true},
		{"darwin reads HOME too", "darwin", nil, none, "", true},
		{"USERPROFILE on Windows", "windows", map[string]string{"USERPROFILE": `C:\Users\u`, "HOME": "/ignored"}, none, `C:\Users\u`, false},
		{"HOME is not Windows's variable", "windows", map[string]string{"HOME": "/ignored"}, entry, "/home/entry", false},
		{"USERPROFILE absent, no entry throws", "windows", nil, none, "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := dir(c.goos, env(c.env), c.current)
			var thrown *Error
			if c.wantErr != errors.As(err, &thrown) || (err == nil) == c.wantErr {
				t.Fatalf("dir = %q, %v; want error %v", got, err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("dir = %q, want %q", got, c.want)
			}
		})
	}
}

func TestErrorIsNodesMessage(t *testing.T) {
	const want = "A system error occurred: uv_os_homedir returned ENOENT (no such file or directory)"
	if got := (&Error{}).Error(); got != want {
		t.Fatalf("message %q, want %q", got, want)
	}
}
