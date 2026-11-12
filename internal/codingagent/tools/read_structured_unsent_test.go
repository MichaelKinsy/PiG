package tools

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A process that reads many large files and never sends a request keeps a few MiB of encodings, not the request cache's whole limit.
func TestReadsThatSendNoRequestKeepFewEncodings(t *testing.T) {
	ai.ReleaseRequestCaches()
	t.Cleanup(ai.ReleaseRequestCaches)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := range 1500 {
		text := fmt.Sprintf("// file %d\n", i) + strings.Repeat("func f() { return \"q\" } // <x> & y\n", (50<<10)/36)
		content := []ai.ToolResultMessageContent{ai.TextContent{Text: text}}
		_ = readStructuredContent(content)
		if _, err := (ai.TextContent{Text: text}).AppendStringify(nil); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if grown := int64(after.HeapAlloc) - int64(before.HeapAlloc); grown > 12<<20 {
		t.Fatalf("1500 reads of 50 KiB with no request left %d MiB live, want a few MiB", grown>>20)
	}
}
