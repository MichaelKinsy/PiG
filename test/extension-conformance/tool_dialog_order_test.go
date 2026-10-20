package extensionconformance

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Upstream agent-loop.ts:586-647 (executeToolCallsParallel) and rpc-mode.ts:91-130, 771-777 (createDialogPromise): the calls of one assistant message start in
// source order and each reaches its first await, here a select dialog, before the next starts, so the dialogs open in call order. A dialog resolves on the
// stdin line that answers it, and two answers read back to back resume their tools in answer order, each running to its return before the next one
// resumes, so the calls' tool_execution_end events follow the answers and the tool results keep source order. One thread gives Pi both orders; a Go, Python or
// Rust SDK runs every request on a thread of its own, so each SDK keeps them itself, and Node needs nothing. The row fails if any SDK, isolated or packed,
// differs: the batch is repeated, the answers come in forward, reverse and rotated order, and the answers are released back to back as RPC mode reads them.
func TestToolBatchDialogsOpenInCallOrderAndResumeInAnswerOrderAcrossSDKs(t *testing.T) {
	const batch, rounds = 6, 9
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			tools, errs := coding.BridgeNewRunnerTools(h.runner.Tools())
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			for round := range rounds {
				runDialogBatch(t, h, tools, round, batch)
			}
		})
	}
}

// answerOrder is the order the round's answers are read in: forward, reversed, or rotated by one.
func answerOrder(round, batch int) []int {
	order := make([]int, batch)
	for i := range batch {
		switch round % 3 {
		case 0:
			order[i] = batch - 1 - i
		case 1:
			order[i] = i
		default:
			order[i] = (i + 1) % batch
		}
	}
	return order
}

func runDialogBatch(t *testing.T, h *harness, tools []agent.AgentTool, round, batch int) {
	t.Helper()
	wait := testbudget.Wait(t)
	titles := make([]string, batch)
	ids := make([]string, batch)
	calls := make([]ai.FauxContentBlock, batch)
	for i := range batch {
		titles[i], ids[i] = fmt.Sprintf("T%d", i), fmt.Sprintf("dialog-%d-%d", round, i)
		calls[i] = ai.FauxToolCall("dialog_order", map[string]any{"title": titles[i]}, &ai.FauxToolCallOptions{ID: ids[i]})
	}

	// The UI is the RPC mode's: a dialog waits for its answer, and the answer is read, which settles the call, before the dialog is released.
	var mu sync.Mutex
	answers := make(map[string]chan struct{}, batch)
	settles := make(map[string]func(), batch)
	for _, title := range titles {
		answers[title] = make(chan struct{})
	}
	opened := make(chan string, batch)
	// Like RPC mode, the UI reports a dialog initiated once it is open, so the host applies the dialogs of different requests in the order their frames arrived.
	h.bridge.SetUIContext(&openingDialogUI{UIContext: h.ui, open: func(ctx context.Context, title string) (string, error) {
		mu.Lock()
		answer := answers[title]
		settles[title] = extension.DialogSettleFromContext(ctx)
		mu.Unlock()
		opened <- title
		extension.CallInitiated(ctx)
		select {
		case <-answer:
			return "a", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}})
	t.Cleanup(func() { h.bridge.SetUIContext(h.ui) })

	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: calls, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
	})
	events := make(chan agent.AgentEvent, 256)
	endsDone := make(chan []string, 1)
	go func() {
		var ends []string
		for event := range events {
			if end, ok := event.(agent.ToolExecutionEndEvent); ok {
				ends = append(ends, end.ToolCallID)
			}
		}
		endsDone <- ends
	}()
	a := mustNewAgent(agent.AgentOptions{Model: &ai.Model{ID: "faux-1", Provider: provider}, Tools: tools, EventCh: events})
	type sent struct {
		messages []agent.AgentMessage
		err      error
	}
	sendDone := make(chan sent, 1)
	go func() {
		messages, err := a.Send(t.Context(), "go")
		close(events)
		sendDone <- sent{messages, err}
	}()

	var opening []string
	timeout := time.After(wait)
	for range batch {
		select {
		case title := <-opened:
			opening = append(opening, title)
		case <-timeout:
			t.Fatalf("round %d: only %v of the dialogs opened", round, opening)
		case result := <-sendDone:
			t.Fatalf("round %d: the batch ended with only %v opened: %v", round, opening, result.err)
		}
	}
	if !reflect.DeepEqual(opening, titles) {
		t.Fatalf("round %d: the dialogs opened in order %v, want the order the calls started %v", round, opening, titles)
	}

	// The answers are read one after the other, as the lines of a single stdin chunk are.
	var answered []string
	mu.Lock()
	for _, index := range answerOrder(round, batch) {
		title := titles[index]
		if settle := settles[title]; settle != nil {
			settle()
		} else {
			mu.Unlock()
			t.Fatalf("round %d: the dialog call of %s carries no settle function", round, title)
		}
		close(answers[title])
		answered = append(answered, ids[index])
	}
	mu.Unlock()

	var result sent
	select {
	case result = <-sendDone:
	case <-time.After(wait):
		t.Fatalf("round %d: the batch did not end after its answers were read", round)
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	ends := <-endsDone
	if !reflect.DeepEqual(ends, answered) {
		t.Fatalf("round %d: tool_execution_end order = %v, want the order the answers were read %v", round, ends, answered)
	}
	var results []string
	for _, message := range result.messages {
		if message.ToolResult != nil {
			results = append(results, message.ToolResult.ToolCallID)
		}
	}
	if !reflect.DeepEqual(results, ids) {
		t.Fatalf("round %d: tool results in order %v, want source order %v", round, results, ids)
	}
	for _, message := range result.messages {
		if message.ToolResult != nil && !strings.HasPrefix(message.ToolResult.Text(), "dialog:T") {
			t.Fatalf("round %d: result %q is not a dialog answer", round, message.ToolResult.Text())
		}
	}
}

// openingDialogUI answers Select with open, which reports the dialog initiated as RPC mode's UI does once its request is written.
type openingDialogUI struct {
	extension.UIContext
	open func(ctx context.Context, title string) (string, error)
}

func (*openingDialogUI) ReportsDialogInitiation() bool { return true }

func (u *openingDialogUI) Select(ctx context.Context, title string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	return u.open(ctx, title)
}
