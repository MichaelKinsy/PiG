package subprocess

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Sixteen cells compiling in parallel are one rewritten line: no newline is
// written, the count only rises, and clear leaves an empty line.
func TestBuildLineIsOneRewrittenLine(t *testing.T) {
	var out bytes.Buffer
	keys := make([]string, 16)
	for i := range keys {
		keys[i] = fmt.Sprintf("cell-%d", i)
	}
	line := newBuildLine(&out, 60, keys)
	var wg sync.WaitGroup
	for i, key := range keys {
		wg.Go(func() {
			if i%2 == 0 {
				line.compileStarted(key, fmt.Sprintf("extension-%d", i))
			}
			line.cellFinished(key)
		})
	}
	wg.Wait()
	line.clear()

	text := out.String()
	if strings.Contains(text, "\n") {
		t.Fatalf("the status line wrote a newline: %q", text)
	}
	if !strings.Contains(text, "Building extensions [") || !strings.Contains(text, "/16]") {
		t.Fatalf("no count in %q", text)
	}
	if !strings.HasSuffix(text, "\r\x1b[2K") {
		t.Fatalf("line not cleared: %q", text)
	}
	if line.shown || len(line.finished) != 16 || len(line.compiling) != 0 {
		t.Fatalf("state after the pass: shown=%v finished=%d compiling=%d", line.shown, len(line.finished), len(line.compiling))
	}
}

// A warm start compiles nothing, so it prints nothing, and a long name never
// wraps the terminal.
func TestBuildLineStaysQuietWhenNothingCompilesAndFitsTheTerminal(t *testing.T) {
	var out bytes.Buffer
	line := newBuildLine(&out, 40, []string{"a", "b"})
	line.cellFinished("a")
	line.cellFinished("b")
	line.clear()
	if out.Len() != 0 {
		t.Fatalf("warm start wrote %q", out.String())
	}
	line = newBuildLine(&out, 40, []string{"a"})
	line.compileStarted("a", strings.Repeat("very-long-extension-name-", 6))
	drawn := strings.TrimPrefix(out.String(), "\r\x1b[2K")
	if n := len([]rune(drawn)); n >= 40 {
		t.Fatalf("drawn %d columns in a 40-column terminal: %q", n, drawn)
	}
}
