package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts:3056 renders each stack line after the first as `  ${line.trim()}`: String.prototype.trim strips U+FEFF and U+00A0 and keeps
// U+0085.
func TestExtensionErrorStackLinesTrimLikeJavaScriptInTheChatContainer(t *testing.T) {
	m := &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 100, 30)}
	m.showExtensionError("ext.ts", "failed", "Error: failed\n\ufeff  at a.ts:1\u00a0\n\u0085at b.ts:2\u0085")
	got := stripANSI(strings.Join(m.chatContainer.Render(100), "\n"))
	if !strings.Contains(got, "   at a.ts:1 ") || strings.Contains(got, "\ufeff") || strings.Contains(got, "\u00a0") {
		t.Errorf("stack line not JS-trimmed of U+FEFF and U+00A0:\n%q", got)
	}
	if !strings.Contains(got, "\u0085at b.ts:2\u0085") {
		t.Errorf("stack line lost the U+0085 that JS trim keeps:\n%q", got)
	}
}
