package experimental

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
	"github.com/MichaelKinsy/PiG/tui"
)

type clientTuiDirectoryFixture struct {
	state *chord.MutableReplicatedState[*services.SessionDirectoryState]
}

func (fixture clientTuiDirectoryFixture) State() chord.ReplicatedStateOf[*services.SessionDirectoryState] {
	return fixture.state
}

// upstream: packages/coding-agent/src/experimental/client-tui-chat.ts:45-77,153-180. The streaming answer's component becomes its entry's; a changed entry prefix, or a partial that vanished, rebuilds the transcript.
func TestClientChatViewReusesStreamingComponentAndRebasesDivergentPrefix(t *testing.T) {
	observation := newClientTuiObservation(t)
	view := NewExperimentalChatView(t.Context(), t.TempDir(), observation.RequestRender, observation.Executor.RunOnMain)
	t.Cleanup(func() {
		if err := view.Dispose(); err != nil {
			t.Error(err)
		}
	})
	assistant := ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "partial"}}}
	streamingView := conversationView([]durable.EntryRecord{userEntry(1, "before")}, &harness.LiveState{Run: &harness.LiveRun{TaskId: 1}, Generation: generationOf(assistant)}, nil)
	var streaming *tui.AssistantMessageBlock
	var beforeChildren int
	if err := observation.Executor.RunOnMain(t.Context(), func() {
		if err := view.Apply(streamingView); err != nil {
			t.Error(err)
		}
		streaming = view.streaming
		beforeChildren = view.Transcript.ChildCount()
		if err := view.Apply(streamingView); err != nil {
			t.Error(err)
		}
		if view.streaming != streaming || view.Transcript.ChildCount() != beforeChildren {
			t.Error("unchanged view replayed transcript components")
		}
		settled := ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "settled"}}, StopReason: ai.StopReasonStop}
		if err := view.Apply(conversationView([]durable.EntryRecord{userEntry(1, "before"), assistantEntry(2, settled)}, nil, nil)); err != nil {
			t.Error(err)
		}
		if view.streaming != nil || view.Transcript.ChildCount() != beforeChildren || streaming.Text() != "settled" {
			t.Error("settled assistant did not reuse the streaming component")
		}
		if !reflect.DeepEqual(view.renderedEntryIds, []durable.EntryId{1, 2}) {
			t.Errorf("rendered IDs = %#v", view.renderedEntryIds)
		}
		if err := view.Apply(conversationView([]durable.EntryRecord{resetEntry(7)}, nil, nil)); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(view.renderedEntryIds, []durable.EntryId{7}) {
			t.Errorf("rebase IDs = %#v", view.renderedEntryIds)
		}
		// A partial dropped without its entry, for example by a retry, renders the transcript again.
		if err := view.Apply(conversationView([]durable.EntryRecord{userEntry(8, "retry")}, &harness.LiveState{Run: &harness.LiveRun{TaskId: 1}, Generation: generationOf(assistant)}, nil)); err != nil {
			t.Error(err)
		}
		if view.streaming == nil {
			t.Error("a new partial did not create a streaming component")
		}
		before := view.streaming
		if err := view.Apply(conversationView([]durable.EntryRecord{userEntry(8, "retry")}, &harness.LiveState{Run: &harness.LiveRun{TaskId: 1}}, nil)); err != nil {
			t.Error(err)
		}
		if view.streaming != nil || view.Transcript.ChildCount() != 2 || before == view.streaming {
			t.Errorf("a dropped partial stayed rendered: children = %d", view.Transcript.ChildCount())
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.Dispose(); err != nil {
		t.Fatal(err)
	}
}

// upstream: packages/coding-agent/src/experimental/client-tui-chat.ts:75
func TestClientQueueWhitespacePreservesBoundarySpaces(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ input, want string }{
		{"", ""}, {"  one\n\t two  ", " one two "}, {"\u00a0one\ufefftwo\u2028", " one two "}, {"x\u0085y", "x\u0085y"},
	} {
		if got := collapseClientQueueWhitespace(row.input); got != row.want {
			t.Errorf("collapse(%q) = %q, want %q", row.input, got, row.want)
		}
	}
}

// upstream: packages/coding-agent/src/experimental/client-tui.ts:387-388; packages/tui/src/components/select-list.ts:259
func TestClientTuiSelectionUsesFirstMatchingValueAndLabelFallback(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	tui.SetTUIKeybindings(tui.NewTUIKeybindingsManager(nil))
	t.Cleanup(func() { tui.SetTUIKeybindings(previous) })
	observation := newClientTuiObservation(t)
	lifetime, cancel := context.WithCancel(t.Context())
	component := &ExperimentalClientTui{ctx: lifetime, cancel: cancel, cwd: t.TempDir(), requestRender: observation.RequestRender, runOnMain: observation.Executor.RunOnMain, queueMicrotask: observation.Executor.QueueMicrotask}
	if err := observation.Executor.RunOnMain(t.Context(), component.initialize); err != nil {
		t.Fatal(err)
	}
	description := "owned description"
	items := []services.PresentationSelectItem{{Value: "duplicate", Description: &description}, {Value: "duplicate", Label: "second"}}
	finished := make(chan struct{})
	var value *string
	var selectError error
	go func() {
		defer close(finished)
		value, selectError = (clientPresentationUI{component: component}).Select(context.Background(), "Choice:", items, new("duplicate"))
	}()
	t.Cleanup(func() {
		if err := component.Close(); err != nil {
			t.Error(err)
		}
		<-finished
	})
	if _, err := observation.Wait(t.Context(), component, 80, func(lines []string) bool { return strings.Contains(strings.Join(lines, "\n"), "Choice:") }); err != nil {
		t.Fatal(err)
	}
	description = "changed by caller"
	if err := observation.Executor.RunOnMain(t.Context(), func() {
		if index := component.selectList.CursorIndex(); index != 0 {
			t.Errorf("selected duplicate index = %d, want first", index)
		}
		if got := component.selectList.Labels; !reflect.DeepEqual(got, []string{"duplicate", "second"}) {
			t.Errorf("labels = %#v", got)
		}
		if got := *component.selection.items[0].Description; got != "owned description" {
			t.Errorf("aliased description = %q", got)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := observation.HandleInput(t.Context(), component, "\r"); err != nil {
		t.Fatal(err)
	}
	<-finished
	if selectError != nil || value == nil || *value != "duplicate" {
		t.Fatalf("selection = %v, %v", value, selectError)
	}
}

type clientTuiPromptContextProbe struct {
	services.AgentController
	calls chan context.Context
}

func (probe clientTuiPromptContextProbe) Prompt(ctx context.Context, _ services.AgentPromptRequest) (services.AgentOperationResponse, error) {
	probe.calls <- ctx
	return services.AgentOperationResponse{Accepted: true, OperationID: new("context-operation")}, nil
}

// The component lifetime controls UI observation, not the Context value delivered to the service.
// upstream: packages/coding-agent/src/experimental/client-tui.ts:563-572
func TestClientTuiPromptPreservesBackgroundServiceContext(t *testing.T) {
	observation := newClientTuiObservation(t)
	provider, err := chord.NewRemoteServiceProvider(chord.SingletonService(services.AgentControllerDefinition))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := provider.Dispose(); err != nil {
			t.Error(err)
		}
	})
	probe := clientTuiPromptContextProbe{calls: make(chan context.Context, 1)}
	if err := chord.Provide[services.AgentController](provider, services.AgentControllerDefinition, probe); err != nil {
		t.Fatal(err)
	}
	binding, err := chord.CreateRemoteServiceBinding(chord.RemoteServiceBindingOptions{Services: []string{services.AgentControllerDefinition.Id()}, Transport: chord.NewLoopbackTransport(provider)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := binding.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	controller, err := chord.UseRemoteClient(binding, services.AgentControllerDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	lifetime, cancel := context.WithCancel(t.Context())
	component := &ExperimentalClientTui{ctx: lifetime, cancel: cancel, cwd: t.TempDir(), controller: controller, requestRender: observation.RequestRender, runOnMain: observation.Executor.RunOnMain, queueMicrotask: observation.Executor.QueueMicrotask}
	t.Cleanup(func() {
		if err := component.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := observation.Executor.RunOnMain(t.Context(), func() { component.initialize(); component.runPrompt("  background call \n") }); err != nil {
		t.Fatal(err)
	}
	select {
	case callContext := <-probe.calls:
		if callContext != context.Background() {
			t.Fatalf("service Context = %T, want context.Background()", callContext)
		}
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
}

// The public bootstrap and Stock driver delegate to the same native renderer/theme core.
func TestClientTuiSharedPresentationBootstrap(t *testing.T) {
	previousTheme, previousRegistry := tui.ActiveTheme(), tui.ActiveThemeRegistry()
	t.Cleanup(func() {
		registry := tui.NewThemeRegistry()
		registry.Add(previousTheme)
		tui.SetThemeRegistry(registry)
		tui.SetThemeByName(previousTheme.Name)
		tui.SetThemeRegistry(previousRegistry)
	})
	observation := newClientTuiObservation(t)
	var output bytes.Buffer
	renderer := codingagent.CreateInteractiveTui(codingagent.InteractiveTuiOptions{TuiMode: "fullscreen", Output: &output, ShowHardwareCursor: new(true), FullscreenCopyOnSelect: new(false)})
	alt, ok := renderer.(*tui.TuiAltScreen)
	if !ok || !alt.GetShowHardwareCursor() || alt.GetCopyOnSelect() {
		t.Fatalf("native renderer/options = %#v", renderer)
	}
	renderer.SetRenderDispatcher(func(render func()) {
		if err := observation.Executor.RunOnMain(t.Context(), render); err != nil && t.Context().Err() == nil {
			t.Errorf("render dispatch: %v", err)
		}
	})
	t.Cleanup(func() {
		if err := observation.Executor.RunOnMain(context.Background(), renderer.Stop); err != nil {
			t.Error(err)
		}
	})
	settings := codingagent.NewSettingsManager(t.TempDir(), t.TempDir())
	if err := settings.SetTheme("light"); err != nil {
		t.Fatal(err)
	}
	changed := 0
	controller, err := codingagent.NewInteractiveThemeController(t.Context(), renderer, codingagent.InteractiveThemeControllerOptions{
		GetSettingsManager: func() *codingagent.SettingsManager { return settings }, Output: &output,
		RunOnMain: observation.Executor.RunOnMain,
		ShowError: func(message string) { t.Errorf("theme error: %s", message) }, OnChanged: func() { changed++; renderer.RequestRender() },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := controller.Dispose(); err != nil {
			t.Error(err)
		}
	})
	if err := controller.ApplyFromSettings(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := observation.Executor.RunOnMain(t.Context(), func() {
		if name := tui.ActiveTheme().Name; name != "light" || changed != 1 {
			t.Errorf("theme = %q, application notifications = %d; want light and one", name, changed)
		}
		controller.DisableAutoSync()
	}); err != nil {
		t.Fatal(err)
	}
	if err := controller.Dispose(); err != nil {
		t.Fatal(err)
	}
}

// The selected override owns its admitted operation independently of the UI observer.
// upstream: packages/coding-agent/src/experimental/client-tui.ts:563-572,600-611
func TestClientTuiAdmitsPromptBeforeAbortWithoutOwningBackgroundCompletion(t *testing.T) {
	observation := newClientTuiObservation(t)
	entered, waiting := make(chan string, 2), make(chan struct{})
	finished := make(chan struct{})
	var contexts []context.Context
	initiator := &clientTuiInitiationProbe{entered: entered, waiting: waiting, finished: finished, contexts: &contexts}
	lifetime, cancel := context.WithCancel(t.Context())
	component := &ExperimentalClientTui{ctx: lifetime, cancel: cancel, controller: initiator, cwd: t.TempDir(), requestRender: observation.RequestRender, runOnMain: observation.Executor.RunOnMain, queueMicrotask: observation.Executor.QueueMicrotask}
	t.Cleanup(func() {
		close(finished)
		if err := component.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := observation.Executor.RunOnMain(t.Context(), func() {
		component.initialize()
		component.runPrompt("prompt")
		view := conversationView(nil, &harness.LiveState{Run: &harness.LiveRun{TaskId: 1}}, nil)
		component.conversation = &view
		component.interrupt()
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"prompt", "abort"} {
		select {
		case got := <-entered:
			if got != want {
				t.Fatalf("admission = %q, want %q", got, want)
			}
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
	<-waiting
	if err := component.Close(); err != nil {
		t.Fatal(err)
	}
	for _, ctx := range contexts {
		if ctx != context.Background() {
			t.Fatalf("admitted Context = %T", ctx)
		}
	}
	select {
	case <-finished:
		t.Fatal("UI closed the background producer")
	default:
	}
}

type clientTuiInitiationProbe struct {
	services.AgentController
	services.AgentControllerInitiator
	entered  chan string
	waiting  chan struct{}
	finished chan struct{}
	contexts *[]context.Context
}

func (probe *clientTuiInitiationProbe) BeginPrompt(ctx context.Context, _ services.AgentPromptRequest) (*chord.ServiceResultInvocation[services.AgentOperationResponse], error) {
	*probe.contexts = append(*probe.contexts, ctx)
	probe.entered <- "prompt"
	return chord.NewServiceResultInvocation(func(observer context.Context) (services.AgentOperationResponse, error) {
		close(probe.waiting)
		select {
		case <-observer.Done():
			return services.AgentOperationResponse{}, observer.Err()
		case <-probe.finished:
			return services.AgentOperationResponse{Accepted: true}, nil
		}
	}), nil
}
func (probe *clientTuiInitiationProbe) BeginAbort(ctx context.Context) (*chord.ServiceInvocation, error) {
	*probe.contexts = append(*probe.contexts, ctx)
	probe.entered <- "abort"
	return chord.NewServiceInvocation(func(context.Context) (json.RawMessage, error) { return nil, nil }), nil
}

// The card owner must join the shell timer even when no final tool result arrives.
// upstream: packages/coding-agent/src/core/tools/renderers/bash.ts:createShellRenderers
func TestClientTuiToolCardDisposalJoinsUnfinishedShell(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var requests atomic.Uint64
		card := codingagent.NewToolRendererCard(t.Context(), "bash", "unfinished", t.TempDir(), []byte(`{"command":"printf unfinished"}`), codingagent.CreateAllToolRenderers()["bash"], func() { requests.Add(1) })
		card.Component.MarkExecutionStarted()
		card.Component.SetResultValue(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}})
		card.Component.SetStreaming("partial")
		card.Component.Render(80)
		card.Dispose()
		card.Dispose()
		before := requests.Load()
		card.Component.Invalidate()
		card.Component.Render(80)
		synctest.Wait()
		if after := requests.Load(); after != before {
			t.Fatalf("disposed card invalidated UI: %d -> %d", before, after)
		}
	})
}

// Nested owner reentry is synchronous, while Promise continuations drain once at the top-level checkpoint.
func TestClientTuiExecutorOwnsMicrotaskCheckpoint(t *testing.T) {
	t.Parallel()
	executor := NewClientTuiExecutor()
	t.Cleanup(executor.Close)
	var order []string
	err := executor.RunOnMain(t.Context(), func() {
		order = append(order, "turn")
		if err := executor.QueueMicrotask(t.Context(), func() {
			order = append(order, "first")
			if err := executor.QueueMicrotask(t.Context(), func() { order = append(order, "nested microtask") }); err != nil {
				t.Error(err)
			}
		}); err != nil {
			t.Error(err)
		}
		if err := executor.RunOnMain(t.Context(), func() {
			order = append(order, "inline")
			if err := executor.QueueMicrotask(t.Context(), func() { order = append(order, "second") }); err != nil {
				t.Error(err)
			}
		}); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(order, []string{"turn", "inline"}) {
			t.Errorf("nested call drained checkpoint early: %#v", order)
		}
		order = append(order, "turn end")
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"turn", "inline", "turn end", "first", "second", "nested microtask"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("turn order = %#v, want %#v", order, want)
	}
	failure := errors.New("microtask failure")
	if err := executor.QueueMicrotask(t.Context(), func() { panic(failure) }); !errors.Is(err, failure) {
		t.Fatalf("checkpoint failure = %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := executor.QueueMicrotask(cancelled, func() { t.Error("cancelled task ran") }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled enqueue = %v", err)
	}
	executor.Close()
	if err := executor.QueueMicrotask(t.Context(), func() { t.Error("closed task ran") }); err == nil {
		t.Fatal("closed executor accepted continuation")
	}
}

// Publication and immediate observation share the actual producer turn, before the close-lane Promise continuation.
func TestClientTuiPublishedTurnPrecedesRecoveryDetachment(t *testing.T) {
	observation := newClientTuiObservation(t)
	lifetime, cancel := context.WithCancel(t.Context())
	component := &ExperimentalClientTui{ctx: lifetime, cancel: cancel, cwd: t.TempDir(), requestRender: observation.RequestRender, runOnMain: observation.Executor.RunOnMain, queueMicrotask: observation.Executor.QueueMicrotask}
	removed := false
	if err := observation.Executor.RunOnMain(t.Context(), func() {
		component.initialize()
		component.opened, component.selectedServerId, component.sessionId = true, "server", "session"
		component.laneUnsubscribe = func() { removed = true }
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := component.Close(); err != nil {
			t.Error(err)
		}
	})
	connection, err := chord.NewReplicatedState(&services.ServerConnectionState{Status: "connected"})
	if err != nil {
		t.Fatal(err)
	}
	remove, err := connection.Subscribe(func(state *services.ServerConnectionState, _ context.Context, _ chord.ReplicatedStateDelivery) {
		if err := observation.Executor.RunOnMain(t.Context(), func() { component.handleConnectionState("server", *state) }); err != nil {
			t.Error(err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remove)
	lines, err := observation.PublishAndRender(t.Context(), component, 80, func() error {
		return connection.Replace(context.Background(), &services.ServerConnectionState{Status: "disconnected", Since: "later", Reason: "network lost"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "retrying") {
		t.Fatalf("published-turn frame lost retry status: %q", lines)
	}
	if err := observation.Executor.RunOnMain(t.Context(), func() {
		if !removed || component.laneUnsubscribe != nil {
			t.Error("checkpoint did not detach before next turn")
		}
		if current := strings.Join(component.Render(80), "\n"); strings.Contains(current, "retrying") {
			t.Error("post-checkpoint render replayed pre-checkpoint retrying frame")
		}
		component.handleConnectionState("server", services.ServerConnectionState{Status: "connecting", Attempt: 1})
		component.handleConnectionState("server", services.ServerConnectionState{Status: "connected", Since: "reconnected"})
		if current := strings.Join(component.Render(80), "\n"); !strings.Contains(current, "Reattaching") || !component.busy {
			t.Errorf("reconnect turn was presented ready before reattachment: %q", current)
		}
	}); err != nil {
		t.Fatal(err)
	}
}

type clientTuiStagedTranscript struct {
	state *clientTuiStagedTranscriptState
}

func (transcript clientTuiStagedTranscript) State() chord.ReplicatedStateOf[services.ConversationView] {
	return transcript.state
}

type clientTuiStagedTranscriptState struct {
	state   *chord.MutableReplicatedState[services.ConversationView]
	entered chan struct{}
	release chan struct{}
	removed atomic.Bool
}

func (state *clientTuiStagedTranscriptState) Value() services.ConversationView {
	return state.state.Value()
}

func (state *clientTuiStagedTranscriptState) Subscribe(listener func(services.ConversationView, context.Context, chord.ReplicatedStateDelivery)) (func(), error) {
	remove, err := state.state.Subscribe(listener)
	if err != nil {
		return nil, err
	}
	close(state.entered)
	<-state.release
	return func() { state.removed.Store(true); remove() }, nil
}

// Staging uses the real replicated state and real owner executor. Only subscription return is gated, so updates can arrive before the atomic mount.
func TestClientTuiStagedLaneCommitFencesAndUpdates(t *testing.T) {
	for _, mode := range []string{"update", "replacement", "close", "initialization error", "reconnect turn"} {
		t.Run(mode, func(t *testing.T) {
			observation := newClientTuiObservation(t)
			state, err := chord.NewReplicatedState(conversationView([]durable.EntryRecord{userEntry(1, "initial")}, nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			staged := &clientTuiStagedTranscriptState{state: state, entered: make(chan struct{}), release: make(chan struct{})}
			release := sync.OnceFunc(func() { close(staged.release) })
			lifetime, cancel := context.WithCancel(t.Context())
			ui := tui.NewWithOutput(io.Discard, 80, 24)
			component := &ExperimentalClientTui{ctx: lifetime, cancel: cancel, ui: ui, cwd: t.TempDir(), requestRender: observation.RequestRender, runOnMain: observation.Executor.RunOnMain, queueMicrotask: observation.Executor.QueueMicrotask}
			feature := &clientTuiSessionFeature{serverId: "server", transcript: clientTuiStagedTranscript{state: staged}}
			t.Cleanup(func() {
				release()
				if err := component.Close(); err != nil {
					t.Error(err)
				}
				ui.CancelPendingRender()
			})
			if err := observation.Executor.RunOnMain(t.Context(), func() {
				component.initialize()
				component.session = feature
				component.opened, component.selectedServerId, component.sessionId = true, "server", "session"
				component.busy, component.status = true, "Reattaching Session…"
				component.rebuild()
				if mode == "reconnect turn" {
					component.handleConnectionState("server", services.ServerConnectionState{Status: "connecting", Attempt: 1})
					component.handleConnectionState("server", services.ServerConnectionState{Status: "connected", Since: "reconnected"})
				}
				component.handleAttachmentState(feature, services.SessionAttachmentState{Status: "attached", SessionID: "session"})
			}); err != nil {
				t.Fatal(err)
			}
			<-staged.entered
			lines, err := observation.Render(t.Context(), component, 80)
			if err != nil || !strings.Contains(strings.Join(lines, "\n"), "Reattaching") {
				t.Fatalf("staging exposed ready/blank frame: %q, %v", lines, err)
			}
			var closed chan struct{}
			var closeError error
			switch mode {
			case "update":
				if err := state.Replace(context.Background(), conversationView([]durable.EntryRecord{userEntry(1, "initial"), userEntry(2, "arrived")}, nil, nil)); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				if err := observation.Executor.RunOnMain(t.Context(), func() {
					component.handleConnectionState("server", services.ServerConnectionState{Status: "disconnected"})
				}); err != nil {
					t.Fatal(err)
				}
			case "close":
				closed = make(chan struct{})
				go func() { defer close(closed); closeError = component.Close() }()
				t.Cleanup(func() { release(); <-closed })
				<-lifetime.Done()
			case "initialization error":
				if err := state.Replace(context.Background(), services.ConversationView{Docs: map[string]durable.JsonObject{services.LiveDocKind: {"run": "not a run"}}}); err != nil {
					t.Fatal(err)
				}
			}
			release()
			component.tasks.Wait()
			if closed != nil {
				<-closed
				if closeError != nil {
					t.Fatal(closeError)
				}
			}
			if err := observation.Executor.RunOnMain(context.Background(), func() {
				if mode == "update" || mode == "reconnect turn" {
					if component.chatView == nil || component.busy {
						t.Error("updated staged lane did not become ready")
						return
					}
					want := []durable.EntryId{1}
					if mode == "update" {
						want = append(want, 2)
					}
					if ids := component.chatView.renderedEntryIds; !reflect.DeepEqual(ids, want) {
						t.Errorf("staged update order = %#v", ids)
					}
					if frame := strings.Join(component.Render(80), "\n"); strings.Contains(frame, "Reattaching") {
						t.Error("ready frame retained reconnect label")
					}
				} else {
					if component.chatView != nil || component.laneUnsubscribe != nil {
						t.Error("retired/error staging left a mounted reference")
					}
					if !staged.removed.Load() {
						t.Error("retired staged subscription remains live")
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The production driver executor owns every UI mutation; observer wakeups never execute UI work themselves.
func TestClientTuiExecutorOwnsMutationAndCancellation(t *testing.T) {
	t.Parallel()
	observation := newClientTuiObservation(t)
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	var firstError error
	go func() {
		defer close(finished)
		firstError = observation.Executor.RunOnMain(t.Context(), func() { close(entered); <-release })
	}()
	t.Cleanup(func() { releaseOnce(); <-finished })
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	if err := observation.Executor.RunOnMain(ctx, func() { called = true }); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancelled mutation = %v, called=%v", err, called)
	}
	releaseOnce()
	<-finished
	if firstError != nil {
		t.Fatal(firstError)
	}
	failure := errors.New("mutation failure")
	if err := observation.Executor.RunOnMain(t.Context(), func() { panic(failure) }); !errors.Is(err, failure) {
		t.Fatalf("mutation failure = %v", err)
	}
	text := tui.NewText("before")
	if err := observation.Executor.RunOnMain(t.Context(), func() { text.SetText("after"); observation.RequestRender() }); err != nil {
		t.Fatal(err)
	}
	// tui.Text pads each line to the render width; the mutation is the trimmed content.
	lines, err := observation.Wait(t.Context(), text, 80, func(lines []string) bool { return len(lines) == 1 && strings.TrimRight(lines[0], " ") == "after" })
	if err != nil || len(lines) != 1 || lines[0] != "after"+strings.Repeat(" ", 75) {
		t.Fatalf("observed mutation = %#v, %v", lines, err)
	}
	if got := observation.Requests(); got != 1 {
		t.Fatalf("render requests = %d, want one mutation notification", got)
	}
	input := tui.NewInput(tui.InputOptions{})
	if err := observation.HandleInput(t.Context(), input, "typed"); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := observation.Executor.RunOnMain(t.Context(), func() { value = input.Text() }); err != nil {
		t.Fatal(err)
	}
	if value != "typed" {
		t.Fatalf("input mutation = %q", value)
	}
	observation.Executor.Close()
	observation.Executor.Close()
	if err := observation.Executor.RunOnMain(t.Context(), func() { called = true }); err == nil || called {
		t.Fatalf("closed mutation = %v, called=%v", err, called)
	}
}

// This guards namespace ownership and hydration in the shared source fixture, not the original new/continued/plugin-selected integration rows.
// upstream: packages/coding-agent/test/experimental-client-tui.test.ts:274-335
func TestClientTuiLoopbackSourcesRetainBindingsAcrossScopes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	directory, err := chord.NewReplicatedState(&services.SessionDirectoryState{Revision: 7, Sessions: []services.SessionSummary{}})
	if err != nil {
		t.Fatal(err)
	}
	serverProvider, err := chord.NewRemoteServiceProvider(chord.SingletonService(services.SessionDirectoryDefinition))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := serverProvider.Dispose(); err != nil {
			t.Error(err)
		}
	})
	if err := chord.Provide[services.SessionDirectory](serverProvider, services.SessionDirectoryDefinition, clientTuiDirectoryFixture{state: directory}); err != nil {
		t.Fatal(err)
	}
	sessionProvider, err := chord.NewRemoteServiceProvider()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sessionProvider.Dispose(); err != nil {
			t.Error(err)
		}
	})
	connection, err := chord.NewReplicatedState(&services.ServerConnectionState{Status: "connected", Since: "now"})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := chord.NewReplicatedState(&services.SessionAttachmentState{Status: "detached"})
	if err != nil {
		t.Fatal(err)
	}
	server, closeBindings := newClientTuiLoopbackServer(t, "source-fixture", serverProvider, sessionProvider, connection, attachment)
	scope, err := server.Server.Open(chord.RemoteServiceSourceOpenOptions{Services: []string{services.SessionDirectoryDefinition.Id()}})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := scope.Use(services.SessionDirectoryDefinition.Id())
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := remote.State("state")
	if err != nil {
		t.Fatal(err)
	}
	replica := chord.TypedReplica[*services.SessionDirectoryState](state)
	if got := replica.Value(); !reflect.DeepEqual(got, directory.Value()) {
		t.Fatalf("hydration = %#v, want %#v", got, directory.Value())
	}
	if err := scope.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if err := directory.Replace(ctx, &services.SessionDirectoryState{Revision: 8, Sessions: []services.SessionSummary{}}); err != nil {
		t.Fatal(err)
	}
	if got := replica.Value().Revision; got != 8 {
		t.Fatalf("scope disposed shared namespace: revision = %d, want 8", got)
	}
	var attachmentStates []services.SessionAttachmentState
	remove, err := server.Session.Attachment().Subscribe(func(state *services.SessionAttachmentState, _ context.Context, _ chord.ReplicatedStateDelivery) {
		attachmentStates = append(attachmentStates, *state)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Session.WhenAttached(ctx, "attached-session"); err != nil {
		t.Fatal(err)
	}
	if err := server.Session.WhenDetached(ctx); err != nil {
		t.Fatal(err)
	}
	remove()
	if err := attachment.Replace(ctx, &services.SessionAttachmentState{Status: "attaching", SessionID: "unobserved"}); err != nil {
		t.Fatal(err)
	}
	want := []services.SessionAttachmentState{{Status: "detached"}, {Status: "attached", SessionID: "attached-session"}, {Status: "detached"}}
	if !reflect.DeepEqual(attachmentStates, want) {
		t.Fatalf("attachment deliveries = %#v, want %#v", attachmentStates, want)
	}
	if err := closeBindings(ctx); err != nil {
		t.Fatal(err)
	}
	if err := closeBindings(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := replica.Load(); err == nil {
		t.Fatal("disposed binding still permits state access")
	}
}

// Sol review P1: Pi's close (client-tui.ts:339-366) never awaits a pending controller Promise
// (client-tui.ts:531-572, 130-138), so Ctrl-C/Ctrl-D must finish while a no-admission call is blocked.
func TestClientTuiCloseDoesNotJoinBlockedNoAdmissionCalls(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	tui.SetTUIKeybindings(tui.NewTUIKeybindingsManager(nil))
	t.Cleanup(func() { tui.SetTUIKeybindings(previous) })
	for _, row := range []struct {
		name  string
		start func(component *ExperimentalClientTui, blocked func() (struct{}, error))
	}{
		{"result invocation", func(component *ExperimentalClientTui, blocked func() (struct{}, error)) {
			startClientResultInvocation(component, func() (*chord.ServiceResultInvocation[struct{}], error) {
				return nil, chord.ErrInvocationAdmissionUnavailable
			}, blocked, func(struct{}) {})
		}},
		{"slash command", func(component *ExperimentalClientTui, blocked func() (struct{}, error)) {
			component.commands = []services.SlashCommandContribution{{Name: "slow", Run: func(context.Context, string) (services.SlashCommandRunResult, error) {
				_, err := blocked()
				return nil, err
			}}}
			component.executeSlashCommand("slow", "")
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			observation := newClientTuiObservation(t)
			lifetime, cancel := context.WithCancel(t.Context())
			component := &ExperimentalClientTui{ctx: lifetime, cancel: cancel, cwd: t.TempDir(), requestRender: observation.RequestRender, runOnMain: observation.Executor.RunOnMain, queueMicrotask: observation.Executor.QueueMicrotask}
			if err := observation.Executor.RunOnMain(t.Context(), component.initialize); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			t.Cleanup(func() {
				close(release)
				component.detached.Wait()
			})
			if err := observation.Executor.RunOnMain(t.Context(), func() {
				row.start(component, func() (struct{}, error) {
					close(entered)
					<-release
					return struct{}{}, nil
				})
			}); err != nil {
				t.Fatal(err)
			}
			<-entered
			closed := make(chan error, 1)
			go func() { closed <- component.Close() }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Close is joined to a blocked no-admission transport call")
			}
		})
	}
}

// blockedAdmissionController blocks BeginPrompt/BeginAbort before admission until released.
type blockedAdmissionController struct {
	services.AgentController
	services.AgentControllerInitiator
	entered chan string
	release chan struct{}
	// disposed reports whether the facet generations were disposed when an admission entered.
	disposed    *atomic.Bool
	sawDisposed atomic.Bool
}

func (probe *blockedAdmissionController) enter(name string) {
	if probe.disposed != nil && probe.disposed.Load() {
		probe.sawDisposed.Store(true)
	}
	probe.entered <- name
}

func (probe *blockedAdmissionController) BeginPrompt(context.Context, services.AgentPromptRequest) (*chord.ServiceResultInvocation[services.AgentOperationResponse], error) {
	probe.enter("prompt")
	<-probe.release
	return chord.NewServiceResultInvocation(func(context.Context) (services.AgentOperationResponse, error) {
		return services.AgentOperationResponse{Accepted: true}, nil
	}), nil
}

func (probe *blockedAdmissionController) BeginAbort(context.Context) (*chord.ServiceInvocation, error) {
	probe.enter("abort")
	return chord.NewServiceInvocation(func(context.Context) (json.RawMessage, error) { return nil, nil }), nil
}

// Pi's close (client-tui.ts:339-366) never awaits the Promise of #submitPrompt (client-tui.ts:563-572), so a transport
// that has not yet admitted the call cannot hold Close. Pi issues every controller call on the input turn, before
// #close disposes the host (client-tui.ts:355-361); an admission that has not started when Close begins is dropped
// rather than issued against the disposed host after Close returns.
func TestClientTuiCloseDoesNotJoinBlockedAdmissionOrAdmitQueuedOneAfterClose(t *testing.T) {
	observation := newClientTuiObservation(t)
	probe := &blockedAdmissionController{entered: make(chan string, 2), release: make(chan struct{})}
	lifetime, cancel := context.WithCancel(t.Context())
	component := &ExperimentalClientTui{ctx: lifetime, cancel: cancel, controller: probe, cwd: t.TempDir(), requestRender: observation.RequestRender, runOnMain: observation.Executor.RunOnMain, queueMicrotask: observation.Executor.QueueMicrotask}
	var released sync.Once
	release := func() { released.Do(func() { close(probe.release) }) }
	t.Cleanup(func() { release(); component.detached.Wait() })
	if err := observation.Executor.RunOnMain(t.Context(), func() {
		component.initialize()
		component.runPrompt("prompt")
		view := conversationView(nil, &harness.LiveState{Run: &harness.LiveRun{TaskId: 1}}, nil)
		component.conversation = &view
		component.interrupt()
	}); err != nil {
		t.Fatal(err)
	}
	if got := <-probe.entered; got != "prompt" {
		t.Fatalf("first admission = %q, want prompt", got)
	}
	closed := make(chan error, 1)
	go func() { closed <- component.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close is joined to a transport call that has not been admitted")
	}
	select {
	case got := <-probe.entered:
		t.Fatalf("%s was admitted before the blocked prompt", got)
	default:
	}
	release()
	component.detached.Wait()
	select {
	case got := <-probe.entered:
		t.Fatalf("%s was admitted after Close had returned and disposed the host", got)
	default:
	}
}

// An admission that has started before Close begins is issued before Close returns, as Pi's synchronous call on the input turn precedes #close (client-tui.ts:563-572, 339-366).
func TestClientTuiCloseIssuesStartedAdmissionsBeforeDisposingFacets(t *testing.T) {
	observation := newClientTuiObservation(t)
	var disposed atomic.Bool
	probe := &blockedAdmissionController{entered: make(chan string, 2), release: make(chan struct{}), disposed: &disposed}
	close(probe.release)
	lifetime, cancel := context.WithCancel(t.Context())
	component := &ExperimentalClientTui{ctx: lifetime, cancel: cancel, controller: probe, sharedFacets: chord.LoadedFacets{Dispose: func(context.Context) error { disposed.Store(true); return nil }}, cwd: t.TempDir(), requestRender: observation.RequestRender, runOnMain: observation.Executor.RunOnMain, queueMicrotask: observation.Executor.QueueMicrotask}
	t.Cleanup(component.detached.Wait)
	if err := observation.Executor.RunOnMain(t.Context(), func() {
		component.initialize()
		component.runPrompt("prompt")
		view := conversationView(nil, &harness.LiveState{Run: &harness.LiveRun{TaskId: 1}}, nil)
		component.conversation = &view
		component.interrupt()
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"prompt", "abort"} {
		select {
		case got := <-probe.entered:
			if got != want {
				t.Fatalf("admission = %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s was not admitted before Close began", want)
		}
	}
	if err := component.Close(); err != nil {
		t.Fatal(err)
	}
	if !disposed.Load() || probe.sawDisposed.Load() {
		t.Fatalf("disposed = %v, admitted after disposal = %v; want disposed after both admissions", disposed.Load(), probe.sawDisposed.Load())
	}
}
