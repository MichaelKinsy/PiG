// Ports packages/durable/test/harness-tools.test.ts: the "tool progress and lifetime" cases. See harness_tasks_test.go
// for the Go mappings that apply to every case.

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
	jsonlnode "github.com/MichaelKinsy/PiG/durable/storage/jsonl/node"
)

// tlLiveTool returns the first tool slot of a conversation's pi.live document.
func tlLiveTool(t *testing.T, harness Harness, conversationId durable.ConversationId) map[string]any {
	t.Helper()
	live, err := harness.SnapshotErased(testContext, LiveDoc, conversationId)
	if err != nil {
		t.Fatal(err)
	}
	slots, _ := live["tools"].([]any)
	if len(slots) == 0 {
		return nil
	}
	return slots[0].(map[string]any)
}

func TestToolProgressAndLifetime(t *testing.T) {
	t.Run("applies the default output limits", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tlTool("lines", func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			for index := 1; index <= 2500; index++ {
				api.Output(strconv.Itoa(index) + "\n")
			}
			return durable.ToolExecutionResult{}, nil
		}))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"lines", map[string]any{}, "c1"}), tlDone()}, nil)
		text := tlResultText(tlResults(run.entries)[0])
		if !strings.HasPrefix(text, "1\n2\n") {
			t.Fatalf("text starts %q, want the head of the output", text[:min(len(text), 12)])
		}
		if suffix := "\n2000\n|<harness>\n[warn] Output truncated to its beginning: 500 lines, 2500 bytes dropped\n</harness>"; !strings.HasSuffix(text, suffix) {
			t.Fatalf("text ends %q, want %q", text[max(0, len(text)-len(suffix)-10):], suffix)
		}
		mustClose(t, run.harness)
	})

	t.Run("commits running output no more often than progress.outputIntervalMs", func(t *testing.T) {
		// Not an upstream case: upstream tests the interval through Progress alone (harness-output.test.ts:237) and the
		// configured pace through the offered window (harness-tools.test.ts:328); this drives the setting to the commits.
		// The tool's progress runs on a fake clock, so the second chunk waits exactly the configured interval.
		clock := &fakeProgressClock{}
		previous := toolProgressClock
		toolProgressClock = clock
		t.Cleanup(func() { toolProgressClock = previous })
		slotsAt := func(progress *ProgressPolicyPatch, advances ...float64) []any {
			setup := chatSetup(t)
			setup.SetSettings(func(settings *HarnessSettings) { settings.Progress = progress })
			slots := &syncValue[[]any]{}
			addTool(t, setup.Registry, tlTool("paced", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				internal, ok := api.(*toolApi)
				if !ok {
					return durable.ToolExecutionResult{}, fmt.Errorf("tool api is %T, not *toolApi", api)
				}
				// The first change after an idle period commits at once; the second waits for the interval.
				api.Output("one\n")
				waitIdle(internal.progress)
				api.Output("two\n")
				var outputs []any
				for _, ms := range advances {
					clock.advance(internal.progress, ms)
					live, err := api.SnapshotErased(ctx, LiveDoc, api.ConversationId())
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
					tools, _ := live["tools"].([]any)
					if len(tools) == 0 {
						return durable.ToolExecutionResult{}, errors.New("no running tool slot")
					}
					outputs = append(outputs, tools[0].(map[string]any)["output"])
				}
				slots.put(outputs)
				return durable.ToolExecutionResult{}, nil
			}))
			run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"paced", map[string]any{}, "c1"}), tlDone()}, nil)
			mustClose(t, run.harness)
			got, _ := slots.get()
			return got
		}
		if got, want := slotsAt(nil, 99, 1), []any{"one\n", "one\ntwo\n"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("default pace: slot outputs %q, want %q", got, want)
		}
		if got, want := slotsAt(&ProgressPolicyPatch{OutputIntervalMs: new(500.0)}, 499, 1), []any{"one\n", "one\ntwo\n"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("outputIntervalMs 500: slot outputs %q, want %q", got, want)
		}
	})

	t.Run("sanitizes running output but keeps explicit result content as the tool returned it", func(t *testing.T) {
		setup := chatSetup(t)
		slotOutput := &syncValue[any]{}
		addTool(t, setup.Registry, tlTool("noisy", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			api.Output("a\u0007b\r\n")
			if err := api.Details(ctx, map[string]any{"ready": true}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			live, err := api.SnapshotErased(ctx, LiveDoc, api.ConversationId())
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if slots, _ := live["tools"].([]any); len(slots) > 0 {
				slotOutput.put(slots[0].(map[string]any)["output"])
			}
			return durable.ToolExecutionResult{}, nil
		}))
		addTool(t, setup.Registry, tlTool("explicit", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return ownText("c\u001bd"), nil
		}))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"noisy", map[string]any{}, "c1"}, tlCall{"explicit", map[string]any{}, "c2"}), tlDone()}, nil)
		if got, _ := slotOutput.get(); got != "ab\n" {
			t.Fatalf("slot output %q, want %q", got, "ab\n")
		}
		byId := tlResultById(run.entries)
		tlExpectText(t, tlResultText(byId["c1"]), "ab\n")
		tlExpectText(t, tlResultText(byId["c2"]), "c\u001bd")
		mustClose(t, run.harness)
	})

	t.Run("drops control keys set to undefined instead of faulting", func(t *testing.T) {
		// Go has no undefined: an unset terminate is the zero value, Pi's `terminate?: true` left out. The task stores the
		// control as Pi's copyJson with omitUndefinedProperties leaves it, { addTools: ["extra"] }, and does not fault.
		setup := chatSetup(t)
		addTool(t, setup.Registry, tlTool("grow", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}, Control: &durable.ToolControl{AddTools: []string{"extra"}}}, nil
		}))
		extra := tlTool("extra", tlNoContent)
		addTool(t, setup.Registry, extra)
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"grow", map[string]any{}, "c1"}), tlDone()}, func(_ Harness, conversation Conversation) {
			mustConfigure(t, conversation, AgentChange{Tools: SetTo(ToolChange{Remove: []*durable.ToolRegistration{extra}})})
		})
		if run.status != durable.SubmissionDone {
			t.Fatalf("status %s, want done", run.status)
		}
		tools := tlToolTasks(tlScanTasks(t, run.harness, run.root.Id()))
		if len(tools) != 1 || tools[0].State.Outcome == nil || tools[0].State.Outcome.Status != durable.OutcomeCompleted || tools[0].State.Outcome.Result == nil {
			t.Fatalf("tool tasks %+v, want one completed", tools)
		}
		result := *tools[0].State.Outcome.Result
		stored, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf(`{"control":{"addTools":["extra"]},"entryId":%v}`, result.(map[string]any)["entryId"]); string(stored) != want {
			t.Fatalf("stored tool result %s, want %s", stored, want)
		}
		// addTools deletes the name from a stored { remove } filter.
		state, err := durable.Snapshot[AgentState](testContext, run.harness, AgentDoc, run.root.Id())
		if err != nil || state == nil || state.Tools == nil || state.Tools.Exact || !reflect.DeepEqual(state.Tools.Remove, []string{}) {
			t.Fatalf("stored tools %+v %v, want { remove: [] }", state.Tools, err)
		}
		mustClose(t, run.harness)
	})

	t.Run("uses explicit null details instead of the last reported value", func(t *testing.T) {
		// Upstream runs this over MemoryStorage. The persistent storages also keep the null through their JSON form and a
		// reopen, and a tool that never reports details stores a message without the key, as JSON.stringify omits
		// undefined.
		for _, kind := range []string{"memory", "sqlite", "jsonl"} {
			t.Run(kind, func(t *testing.T) {
				directory := t.TempDir()
				open := func() durable.Storage {
					switch kind {
					case "sqlite":
						return tkOpenSqlite(t, filepath.Join(directory, "session.sqlite"))
					case "jsonl":
						store, err := jsonlnode.OpenNodeJsonlStorage(testContext, filepath.Join(directory, "jsonl"), jsonl.JsonlStorageOptions{})
						if err != nil {
							t.Fatal(err)
						}
						return store
					}
					return storage.NewMemoryStorage()
				}
				setup := chatSetup(t)
				addTool(t, setup.Registry, tlTool("null", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					if err := api.Details(ctx, map[string]any{"old": float64(1)}); err != nil {
						return durable.ToolExecutionResult{}, err
					}
					return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}, Details: nil, HasDetails: true}, nil
				}))
				addTool(t, setup.Registry, tlTool("none", tlNoContent))
				setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"null", map[string]any{}, "c1"}), tlCallsStep(tlCall{"none", map[string]any{}, "c2"}), tlDone()})
				harness, root := openChat(t, open(), setup)
				if _, err := ownSubmit(t, root, ownInput("go")).Wait(testContext); err != nil {
					t.Fatal(err)
				}
				entries := allEntries(t, root)
				if kind != "memory" {
					mustClose(t, harness)
					harness, root = openChat(t, open(), setup)
					entries = allEntries(t, root)
				}
				want := []string{
					`{"role":"toolResult","toolCallId":"c1","toolName":"null","content":[],"details":null,"isError":false,"timestamp":%d}`,
					`{"role":"toolResult","toolCallId":"c2","toolName":"none","content":[],"isError":false,"timestamp":%d}`,
				}
				results := tlResults(entries)
				if len(results) != len(want) {
					t.Fatalf("results %d, want %d", len(results), len(want))
				}
				for index, result := range results {
					// toBeNull: the stored message carries details: null, not the reported value and not an absent key.
					if result.Details != nil || result.DetailsNull != (index == 0) {
						t.Fatalf("result %d details %v null %t", index, result.Details, result.DetailsNull)
					}
					encoded, err := json.Marshal(result)
					if err != nil {
						t.Fatal(err)
					}
					if expected := fmt.Sprintf(want[index], result.Timestamp); string(encoded) != expected {
						t.Fatalf("stored message\n got %s\nwant %s", encoded, expected)
					}
				}
				mustClose(t, harness)
			})
		}
	})

	t.Run("settles details() promises with coalesced progress commits and the terminal commit", func(t *testing.T) {
		setup := chatSetup(t)
		settled := &syncList[string]{}
		addTool(t, setup.Registry, tlTool("details", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			// Three updates in one throttle window coalesce; the last is still pending when execute() returns. Upstream
			// holds each as a Promise; here each waits on its own goroutine. Upstream's details() records the value
			// synchronously and only its settlement is pending (tool.ts:202-209), so each goroutine's value is recorded
			// before the tool goes on.
			first := make(chan struct{})
			go func() {
				_ = api.Details(testContext, map[string]any{"n": float64(1)})
				settled.add("first")
				close(first)
			}()
			if err := awaitDetailsRecorded(ctx, api, 1); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			go func() {
				_ = api.Details(testContext, map[string]any{"n": float64(2)})
				settled.add("second")
			}()
			if err := awaitDetailsRecorded(ctx, api, 2); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			<-first
			go func() {
				_ = api.Details(testContext, map[string]any{"n": float64(3)})
				settled.add("third")
			}()
			if err := awaitDetailsRecorded(ctx, api, 3); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
		}))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"details", map[string]any{}, "c1"}), tlDone()}, nil)
		eventually(t, func() bool { return settled.len() == 3 })
		if got := settled.all(); slices.Index(got, "first") > slices.Index(got, "third") || !slices.Contains(got, "second") {
			t.Fatalf("settled %v, want first before third and second included", got)
		}
		if got := tlResults(run.entries)[0].Details; !reflect.DeepEqual(got, map[string]any{"n": float64(3)}) {
			t.Fatalf("details %v, want n: 3", got)
		}
		mustClose(t, run.harness)
	})

	t.Run("finishes a call under the implementation it resolved when the tool is replaced mid-call", func(t *testing.T) {
		setup := chatSetup(t)
		started, finished := deferred(), deferred()
		v1 := tlTool("work", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			started.resolve()
			if err := finished.wait(testContext); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return ownText("v1"), nil
		})
		addTool(t, setup.Registry, v1)
		setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"work", map[string]any{}, "c1"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := ownSubmit(t, root, ownInput("go"))
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		// The same extension name replaces the old one in place.
		addTool(t, setup.Registry, tlTool("work", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return ownText("v2"), nil
		}))
		finished.resolve()
		if _, err := submission.Wait(testContext); err != nil {
			t.Fatal(err)
		}
		tlExpectText(t, tlResultText(tlResults(allEntries(t, root))[0]), "v1")
		mustClose(t, harness)
	})

	t.Run("uses a section and hook extension reloaded mid-run from the run's next request", func(t *testing.T) {
		setup := chatSetup(t)
		requests := &syncList[string]{}
		prompt := func(version string) *durable.Extension {
			return new(durable.Extension{
				Name:     "prompt",
				Sections: []*durable.PromptSection{Section("mode", func(context.Context, durable.PromptInput) (*string, error) { return &version, nil })},
				Hooks: []durable.HookRegistration{Hook(GenerationTask, &GenerationHooks{BeforeRequest: func(context.Context, GenerationRequest, HookApi) (*GenerationRequest, error) {
					requests.add(version)
					return nil, nil
				}})},
			})
		}
		mustInstall(t, setup.Registry, prompt("v1"))
		running, reloaded := deferred(), deferred()
		addTool(t, setup.Registry, tlTool("work", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			running.resolve()
			if err := reloaded.wait(testContext); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"work", map[string]any{}, "c1"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := ownSubmit(t, root, ownInput("go"))
		if err := running.wait(testContext); err != nil {
			t.Fatal(err)
		}
		mustInstall(t, setup.Registry, prompt("v2"))
		reloaded.resolve()
		if _, err := submission.Wait(testContext); err != nil {
			t.Fatal(err)
		}
		sections := []string{}
		for _, entry := range allEntries(t, root) {
			if len(entry.Model) == 0 {
				continue
			}
			if message, ok := entry.Model[0].(ai.SystemMessage); ok && len(message.Sections) > 0 {
				for _, each := range message.Sections {
					sections = append(sections, each.Name+"="+*each.Value)
				}
			}
		}
		if want := []string{"mode=<mode>\nv1\n</mode>", "mode=<mode>\nv2\n</mode>"}; !reflect.DeepEqual(sections, want) {
			t.Fatalf("sections %q, want %q", sections, want)
		}
		if !reflect.DeepEqual(requests.all(), []string{"v1", "v2"}) {
			t.Fatalf("requests %v, want [v1 v2]", requests.all())
		}
		mustClose(t, harness)
	})

	t.Run("rejects invocation-bound waits and stops watches when the tool's invocation ends", func(t *testing.T) {
		setup := chatSetup(t)
		type neverInput struct{}
		never := durable.DefineTask(durable.TaskDefinition[neverInput, stepState, durable.JsonValue, any]{
			Name:    "test.never",
			Version: 1,
			Initial: func(neverInput) stepState { return stepState{Phase: "never"} },
			Phases: map[string]durable.PhaseHandler[neverInput, stepState, durable.JsonValue, any]{
				"never": func(context.Context, durable.RunningTask[neverInput, stepState, durable.JsonValue], durable.TaskRuntime[neverInput, stepState, durable.JsonValue, any]) error {
					return nil
				},
			},
			Abort: tkNoopAbort[neverInput, stepState, durable.JsonValue, any],
		})
		waited := make(chan error, 1)
		watched := &syncValue[durable.WatchHandle[durable.JsonObject]]{}
		addTool(t, setup.Registry, tlTool("detach", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			// The child's definition is not registered, so it stays pending.
			child, err := api.CreateTaskErased(ctx, never, durable.JsonObject{}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			go func() {
				_, waitErr := api.WaitForTask(testContext, child)
				waited <- waitErr
			}()
			watch, err := api.WatchDocErased(ctx, LiveDoc, api.ConversationId())
			if err != nil || watch == nil {
				return durable.ToolExecutionResult{}, errors.New("no live document watch")
			}
			watched.put(watch)
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
		}))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"detach", map[string]any{}, "c1"}), tlDone()}, nil)
		tkContainsError(t, tkWaitErr(t, waited), "invocation has ended")
		watch, _ := watched.get()
		select {
		case <-watch.Closed():
		case <-time.After(10 * time.Second):
			t.Fatal("the watch stayed open after the invocation ended")
		}
		mustClose(t, run.harness)
	})

	t.Run("rejects details() still waiting when the call is aborted during afterTool", func(t *testing.T) {
		// Hold every throttled progress commit, so only the abort can settle the details' waiter.
		previous := toolProgressClock
		toolProgressClock = heldProgressClock{}
		t.Cleanup(func() { toolProgressClock = previous })
		setup := chatSetup(t)
		pendingDetails := make(chan error, 1)
		inAfterTool := deferred()
		addTool(t, setup.Registry, tlTool("slow", func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			api.Output("first\n")
			// The output commit is in flight, so these details wait for the next throttle window.
			go func() { pendingDetails <- api.Details(testContext, map[string]any{"step": float64(1)}) }()
			// Upstream records the details and queues their waiter before details() returns its Promise; wait for the
			// queued waiter so the call has not settled before the goroutine's details() is pending.
			if err := awaitDetailsWaiting(testContext, api); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return durable.ToolExecutionResult{}, nil
		}))
		addHooks(t, setup.Registry, ToolTask, &ToolHooks{AfterTool: func(ctx context.Context, _ ai.ToolCall, _ durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
			inAfterTool.resolve()
			return nil, abortedBy(ctx)
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"slow", map[string]any{}, "c1"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := ownSubmit(t, root, ownInput("go"))
		if err := inAfterTool.wait(testContext); err != nil {
			t.Fatal(err)
		}
		slot := tlLiveTool(t, harness, root.Id())
		if _, err := harness.AbortTask(testContext, durable.TaskId(slot["taskId"].(float64))); err != nil {
			t.Fatal(err)
		}
		if err := tkWaitErr(t, pendingDetails); err == nil {
			t.Fatal("details() resolved although its call was aborted during afterTool")
		}
		if _, err := submission.Wait(testContext); err != nil {
			t.Fatal(err)
		}
		mustClose(t, harness)
	})

	t.Run("answers an aborted tool with only its durable output, discarding buffered output", func(t *testing.T) {
		setup := chatSetup(t)
		buffered := deferred()
		addTool(t, setup.Registry, tlTool("slow", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			api.Output("durable\n")
			// The first output commits at once; this one waits for the next throttle window.
			time.Sleep(20 * time.Millisecond)
			api.Output("buffered\n")
			buffered.resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"slow", map[string]any{}, "c1"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := ownSubmit(t, root, ownInput("go"))
		if err := buffered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		slot := tlLiveTool(t, harness, root.Id())
		if _, err := harness.AbortTask(testContext, durable.TaskId(slot["taskId"].(float64))); err != nil {
			t.Fatal(err)
		}
		if _, err := submission.Wait(testContext); err != nil {
			t.Fatal(err)
		}
		tlExpectText(t, tlResultText(tlResults(allEntries(t, root))[0]), "durable\n|<harness>\n[error] Tool slow was aborted\n</harness>")
		mustClose(t, harness)
	})
}

// awaitDetailsRecorded waits until the call has recorded count details values. Upstream's api.details records its value
// before it returns the Promise (tool.ts:202-209); the Go Details records and then blocks until the commit, so a caller
// that leaves the wait pending runs it on a goroutine and observes the record here before going on.
// heldProgressClock stands still and never fires a throttled commit: the first commit, due at once, runs, and every
// later one waits on a timer that does not fire, however slow the first commit was.
type heldProgressClock struct{}

func (heldProgressClock) now() float64 { return 0 }

func (heldProgressClock) afterFunc(float64, func()) func() { return func() {} }

// awaitDetailsWaiting returns once a details() call has queued its progress waiter.
func awaitDetailsWaiting(ctx context.Context, api durable.ToolExecutionApi) error {
	internal, ok := api.(*toolApi)
	if !ok {
		return fmt.Errorf("tool api is %T, not *toolApi", api)
	}
	for {
		internal.progress.mu.Lock()
		waiting := len(internal.progress.waiters)
		internal.progress.mu.Unlock()
		if waiting > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(time.Millisecond):
		}
	}
}

func awaitDetailsRecorded(ctx context.Context, api durable.ToolExecutionApi, count int) error {
	internal, ok := api.(*toolApi)
	if !ok {
		return fmt.Errorf("tool api is %T, not *toolApi", api)
	}
	for {
		internal.reported.mu.Lock()
		recorded := internal.reported.detailsVersion
		internal.reported.mu.Unlock()
		if recorded >= count {
			return nil
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(time.Millisecond):
		}
	}
}
