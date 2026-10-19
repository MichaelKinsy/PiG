//go:build windows

package daemon

// pi: packages/env/daemon/src/sys/windows.rs

// Ports quotes_arguments_like_libuv (windows.rs:648-664). The Go daemon runs commands through os/exec, whose
// syscall.EscapeArg quotes a Windows command line by the same rules as Pi's quote_argument, so the test pins that
// equivalence on Pi's vectors.

import (
	"syscall"
	"testing"
)

func TestEscapeArgQuotesArgumentsLikeLibuv(t *testing.T) {
	for argument, want := range map[string]string{
		"":                   `""`,
		"plain":              "plain",
		"a b":                `"a b"`,
		"*.txt":              "*.txt",
		`say "hi"`:           `"say \"hi\""`,
		`C:\dir with space\`: `"C:\dir with space\\"`,
		`a\\b c`:             `"a\\b c"`,
	} {
		if got := syscall.EscapeArg(argument); got != want {
			t.Errorf("EscapeArg(%q) = %q, want %q", argument, got, want)
		}
	}
}
