package export

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// Ports the HTML export half of upstream read-line-range-display.test.ts (Pi 91014991, #9887): the exported viewer's
// formatReadLineRange formats Pi's cases, and the exported script is Pi's template.
func TestExportedViewerFormatsReadLineRangeLikePi(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	html := ToHTML(SessionData{Entries: []json.RawMessage{json.RawMessage(`{"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","name":"read","arguments":{"path":"notes.txt","offset":"25","limit":"13"}}]}}`)}})
	var script strings.Builder
	for _, name := range []string{"function parseLineNumber(value) {", "function formatReadLineRange(offset, limit) {"} {
		start := strings.Index(html, name)
		if start < 0 {
			t.Fatalf("%q not found in the exported template", name)
		}
		depth := 0
		for i := start; i < len(html); i++ {
			if html[i] == '{' {
				depth++
			} else if html[i] == '}' {
				if depth--; depth == 0 {
					script.WriteString(html[start:i+1] + "\n")
					break
				}
			}
		}
	}
	script.WriteString(`for (const [o, l] of [[25, 13], ["25", "13"], [null, "13"], ["25", null], [undefined, undefined], ["abc", 13], [25, ""], [25, "Infinity"]]) console.log(JSON.stringify(formatReadLineRange(o, l)));`)
	out, err := exec.CommandContext(t.Context(), node, "-e", script.String()).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "\":25-37\"\n\":25-37\"\n\":1-13\"\n\":25\"\n\"\"\n\"\"\n\"\"\n\"\"\n"
	if string(out) != want {
		t.Fatalf("ranges:\n%s\nwant:\n%s", out, want)
	}
}
