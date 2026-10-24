//go:build windows

package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var diagDA1Reply = regexp.MustCompile(`^\x1b\[\?[\d;]*c$`)

// TestDiagDA1Count (throwaway): one keyboard protocol query written by the process terminal in a pseudo console, how many DA1 replies come back on stdin, and does the DA1 query reach the outer terminal (this test's drain of the console output).
func TestDiagDA1Count(t *testing.T) {
	if res := os.Getenv("DIAG_DA1_RESULT"); res != "" {
		restore, err := EnterRawMode()
		if err != nil {
			t.Fatal(err)
		}
		terminal := NewProcessTerminalWithOutput(os.Stdin, nil, ioDiscard{})
		ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
		defer cancel()
		var got []string
		terminal.forwardInput(ctx, func(d []byte) { got = append(got, string(d)) }, nil)
		restore()
		if err := os.WriteFile(res, []byte(fmt.Sprintf("%q", got)), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	res := filepath.Join(t.TempDir(), "res")
	t.Setenv("DIAG_DA1_RESULT", res)
	console := startInPseudoConsole(t, os.Args[0], "-test.run=^TestDiagDA1Count$", "-test.count=1")
	console.wait(t)
	data, _ := os.ReadFile(res)
	out := console.output()
	replies := 0
	for _, part := range strings.Split(strings.ReplaceAll(string(data), `\x1b`, "\x1b"), `", "`) {
		part = strings.Trim(part, `[]"`)
		if diagDA1Reply.MatchString(part) {
			replies++
		}
	}
	t.Logf("DA1COUNT replies_on_stdin=%d da1_query_reached_outer_terminal=%v forwarded=%s output=%q", replies, strings.Contains(out, "\x1b[c"), data, out)
}
