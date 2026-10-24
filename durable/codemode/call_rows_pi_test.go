package codemode

// pi: packages/durable/src/codemode/codemode.ts

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Each nested call of the details has its status and error text from its result (codemode.ts finishRow, the code that
// builds `calls`): an error result is an error row, whether the script got its value or a rejection; the text of an error row
// is the outcome's message for a rejection and the error diagnostics for a value the script got; a result that is not an error
// is an ok row without text, whatever diagnostics it carries; arguments that are not an object are an error row.
func TestNestedCallRowsTakeTheirStatusAndErrorTextFromTheResult(t *testing.T) {
	f := codemodeChat(t)
	exit := map[string]any{"type": "object", "properties": map[string]any{"output": map[string]any{"type": "string"}, "exitCode": map[string]any{"type": "number"}}}
	empty := map[string]any{"type": "object", "properties": map[string]any{}}
	isError := true
	value := map[string]any{"output": "out", "exitCode": 2}
	diagnostics := func(list ...durable.ToolDiagnostic) []durable.ToolDiagnostic { return list }
	add := func(name string, schema map[string]any, result durable.ToolExecutionResult) {
		registration := durable.ToolRegistration{
			ToolSchema:             ai.ToolSchema{Name: name, Description: name, Parameters: empty},
			StructuredOutputSchema: schema,
			Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				return result, nil
			},
		}
		addTool(t, f.setup.registry, harness.DefineTool(registration))
	}
	text := []ai.ToolResultMessageContent{ai.TextContent{Text: "text"}}
	// A schema tool whose error result carries its value, with a warning only; then one with an error diagnostic, listed
	// before and after the warning.
	add("warned", exit, durable.ToolExecutionResult{Output: text, StructuredOutput: value, HasStructuredOutput: true, IsError: &isError,
		Diagnostics: diagnostics(durable.ToolDiagnostic{Severity: durable.SeverityWarn, Message: "careful"})})
	add("failed", exit, durable.ToolExecutionResult{Output: text, StructuredOutput: value, HasStructuredOutput: true, IsError: &isError,
		Diagnostics: diagnostics(durable.ToolDiagnostic{Severity: durable.SeverityInfo, Message: "fyi"}, durable.ToolDiagnostic{Severity: durable.SeverityError, Message: "exit code 2"}, durable.ToolDiagnostic{Severity: durable.SeverityWarn, Message: "careful"})})
	// A result that is not an error but carries an error diagnostic: an ok row without text.
	add("fine", nil, durable.ToolExecutionResult{Output: text, Diagnostics: diagnostics(durable.ToolDiagnostic{Severity: durable.SeverityError, Message: "ignored"})})
	// Registered after the schema tools: a schema tool is not found by the tool before it in the list.
	add("plain", nil, durable.ToolExecutionResult{Output: text})
	f.holds.block.Store(false)
	f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`const a = await tools.warned({});
		const b = await tools.failed({});
		await tools.fine({});
		await tools.plain({});
		let e; try { await tools.echo(5); } catch (error) { e = "rejected"; }
		return [a.exitCode, b.output, e];`), done()})
	opened, root := f.setup.open(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")))
	mustSubmitDone(t, root)
	got, failed := resultText(t, root)
	expectText(t, got, failed, `[2,"out","rejected"]`, false)
	var rows []string
	for _, row := range detailsOf(t, root).Calls {
		rows = append(rows, row.Name+" "+row.Status+" "+row.Error)
	}
	if !slices.Equal(rows[:4], []string{"warned error ", "failed error exit code 2", "fine ok ", "plain ok "}) {
		t.Errorf("rows %q", rows)
	}
	if len(rows) != 5 || !strings.HasPrefix(rows[4], "echo error ") || rows[4] == "echo error " {
		t.Errorf("the call with arguments that are not an object is an error row with its validation text, got %q", rows[len(rows)-1])
	}
	mustClose(t, opened)
}
