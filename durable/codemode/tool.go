// Ports packages/durable/src/codemode/codemode.ts (createCodemodeTool, createCodemodeExtension, outcomeOf, toolResult).

// Package codemode is durable code mode: a tool that runs model-written JavaScript in a QuickJS sandbox, whose script
// survives a restart.
//
// A script is in-memory VM state. A restart while
//
//	const results = await Promise.all(paths.map((path) => tools.read({ path })));
//
// waits for the last of 200 reads would lose the script and every read it made.
//
// The solution is deterministic replay (Sandbox.Start in package codemode). The VM only runs when the host hands it a
// result; given the same start data, clock, seed, and results in the same order, it makes the same calls with the same
// arguments. The tool is replay-safe: after a restart, Execute runs again, starts the script again from the stored start
// data, and hands it the results it got before, in the order it got them, from a journal written before each delivery.
// Nested tool calls reattach to their tasks by key (`c<VM call id>`), so they never run twice unless their own replay
// policy reruns them; their results come from NestedCallDoc. Then the script continues live.
//
// Documents, scoped to the code mode call's task and gone when it settles:
//   - RunDoc: the start data, written once before the VM starts, and the outcome, written once when the call decides it
//     (with the store writes, in one transaction), so a rerun after that returns it without a VM.
//   - JournalDoc: one entry per delivered result, in delivery order: the VM call ID, the clock value, the calls the VM had
//     issued after processing it (once acknowledged), and a global's outcome, which nothing else stores.
//
// Each result's acknowledged call count is recorded with the next journal commit, or at once when the VM has processed
// everything delivered: a script waiting on a long call is where a restart usually finds it. A sequential script
// therefore costs one journal commit and one count commit per result, on top of its nested calls' own; that is the price
// of checking the most common crash point.
//
// A call is launched only once the count of calls issued in the drain that made it is committed. So every key an earlier
// attempt admitted is at most its last recorded count; a replay checks every recorded count, so when it releases its
// calls it has made every call an earlier attempt could have admitted, and later calls are new.
//
// A rerun refuses to replay (no new nested calls; nested calls admitted earlier may still resume on their own and are
// aborted by the call's cleanup) when this package's version or the VM's fingerprint changed, when the deadline passed,
// or when the script diverges: it makes another call, or calls in another order, than the journal says. Each ends the call
// with an error result.
package codemode

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Sandbox runs the scripts: a *codemode.Sandbox, or anything that starts deterministic runs as it does.
type Sandbox interface {
	Start(ctx context.Context, code string, options cm.RunOptions) (*cm.Run, error)
}

// Global is a function scripts call as a top-level global; its result is journaled.
type Global struct {
	Name   string
	Spread bool
	// Execute runs the call. args is what the script passed (nil: undefined); the result is JSON, nil for undefined.
	Execute func(ctx context.Context, args json.RawMessage, api durable.ToolExecutionApi) (json.RawMessage, error)
}

// ToolOptions are the options of the code mode tool.
type ToolOptions struct {
	// Sandbox runs the scripts.
	Sandbox Sandbox
	// Namespaces are groups of tools by name prefix, such as MCP servers, for the `codemode` section and tool search.
	Namespaces []Namespace
	// InlineBudget is the estimated tokens the tool declarations in the `codemode` section may use. Nil: 3000.
	InlineBudget *int
	// Models gives scripts `models.*`: the model catalog, classifiers, and image generation. Nil: true.
	Models *bool
	// Globals are host functions scripts call as globals.
	Globals []Global
}

func (o ToolOptions) models() bool { return o.Models == nil || *o.Models }

// outputLimits bound the tool's output by the script's own token budget; the Harness's limits only stop a runaway.
var toolOutputLimits = outputLimits{maxBytes: 4 * 1024 * 1024, maxLines: 100_000}

// discoveryGlobals are globals for finding tools, answered from the start data, so a replay sees what the first run saw.
var discoveryGlobals = []string{"searchTools", "describeTool", "describeNamespace"}

const defaultSearchLimit = 8

// ToolInput is the input of the code mode tool.
type ToolInput struct {
	Code string `json:"code"`
}

const parameters = `{"type":"object","properties":{"code":{"type":"string","description":"Raw JavaScript source."}},"required":["code"]}`

// CreateTool creates the code mode tool. Scripts call the conversation's callable tools as `tools.<name>(args)`.
func CreateTool(options ToolOptions) *durable.ToolRegistration {
	schema := mustSchema(ToolName, Description(options.models()))
	// Capable models write the script as raw text instead of a JSON-escaped string; pi-ai applies it only for them.
	schema.ConstrainedSampling = &ai.ConstrainedSamplingConfig{Type: ai.ConstrainedSamplingGrammar, Variants: ai.GrammarVariants{"openai_lark": cm.CodemodeSourceGrammar}}
	maxBytes, maxLines := toolOutputLimits.maxBytes, toolOutputLimits.maxLines
	return harness.DefineTool(durable.ToolRegistration{
		ToolSchema:   schema,
		OutputLimits: &durable.ToolOutputLimits{MaxBytes: &maxBytes, MaxLines: &maxLines},
		Callers:      []durable.ToolCaller{durable.CallerModel},
		Replay:       durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			input, err := durable.FromJsonValue[ToolInput](args)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return newCall(options, api, ctx).execute(input.Code)
		},
	})
}

// CreateExtension creates the `codemode` extension: the code mode tool, and the `codemode` prompt section listing the
// tools scripts can call.
func CreateExtension(options ToolOptions) *durable.Extension {
	tools := []*durable.ToolRegistration{CreateTool(options)}
	if options.models() {
		tools = append(tools, createModelTools()...)
	}
	budget := DefaultInlineBudget
	if options.InlineBudget != nil {
		budget = *options.InlineBudget
	}
	return &durable.Extension{Name: "codemode", Tools: tools, Sections: []*durable.PromptSection{
		harness.Section("codemode", func(_ context.Context, input durable.PromptInput) (*string, error) {
			var callable []*durable.ToolRegistration
			for _, tool := range input.Agent.Callable {
				// Deferred tools are found with searchTools(), as the coding agent leaves them out too.
				if !strings.HasPrefix(tool.Name, InternalPrefix) && !tool.Deferred {
					callable = append(callable, tool)
				}
			}
			return Section(callable, options.Namespaces, budget)
		}),
	}}
}

func mustSchema(name, description string) ai.ToolSchema {
	return mustSchemaOf(name, description, parameters)
}

func mustSchemaOf(name, description, parameters string) ai.ToolSchema {
	encoded, err := json.Marshal(struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}{name, description, json.RawMessage(parameters)})
	if err != nil {
		panic(err)
	}
	var schema ai.ToolSchema
	//portlint:allow jsonclone builds a tool schema once at tool construction; ToolSchema.UnmarshalJSON records the parameters' member order
	if err := json.Unmarshal(encoded, &schema); err != nil { //perf:ok builds a tool schema once at tool construction
		panic(err)
	}
	return schema
}

// outcomeOf is what a script gets for a nested call, as in the coding agent. A tool with a schema resolves with its value,
// also on an error result that carries one (`bash`'s `{ output, exitCode }` for a nonzero exit); a schema-less tool
// resolves with its text output, images left out, as its declaration says. Any other error rejects, with its text: a
// schema-less tool's output, else its error diagnostics.
func outcomeOf(name string, structured bool, result durable.NestedToolExecutionResult) cm.Outcome {
	// A round trip through JSON: the value is the one a stored result holds, whatever Go type the tool produced.
	var value any
	var encoded json.RawMessage
	if result.HasStructuredOutput {
		if raw, err := json.Marshal(result.StructuredOutput); err == nil {
			encoded = raw
			//portlint:allow jsonclone the value must be the stored strict-JSON form a replay reads, whatever Go type the tool produced
			_ = json.Unmarshal(raw, &value) //perf:ok once per nested tool call: the value must be the stored strict-JSON form, whatever Go type the tool produced
		}
	}
	text := ""
	if !structured {
		text = textOf(value)
		encoded, _ = json.Marshal(text)
	}
	if !result.IsError || (structured && encoded != nil) {
		return cm.Outcome{OK: true, Value: encoded}
	}
	message := text
	var diagnostics []string
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Severity == "error" {
			diagnostics = append(diagnostics, diagnostic.Message)
		}
	}
	if message == "" {
		message = strings.Join(diagnostics, "\n")
	}
	if message == "" {
		message = "Tool " + name + " failed"
	}
	outcome := cm.Outcome{Message: message}
	if encoded != nil {
		outcome.Properties = append(append(json.RawMessage(`{"structuredOutput":`), encoded...), '}')
	}
	return outcome
}

// textOf is the text of a schema-less tool's collapsed output: a string, one image, or an array of text and image blocks.
func textOf(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		var texts []string
		for _, item := range v {
			if object, ok := item.(map[string]any); ok && object["type"] == "text" {
				text, _ := object["text"].(string)
				texts = append(texts, text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

// storedContent is one output item of a stored result.
type storedContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// storedResult is a tool result as the run document keeps it.
type storedResult struct {
	Output  []storedContent `json:"output"`
	IsError bool            `json:"isError"`
	Details json.RawMessage `json:"details,omitempty"`
}

func (stored storedResult) result() durable.ToolExecutionResult {
	output := make([]ai.ToolResultMessageContent, 0, len(stored.Output))
	for _, item := range stored.Output {
		if item.Type == "image" {
			output = append(output, ai.ImageContent{Data: item.Data, MimeType: item.MimeType})
		} else {
			output = append(output, ai.TextContent{Text: item.Text})
		}
	}
	isError := stored.IsError
	result := durable.ToolExecutionResult{Output: output, IsError: &isError}
	if stored.Details != nil {
		var details any
		if json.Unmarshal(stored.Details, &details) == nil {
			result.Details, result.HasDetails = details, true
		}
	}
	return result
}

// contentOf is the stored form of formatted content.
func contentOf(content []ai.ToolResultMessageContent) []storedContent {
	output := make([]storedContent, 0, len(content))
	for _, item := range content {
		switch item := item.(type) {
		case ai.ImageContent:
			output = append(output, storedContent{Type: "image", Data: item.Data, MimeType: item.MimeType})
		case ai.TextContent:
			output = append(output, storedContent{Type: "text", Text: item.Text})
		}
	}
	return output
}

func errorResult(message string) storedResult {
	return storedResult{Output: []storedContent{{Type: "text", Text: message}}, IsError: true}
}
