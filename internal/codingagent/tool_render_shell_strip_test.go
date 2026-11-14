package codingagent

import (
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// getTextOutput runs on every update of a tool card. For output that holds no escape sequence, binary character or
// carriage return, the shell, generic and codemode ports of it must hand the text back without copying it. Measured in a
// process of its own with its collector off (GOGC=off): runtime.MemStats counts the whole process, so in the process that
// runs the package whatever else allocates during the measure is counted against the bound.
func TestTextOutputOfCleanTextIsNotCopied(t *testing.T) {
	if os.Getenv("PIG_TEST_CLEAN_TEXT_ONLY") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestTextOutputOfCleanTextIsNotCopied$", "-test.count=1")
		cmd.Env = append(os.Environ(), "PIG_TEST_CLEAN_TEXT_ONLY=1", "GOGC=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the measuring process failed: %v\n%s", err, out)
		}
		return
	}
	if percent := debug.SetGCPercent(-1); percent != -1 {
		t.Fatalf("the measuring process collects at GOGC=%d, want GOGC=off", percent)
	}
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
