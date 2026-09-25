//go:build !windows

package tui

import (
	"os"
	"testing"
)

// withTerminalInput runs body on a pipe. A pipe signals readiness to poll as a
// terminal does, so it stands in for interactive input here.
func withTerminalInput(t *testing.T, _ string, _ int, body func(*testing.T, interactiveTestInput)) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	body(t, interactiveTestInput{
		file: r,
		send: func(t *testing.T, keys string) {
			t.Helper()
			if _, err := w.WriteString(keys); err != nil {
				t.Fatal(err)
			}
		},
		release: func() { _ = w.Close() },
	})
}
