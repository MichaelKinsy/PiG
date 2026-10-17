package codemode

import (
	"strings"
	"testing"
)

// execute.ts carries a script's string result and its console lines into the tool result text unchanged, and a JavaScript string can hold an
// unpaired surrogate. PiG holds that unit as a WTF-8 sequence, which the session writer and the json events write as the escape \ud800, as Pi
// does. This drives the whole execute path (sandbox, valueText, join, truncation), not only valueText.
func TestExecuteKeepsALoneSurrogateInTheToolResult(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"returned string", `return "\ud800 é";`},
		{"console line", `console.log("\ud800 é");`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := executeJoinResult(t, tc.code)
			if len(content) < 2 {
				t.Fatalf("content = %#v, want the header and the script text", content)
			}
			if text := labelText(t, content[1]); !strings.Contains(text, "\xed\xa0\x80 é") || strings.Contains(text, "\uFFFD") {
				t.Fatalf("tool result text = %q, want the lone surrogate kept as WTF-8", text)
			}
		})
	}
}
