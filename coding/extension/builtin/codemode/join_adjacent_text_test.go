package codemode

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// referenceJoinAdjacentText is upstream execute.ts joinAdjacentText: it rebuilds the joined text for every item.
func referenceJoinAdjacentText(items []ai.ToolResultMessageContent) []ai.ToolResultMessageContent {
	joined := make([]ai.ToolResultMessageContent, 0, len(items))
	for _, item := range items {
		text, isText := item.(ai.TextContent)
		if isText && len(joined) > 0 {
			if last, lastIsText := joined[len(joined)-1].(ai.TextContent); lastIsText {
				separator := "\n"
				if last.Text == "" || strings.HasSuffix(last.Text, "\n") {
					separator = ""
				}
				joined[len(joined)-1] = ai.TextContent{Text: last.Text + separator + text.Text}
				continue
			}
		}
		joined = append(joined, item)
	}
	return joined
}

// Pi #10678: joinAdjacentText is quadratic in the number of text items, and runs on the host before truncation. A
// script with tens of thousands of text() calls must cost memory and time in proportion to its output.
func TestJoinAdjacentTextIsLinearInTheNumberOfItems(t *testing.T) {
	const count = 40_000
	items := make([]ai.ToolResultMessageContent, count)
	total := 0
	for i := range items {
		text := fmt.Sprintf("==> text %d/%d <==\nline %d", i+1, count, i)
		items[i] = ai.TextContent{Text: text}
		total += len(text) + 1
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got := joinAdjacentText(items)
	runtime.ReadMemStats(&after)
	if len(got) != 1 || len(got[0].(ai.TextContent).Text) != total-1 {
		t.Fatalf("got %d items, want one joined text of %d bytes", len(got), total-1)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > uint64(8*total) {
		t.Fatalf("joining %d items of %d bytes allocated %d bytes; the cost must be linear in the output", count, total, allocated)
	}
}

func TestJoinAdjacentTextMatchesUpstreamOnMixedItems(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	pieces := []string{"", "a", "b\n", "\n", "line one\nline two", "tail\n\n", "x"}
	image := ai.ImageContent{Data: "AAAA", MimeType: "image/png"}
	for range 2000 {
		items := make([]ai.ToolResultMessageContent, rng.IntN(12))
		for i := range items {
			if rng.IntN(5) == 0 {
				items[i] = image
			} else {
				items[i] = ai.TextContent{Text: pieces[rng.IntN(len(pieces))]}
			}
		}
		if got, want := joinAdjacentText(items), referenceJoinAdjacentText(items); !reflect.DeepEqual(got, want) {
			t.Fatalf("items %#v:\ngot  %#v\nwant %#v", items, got, want)
		}
	}
}
