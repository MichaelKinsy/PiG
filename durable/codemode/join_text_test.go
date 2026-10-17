package codemode

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// joinAdjacentTextQuadratic is the code before Pi f11e50f7: it reads the end of the growing joined text on every item.
func joinAdjacentTextQuadratic(items []ai.ToolResultMessageContent) []ai.ToolResultMessageContent {
	var joined []ai.ToolResultMessageContent
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

// Pi f11e50f7 (#10678): joining the text items checks the separator against the last non-empty part and joins once; the result is
// the one the quadratic code gave, for runs with empty items, items that end in a newline and images between runs.
func TestJoinAdjacentTextEqualsTheQuadraticJoinOnRandomItems(t *testing.T) {
	rng := rand.New(rand.NewSource(10678))
	pieces := []string{"", "", "a", "b\n", "\n", "line one", "x\ny", "é", "\n\n"}
	for range 2000 {
		items := make([]ai.ToolResultMessageContent, rng.Intn(12))
		for i := range items {
			if rng.Intn(6) == 0 {
				items[i] = ai.ImageContent{Data: "aW1n", MimeType: "image/png"}
			} else {
				items[i] = ai.TextContent{Text: pieces[rng.Intn(len(pieces))]}
			}
		}
		if got, want := joinAdjacentText(items), joinAdjacentTextQuadratic(items); !reflect.DeepEqual(got, want) {
			t.Fatalf("items %#v:\n got  %#v\n want %#v", items, got, want)
		}
	}
}

// Pi test "formats output at the item limit in linear time": 100,000 text items join in well under the five seconds Pi allows.
func TestJoinAdjacentTextIsLinearAtTheItemLimit(t *testing.T) {
	items := make([]ai.ToolResultMessageContent, 100_000)
	for i := range items {
		items[i] = ai.TextContent{Text: "x"}
	}
	start := time.Now()
	joined := joinAdjacentText(items)
	elapsed := time.Since(start)
	testenv.TimingGateFatal(t, elapsed > 2*time.Second, "joining %d items took %v", len(items), elapsed)
	if text, ok := joined[0].(ai.TextContent); len(joined) != 1 || !ok || len(text.Text) != 2*len(items)-1 {
		t.Fatalf("joined = %d items", len(joined))
	}
}
