// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Ports the handoff tests of packages/durable/test/harness-inbox.test.ts (Pi 38ff99177 "continue runs after tool handoffs") over
// both engines: a tool's handoff starts a new context and the run continues in it; the handoff's reset precedes every queued
// item of the boundary.

// handoffRig is a Harness over the reference or the core with tools the test registers and the faux model scripted.
type handoffRig struct {
	h    harness.Harness
	root harness.Conversation
	faux *ai.FauxProviderHandle
}

func holdingTool(name string, started chan<- struct{}, gate <-chan struct{}, control durable.ToolControl) *durable.ToolRegistration {
	return &durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: name, Description: name, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			if started != nil {
				select {
				case started <- struct{}{}:
				default:
				}
			}
			if gate != nil {
				select {
				case <-gate:
				case <-ctx.Done():
					return durable.ToolExecutionResult{}, ctx.Err()
				}
			}
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}, Control: &control}, nil
		},
	}
}

func openHandoff(t *testing.T, core bool, tools []*durable.ToolRegistration, steps ...ai.FauxResponseStep) *handoffRig {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{Models: []ai.FauxModelDefinition{{ID: "faux-1"}}})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses(steps)
	registry := harness.CreateRegistry()
	if err := registry.Install(&durable.Extension{Name: "handoff", Tools: tools}); err != nil {
		t.Fatal(err)
	}
	options := harness.HarnessOptions{Models: models, Registry: registry}
	var h harness.Harness
	var err error
	if core {
		h, err = driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), options)
	} else {
		h, err = harness.OpenHarness(t.Context(), storage.NewMemoryStorage(), options)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	return &handoffRig{h: h, root: root, faux: faux}
}

func toolCallsStep(calls ...ai.FauxContentBlock) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: calls, StopReason: "toolUse"})
}

func holdCall(name, id string) ai.FauxContentBlock {
	return ai.FauxToolCall(name, map[string]any{}, &ai.FauxToolCallOptions{ID: id})
}

// userTextOf is the text of a user message.
func userTextOf(message ai.Message) string {
	if user, ok := message.(ai.UserMessage); ok {
		if text, isText := user.Content.(ai.UserText); isText {
			return string(text)
		}
	}
	return ""
}

// requestLog records the user messages of each request its steps answer (harness-inbox.test.ts recordRequest).
type requestLog struct {
	mu       sync.Mutex
	requests [][]string
}

func (log *requestLog) step(text string) ai.FauxResponseStep {
	return ai.FauxFactoryStep(func(transcript ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		lines := []string{}
		for _, message := range transcript.Messages() {
			if _, isUser := message.(ai.UserMessage); isUser {
				lines = append(lines, "user:"+userTextOf(message))
			} else if _, isSystem := message.(ai.SystemMessage); isSystem {
				lines = append(lines, "system:")
			}
		}
		log.mu.Lock()
		log.requests = append(log.requests, lines)
		log.mu.Unlock()
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}}.AssistantMessage(), nil
	})
}

func (log *requestLog) all() [][]string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([][]string{}, log.requests...)
}

// gatedText is a faux answer held until release; reached closes when the request is sent.
func gatedText(text string) (step ai.FauxResponseStep, reached chan struct{}, release func()) {
	reached, gate := make(chan struct{}), make(chan struct{})
	var once sync.Once
	step = ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		once.Do(func() { close(reached) })
		select {
		case <-gate:
		case <-options.Signal.Done():
			return ai.FauxResponse{}.AssistantMessage(), options.Signal.Err()
		}
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}}.AssistantMessage(), nil
	})
	return step, reached, func() { close(gate) }
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(20 * time.Second):
		t.Fatal("timed out")
	}
}

// transcriptOf is the kind and text of each entry but pi.system and tool results' text, as harness-inbox.test.ts transcript.
func transcriptOf(t *testing.T, root harness.Conversation) []string {
	t.Helper()
	var lines []string
	var cursor durable.Cursor
	for {
		page, err := root.Entries(t.Context(), durable.EntryQuery{Order: &ascending}, 100, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page.Items {
			if entry.Kind == "pi.system" {
				continue
			}
			line := entry.Kind
			if len(entry.Model) > 0 {
				switch message := entry.Model[0].(type) {
				case ai.UserMessage:
					line += ":" + userTextOf(message)
				case ai.AssistantMessage:
					for _, block := range message.Content {
						if text, ok := block.(ai.TextContent); ok {
							line += ":" + text.Text
							break
						}
					}
				}
			}
			lines = append(lines, line)
		}
		if page.Next == nil {
			return lines
		}
		cursor = *page.Next
	}
}

// ascending reads entries oldest first.
var ascending = durable.ScanAscending

func entryIdsOfKind(t *testing.T, root harness.Conversation, kind string) []durable.EntryId {
	t.Helper()
	page, err := root.Entries(t.Context(), durable.EntryQuery{Order: &ascending}, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []durable.EntryId
	for _, entry := range page.Items {
		if entry.Kind == kind {
			ids = append(ids, entry.Id)
		}
	}
	return ids
}

func expectLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("transcript\n got %q\nwant %q", got, want)
	}
}

func expectAnsweredBy(t *testing.T, submission durable.Submission, want durable.EntryId) {
	t.Helper()
	record, err := submission.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != durable.SubmissionDone || record.Answer == nil || *record.Answer != want {
		t.Fatalf("submission %+v, want done answered by %d", record, want)
	}
}

func queuedNoteDraft(text string) durable.SubmissionDraft {
	entry := durable.EntryDraft{Kind: "note", Model: []ai.Message{ai.UserMessage{Content: ai.UserText(text)}}}
	return durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &entry}
}

func TestInboxHandoff(t *testing.T) {
	handoff := func(text string) durable.ToolControl { return durable.ToolControl{Handoff: &text} }

	// harness-inbox.test.ts:355
	t.Run("continues the run in a new context when a tool requests a handoff", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			requests := &requestLog{}
			rig := openHandoff(t, core, []*durable.ToolRegistration{holdingTool("hold", nil, nil, handoff("continue here"))}, toolCallsStep(holdCall("hold", "c1")), requests.step("after handoff"))
			input := submitInputWhenBusy(t, rig.root, "a", "")
			settled := settledOf(t, input)
			expectLines(t, transcriptOf(t, rig.root), "pi.user:a", "pi.assistant", "pi.tool-result", "pi.reset:continue here", "pi.assistant:after handoff")
			answers := entryIdsOfKind(t, rig.root, "pi.assistant")
			if settled.Status != durable.SubmissionDone || settled.Answer == nil || *settled.Answer != answers[len(answers)-1] {
				t.Fatalf("settled %+v, want done answered by the last assistant entry", settled)
			}
			if got := requests.all(); !reflect.DeepEqual(got, [][]string{{"system:", "user:continue here"}}) {
				t.Fatalf("requests %q, want the handoff text alone", got)
			}
			if calls := rig.faux.CallCount(); calls != 2 {
				t.Fatalf("calls = %d, want 2", calls)
			}
		})
	})

	// harness-inbox.test.ts:381
	t.Run("places queued writes and steers after the handoff text and adds the steers to the continuing run", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			started, gate := make(chan struct{}, 1), make(chan struct{})
			requests := &requestLog{}
			rig := openHandoff(t, core, []*durable.ToolRegistration{holdingTool("hold", started, gate, handoff("continue here"))}, toolCallsStep(holdCall("hold", "c1")), requests.step("after handoff"))
			input := submitInputWhenBusy(t, rig.root, "a", "")
			awaitSignal(t, started)
			steered := submitInputWhenBusy(t, rig.root, "s", durable.WhenBusySteer)
			write, err := rig.root.Submit(t.Context(), queuedNoteDraft("w"))
			if err != nil {
				t.Fatal(err)
			}
			close(gate)
			settledOf(t, input)
			expectLines(t, transcriptOf(t, rig.root), "pi.user:a", "pi.assistant", "pi.tool-result", "pi.reset:continue here", "note:w", "pi.user:s", "pi.assistant:after handoff")
			if got := requests.all(); !reflect.DeepEqual(got, [][]string{{"system:", "user:continue here", "user:w", "user:s"}}) {
				t.Fatalf("requests %q, want handoff, write, steer", got)
			}
			last := entryIdsOfKind(t, rig.root, "pi.assistant")[1]
			expectAnsweredBy(t, input, last)
			expectAnsweredBy(t, steered, last)
			if record, err := write.Status(t.Context()); err != nil || record.Status != durable.SubmissionDone {
				t.Fatalf("write %+v, %v, want done", record, err)
			}
		})
	})

	// harness-inbox.test.ts:412
	t.Run("holds a follow-up queued across a handoff until the continuing run's final boundary", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			started, gate := make(chan struct{}, 1), make(chan struct{})
			after, reached, release := gatedText("after handoff")
			rig := openHandoff(t, core, []*durable.ToolRegistration{holdingTool("hold", started, gate, handoff("continue here"))}, toolCallsStep(holdCall("hold", "c1")), after, fauxText("follow-up"))
			input := submitInputWhenBusy(t, rig.root, "a", "")
			awaitSignal(t, started)
			followUp := submitInputWhenBusy(t, rig.root, "f", "")
			close(gate)
			awaitSignal(t, reached)
			if record, err := input.Status(t.Context()); err != nil || record.Status != durable.SubmissionPlaced {
				t.Fatalf("input %+v, %v, want placed", record, err)
			}
			if record, err := followUp.Status(t.Context()); err != nil || record.Status != durable.SubmissionQueued {
				t.Fatalf("follow-up %+v, %v, want queued", record, err)
			}
			release()
			settledOf(t, followUp)
			expectLines(t, transcriptOf(t, rig.root), "pi.user:a", "pi.assistant", "pi.tool-result", "pi.reset:continue here", "pi.assistant:after handoff", "pi.user:f", "pi.assistant:follow-up")
			answers := entryIdsOfKind(t, rig.root, "pi.assistant")
			expectAnsweredBy(t, input, answers[1])
			expectAnsweredBy(t, followUp, answers[2])
		})
	})

	// harness-inbox.test.ts:444
	t.Run("ends a handed-off run at a queued reset and runs earlier follow-ups after it", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			started, gate := make(chan struct{}, 1), make(chan struct{})
			requests := &requestLog{}
			rig := openHandoff(t, core, []*durable.ToolRegistration{holdingTool("hold", started, gate, handoff("continue here"))}, toolCallsStep(holdCall("hold", "c1")), requests.step("fresh"))
			input := submitInputWhenBusy(t, rig.root, "a", "")
			awaitSignal(t, started)
			followUp := submitInputWhenBusy(t, rig.root, "f", "")
			if err := rig.root.Reset(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			close(gate)
			settledOf(t, followUp)
			expectUnanswered(t, settledOf(t, input), "reset")
			expectLines(t, transcriptOf(t, rig.root), "pi.user:a", "pi.assistant", "pi.tool-result", "pi.reset:continue here", "pi.reset", "pi.user:f", "pi.assistant:fresh")
			if got := requests.all(); !reflect.DeepEqual(got, [][]string{{"system:", "user:f"}}) {
				t.Fatalf("requests %q, want the follow-up alone", got)
			}
			if calls := rig.faux.CallCount(); calls != 2 {
				t.Fatalf("calls = %d, want 2", calls)
			}
		})
	})

	// harness-inbox.test.ts:472
	t.Run("hands off again in a later round of the same run", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			var mu sync.Mutex
			handoffs := 0
			again := holdingTool("hold", nil, nil, durable.ToolControl{})
			again.Execute = func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				mu.Lock()
				defer mu.Unlock()
				handoffs++
				text := "h" + strconv.Itoa(handoffs)
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}, Control: &durable.ToolControl{Handoff: &text}}, nil
			}
			rig := openHandoff(t, core, []*durable.ToolRegistration{again}, toolCallsStep(holdCall("hold", "c1")), toolCallsStep(holdCall("hold", "c2")), fauxText("done"))
			settled := settledOf(t, submitInputWhenBusy(t, rig.root, "a", ""))
			expectLines(t, transcriptOf(t, rig.root), "pi.user:a", "pi.assistant", "pi.tool-result", "pi.reset:h1", "pi.assistant", "pi.tool-result", "pi.reset:h2", "pi.assistant:done")
			answers := entryIdsOfKind(t, rig.root, "pi.assistant")
			if settled.Status != durable.SubmissionDone || settled.Answer == nil || *settled.Answer != answers[len(answers)-1] {
				t.Fatalf("settled %+v, want done answered by the last assistant entry", settled)
			}
			view, err := rig.root.Context(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			var users []string
			for _, message := range view.Messages {
				if _, isUser := message.(ai.UserMessage); isUser {
					users = append(users, userTextOf(message))
				}
			}
			if !reflect.DeepEqual(users, []string{"h2"}) || len(view.Messages) != 3 {
				t.Fatalf("context %+v, want the second handoff and the answer", view.Messages)
			}
		})
	})

	// harness-inbox.test.ts:789
	t.Run("writes only the last handoff in call order and runs queued follow-ups after the continuing run", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			started, firstGate, secondGate := make(chan struct{}, 1), make(chan struct{}), make(chan struct{})
			laterDone := make(chan struct{}, 1)
			later := holdingTool("later", nil, secondGate, handoff("two"))
			execute := later.Execute
			later.Execute = func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				result, err := execute(ctx, args, api)
				laterDone <- struct{}{}
				return result, err
			}
			rig := openHandoff(t, core, []*durable.ToolRegistration{holdingTool("hold", started, firstGate, handoff("one")), later},
				toolCallsStep(holdCall("hold", "c1"), holdCall("later", "c2")), fauxText("after handoff"), fauxText("follow-up"))
			input := submitInputWhenBusy(t, rig.root, "a", "")
			awaitSignal(t, started)
			followUp := submitInputWhenBusy(t, rig.root, "f", "")
			close(secondGate)
			awaitSignal(t, laterDone)
			close(firstGate)
			settledOf(t, followUp)
			expectLines(t, transcriptOf(t, rig.root), "pi.user:a", "pi.assistant", "pi.tool-result", "pi.tool-result", "pi.reset:two", "pi.assistant:after handoff", "pi.user:f", "pi.assistant:follow-up")
			expectAnsweredBy(t, input, entryIdsOfKind(t, rig.root, "pi.assistant")[1])
		})
	})
}
