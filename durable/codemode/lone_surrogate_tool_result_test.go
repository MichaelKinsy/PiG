package codemode

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// A tool's result that holds a lone surrogate (Pi's strings hold UTF-16 units, so a model or a tool can produce one) reaches
// the entry as Pi's does: the unit in the output text, in the details and in the structured value a script gets, written as
// the escape `\ud83d` by JSON.stringify and read back as the same unit by JSON.parse. Here a tool returns one in each and a
// script returns what it gets; the entry's message and the script's value must hold the unit, not U+FFFD.
func TestAToolResultWithALoneSurrogateKeepsItThroughTheHarnessAndTheEntry(t *testing.T) {
	const wtf = "a\xed\xa0\xbd<&>"
	f := codemodeChat(t)
	schema := map[string]any{"type": "object", "properties": map[string]any{"s": map[string]any{"type": "string"}}}
	addTool(t, f.setup.registry, harness.DefineTool(durable.ToolRegistration{
		ToolSchema:             ai.ToolSchema{Name: "lone", Description: "lone", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		StructuredOutputSchema: schema,
		Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{
				Output: []ai.ToolResultMessageContent{ai.TextContent{Text: wtf}}, StructuredOutput: map[string]any{"s": wtf}, HasStructuredOutput: true,
				Details: map[string]any{"d": wtf}, HasDetails: true,
			}, nil
		},
	}))
	f.holds.block.Store(false)
	f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`const r = await tools.lone({}); return { s: r.s, same: r.s.charCodeAt(1) === 0xd83d };`), done()})
	opened, root := f.setup.open(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")))
	mustSubmitDone(t, root)
	text, failed := resultText(t, root)
	if failed || !strings.Contains(text, `"same":true`) {
		t.Errorf("the script did not get the unit: %q (error %v)", text, failed)
	}
	var seen []string
	for _, entry := range allEntries(t, root) {
		if !durable.ToolResultEntry.Is(&entry) {
			continue
		}
		message := entry.Model[0].(ai.ToolResultMessage)
		encoded, err := jsstring.MarshalJSON(message)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, string(encoded))
	}
	if len(seen) != 1 || strings.Contains(seen[0], "\ufffd") {
		t.Errorf("the tool result entries %q must hold no U+FFFD", seen)
	}
	mustClose(t, opened)
}
