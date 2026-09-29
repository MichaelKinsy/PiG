//go:build windows

package testenv

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// Git for Windows bash must receive every argument byte for byte, including
// doubled backslashes (UNC paths), quotes, glob characters and empty strings.
func TestVerbatimArgsReachGitBashExactly(t *testing.T) {
	args := []string{
		`\\server\share\dir`,
		`a\\b`,
		`C:\p\\q`,
		`trailing\`,
		`trailing\\`,
		`quote'and"double`,
		`\"escaped\"`,
		`*`,
		`a b	c`,
		``,
		`%PATH%`,
		`$HOME`,
	}
	cmd := exec.Command(Bash(t), append([]string{"-c", `printf '%s\0' "$@"`, "bash"}, args...)...)
	VerbatimArgs(cmd)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if !slices.Equal(got, args) {
		t.Fatalf("bash received %q, want %q", got, args)
	}
}
