package host_test

import (
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/codemode"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/coding-tools"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/quiet-tools"
)

// test/p2/tui-render.test.ts "the copied components and tool renderers run inside the permission-dropped worker": the compiled client on a real server draws the
// quiet-tools line, the code mode card and a Mermaid diagram, and reports no failure. Pi's client runs those components in a permission-dropped worker and the
// test shows that the sandbox lets them draw; the Go client has no sandbox (D473: the plugins are compiled in), so what the test protects is that the compiled
// facets draw them on a real server with nothing failing.
func TestTheCopiedComponentsAndToolRenderersRunInsideThePermissionDroppedWorker(t *testing.T) {
	var mu sync.Mutex
	var failures []string
	l := startRealWith(t, append(append([]string(nil), realPlugins...), "codemode", "coding-tools", "quiet-tools"), launcherOptions{
		columns: 100, rows: 40,
		options: func(options *host.ClientOptions) {
			options.OnFailure = func(message string) {
				mu.Lock()
				failures = append(failures, message)
				mu.Unlock()
			}
		},
	})
	l.openNewestSession()
	// The editor appears once the conversation handle is live.
	l.until(10*time.Second, "the editor", func() bool {
		if l.draft() == "codemode" {
			return true
		}
		l.terminal.SendInput("\x15")
		l.typed("codemode")
		return false
	})
	l.terminal.SendInput(keyEnter)
	// quiet-tools draws the call as one line; Ctrl+O expands it into the full card.
	l.until(30*time.Second, "the quiet code mode line", func() bool {
		return regexp.MustCompile(`› codemode 2 calls: bash, read ✓`).MatchString(l.terminal.screen())
	})
	l.terminal.SendInput("\x0f")
	// pi's code mode card: the script, its nested bash/read calls and the output. Live rows can settle before the result commits; the output comes with the result.
	l.until(30*time.Second, "the code mode card", func() bool {
		return regexp.MustCompile(`(?s)✓ bash.*✓ read.*bash: written`).MatchString(l.terminal.screen())
	})
	shown := l.terminal.screen()
	if !regexp.MustCompile(`codemode\s*\n\s*const written = await tools\.bash`).MatchString(shown) {
		t.Fatalf("the card lacks the script:\n%s", shown)
	}
	if regexp.MustCompile(`ERR_ACCESS_DENIED|Error:`).MatchString(shown) {
		t.Fatalf("the card shows an error:\n%s", shown)
	}
	// Mermaid (grok-mermaid) renders too: a message with a diagram, Shift+Enter between lines.
	lines := []string{"```mermaid", "graph TD", "Alpha-->Beta", "```"}
	for index, line := range lines {
		l.typed(line)
		if index < len(lines)-1 {
			l.terminal.SendInput("\x1b[13;2u")
		}
	}
	l.terminal.SendInput(keyEnter)
	l.until(30*time.Second, "the mermaid diagram", func() bool {
		return regexp.MustCompile(`(?s)│ Alpha │.*│ Beta │`).MatchString(l.terminal.screen())
	})
	if screen := l.terminal.screen(); regexp.MustCompile(`ERR_ACCESS_DENIED|Error:`).MatchString(screen) {
		t.Fatalf("the diagram shows an error:\n%s", screen)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(failures) != 0 {
		t.Fatalf("failures = %v", failures)
	}
}
