// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// nestedCoreHarness opens a native Harness over the core whose registry holds the given tools and whose model calls batch
// once, then answers.
func nestedCoreHarness(t *testing.T, tools ...*durable.ToolRegistration) (harness.Harness, harness.Conversation, durable.Submission) {
	t.Helper()
	return nestedCoreHarnessWith(t, &durable.Extension{Name: "nested", Tools: tools})
}

// nestedCoreHarnessWith is nestedCoreHarness over an extension that may also hold hooks.
func nestedCoreHarnessWith(t *testing.T, extension *durable.Extension, onReport ...func(error, durable.ReportInfo)) (harness.Harness, harness.Conversation, durable.Submission) {
	t.Helper()
	h, root, submit := nestedCoreOpen(t, extension, onReport...)
	return h, root, submit()
}

// nestedCoreHarnessPaused is nestedCoreHarness that submits when told, so a test can watch events first.
func nestedCoreHarnessPaused(t *testing.T, tools ...*durable.ToolRegistration) (harness.Harness, harness.Conversation, func() durable.Submission) {
	t.Helper()
	return nestedCoreOpen(t, &durable.Extension{Name: "nested", Tools: tools})
}

func nestedCoreOpen(t *testing.T, extension *durable.Extension, onReport ...func(error, durable.ReportInfo)) (harness.Harness, harness.Conversation, func() durable.Submission) {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	registry := harness.CreateRegistry()
	if err := registry.Install(extension); err != nil {
		t.Fatal(err)
	}
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("batch", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
	})
	h, err := driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), harness.HarnessOptions{Models: models, Registry: registry, OnReport: append(onReport, nil)[0]})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	return h, root, func() durable.Submission {
		sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")})
		if err != nil {
			t.Fatal(err)
		}
		return sub
	}
}

func nestedCoreTool(name string, execute func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error)) *durable.ToolRegistration {
	return &durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: name, Description: name, Parameters: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}},
		Execute:    execute,
	}
}

// Pi 42a3497d harness-nested-tools.test.ts "runs a nested call as its own tool task and returns its result to the caller,
// not the transcript", over the core: the nested call is a pi.tool task of kind nested owned by the caller, and its result
// reaches the caller as the structured output while the transcript keeps only the model-issued call's result.
func TestCoreRunsANestedCallAsItsOwnToolTask(t *testing.T) {
	var nested durable.NestedToolExecutionResult
	var nestedErr error
	h, root, sub := nestedCoreHarness(t,
		nestedCoreTool("echo", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			text, _ := args.(map[string]any)["text"].(string)
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "echo " + text}}}, nil
		}),
		nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			nested, nestedErr = api.ExecuteTool(ctx, "echo", map[string]any{"text": "a"})
			text, _ := nested.StructuredOutput.(string)
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "got " + text}}}, nil
		}),
	)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	settled, err := sub.Wait(ctx)
	if err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("settled %+v, %v", settled, err)
	}
	if nestedErr != nil || !nested.HasStructuredOutput || nested.StructuredOutput != "echo a" || nested.IsError || len(nested.Diagnostics) != 0 || nested.DurationMs == nil {
		t.Fatalf("nested = %+v, %v", nested, nestedErr)
	}
	page, err := root.Entries(ctx, durable.EntryQuery{}, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	var results []string
	for _, entry := range page.Items {
		if entry.Kind == "pi.tool-result" && len(entry.Model) > 0 {
			if message, ok := entry.Model[0].(ai.ToolResultMessage); ok {
				for _, block := range message.Content {
					if text, isText := block.(ai.TextContent); isText {
						results = append(results, text.Text)
					}
				}
			}
		}
	}
	if len(results) != 1 || !strings.Contains(results[0], "got echo a") {
		t.Fatalf("results = %q, want the model-issued call's alone", results)
	}
	_ = h
}

func nestedCoreWait(t *testing.T, sub durable.Submission, want durable.SubmissionStatus) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	settled, err := sub.Wait(ctx)
	if err != nil || settled.Status != want {
		t.Fatalf("settled %+v, %v", settled, err)
	}
}

func nestedCoreText(text string) durable.ToolExecutionResult {
	return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}
}

func echoCoreTool() *durable.ToolRegistration {
	return nestedCoreTool("echo", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		text, _ := args.(map[string]any)["text"].(string)
		return nestedCoreText("echo " + text), nil
	})
}

// harness-nested-tools.test.ts "runs the tool hooks on nested calls, which see their parent": the hook payload names the
// calling task and call, so a hook can block or rewrite a nested call.
func TestCoreToolHooksSeeTheParentOfANestedCall(t *testing.T) {
	var mu sync.Mutex
	var parents []*harness.ToolParent
	var nested []durable.NestedToolExecutionResult
	extension := &durable.Extension{
		Name: "nested",
		Tools: []*durable.ToolRegistration{
			echoCoreTool(),
			nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				for _, text := range []string{"secret", "public"} {
					result, err := api.ExecuteTool(ctx, "echo", map[string]any{"text": text})
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
					nested = append(nested, result)
				}
				return durable.ToolExecutionResult{}, nil
			}),
		},
		Hooks: []durable.HookRegistration{{Task: harness.ToolTask.AnyDefinition().Name, Handlers: &harness.ToolHooks{
			BeforeTool: func(_ context.Context, call harness.ToolHookCall, _ harness.HookApi) (*harness.BeforeToolResult, error) {
				mu.Lock()
				defer mu.Unlock()
				parents = append(parents, call.Parent)
				if call.Parent != nil && call.Arguments["text"] == "secret" {
					block := "no secrets"
					return &harness.BeforeToolResult{Block: &block}, nil
				}
				return nil, nil
			},
			AfterTool: func(_ context.Context, call harness.ToolHookCall, result durable.ToolExecutionResult, _ harness.HookApi) (*durable.ToolExecutionResult, error) {
				if call.Parent == nil {
					return nil, nil
				}
				result.Output = []ai.ToolResultMessageContent{ai.TextContent{Text: "rewritten"}}
				return &result, nil
			},
		}}},
	}
	_, _, sub := nestedCoreHarnessWith(t, extension)
	nestedCoreWait(t, sub, durable.SubmissionDone)
	mu.Lock()
	defer mu.Unlock()
	if len(parents) != 3 || parents[0] != nil || parents[1] == nil || parents[1].CallId != "c1" || parents[1].TaskId == 0 || *parents[2] != *parents[1] {
		t.Fatalf("hook parents = %+v", parents)
	}
	if len(nested) != 2 || !nested[0].IsError || len(nested[0].Diagnostics) != 1 || nested[0].Diagnostics[0].Code != "blocked" ||
		nested[0].Diagnostics[0].Message != "Tool call blocked: no secrets" || nested[1].StructuredOutput != "rewritten" {
		t.Fatalf("nested = %+v", nested)
	}
}

// "returns an error result for an unavailable tool and invalid arguments".
func TestCoreANestedCallToAnUnavailableToolIsAnErrorResult(t *testing.T) {
	var codes []string
	_, _, sub := nestedCoreHarness(t, echoCoreTool(),
		nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			for _, call := range []struct {
				name string
				args map[string]any
			}{{"missing", map[string]any{}}, {"echo", map[string]any{"text": map[string]any{"not": "a string"}}}} {
				result, err := api.ExecuteTool(ctx, call.name, call.args)
				if err != nil || !result.IsError || len(result.Diagnostics) == 0 {
					return durable.ToolExecutionResult{}, errors.Join(err, errors.New("no error result"))
				}
				codes = append(codes, result.Diagnostics[0].Code)
			}
			return durable.ToolExecutionResult{}, nil
		}))
	nestedCoreWait(t, sub, durable.SubmissionDone)
	if len(codes) != 2 || codes[0] != "tool_unavailable" || codes[1] != "invalid_arguments" {
		t.Fatalf("codes = %v", codes)
	}
}

// "returns the call a key already names, and rejects reusing the key for another call", and a nested call's usage lands
// under its tool.
func TestCoreANestedKeyReturnsItsCallAndRecordsUsage(t *testing.T) {
	var runs atomic.Int32
	usage := ai.Usage{Input: 1, Output: 2, TotalTokens: 3, Cost: ai.UsageCost{Input: 0.1, Output: 0.2, Total: 0.3}}
	var reused durable.NestedToolExecutionResult
	var reuseErr error
	h, root, sub := nestedCoreHarness(t,
		nestedCoreTool("paid", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			runs.Add(1)
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "paid"}}, Usage: &usage}, nil
		}),
		nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			key := "k"
			options := durable.NestedCallOptions{Key: &key}
			if _, err := api.ExecuteTool(ctx, "paid", map[string]any{"text": "a"}, options); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			var err error
			if reused, err = api.ExecuteTool(ctx, "paid", map[string]any{"text": "a"}, options); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			_, reuseErr = api.ExecuteTool(ctx, "paid", map[string]any{"text": "b"}, options)
			return durable.ToolExecutionResult{}, nil
		}))
	nestedCoreWait(t, sub, durable.SubmissionDone)
	if runs.Load() != 1 || reused.StructuredOutput != "paid" || reuseErr == nil || !strings.Contains(reuseErr.Error(), "Nested call c1/k was already made with another tool or other arguments") {
		t.Fatalf("runs %d, reused %+v, reuse error %v", runs.Load(), reused, reuseErr)
	}
	ledger, err := durable.Snapshot[harness.UsageState](t.Context(), h, harness.UsageDoc, root.Id())
	if err != nil || ledger == nil || ledger.Tools["paid"] != usage {
		t.Fatalf("pi.usage.tools = %+v, %v", ledger, err)
	}
}

// "records ... a nested call's structuredOutput that breaks its schema is an error result without it", and a schema-less
// tool's nested result is its output.
func TestCoreANestedCallsStructuredOutput(t *testing.T) {
	schema := nestedCoreTool("typed", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "shown"}}, StructuredOutput: map[string]any{"n": "x"}, HasStructuredOutput: true}, nil
	})
	schema.StructuredOutputSchema = map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}, "required": []any{"n"}}
	var broken, plain durable.NestedToolExecutionResult
	_, _, sub := nestedCoreHarness(t, schema, echoCoreTool(),
		nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			var err error
			if broken, err = api.ExecuteTool(ctx, "typed", map[string]any{}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			plain, err = api.ExecuteTool(ctx, "echo", map[string]any{"text": "a"})
			return durable.ToolExecutionResult{}, err
		}))
	nestedCoreWait(t, sub, durable.SubmissionDone)
	// Pi 97195ec3 tool.ts finalResult: the broken value is dropped, and a schema tool's error result carries no structuredOutput.
	if !broken.IsError || broken.HasStructuredOutput || len(broken.Diagnostics) != 1 || broken.Diagnostics[0].Code != "invalid_structured_output" {
		t.Fatalf("broken = %+v", broken)
	}
	if plain.IsError || plain.StructuredOutput != "echo a" {
		t.Fatalf("plain = %+v", plain)
	}
}

// harness-structured-output.test.ts "gives callers a schema tool's error text as a diagnostic, never as structured output"
// (Pi 1.2.0 test :240-318, tool.ts finalResult :534-546), over the core: a schema tool's error result without structuredOutput
// gives callers its text as one error tool_error diagnostic and no structuredOutput; a thrown error keeps its one tool_error;
// an error result whose own diagnostics say what went wrong gets no second one; an error result with structured output keeps it.
func TestCoreASchemaToolsErrorTextIsADiagnosticNeverStructuredOutput(t *testing.T) {
	yes := true
	count := map[string]any{"type": "object", "properties": map[string]any{"count": map[string]any{"type": "number"}}, "required": []any{"count"}}
	schemaTool := func(name string, execute func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error)) *durable.ToolRegistration {
		tool := nestedCoreTool(name, execute)
		tool.StructuredOutputSchema = count
		return tool
	}
	nope := []ai.ToolResultMessageContent{ai.TextContent{Text: "nope"}}
	fails := schemaTool("fails", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Output: nope, IsError: &yes}, nil
	})
	throws := schemaTool("throws", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{}, errors.New("permission denied")
	})
	explained := schemaTool("explained", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{
			Output:      []ai.ToolResultMessageContent{ai.TextContent{Text: "partial log"}},
			IsError:     &yes,
			Diagnostics: []durable.ToolDiagnostic{{Severity: "error", Code: "quota", Message: "quota exceeded"}},
		}, nil
	})
	kept := schemaTool("kept", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Output: nope, StructuredOutput: map[string]any{"count": 0}, HasStructuredOutput: true, IsError: &yes}, nil
	})
	results := map[string]*durable.NestedToolExecutionResult{}
	_, _, sub := nestedCoreHarness(t, fails, throws, explained, kept,
		nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			for _, name := range []string{"fails", "throws", "explained", "kept"} {
				result, err := api.ExecuteTool(ctx, name, map[string]any{})
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				results[name] = &result
			}
			return durable.ToolExecutionResult{}, nil
		}))
	nestedCoreWait(t, sub, durable.SubmissionDone)
	failed := results["fails"]
	if failed == nil || !failed.IsError || failed.HasStructuredOutput || len(failed.Diagnostics) != 1 ||
		failed.Diagnostics[0] != (durable.ToolDiagnostic{Severity: "error", Code: "tool_error", Message: "nope"}) {
		t.Errorf("fails = %+v, want isError, no structuredOutput, one error tool_error \"nope\"", failed)
	}
	thrown := results["throws"]
	if thrown == nil || !thrown.IsError || thrown.HasStructuredOutput || len(thrown.Diagnostics) != 1 ||
		thrown.Diagnostics[0].Code != "tool_error" || thrown.Diagnostics[0].Message != "permission denied" {
		t.Errorf("throws = %+v, want isError, no structuredOutput, one tool_error \"permission denied\"", thrown)
	}
	explainedResult := results["explained"]
	if explainedResult == nil || len(explainedResult.Diagnostics) != 1 ||
		explainedResult.Diagnostics[0] != (durable.ToolDiagnostic{Severity: "error", Code: "quota", Message: "quota exceeded"}) {
		t.Errorf("explained = %+v, want only its own quota diagnostic", explainedResult)
	}
	keptResult := results["kept"]
	if keptResult == nil || !keptResult.IsError || !keptResult.HasStructuredOutput || mustJSONCore(t, keptResult.StructuredOutput) != `{"count":0}` {
		t.Errorf("kept = %+v, want isError and structuredOutput {\"count\":0}", keptResult)
	}
}

// "aborts nested calls with their caller", over the core's abort handling.
func TestCoreNestedCallsAreAbortedWithTheirCaller(t *testing.T) {
	running := make(chan struct{})
	h, root, sub := nestedCoreHarness(t,
		nestedCoreTool("hang", func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			close(running)
			<-ctx.Done()
			return durable.ToolExecutionResult{}, ctx.Err()
		}),
		nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			_, err := api.ExecuteTool(ctx, "hang", map[string]any{})
			return durable.ToolExecutionResult{}, err
		}))
	<-running
	if err := root.Abort(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	nestedCoreWait(t, sub, durable.SubmissionUnanswered)
	live, err := durable.Snapshot[harness.LiveState](t.Context(), h, harness.LiveDoc, root.Id())
	if err != nil || live == nil || len(live.Tools) != 0 || len(live.NestedTools) != 0 {
		t.Fatalf("live = %+v, %v", live, err)
	}
}

// "aborts the nested calls a caller left running when it returns": the settlement aborts what the call left running.
func TestCoreACallerThatReturnsAbortsTheNestedCallsItLeftRunning(t *testing.T) {
	running := make(chan struct{})
	h, root, sub := nestedCoreHarness(t,
		nestedCoreTool("hang", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			api.Output("partial\n")
			close(running)
			<-ctx.Done()
			return durable.ToolExecutionResult{}, ctx.Err()
		}),
		nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			go func() { _, _ = api.ExecuteTool(ctx, "hang", map[string]any{}) }()
			<-running
			return nestedCoreText("returned early"), nil
		}))
	nestedCoreWait(t, sub, durable.SubmissionDone)
	live, err := durable.Snapshot[harness.LiveState](t.Context(), h, harness.LiveDoc, root.Id())
	if err != nil || live == nil || len(live.Tools) != 0 || len(live.NestedTools) != 0 {
		t.Fatalf("live = %+v, %v", live, err)
	}
}

// harness/tool.ts finalResult: a model-issued call whose structuredOutput breaks the tool's contract keeps its result; the
// host learns of the broken tool (runtime.report).
func TestCoreABrokenStructuredOutputOfAModelIssuedCallIsReported(t *testing.T) {
	var mu sync.Mutex
	var reports []string
	batch := nestedCoreTool("batch", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "shown"}}, StructuredOutput: map[string]any{"n": "x"}, HasStructuredOutput: true}, nil
	})
	batch.StructuredOutputSchema = map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}, "required": []any{"n"}}
	_, root, sub := nestedCoreHarnessWith(t, &durable.Extension{Name: "nested", Tools: []*durable.ToolRegistration{batch}}, func(err error, _ durable.ReportInfo) {
		mu.Lock()
		defer mu.Unlock()
		reports = append(reports, err.Error())
	})
	nestedCoreWait(t, sub, durable.SubmissionDone)
	mu.Lock()
	defer mu.Unlock()
	if len(reports) != 1 || reports[0] != "Tool batch returned structuredOutput that does not match its schema: n: must be number" {
		t.Fatalf("reports = %q", reports)
	}
	page, err := root.Entries(t.Context(), durable.EntryQuery{}, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, entry := range page.Items {
		if entry.Kind == "pi.tool-result" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("the call's result entry is missing")
	}
}

// harness-structured-output.test.ts "validates after afterTool": the host judges the settled result, so an afterTool hook that
// repairs a broken structured output makes it keep the contract, and one that breaks a good output makes it fail.
func TestCoreStructuredOutputIsJudgedAfterAfterTool(t *testing.T) {
	typed := func(name string, value any) *durable.ToolRegistration {
		tool := nestedCoreTool(name, func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "shown"}}, StructuredOutput: value, HasStructuredOutput: true}, nil
		})
		tool.StructuredOutputSchema = map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}, "required": []any{"n"}}
		return tool
	}
	var repaired, spoiled durable.NestedToolExecutionResult
	extension := &durable.Extension{
		Name: "nested",
		Tools: []*durable.ToolRegistration{
			typed("repaired", map[string]any{"n": "x"}),
			typed("spoiled", map[string]any{"n": 1.0}),
			nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				var err error
				if repaired, err = api.ExecuteTool(ctx, "repaired", map[string]any{}); err != nil {
					return durable.ToolExecutionResult{}, err
				}
				spoiled, err = api.ExecuteTool(ctx, "spoiled", map[string]any{})
				return durable.ToolExecutionResult{}, err
			}),
		},
		Hooks: []durable.HookRegistration{{Task: harness.ToolTask.AnyDefinition().Name, Handlers: &harness.ToolHooks{
			AfterTool: func(_ context.Context, call harness.ToolHookCall, result durable.ToolExecutionResult, _ harness.HookApi) (*durable.ToolExecutionResult, error) {
				switch call.Name {
				case "repaired":
					result.StructuredOutput = map[string]any{"n": 2.0}
				case "spoiled":
					result.StructuredOutput = map[string]any{"n": "bad"}
				default:
					return nil, nil
				}
				return &result, nil
			},
		}}},
	}
	_, _, sub := nestedCoreHarnessWith(t, extension)
	nestedCoreWait(t, sub, durable.SubmissionDone)
	if repaired.IsError || !repaired.HasStructuredOutput || mustJSONCore(t, repaired.StructuredOutput) != `{"n":2}` {
		t.Fatalf("repaired = %+v", repaired)
	}
	// Pi 97195ec3: the spoiled value is dropped and nothing replaces it.
	if !spoiled.IsError || spoiled.HasStructuredOutput || len(spoiled.Diagnostics) != 1 || spoiled.Diagnostics[0].Code != "invalid_structured_output" {
		t.Fatalf("spoiled = %+v", spoiled)
	}
}

func mustJSONCore(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// harness-nested-tools.test.ts "reports nested calls ... as tool events with their parent", over the core: agent events work on
// driver.OpenHarness, the nested slots and the nested result reach them, and the events name the parent.
func TestCoreNestedCallsAreToolEventsWithTheirParent(t *testing.T) {
	var mu sync.Mutex
	var summary []string
	var nestedEnd *harness.ToolExecutionEndEvent
	runEnded := false
	h, root, submit := nestedCoreHarnessPaused(t,
		nestedCoreTool("slow", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return nestedCoreText("slow done"), nil
		}),
		nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			result, err := api.ExecuteTool(ctx, "slow", map[string]any{})
			text, _ := result.StructuredOutput.(string)
			return nestedCoreText(text), err
		}))
	stream, err := harness.WatchEvents(t.Context(), h, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	if stream.Snapshot.NestedTools == nil || len(stream.Snapshot.NestedTools) != 0 {
		t.Fatalf("snapshot nestedTools = %#v, want an empty list", stream.Snapshot.NestedTools)
	}
	stream.Start(func(_ context.Context, batch []harness.AgentEvent) error {
		mu.Lock()
		defer mu.Unlock()
		for _, event := range batch {
			switch typed := event.(type) {
			case harness.RunEndEvent:
				runEnded = true
			case harness.ToolExecutionStartEvent:
				summary = append(summary, "start "+typed.ToolCallId+" <"+typed.ParentToolCallId+">")
			case harness.ToolExecutionEndEvent:
				summary = append(summary, "end "+typed.ToolCallId+" <"+typed.ParentToolCallId+">")
				if typed.ToolCallId == "c1/1" {
					nestedEnd = &typed
				}
			}
		}
		return nil
	})
	nestedCoreWait(t, submit(), durable.SubmissionDone)
	if err := root.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The stream delivers on its own goroutine, so idle does not mean delivered: wait for the run's end before stopping,
	// which drops undelivered batches.
	pollUntil(t, "the run's end event", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return runEnded
	})
	_, _ = stream.Stop()
	mu.Lock()
	defer mu.Unlock()
	want := []string{"start c1 <>", "start c1/1 <c1>", "end c1/1 <c1>", "end c1 <>"}
	if !slices.Equal(summary, want) {
		t.Fatalf("tool events = %q, want %q", summary, want)
	}
	if nestedEnd == nil || nestedEnd.Result == nil || nestedEnd.Result.StructuredOutput != "slow done" || nestedEnd.TaskId == nil || nestedEnd.ParentTaskId == nil {
		t.Fatalf("nested end = %+v", nestedEnd)
	}
}
