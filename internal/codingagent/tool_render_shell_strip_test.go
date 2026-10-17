package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// getTextOutput runs on every update of a tool card. For output that holds no escape sequence, binary character or
// carriage return, the shell, generic and codemode ports of it must hand the text back without copying it.
func TestTextOutputOfCleanTextIsNotCopied(t *testing.T) {
	clean := strings.Repeat("clean output line\n", (256<<10)/18)
	content := []ai.ToolResultMessageContent{ai.TextContent{Text: clean}}
	for name, read := range map[string]func() string{
		"shellTextOutput":    func() string { return shellTextOutput(clean) },
		"GetTextOutput":      func() string { return tools.GetTextOutput(content, true) },
		"codemodeTextOutput": func() string { return codemodeTextOutput(content, true) },
	} {
		if got := read(); got != clean {
			t.Fatalf("%s changed clean text", name)
		}
		if bytes := renderBytesPerCall(20, func() { read() }); bytes > 1024 {
			t.Errorf("%s copies clean text: %.0f B per call for %d B of text", name, bytes, len(clean))
		}
	}
}
