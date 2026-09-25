package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// printModeTestHost builds a print-mode runtime whose session talks to
// provider in fresh, isolated directories.
func printModeTestHost(t *testing.T, provider ai.Provider, tools ...agent.AgentTool) printModeRuntime {
	t.Helper()
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return printModeRuntime{
		Services: services,
		Session: coding.SessionStartOptions{
			Model: &ai.Model{
				ID: "faux-1", Provider: provider,
				ProviderMeta: ai.ProviderMetadata{ProviderID: provider.ID()},
				Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 8192, SupportsToolUse: true},
			},
			SkipBuiltinTools: true,
			ExtraTools:       tools,
			SessionDir:       t.TempDir(),
		},
	}
}

// printModeTestResult is one in-process print-mode run.
type printModeTestResult struct {
	stdout, stderr string
	err            error
}

// runPrintModeForTest runs print mode in process and fails the test if it
// does not return within the test budget.
func runPrintModeForTest(t *testing.T, host printModeRuntime, opts printModeOptions) printModeTestResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	opts.stdout, opts.stderr = &stdout, &stderr
	done := make(chan error, 1)
	go func() { done <- runPrintMode(context.Background(), host, opts) }()
	select {
	case err := <-done:
		return printModeTestResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("print mode did not return: the run is deadlocked")
		return printModeTestResult{}
	}
}

// Print and JSON mode call AgentSession.prompt upstream, so each initial and
// additional prompt reaches input handlers once with the interactive source.
// A handled prompt never reaches the provider.
func TestPrintAndJSONInputHandlersConsumeEveryPrompt(t *testing.T) {
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			var seen []extension.InputEvent
			host := printModeTestHost(t, provider)
			host.Extensions = []extension.Extension{{Path: "input-probe", Handlers: map[string][]extension.HandlerFn{
				"input": {func(args ...any) (any, error) {
					event := args[0].(extension.InputEvent)
					seen = append(seen, event)
					return extension.InputEventResultHandled{}, nil
				}},
			}}}
			result := runPrintModeForTest(t, host, printModeOptions{
				Mode: mode, InitialMessage: "initial", Messages: []string{"additional"},
			})
			if result.err != nil || result.stderr != "" {
				t.Fatalf("run = %v, stderr %q", result.err, result.stderr)
			}
			if calls := provider.CallCount(); calls != 0 {
				t.Fatalf("provider calls = %d, want 0 for handled prompts", calls)
			}
			if len(seen) != 2 {
				t.Fatalf("input handler calls = %d, want 2", len(seen))
			}
			for i, want := range []string{"initial", "additional"} {
				if seen[i].Text != want || seen[i].Source != extension.InputSourceUser || seen[i].StreamingBehavior != "" {
					t.Fatalf("input event %d = %+v", i, seen[i])
				}
			}
		})
	}
}

// A text-only input transform retains the caller's images, matching
// `_runInputHandlers`'s `inputResult.images ?? images`.
func TestPrintInputTransformRetainsImages(t *testing.T) {
	captured := make(chan []ai.Message, 1)
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(context ai.TranscriptContext, _ ai.StreamOptions, _ int) ai.FauxResponse {
		captured <- context.Messages()
		return fauxTextResponse("done")
	})})
	var encoded bytes.Buffer
	pixels := image.NewRGBA(image.Rect(0, 0, 1, 1))
	pixels.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&encoded, pixels); err != nil {
		t.Fatal(err)
	}
	image := ai.ImageContent{Data: base64.StdEncoding.EncodeToString(encoded.Bytes()), MimeType: "image/png"}
	var input extension.InputEvent
	host := printModeTestHost(t, provider)
	host.Extensions = []extension.Extension{{Path: "input-transform", Handlers: map[string][]extension.HandlerFn{
		"input": {func(args ...any) (any, error) {
			input = args[0].(extension.InputEvent)
			return extension.InputEventResultTransform{Text: "transformed"}, nil
		}},
	}}}
	result := runPrintModeForTest(t, host, printModeOptions{
		Mode: "text", InitialMessage: "original", InitialImages: []ai.ImageContent{image},
	})
	if result.err != nil || result.stdout != "done\n" || result.stderr != "" {
		t.Fatalf("run = %v, stdout %q, stderr %q", result.err, result.stdout, result.stderr)
	}
	if input.Text != "original" || input.Source != extension.InputSourceUser || len(input.Images) != 1 {
		t.Fatalf("input event = %+v", input)
	}
	messages := <-captured
	user, ok := messages[len(messages)-1].(ai.UserMessage)
	if !ok {
		t.Fatalf("last provider message = %T, want ai.UserMessage", messages[len(messages)-1])
	}
	blocks, ok := user.Content.(ai.UserContentBlocks)
	if !ok || len(blocks) != 2 {
		t.Fatalf("user content = %#v, want transformed text and original image", user.Content)
	}
	if text, ok := blocks[0].(ai.TextContent); !ok || text.Text != "transformed" {
		t.Fatalf("text block = %#v", blocks[0])
	}
	if got, ok := blocks[1].(ai.ImageContent); !ok || got.MimeType != image.MimeType || got.Data == "" {
		t.Fatalf("image block = %#v, want retained PNG", blocks[1])
	}
}

// TestJSONModeEventConversionFailureFailsTheRun pins upstream print-mode.ts
// json output: toJsonEvent throwing inside the session subscriber rejects
// prompt(), and print mode reports the error and exits 1. Pig's JSON forwarder
// returned on the first conversion error and stopped reading the event
// channel, so a run with more events than the buffers hold blocked the agent's
// emit forever.
func TestJSONModeEventConversionFailureFailsTheRun(t *testing.T) {
	// Thousands of one-character deltas: far more events than the session's
	// event buffers hold.
	provider := ai.NewFauxProvider(ai.FauxConfig{MinTokenSize: 1, MaxTokenSize: 1})
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{
		Content: []ai.FauxContentBlock{ai.FauxText(strings.Repeat("x", 4000))}, StopReason: "stop",
	})})
	result := runPrintModeForTest(t, printModeTestHost(t, provider), printModeOptions{
		Mode: "json", InitialMessage: "stream a long answer",
		convertEvent: func(ev agent.AgentEvent) ([]any, error) {
			if _, ok := ev.(agent.MessageUpdateEvent); ok {
				return nil, errors.New("cannot encode message_update")
			}
			return rpcAgentEvent(ev)
		},
	})
	if !errors.Is(result.err, errPrintModeHandled) || result.stderr != "convert session event: cannot encode message_update\n" {
		t.Fatalf("err = %v, stderr %q; want exit 1 reporting the event conversion failure", result.err, result.stderr)
	}
}

// TestPrintModeReportsRunErrorsVerbatim pins upstream print-mode.ts's catch,
// console.error(error.message) and exit 1. Pig replaced any failure whose
// text contained "401", "Bad credentials", or "refresh failed" with a
// made-up "authentication expired: re-authenticate via /login in the TUI",
// hiding the real error.
func TestPrintModeReportsRunErrorsVerbatim(t *testing.T) {
	const failure = "HTTP 401 from upstream: Bad credentials (request 401-abc)"
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses(fauxSteps(fauxTextResponse("answer")))
	result := runPrintModeForTest(t, printModeTestHost(t, provider), printModeOptions{
		Mode: "json", InitialMessage: "say something",
		convertEvent: func(ev agent.AgentEvent) ([]any, error) {
			if _, ok := ev.(agent.MessageEndEvent); ok {
				return nil, errors.New(failure)
			}
			return rpcAgentEvent(ev)
		},
	})
	if !errors.Is(result.err, errPrintModeHandled) || !strings.HasSuffix(result.stderr, failure+"\n") || strings.Contains(result.stderr, "authentication expired") {
		t.Fatalf("err = %v, stderr %q; want the failure's own text", result.err, result.stderr)
	}
}

// printTestTool is a sequential tool that always succeeds.
type printTestTool struct{}

func (printTestTool) Name() string                           { return "print_test_tool" }
func (printTestTool) Label() string                          { return "print test tool" }
func (printTestTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeSequential }
func (printTestTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "print_test_tool", Parameters: map[string]any{"type": "object"}}
}
func (printTestTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{Content: "tool output"}, nil
}

func fauxSteps(responses ...ai.FauxResponse) []ai.FauxResponseStep {
	steps := make([]ai.FauxResponseStep, len(responses))
	for i, response := range responses {
		steps[i] = ai.FauxStaticStep(response)
	}
	return steps
}

func fauxTextResponse(text string) ai.FauxResponse {
	return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}, StopReason: "stop"}
}

// TestPrintModePrintsOnlyTheFinalMessage pins upstream print-mode.ts text
// output: only the text of the session's last message. Pig printed the text
// of every assistant message of the run, so tool-use narration reached
// stdout ahead of the answer.
func TestPrintModePrintsOnlyTheFinalMessage(t *testing.T) {
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses(fauxSteps(
		ai.FauxResponse{Content: []ai.FauxContentBlock{
			ai.FauxText("I'll run the tool first."),
			ai.FauxToolCall("print_test_tool", map[string]any{}, "print-call-1"),
		}, StopReason: "toolUse"},
		fauxTextResponse("final answer"),
	))
	result := runPrintModeForTest(t, printModeTestHost(t, provider, printTestTool{}), printModeOptions{
		Mode: "text", InitialMessage: "use the tool",
	})
	if result.err != nil {
		t.Fatalf("err = %v, stderr %q", result.err, result.stderr)
	}
	if result.stdout != "final answer\n" {
		t.Fatalf("stdout = %q, want only the final message %q", result.stdout, "final answer\n")
	}
}

// TestPrintModePrintsAnswerAfterOverflowRecovery pins upstream print-mode.ts
// reading the last session message after prompt() resolves. A context
// overflow compacts the session and retries; pig sliced the messages the run
// returned at the pre-run length, which no longer lines up after compaction,
// so it printed nothing and exited 0.
func TestPrintModePrintsAnswerAfterOverflowRecovery(t *testing.T) {
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	var workTurns, summaries int
	overflowed := false
	respond := func(request ai.TranscriptContext) ai.FauxResponse {
		messages := request.Messages()
		if last, _ := json.Marshal(messages[len(messages)-1]); strings.Contains(string(last), "checkpoint") {
			summaries++
			return fauxTextResponse("## Goal\nsummary of the earlier work")
		}
		switch {
		case workTurns < 4:
			workTurns++
			return ai.FauxResponse{Content: []ai.FauxContentBlock{
				ai.FauxText(strings.Repeat("working notes ", 2500)),
				ai.FauxToolCall("print_test_tool", map[string]any{}, ""),
			}, StopReason: "toolUse"}
		case !overflowed:
			overflowed = true
			return ai.FauxResponse{StopReason: "error", ErrorMessage: "prompt is too long: 500000 tokens > 200000 maximum"}
		default:
			return fauxTextResponse("recovered answer")
		}
	}
	steps := make([]ai.FauxResponseStep, 16)
	for i := range steps {
		steps[i] = ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ int) ai.FauxResponse {
			return respond(request)
		})
	}
	provider.SetResponses(steps)

	result := runPrintModeForTest(t, printModeTestHost(t, provider, printTestTool{}), printModeOptions{Mode: "text", InitialMessage: "do the long task"})
	if result.err != nil {
		t.Fatalf("err = %v, stderr %q", result.err, result.stderr)
	}
	if !overflowed || summaries == 0 {
		t.Fatalf("overflowed=%v summaries=%d: the run did not compact after the overflow", overflowed, summaries)
	}
	if result.stdout != "recovered answer\n" {
		t.Fatalf("stdout = %q, want the answer after overflow recovery %q", result.stdout, "recovered answer\n")
	}
}
