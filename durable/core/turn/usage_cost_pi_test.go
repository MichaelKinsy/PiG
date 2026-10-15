package turn_test

// pi: packages/durable/src/harness/usage.ts (addUsage), generation.ts appendAssistant (:652-660)

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// generation.ts appendAssistant: every assistant entry's usage, cost included, is added to the conversation's `pi.usage` totals in the commit that appends it; two messages sum their cost fields.
// mutation-checked: skipping the cost counters of a document that has a cost object fails it.
func TestUsageDocumentSumsTheCostOfEveryAssistantMessage(t *testing.T) {
	r := newRig(t)
	r.agent.Tools = []turn.Tool{echoTool()}
	conv, _ := r.start("hi")
	r.resume()
	first := strings.Replace(toolUseWith("echo", `{}`), `"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}`,
		`"cost":{"input":0.5,"output":0.25,"cacheRead":0,"cacheWrite":0,"total":0.75}`, 1)
	r.finish(first)
	r.toolRuns[0].then(mustParse(t, `{"output":[]}`), nil)
	r.run()
	r.finish(assistantMessage("stop", "done")) // cost input 0.1, output 0.2, total 0.3
	input, output, total := 0.5, 0.25, 0.75
	input, output, total = input+0.1, output+0.2, total+0.3
	want := `"cost":{"input":` + jv.Stringify(input) + `,"output":` + jv.Stringify(output) + `,"cacheRead":0,"cacheWrite":0,"total":` + jv.Stringify(total) + `}`
	if got := r.doc(turn.UsageDef, conv); !strings.Contains(got, want) {
		t.Errorf("usage = %s, want it to hold %s", got, want)
	}
}
