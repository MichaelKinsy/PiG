package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ─── Canned responses for compaction / branch summarization ──────────────────
// These must be identical in ai/test_faux.go (pig) and
// parity/testdata/test-faux-provider.ts (upstream pi).

const testFauxSummaryResponse = `## Goal
The user explored Go programming language features and error handling patterns.

## Constraints & Preferences
- (none)

## Progress
### Done
- [x] Discussed Go language features (goroutines, channels, GC, interfaces)
- [x] Covered error handling patterns (sentinel, wrapping, Is/As)

### In Progress
- [ ] (none)

### Blocked
- (none)

## Key Decisions
- (none)

## Next Steps
1. Continue exploring Go topics as needed

## Critical Context
- (none)`

const testFauxBranchSummaryResponse = `## Goal
The user was exploring a conversation branch.

## Constraints & Preferences
- (none)

## Progress
### Done
- [x] Explored branch topic

### In Progress
- [ ] (none)

### Blocked
- (none)

## Key Decisions
- (none)

## Next Steps
1. Return to main branch

## Critical Context
- (none)`

const testFauxTurnPrefixSummaryResponse = `## Original Request
The user asked a complex question requiring multi-step analysis.

## Early Progress
- Started analyzing the request

## Context for Suffix
- Analysis is ongoing`

// TestFauxProvider is a deterministic, test-only provider used by the
// integration parity harness. It is deliberately NOT part of the normal model
// catalog and is only enabled when the binary is launched with the
// PIG_TEST_FAUX=1 environment variable.
//
// Why this exists: live-model parity gates in tmux are prone to model/provider
// nondeterminism. Upstream is the source of truth, so parity gates must use a
// deterministic oracle when the live provider does not reliably materialize a
// scenario. This provider gives pig a stable scripted backend while upstream
// gets the same scripted behavior via a temp extension that registers an
// equivalent custom provider.
//
// Supported scenario values (via PIG_TEST_FAUX_SCENARIO):
//   - parity-basic
//
// Any other value returns an EventError.
// Test-faux model limits match the model the upstream side of the parity
// harness registers (parity/testdata/test-faux-provider.ts), so both sides
// report the same context window and output budget.
const (
	TestFauxContextWindow = 128000
	TestFauxMaxTokens     = 4096
)

// Tool-call IDs use a deterministic counter per StreamOptions.SessionID. An omitted session ID shares the provider's default counter; resumed history seeds a new counter.
type TestFauxProvider struct {
	Scenario string

	toolCallMu       sync.Mutex
	toolCallCounters map[string]uint64
}

// TestFauxToolStartedMarker is created in the working directory by the
// "Run: sleep for a while" scenario's tool as soon as it starts running, so a
// test can wait for the run to reach tool execution rather than sleeping for a
// guessed interval.
const TestFauxToolStartedMarker = ".pig-faux-tool-started"

var testFauxRetryState sync.Map

type testFauxToolCall struct {
	Name string
	Args map[string]any
}

func (p *TestFauxProvider) ID() string   { return "test-faux" }
func (p *TestFauxProvider) Close() error { return nil }

func (p *TestFauxProvider) Stream(ctx context.Context, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("test-faux: invalid transcript: %w", err)
	}
	builder := newAssistantStreamBuilder(ctx, "faux", p.ID(), "faux-1")
	messages := transcript.Messages()
	go func() {
		if err := ctx.Err(); err != nil {
			builder.fail(StopReasonAborted, err)
			return
		}
		scenario := p.Scenario
		if scenario == "" {
			scenario = "parity-basic"
		}
		if scenario != "parity-basic" {
			builder.fail(StopReasonError, fmt.Errorf("test-faux: unsupported scenario %q", scenario))
			return
		}

		lastText := ""
		if len(messages) > 0 {
			lastText = messageText(messages[len(messages)-1])
		}
		if strings.Contains(lastText, "TUI_LIVE_STREAM") {
			for i := 1; i <= 24; i++ {
				if err := ctx.Err(); err != nil {
					builder.fail(StopReasonAborted, err)
					return
				}
				builder.textDelta(fmt.Sprintf("LIVE-STREAM-%02d\n", i))
				if i == 8 && strings.Contains(lastText, "TUI_LIVE_STREAM_PAUSE") {
					time.Sleep(time.Second)
					continue
				}
				time.Sleep(40 * time.Millisecond)
			}
			builder.done(StopReasonStop, nil, "")
			return
		}
		if strings.Contains(lastText, "Trigger: retryable provider error") {
			if _, loaded := testFauxRetryState.LoadOrStore("retryable-provider-error", true); !loaded {
				builder.fail(StopReasonError, errors.New("please retry your request"))
				return
			}
			builder.textDelta("retry-ok")
			builder.done(StopReasonStop, nil, "")
			return
		}

		slowCompactionProbe := os.Getenv("TEST_FAUX_SLOW_COMPACTION") == "1" &&
			(strings.Contains(lastText, "Create a concise checkpoint of the user's request and the progress shown above.") ||
				strings.Contains(lastText, "Create a structured context checkpoint summary") ||
				strings.Contains(lastText, "NEW conversation messages to incorporate into the existing summary"))
		slowOverflowSummary := os.Getenv("TUI_LIVE_PROBE") == "1" &&
			strings.Contains(lastText, "Create a structured context checkpoint summary") ||
			strings.Contains(lastText, "Create a structured context checkpoint summary") &&
				strings.Contains(historyText(messages), "Trigger: overflow error with queued message")
		if slowCompactionProbe || slowOverflowSummary {
			builder.start()
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				builder.fail(StopReasonAborted, errors.New("This operation was aborted"))
				return
			}
		}

		kind, text, toolCalls := classifyTestFauxRequest(messages)
		switch kind {
		case "text":
			builder.textDelta(text)
			if text == "over-window-ok" {
				builder.done(StopReasonStop, &Usage{Input: 130000, Output: 1000, TotalTokens: 131000}, "")
			} else {
				builder.done(StopReasonStop, &Usage{}, "")
			}
		case "tool":
			firstID := p.reserveToolCallIDs(options.SessionID, messages, len(toolCalls))
			for index, call := range toolCalls {
				arguments, _ := json.Marshal(call.Args)
				builder.toolCallDelta(streamToolCallDelta{
					index: index, id: fmt.Sprintf("call_test_faux_%d", firstID+uint64(index)), name: call.Name, argumentsDelta: string(arguments),
				})
			}
			builder.done(StopReasonToolUse, nil, "")
		case "error":
			builder.fail(StopReasonError, errors.New(text))
		default:
			builder.fail(StopReasonError, fmt.Errorf("test-faux: unknown kind %q", kind))
		}
	}()
	return builder.stream, nil
}

// Reserve a whole batch under one lock so concurrent requests cannot interleave its IDs. Keep only counters, not transcripts, for the provider's lifetime.
func (p *TestFauxProvider) reserveToolCallIDs(sessionID string, messages []Message, count int) uint64 {
	p.toolCallMu.Lock()
	defer p.toolCallMu.Unlock()
	if p.toolCallCounters == nil {
		p.toolCallCounters = make(map[string]uint64)
	}
	last, exists := p.toolCallCounters[sessionID]
	if !exists {
		seed := func(id string) {
			if suffix, ok := strings.CutPrefix(id, "call_test_faux_"); ok {
				if n, err := strconv.ParseUint(suffix, 10, 64); err == nil {
					last = max(last, n)
				}
			}
		}
		for _, message := range messages {
			switch message := message.(type) {
			case AssistantMessage:
				for _, block := range message.Content {
					if call, ok := block.(ToolCall); ok {
						seed(call.ID)
					}
				}
			case ToolResultMessage:
				seed(message.ToolCallID)
			}
		}
	}
	p.toolCallCounters[sessionID] = last + uint64(count)
	return last + 1
}

func classifyTestFauxRequest(msgs []Message) (kind, text string, toolCalls []testFauxToolCall) {
	if len(msgs) == 0 {
		return "error", "test-faux: empty message list", nil
	}
	last := msgs[len(msgs)-1]
	lastText := messageText(last)
	lastImages := messageImages(last)
	historyText := historyText(msgs)
	currentUserText := ""
	for _, message := range slices.Backward(msgs) {
		if _, ok := message.(UserMessage); ok {
			currentUserText = messageText(message)
			break
		}
	}

	// Compaction summarization: triggered by /compact or auto-compaction.
	// The summarization prompt contains this verbatim string from
	// compaction.go SUMMARIZATION_PROMPT / UPDATE_SUMMARIZATION_PROMPT.
	if strings.Contains(lastText, "Create a structured context checkpoint summary") ||
		strings.Contains(lastText, "NEW conversation messages to incorporate into the existing summary") {
		return "text", testFauxSummaryResponse, nil
	}
	// Branch summarization: triggered by tree navigation with summarize=true.
	if strings.Contains(lastText, "Create a structured summary of this conversation branch") {
		return "text", testFauxBranchSummaryResponse, nil
	}
	// Split-turn prefix summarization.
	if strings.Contains(lastText, "Create a concise checkpoint of the user's request and the progress shown above.") {
		return "text", testFauxTurnPrefixSummaryResponse, nil
	}

	// Deterministic Porter Profile activation probe. This response is used only
	// by the local headless smoke and does not stand in for closure evidence.
	if strings.Contains(lastText, "PORTER_HEADLESS_VERIFY") && strings.Contains(lastText, "/skill:pig-porter") {
		return "text", "pig-porter-headless-ok", nil
	}

	// Session-wire prompt.
	if strings.Contains(lastText, "What is 20+22?") {
		return "text", "42", nil
	}

	// Multi-turn memory, turn 1.
	if strings.Contains(lastText, "Remember this exact code:") {
		return "text", "Alpha", nil
	}

	// Multi-turn memory, turn 2: require the prior turn to be present in
	// history so the test proves context carry-over rather than a hardcoded
	// response.
	if strings.Contains(lastText, "What was the code I told you to remember?") {
		if strings.Contains(historyText, "letters A L P H A") && strings.Contains(historyText, "digits 7 7 4 9") {
			return "text", "ALPHA-7749", nil
		}
		return "error", "test-faux: missing memory context for code recall", nil
	}

	if strings.Contains(lastText, `<file name="`) && strings.Contains(lastText, "CLI_FILE_PAYLOAD") && strings.Contains(lastText, "prompt-after-file") {
		return "text", "cli-file-ok", nil
	}
	if strings.Contains(lastText, `</file>`) && strings.Contains(lastText, "describe sample image") {
		if len(lastImages) == 1 && lastImages[0].MimeType == "image/png" {
			return "text", "cli-image-mime-ok", nil
		}
		return "error", fmt.Sprintf("test-faux: expected one image/png attachment, got %+v", lastImages), nil
	}
	if strings.Contains(lastText, "original 4000x2000, displayed at 2000x1000") && strings.Contains(lastText, "describe resized image") {
		if len(lastImages) == 1 && lastImages[0].MimeType == "image/png" {
			return "text", "cli-image-resize-ok", nil
		}
		return "error", fmt.Sprintf("test-faux: expected resized image/png attachment, got %+v", lastImages), nil
	}

	if strings.Contains(lastText, "PT_ARGS:first|two words") {
		return "text", "prompt-template-ok", nil
	}

	if strings.Contains(lastText, "<skill name=\"sample-skill\"") && strings.Contains(lastText, "extra words") {
		return "text", "skill-command-ok", nil
	}

	// Tool execution parity.
	if strings.Contains(lastText, "Run: expr 20 + 22") {
		return "tool", "", []testFauxToolCall{{
			Name: "bash",
			Args: map[string]any{"command": "expr 20 + 22"},
		}}
	}

	// Long-running tool, for tests that must signal or abort a run while a
	// tool is still executing. The marker file lets a test wait until the tool
	// is actually running instead of guessing a delay.
	if strings.Contains(lastText, "Run: sleep for a while") {
		return "tool", "", []testFauxToolCall{{
			Name: "bash",
			Args: map[string]any{"command": "touch " + TestFauxToolStartedMarker + "; sleep 45"},
		}}
	}

	// Read tool parity.
	if strings.Contains(lastText, "Run: read parity-read-target.txt") {
		return "tool", "", []testFauxToolCall{{
			Name: "read",
			Args: map[string]any{"path": "parity-read-target.txt"},
		}}
	}

	// Write tool parity.
	if strings.Contains(lastText, "Run: write parity-write-output.txt") {
		return "tool", "", []testFauxToolCall{{
			Name: "write",
			Args: map[string]any{
				"path":    "parity-write-output.txt",
				"content": "hello from faux\nline two\n",
			},
		}}
	}

	// Same-file write+edit batch parity. Upstream runs both calls in the
	// default parallel tool batch, so file-mutation-queue.ts must preserve
	// order for the edit to observe the freshly-written content.
	if strings.Contains(lastText, "Run: write then edit parity-batched-target.txt") {
		return "tool", "", []testFauxToolCall{
			{
				Name: "write",
				Args: map[string]any{
					"path":    "parity-batched-target.txt",
					"content": "REPLACE_ME\n",
				},
			},
			{
				Name: "edit",
				Args: map[string]any{
					"path": "parity-batched-target.txt",
					"edits": []map[string]any{{
						"oldText": "REPLACE_ME",
						"newText": "DONE",
					}},
				},
			},
		}
	}

	// Extension tool bridge parity.
	if strings.Contains(lastText, "Run: extension echo hello") {
		return "tool", "", []testFauxToolCall{{
			Name: "echo_bridge",
			Args: map[string]any{"text": "hello"},
		}}
	}
	if strings.Contains(lastText, "Run: extension details") {
		return "tool", "", []testFauxToolCall{{
			Name: "echo_bridge",
			Args: map[string]any{"text": "DETAILS_ALPHA_BRAVO_CHARLIE_DELTA_ECHO_FOXTROT_GOLF_HOTEL_INDIA_JULIET_KILO_LIMA_TAIL"},
		}}
	}
	if strings.Contains(lastText, "Run: extension UI dialogs") {
		return "tool", "", []testFauxToolCall{{Name: "ui_dialog_probe", Args: map[string]any{}}}
	}
	if strings.Contains(lastText, "Run: extension render cards") {
		return "tool", "", []testFauxToolCall{
			{Name: "render_card", Args: map[string]any{"topic": "alpha"}},
			{Name: "render_self", Args: map[string]any{"topic": "beta"}},
			{Name: "render_throw", Args: map[string]any{"topic": "gamma"}},
			{Name: "render_fail", Args: map[string]any{"topic": "delta"}},
		}
	}

	// Extension tool_call blocker parity.
	if strings.Contains(lastText, "Run: bash BLOCK_ME") {
		return "tool", "", []testFauxToolCall{{
			Name: "bash",
			Args: map[string]any{"command": "echo BLOCK_ME"},
		}}
	}

	// Extension tool_result modifier parity.
	if strings.Contains(lastText, "Run: bash modified") {
		return "tool", "", []testFauxToolCall{{
			Name: "bash",
			Args: map[string]any{"command": "echo parity-base"},
		}}
	}

	// Slow parallel tools keep both live cards visible long enough for the
	// no-input rendering probe to observe independent streamed output.
	if strings.Contains(lastText, "Run: tui live parallel tools") {
		return "tool", "", []testFauxToolCall{
			{Name: "read", Args: map[string]any{"path": ".pig-live-parallel-a"}},
			{Name: "read", Args: map[string]any{"path": ".pig-live-parallel-b"}},
		}
	}

	// Compact read labels: a skill file, a context file with a line range,
	// and an ordinary file.
	if strings.Contains(lastText, "Run: compact reads") {
		return "tool", "", []testFauxToolCall{
			{Name: "read", Args: map[string]any{"path": "skills/demo-skill/SKILL.md"}},
			{Name: "read", Args: map[string]any{"path": "AGENTS.md", "offset": 2, "limit": 1}},
			{Name: "read", Args: map[string]any{"path": "notes.txt"}},
		}
	}

	// Parallel tool call parity: two tools dispatched simultaneously.
	if strings.Contains(lastText, "Run: parallel reads") {
		return "tool", "", []testFauxToolCall{
			{
				Name: "read",
				Args: map[string]any{"path": "parity-read-target.txt"},
			},
			{
				Name: "bash",
				Args: map[string]any{"command": "echo parallel-ok", "timeout": 10},
			},
		}
	}

	// Grep tool parity.
	if strings.Contains(lastText, "Run: grep hello") {
		return "tool", "", []testFauxToolCall{{
			Name: "grep",
			Args: map[string]any{
				"pattern": "hello",
				"path":    "parity-read-target.txt",
			},
		}}
	}

	// Find tool parity.
	if strings.Contains(lastText, "Run: find txt files") {
		return "tool", "", []testFauxToolCall{{
			Name: "find",
			Args: map[string]any{
				"pattern": "*.txt",
				"path":    ".",
			},
		}}
	}

	// Ls tool parity.
	if strings.Contains(lastText, "Run: ls here") {
		return "tool", "", []testFauxToolCall{{
			Name: "ls",
			Args: map[string]any{
				"path": ".",
			},
		}}
	}

	// Edit tool parity.
	if strings.Contains(lastText, "Run: edit parity-edit-target.txt") {
		return "tool", "", []testFauxToolCall{{
			Name: "edit",
			Args: map[string]any{
				"path":    "parity-edit-target.txt",
				"oldText": "REPLACE_ME",
				"newText": "REPLACED",
			},
		}}
	}

	// Long-output bash tool parity (exercises visual-truncate.ts via bash.ts collapse hint).
	if strings.Contains(lastText, "Run: bash long output") {
		return "tool", "", []testFauxToolCall{{
			Name: "bash",
			Args: map[string]any{"command": "seq 1 30"},
		}}
	}

	// Slow-output tool used by the live main-screen parity probe.
	if strings.Contains(lastText, "Run: tui live tool") {
		command := `for i in $(seq 1 24); do printf 'LIVE-TOOL-%02d\n' "$i"; sleep 0.04; done`
		if strings.Contains(lastText, "Run: tui live tool pause") {
			command = `for i in $(seq 1 24); do printf 'LIVE-TOOL-%02d\n' "$i"; if [ "$i" -eq 8 ]; then sleep 5; elif [ "$i" -eq 24 ]; then sleep 5; else sleep 0.04; fi; done`
		}
		return "tool", "", []testFauxToolCall{{
			Name: "bash",
			Args: map[string]any{"command": command},
		}}
	}

	// Tool result follow-ups dispatch from the latest user turn rather than
	// overlapping tool output or an older turn in a multi-turn fixture.
	if _, ok := last.(ToolResultMessage); ok {
		if strings.Contains(currentUserText, "Run: expr 20 + 22") {
			return "text", "42", nil
		}
		if strings.Contains(currentUserText, "Run: read parity-read-target.txt") {
			return "text", "done", nil
		}
		if strings.Contains(currentUserText, "Run: write parity-write-output.txt") {
			return "text", "wrote", nil
		}
		if strings.Contains(currentUserText, "Run: write then edit parity-batched-target.txt") {
			if strings.Contains(historyText, "Successfully wrote") &&
				strings.Contains(historyText, "Successfully replaced 1 block(s) in parity-batched-target.txt.") {
				return "text", "batched-done", nil
			}
			return "error", "test-faux: batched mutation missing success markers", nil
		}
		if strings.Contains(currentUserText, "Run: extension echo hello") {
			if strings.Contains(historyText, "echo-bridge: hello") {
				return "text", "echo-bridge: hello", nil
			}
			return "error", "test-faux: extension tool result missing echo-bridge marker", nil
		}
		if strings.Contains(currentUserText, "Run: extension details") {
			if strings.Contains(historyText, "DETAILS_ALPHA_BRAVO_CHARLIE") && strings.Contains(historyText, "KILO_LIMA_TAIL") {
				return "text", "details-probe-done", nil
			}
			return "error", "test-faux: extension details marker missing", nil
		}
		if strings.Contains(currentUserText, "Run: extension render cards") {
			if strings.Contains(historyText, "done alpha") && strings.Contains(historyText, "cannot render delta") {
				return "text", "render-cards-done", nil
			}
			return "error", "test-faux: render card results missing", nil
		}
		if strings.Contains(currentUserText, "Run: extension UI dialogs") {
			for _, marker := range []string{"dialogs-ok:", "dialogs-cancelled:"} {
				if strings.Contains(historyText, marker) {
					return "text", marker + strings.SplitN(historyText, marker, 2)[1], nil
				}
			}
			return "error", "test-faux: extension UI result missing dialog marker", nil
		}
		if strings.Contains(currentUserText, "Run: bash BLOCK_ME") {
			if strings.Contains(historyText, "parity-blocked") {
				return "text", "blocked:parity-blocked", nil
			}
			return "text", "not-blocked", nil
		}
		if strings.Contains(currentUserText, "Run: bash modified") {
			if strings.Contains(historyText, "[parity-modified]") {
				return "text", "modified:parity-modified", nil
			}
			return "text", "not-modified", nil
		}
		if strings.Contains(currentUserText, "Run: grep hello") {
			return "text", "found", nil
		}
		if strings.Contains(currentUserText, "Run: find txt files") {
			return "text", "listed", nil
		}
		if strings.Contains(currentUserText, "Run: ls here") {
			return "text", "listed-ls", nil
		}
		if strings.Contains(currentUserText, "Run: edit parity-edit-target.txt") {
			return "text", "edited", nil
		}
		if strings.Contains(currentUserText, "Run: tui live parallel tools") {
			return "text", "LIVE-PARALLEL-DONE", nil
		}
		if strings.Contains(currentUserText, "Run: tui live tool") {
			return "text", "LIVE-TOOL-DONE", nil
		}
		if strings.Contains(currentUserText, "Run: bash long output") {
			return "text", "ran", nil
		}
		if strings.Contains(currentUserText, "Run: parallel reads") {
			return "text", "parallel-done", nil
		}
		if strings.Contains(currentUserText, "Run: compact reads") {
			return "text", "compact-reads-done", nil
		}
		if strings.Contains(currentUserText, "Run: bash control-chars") {
			return "text", "sanitized", nil
		}
		if strings.Contains(currentUserText, "Run: bash with invalid args") {
			return "text", "validation-handled", nil
		}
	}

	if strings.Contains(lastText, "markdown parity fixture") {
		return "text", "# Fixture Heading\n\n> quoted line\ncontinued quote\n> - quoted bullet\n\n1. first item\n   1. nested ordered\n   - nested bullet\n\n| Name | Value |\n| --- | ---: |\n| alpha | 10 |\n| beta | `wrapped code sample` |\n\nRead [docs](https://example.com/docs)", nil
	}

	// Over-window threshold parity: normal response with usage above the
	// configured context window threshold. This must compact without retrying.
	if strings.Contains(lastText, "Trigger: over-window response") {
		return "text", "over-window-ok", nil
	}

	// Overflow error parity: provider returns an error matching the overflow
	// regex. Both binaries should detect it via IsContextOverflow.
	if strings.Contains(lastText, "Trigger: overflow error") {
		return "error", "prompt is too long: 500000 tokens > 200000 maximum", nil
	}
	if strings.Contains(lastText, "Send after overflow compaction") {
		return "text", "queued-after-compaction-ok", nil
	}

	// Sanitize parity: trigger bash with control-char output.
	if strings.Contains(lastText, "Run: bash control-chars") {
		return "tool", "", []testFauxToolCall{{
			Name: "bash",
			Args: map[string]any{"command": "printf 'hello\x01\x02world'"},
		}}
	}

	// Validation parity: trigger a tool call whose args fail schema validation.
	// The bash tool requires "command" (string); sending a number should fail.
	if strings.Contains(lastText, "Run: bash with invalid args") {
		return "tool", "", []testFauxToolCall{{
			Name: "bash",
			Args: map[string]any{"command": 42},
		}}
	}

	// Mermaid rendering parity: return a multi-line ```mermaid block so the
	// assistant markdown pipeline runs the Mermaid transformer + engine. The
	// rendered box-drawing diagram is theme-independent after ANSI
	// normalization, so pig and pi must produce the same plain art.
	if strings.Contains(lastText, "show mermaid flowchart") {
		return "text", "```mermaid\ngraph TD\n  A[Start] --> B[End]\n```", nil
	}

	// Generic "reply with exactly: <word>": used by tree parity scenarios
	// to populate session entries without a live model.
	if strings.Contains(lastText, "reply with exactly:") {
		after, ok := strings.CutPrefix(strings.TrimSpace(lastText), "reply with exactly:")
		if ok {
			return "text", strings.TrimSpace(after), nil
		}
		return "text", "done", nil
	}

	// Unknown slash parity: unresolved slash-prefixed text should reach the
	// model and produce a normal assistant response, not a local dispatcher
	// error. Keep this response generic; the test only cares that a response is
	// produced without the local error string.
	if strings.HasPrefix(strings.TrimSpace(lastText), "/help") {
		return "text", "Tell me what you want me to do and I will use the appropriate tools.", nil
	}

	return "error", fmt.Sprintf("test-faux: unhandled request %q", lastText), nil
}

func historyText(msgs []Message) string {
	parts := make([]string, 0, len(msgs))
	for _, m := range msgs {
		parts = append(parts, messageText(m))
	}
	return strings.Join(parts, "\n\n")
}

func messageText(message Message) string {
	var parts []string
	switch message := message.(type) {
	case SystemMessage:
		return systemContentText(message.Content)
	case UserMessage:
		switch content := message.Content.(type) {
		case UserText:
			return string(content)
		case UserContentBlocks:
			for _, block := range content {
				if block, ok := block.(TextContent); ok {
					parts = append(parts, block.Text)
				}
			}
		}
	case AssistantMessage:
		for _, block := range message.Content {
			switch block := block.(type) {
			case TextContent:
				parts = append(parts, block.Text)
			case ThinkingContent:
				parts = append(parts, block.Thinking)
			case ToolCall:
				parts = append(parts, block.Name)
			}
		}
	case ToolResultMessage:
		for _, block := range message.Content {
			if block, ok := block.(TextContent); ok {
				parts = append(parts, block.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func messageImages(message Message) []ImageContent {
	var images []ImageContent
	switch message := message.(type) {
	case UserMessage:
		blocks, _ := message.Content.(UserContentBlocks)
		for _, block := range blocks {
			if block, ok := block.(ImageContent); ok {
				images = append(images, block)
			}
		}
	case ToolResultMessage:
		for _, block := range message.Content {
			if block, ok := block.(ImageContent); ok {
				images = append(images, block)
			}
		}
	}
	return images
}
