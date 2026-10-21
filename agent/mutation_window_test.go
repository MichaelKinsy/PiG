package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// windowedTool is an extension tool that may take the file mutation queue: it logs its start, waits for the test, closes its start window (its synchronous
// prefix is over), and finishes when the test lets it.
type windowedTool struct {
	*scriptTool
}

func (windowedTool) MayTakeFileMutationQueue() bool { return true }

// registeringTool is a tool that registers its call in the file mutation queue (the edit and write tools).
type registeringTool struct {
	*orderableScriptTool
}

func (registeringTool) RegistersInFileMutationQueue() bool { return true }

func newWindowedTool(name string, log *orderLog, proceed, finish <-chan struct{}) *windowedTool {
	return &windowedTool{&scriptTool{name: name, mode: ToolModeParallel, params: valueSchema,
		execute: func(ctx context.Context, id string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
			log.add(name + "-start")
			if proceed != nil {
				<-proceed
			}
			if window := ToolStartWindowFromContext(ctx, id); window != nil {
				log.add(name + "-window")
				window.Close()
			}
			if finish != nil {
				<-finish
			}
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: argValue(args)}}}, nil
		}}}
}

func newRegisteringTool(name string, log *orderLog) *registeringTool {
	base := &scriptTool{name: name, mode: ToolModeParallel, params: valueSchema,
		execute: func(ctx context.Context, id string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
			ticket, ok := MutationTicketFromContext(ctx)
			if !ok {
				return AgentToolResult{}, nil
			}
			ticket.Wait()
			log.add(name + "-run")
			ticket.Release()
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: argValue(args)}}}, nil
		}}
	return &registeringTool{&orderableScriptTool{scriptTool: base, queue: &fakeMutationQueue{}, log: log}}
}

// agent-loop.ts:646-648 runs each call's synchronous prefix in call order, and an extension tool that takes the file mutation queue in its first statement
// registers before the edit or write call behind it. The extension's registration reaches the host a hop after the call started, so the dispatcher holds the
// registration of the call behind it until the extension tool's synchronous prefix is over, and an extension tool behind the call starts after it registered.
func TestAMutationCallRegistersAfterTheStartWindowOfTheExtensionToolBeforeIt(t *testing.T) {
	var log orderLog
	proceed, finish := make(chan struct{}), make(chan struct{})
	ext := newWindowedTool("ext", &log, proceed, finish)
	edit := newRegisteringTool("edit", &log)
	after := newWindowedTool("after", &log, nil, nil)
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(
			toolCall("call-1", "ext", ai.JsonObject{"value": "call-1"}),
			toolCall("call-2", "edit", ai.JsonObject{"value": "call-2"}),
			toolCall("call-3", "after", ai.JsonObject{"value": "call-3"}),
		)}),
		Tools: []AgentTool{ext, edit, after},
	})
	done := sendAsync(t, a, "run")
	// The extension tool started and its prefix is not over: nothing registered, and the tool behind the edit call did not start.
	awaitLog(t, &log, "ext-start")
	if got := log.list(); !reflect.DeepEqual(got, []string{"ext-start"}) {
		t.Fatalf("before the extension tool's prefix is over: %v, want only its start", got)
	}
	close(proceed)
	awaitLog(t, &log, "after-start")
	close(finish)
	waitSignal(t, done, "the run")
	got := log.list()
	index := func(entry string) int { return slices.Index(got, entry) }
	if index("ext-window") < 0 || index("ext-window") >= index("reserve:call-2") || index("reserve:call-2") >= index("after-start") {
		t.Fatalf("order = %v, want the extension tool's window, then the edit call's registration, then the start of the extension tool behind it", got)
	}
}

// A batch without an extension tool before the call registers it in the dispatcher's loop, before any goroutine starts, as before.
func TestAMutationCallBehindNoExtensionToolRegistersInTheDispatchLoop(t *testing.T) {
	var log orderLog
	edit := newRegisteringTool("edit", &log)
	ext := newWindowedTool("ext", &log, nil, nil)
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(
			toolCall("call-1", "edit", ai.JsonObject{"value": "call-1"}),
			toolCall("call-2", "ext", ai.JsonObject{"value": "call-2"}),
		)}),
		Tools: []AgentTool{edit, ext},
	})
	waitSignal(t, sendAsync(t, a, "run"), "the run")
	if got := log.list(); len(got) == 0 || got[0] != "reserve:call-1" {
		t.Fatalf("order = %v, want the edit call's reservation first, in the dispatch loop", got)
	}
}

// awaitLog waits until the log holds entry.
func awaitLog(t *testing.T, log *orderLog, entry string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !slices.Contains(log.list(), entry) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q in %v", entry, log.list())
		}
		time.Sleep(time.Millisecond)
	}
}
