// Ports packages/coding-agent/src/experimental/client-tui.ts.
package experimental

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ClientTuiExecutor serializes input, render and UI mutations for one experimental presentation. Operations admitted by RunOnMain finish before it returns. RPC and plugin operations run outside this executor. Close runs off-loop after the component has closed.
type ClientTuiExecutor struct {
	calls      chan clientTuiCall
	stop       chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
	owner      atomic.Uint64
	microtasks []clientTuiMicrotask
}

type clientTuiMicrotask struct {
	ctx   context.Context
	apply func()
}

type clientTuiCall struct {
	context  context.Context
	apply    func()
	complete chan error
}

// NewClientTuiExecutor starts the presentation's owner loop. The caller owns Close; the loop stays alive during component cleanup.
func NewClientTuiExecutor() *ClientTuiExecutor {
	executor := &ClientTuiExecutor{calls: make(chan clientTuiCall), stop: make(chan struct{}), done: make(chan struct{})}
	go executor.run()
	return executor
}

func (executor *ClientTuiExecutor) run() {
	executor.owner.Store(chord.CurrentGoroutineID())
	defer executor.owner.Store(0)
	defer close(executor.done)
	for {
		select {
		case <-executor.stop:
			return
		case call := <-executor.calls:
			failure := executor.apply(call)
			for len(executor.microtasks) > 0 {
				task := executor.microtasks[0]
				executor.microtasks[0] = clientTuiMicrotask{}
				executor.microtasks = executor.microtasks[1:]
				if task.ctx.Err() == nil {
					failure = errors.Join(failure, executor.apply(clientTuiCall{context: task.ctx, apply: task.apply}))
				}
			}
			executor.microtasks = nil
			call.complete <- failure
		}
	}
}

func (executor *ClientTuiExecutor) apply(call clientTuiCall) (err error) {
	defer recoverSourceError(&err)
	if err := call.context.Err(); err != nil {
		return err
	}
	select {
	case <-executor.stop:
		return errors.New("Experimental TUI executor is closed")
	default:
	}
	call.apply()
	return nil
}

// RunOnMain waits for a mutation and its FIFO microtask checkpoint to finish on the owner loop. A call made on the owner executes inline without draining microtasks. Cancellation before admission does not execute the mutation; an admitted callback completes before returning.
func (executor *ClientTuiExecutor) RunOnMain(ctx context.Context, apply func()) error {
	if executor.owner.Load() == chord.CurrentGoroutineID() {
		return executor.apply(clientTuiCall{context: ctx, apply: apply})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-executor.stop:
		return errors.New("Experimental TUI executor is closed")
	default:
	}
	call := clientTuiCall{context: ctx, apply: apply, complete: make(chan error, 1)}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-executor.stop:
		return errors.New("Experimental TUI executor is closed")
	case executor.calls <- call:
		return <-call.complete
	}
}

// QueueMicrotask appends a fast owner-loop continuation to the current turn's FIFO checkpoint. Nested enqueue joins the same checkpoint. Remote work must be started off-loop; its completion posts another turn.
func (executor *ClientTuiExecutor) QueueMicrotask(ctx context.Context, apply func()) error {
	return executor.RunOnMain(ctx, func() { executor.microtasks = append(executor.microtasks, clientTuiMicrotask{ctx: ctx, apply: apply}) })
}

// Close stops accepting UI mutations and joins the owner loop. Repeated calls wait for the same completion. Call Close only after component cleanup has released all UI work.
func (executor *ClientTuiExecutor) Close() {
	if executor.owner.Load() == chord.CurrentGoroutineID() {
		panic("ClientTuiExecutor.Close must run off the owner loop")
	}
	executor.closeOnce.Do(func() { close(executor.stop) })
	<-executor.done
}

type clientTuiSessionFeature struct {
	serverId   string
	session    SessionServiceSource
	transcript services.Transcript
}

type pendingClientSelection struct {
	title         string
	items         []services.PresentationSelectItem
	selectedValue *string
	result        chan *string
}

// ExperimentalClientTui presents one attached Session through Chord services. UI methods run on the supplied owner executor; admitted transport operations and facet lifecycle operations are owned background work joined by Close, while a selected service call without an admission boundary keeps running on the background Context after Close, as in Pi.
type ExperimentalClientTui struct {
	ui                       tui.Renderer
	requestRender            func()
	finish                   func()
	runOnMain                func(context.Context, func()) error
	queueMicrotask           func(context.Context, func()) error
	ctx                      context.Context
	cancel                   context.CancelFunc
	cwd                      string
	fdPath                   string
	documentContainer        *tui.Container
	sessionHeading           *tui.Text
	pendingMessagesContainer *tui.Container
	statusContainer          *tui.Container
	editorContainer          *tui.Container
	footerComponent          *tui.Text
	layoutRoot               tui.Component
	transcriptScrollView     *tui.ScrollView
	sharedFacets             chord.LoadedFacets
	presentationFacets       *chord.LoadedFacets
	facetHost                *chord.FacetHost
	reloadMu                 sync.Mutex
	keybindings              *codingagent.KeybindingsManager
	chatInput                *tui.Editor
	commands                 []services.SlashCommandContribution
	controller               services.AgentController
	session                  *clientTuiSessionFeature
	snapshot                 *agentharness.LaneSnapshot
	selectList               *tui.FilterableList
	selection                *pendingClientSelection
	selectedServerId         string
	sessionId                string
	opened                   bool
	status                   string
	busy                     bool
	closed                   bool
	laneUnsubscribe          func()
	chatView                 *ExperimentalChatView
	recoveryQueue            []func() func(context.Context) error
	recoveryRunning          bool
	laneGeneration           uint64
	invocationTail           <-chan struct{}
	tasks                    sync.WaitGroup
	detached                 sync.WaitGroup
	observeMu                sync.Mutex
	observeClosed            bool
	failuresMu               sync.Mutex
	failures                 []error
	closeOnce                sync.Once
	closeError               error
}

// CreateExperimentalClientTui prepares/attaches the selected Session, loads shared and selected facets, and opens the replicated transcript before returning. Command service calls retain context.Background; ctx owns facet startup and UI observation. Startup failure closes every acquired presentation resource.
func CreateExperimentalClientTui(ctx context.Context, options ExperimentalClientTuiOptions) (*ExperimentalClientTui, error) {
	if options.UI == nil || options.RunOnMain == nil || options.QueueMicrotask == nil || options.RequestRender == nil || options.Finish == nil {
		return nil, errors.New("Experimental TUI requires a renderer, owner executor, render callback and finish callback")
	}
	prepared, err := prepareClientSession(ctx, options.Command, options.Servers)
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	loaders := []chord.FacetLoader{}
	if options.FacetLoader != nil {
		loaders = append(loaders, options.FacetLoader)
	}
	shared, err := chord.CombineFacetLoaders(loaders...).Load(ctx)
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	component := &ExperimentalClientTui{
		ui: options.UI, requestRender: options.RequestRender, finish: options.Finish, runOnMain: options.RunOnMain, queueMicrotask: options.QueueMicrotask,
		ctx: lifetime, cancel: cancel, cwd: cwd, sharedFacets: shared,
		keybindings: codingagent.NewKeybindingsManager(codingagent.AgentDir()),
		status:      "Starting Session…",
	}
	component.fdPath, _ = exec.LookPath("fd")
	err = component.runOnMain(ctx, component.initialize)
	if err == nil {
		err = component.start(prepared)
	}
	if err == nil {
		err = component.openPreparedSession(prepared)
	}
	if err != nil {
		if cleanup := component.Close(); cleanup != nil {
			return nil, &services.AggregateError{Message: "Experimental TUI startup and cleanup failed", Errors: []any{err, cleanup}}
		}
		return nil, err
	}
	return component, nil
}

func (component *ExperimentalClientTui) initialize() {
	component.documentContainer = tui.NewContainer()
	component.sessionHeading = tui.NewPaddedText("", 1, 0, nil)
	component.pendingMessagesContainer = tui.NewContainer()
	component.statusContainer = tui.NewContainer()
	component.editorContainer = tui.NewContainer()
	component.footerComponent = tui.NewPaddedText("", 1, 0, nil)
	component.chatInput = tui.NewEditor()
	component.chatInput.SetPaddingX(1)
	component.chatInput.OnSubmit = component.runPrompt
	component.chatInput.SetAutocompleteTaskOwner(component.ctx, func(run func()) { component.startTask(func(context.Context) error { run(); return nil }) }, component.showErrorFromError)
	// Local and awaited suggestions finish at the same owner-turn checkpoint, not on an arbitrarily scheduled goroutine.
	component.chatInput.SetAsyncApply(func(apply func()) {
		component.recordDeliveryError(component.queueMicrotask(component.ctx, func() { apply(); component.requestRender() }))
	})
	viewport := codingagent.CreateChatViewport(codingagent.ChatViewportOptions{
		Document: component.documentContainer, PendingMessages: component.pendingMessagesContainer,
		Status: component.statusContainer, Editor: component.editorContainer, Footer: component.footerComponent,
		ScrollbarTrackStyle: func(text string) string { return tui.ActiveTheme().FgText("scrollbarTrack", text) },
		ScrollbarThumbStyle: func(text string) string { return tui.ActiveTheme().FgText("scrollbarThumb", text) },
	})
	component.layoutRoot, component.transcriptScrollView = viewport.Root, viewport.Transcript
	component.rebuild()
}

// LayoutRoot supplies the shared fullscreen transcript/dock layout.
func (component *ExperimentalClientTui) LayoutRoot() tui.Component { return component.layoutRoot }

func (component *ExperimentalClientTui) Render(width int) []string {
	var lines []string
	for _, child := range []tui.Component{component.documentContainer, component.pendingMessagesContainer, component.statusContainer, component.editorContainer, component.footerComponent} {
		lines = append(lines, child.Render(width)...)
	}
	return lines
}

func (component *ExperimentalClientTui) Invalidate() { component.layoutRoot.Invalidate() }

func (component *ExperimentalClientTui) HandleInput(data string) {
	if component.closed {
		return
	}
	if component.busy {
		if component.keybindings.Matches(data, "app.clear") || (component.chatInput.Text() == "" && component.keybindings.Matches(data, "app.exit")) {
			component.finish()
		}
		return
	}
	if component.selection != nil {
		component.selectList.HandleInput(data)
		if component.selectList.Done() {
			var value *string
			if !component.selectList.Cancelled() {
				value = new(component.selection.items[component.selectList.SelectedIndex()].Value)
			}
			component.completeSelection(value)
		}
		component.requestRender()
		return
	}
	component.handleChatInput(data)
	component.requestRender()
}

func (component *ExperimentalClientTui) handleChatInput(data string) {
	switch {
	case component.keybindings.Matches(data, "app.clipboard.pasteImage"):
		return
	case component.keybindings.Matches(data, "app.interrupt"):
		if component.chatInput.AutocompleteOpen() {
			component.handleEditorInput(data)
		} else {
			component.interrupt()
		}
		return
	case component.keybindings.Matches(data, "app.exit") && component.chatInput.Text() == "":
		component.finish()
		return
	case component.keybindings.MatchesEditorHistory(data):
		component.handleEditorInput(data)
		return
	case component.keybindings.Matches(data, "app.clear"):
		component.finish()
		return
	case component.keybindings.Matches(data, "app.model.select"):
		component.executeSlashCommand("model", "")
		return
	case component.keybindings.Matches(data, "app.message.followUp"):
		text := strings.TrimFunc(component.chatInput.Text(), widthx.IsJSSpace)
		if text != "" {
			component.chatInput.SetText("")
			component.queueFollowUp(text)
		}
		return
	default:
		component.handleEditorInput(data)
	}
}

func (component *ExperimentalClientTui) handleEditorInput(data string) {
	if component.chatInput.AutocompleteOpen() {
		bindings := tui.GetTUIKeybindings()
		switch {
		case bindings.Matches(data, tui.KBSelectCancel):
			component.chatInput.AutocompleteCancel()
			return
		case bindings.Matches(data, tui.KBSelectConfirm):
			component.chatInput.AcceptAutocomplete(func(submit bool) {
				if submit {
					component.runPrompt(component.chatInput.Text())
				}
			})
			return
		}
	}
	component.chatInput.HandleInput(data)
}

func (component *ExperimentalClientTui) RefreshTheme() {
	if component.snapshot != nil && component.chatView != nil {
		if err := component.chatView.RefreshTheme(component.snapshot); err != nil {
			component.ShowError(err.Error())
			return
		}
	}
	component.rebuild()
}

func (component *ExperimentalClientTui) ShowError(message string) {
	component.status = "Error: " + message
	component.rebuild()
}

func (component *ExperimentalClientTui) showErrorFromError(err error) {
	component.ShowError(err.Error())
}

func (component *ExperimentalClientTui) startTask(run func(context.Context) error) {
	if component.ctx.Err() != nil {
		return
	}
	component.trackTask(run)
}

func (component *ExperimentalClientTui) trackTask(run func(context.Context) error) {
	component.tasks.Go(func() {
		err := func() (err error) { defer recoverSourceError(&err); return run(component.ctx) }()
		if err == nil || component.ctx.Err() != nil {
			return
		}
		if report := component.runOnMain(component.ctx, func() { component.ShowError(err.Error()) }); report != nil && component.ctx.Err() == nil {
			component.failuresMu.Lock()
			component.failures = append(component.failures, err, report)
			component.failuresMu.Unlock()
		}
	})
}

// awaitUncancellable waits for a transport call that takes no context, as Pi awaits the Promise of a selected controller or slash command (client-tui.ts:531-572). Pi's close (client-tui.ts:339-366) never awaits those Promises, so cancelling ctx abandons the wait; the call keeps running on the detached group until its transport returns.
func awaitUncancellable[T any](component *ExperimentalClientTui, ctx context.Context, call func() (T, error)) (T, error) {
	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	component.detached.Go(func() {
		value, err := call()
		done <- outcome{value, err}
	})
	select {
	case result := <-done:
		return result.value, result.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

func loadClientPresentationFacets(ctx context.Context, data pico3.JsonValue) (chord.LoadedFacets, error) {
	loaders, err := CreatePresentationFacetLoaders(data)
	if err != nil {
		return chord.LoadedFacets{}, err
	}
	return chord.CombineFacetLoaders(loaders...).Load(ctx)
}

func (component *ExperimentalClientTui) start(prepared preparedClientSession) error {
	presentation, err := loadClientPresentationFacets(component.ctx, prepared.presentationPlugins)
	if err != nil {
		return err
	}
	component.presentationFacets = &presentation
	facets := []chord.Facet{
		services.CreateSlashCommandsRuntimeFacet(nil), component.presentationBridge(prepared.server),
		services.CreateBuiltInSlashCommandsFacet(services.BuiltInSlashCommandsOptions{ReloadPresentationPlugins: component.reloadPresentationPlugins}),
	}
	facets = append(facets, component.sharedFacets.Facets...)
	facets = append(facets, presentation.Facets...)
	host, err := chord.CreateFacetHost(component.ctx, chord.FacetOptions{Facets: facets, ServiceSources: []chord.RemoteServiceSource{prepared.server.Server, prepared.server.Session}})
	if err != nil {
		return err
	}
	component.facetHost = host
	return nil
}

func (component *ExperimentalClientTui) reloadPresentationPlugins(ctx context.Context, data pico3.JsonValue) error {
	component.reloadMu.Lock()
	defer component.reloadMu.Unlock()
	candidate, err := loadClientPresentationFacets(ctx, data)
	if err != nil {
		return err
	}
	if err := component.facetHost.Reload(ctx, candidate.Facets); err != nil {
		if cleanup := candidate.Dispose(context.Background()); cleanup != nil {
			return &services.AggregateError{Message: "TUI plugin reload and cleanup failed", Errors: []any{err, cleanup}}
		}
		return err
	}
	retired := component.presentationFacets
	component.presentationFacets = &candidate
	return retired.Dispose(ctx)
}

func (component *ExperimentalClientTui) presentationBridge(server ClientTuiServer) chord.Facet {
	return chord.Facet{Id: "@pi/presentation-bridge", Setup: func(env *chord.FacetEnvironment) error {
		if err := chord.ProvideService[services.PresentationUI](env, services.PresentationUIDefinition, clientPresentationUI{component: component}); err != nil {
			return err
		}
		commandsRef, err := chord.UseService(env, services.SlashCommandsDefinition)
		if err != nil {
			return err
		}
		controllerRef, err := chord.UseService(env, services.AgentControllerDefinition)
		if err != nil {
			return err
		}
		transcriptRef, err := chord.UseService(env, services.TranscriptDefinition)
		if err != nil {
			return err
		}
		return env.OnActivate(func(ctx context.Context) error {
			commands, err := commandsRef.Get()
			if err != nil {
				return err
			}
			controller, err := controllerRef.Get()
			if err != nil {
				return err
			}
			transcript, err := transcriptRef.Get()
			if err != nil {
				return err
			}
			feature := &clientTuiSessionFeature{serverId: server.ServerId, session: server.Session, transcript: transcript}
			if err := component.runOnMain(ctx, func() {
				if component.session != nil || component.controller != nil {
					panic(errors.New("Presentation services are already active"))
				}
				component.session, component.controller = feature, controller
			}); err != nil {
				return err
			}
			if err := env.Own(func(ctx context.Context) error {
				return component.runOnMain(ctx, func() {
					if component.session == feature {
						component.session = nil
						component.controller = nil
						component.commands = nil
					}
				})
			}); err != nil {
				return err
			}
			remove := commands.Subscribe(func(commands []services.SlashCommandContribution) {
				commands = slices.Clone(commands)
				component.recordDeliveryError(component.runOnMain(component.ctx, func() { component.commands = commands; component.updateAutocomplete() }))
			})
			if err := env.Own(func(context.Context) error { remove(); return nil }); err != nil {
				remove()
				return err
			}
			if !server.Radius {
				return nil
			}
			removeConnection, err := server.Server.Connection().Subscribe(func(state *services.ServerConnectionState, _ context.Context, _ pico3.ReplicatedStateDelivery) {
				component.recordDeliveryError(component.runOnMain(component.ctx, func() { component.handleConnectionState(server.ServerId, *state) }))
			})
			if err != nil {
				return err
			}
			if err := env.Own(func(context.Context) error { removeConnection(); return nil }); err != nil {
				removeConnection()
				return err
			}
			removeAttachment, err := server.Session.Attachment().Subscribe(func(state *services.SessionAttachmentState, _ context.Context, _ pico3.ReplicatedStateDelivery) {
				component.recordDeliveryError(component.runOnMain(component.ctx, func() { component.handleAttachmentState(feature, *state) }))
			})
			if err != nil {
				return err
			}
			if err := env.Own(func(context.Context) error { removeAttachment(); return nil }); err != nil {
				removeAttachment()
				return err
			}
			return nil
		})
	}}
}

func (component *ExperimentalClientTui) recordDeliveryError(err error) {
	if err != nil && component.ctx.Err() == nil {
		component.failuresMu.Lock()
		component.failures = append(component.failures, err)
		component.failuresMu.Unlock()
	}
}

func (component *ExperimentalClientTui) openPreparedSession(prepared preparedClientSession) error {
	var feature *clientTuiSessionFeature
	if err := component.runOnMain(component.ctx, func() { feature = component.session }); err != nil {
		return err
	}
	if feature == nil {
		return fmt.Errorf("No Session service is available for %s", prepared.server.ServerId)
	}
	if err := feature.session.WhenAttached(context.Background(), prepared.summary.SessionId); err != nil {
		return err
	}
	if err := component.runOnMain(component.ctx, func() {
		component.selectedServerId, component.sessionId, component.opened = feature.serverId, prepared.summary.SessionId, true
		component.updateAutocomplete()
	}); err != nil {
		return err
	}
	if err := component.openLane(component.ctx, feature); err != nil {
		return err
	}
	return component.runOnMain(component.ctx, func() { component.status = ""; component.rebuild() })
}

var errClientLaneReplaced = errors.New("Session attachment changed while opening transcript")

func (component *ExperimentalClientTui) openLane(ctx context.Context, feature *clientTuiSessionFeature) error {
	if err := component.closeLane(ctx); err != nil {
		return err
	}
	var generation uint64
	if err := component.runOnMain(ctx, func() { generation = component.laneGeneration }); err != nil {
		return err
	}
	return component.populateLane(ctx, feature, generation)
}

func (component *ExperimentalClientTui) populateLane(ctx context.Context, feature *clientTuiSessionFeature, generation uint64) error {
	view := NewExperimentalChatView(component.ctx, component.cwd, component.ui.RequestRender, component.runOnMain)
	var gate sync.Mutex
	var latest *services.TranscriptState
	active, closed := false, false
	// Decode initial replica state off-loop, then commit close/mount/hydration as one owner mutation. Pi's resolved closeLane await and synchronous subscription hydration complete in one microtask checkpoint; no fresh render can observe the intermediate cleared status.
	remove, err := feature.transcript.State().Subscribe(func(value *services.TranscriptState, _ context.Context, _ pico3.ReplicatedStateDelivery) {
		gate.Lock()
		if closed {
			gate.Unlock()
			return
		}
		latest = value
		ready := active
		gate.Unlock()
		if !ready || value == nil || value.Snapshot == nil {
			return
		}
		component.recordDeliveryError(component.runOnMain(component.ctx, func() {
			gate.Lock()
			live := !closed
			gate.Unlock()
			if !live || component.chatView != view || component.closed {
				return
			}
			component.snapshot = value.Snapshot
			if err := view.Apply(value.Snapshot); err != nil {
				component.ShowError(err.Error())
				return
			}
			component.rebuild()
		}))
	})
	if err != nil {
		return errors.Join(err, view.Dispose())
	}
	unsubscribe := func() { gate.Lock(); closed = true; gate.Unlock(); remove() }
	var initializeError error
	err = component.runOnMain(ctx, func() {
		if component.closed || component.laneGeneration != generation || component.session != feature {
			initializeError = errClientLaneReplaced
			return
		}
		gate.Lock()
		value := latest
		if value != nil && value.Snapshot != nil {
			active = true
		}
		gate.Unlock()
		if value == nil || value.Snapshot == nil {
			initializeError = errors.New("Transcript has no initialized snapshot")
			return
		}
		initializeError = view.Apply(value.Snapshot)
		if initializeError != nil {
			return
		}
		component.retireLane()
		component.chatView, component.laneUnsubscribe = view, unsubscribe
		component.snapshot = value.Snapshot
		component.documentContainer.Add(component.sessionHeading)
		component.documentContainer.Add(view.Transcript)
		component.pendingMessagesContainer.Add(view.PendingMessages)
		component.rebuild()
	})
	if err != nil || initializeError != nil {
		unsubscribe()
		detachError := component.runOnMain(context.Background(), func() {
			if component.chatView == view {
				component.detachLane()
			}
		})
		return errors.Join(err, initializeError, detachError, view.Dispose())
	}
	return nil
}

func (component *ExperimentalClientTui) detachLane() *ExperimentalChatView {
	if component.laneUnsubscribe != nil {
		component.laneUnsubscribe()
	}
	view := component.chatView
	if view != nil {
		view.cancel()
	}
	component.laneUnsubscribe, component.chatView, component.snapshot = nil, nil, nil
	if component.documentContainer != nil {
		component.documentContainer.Clear()
		component.pendingMessagesContainer.Clear()
		component.statusContainer.Clear()
	}
	return view
}

func (component *ExperimentalClientTui) closeLane(ctx context.Context) error {
	var view *ExperimentalChatView
	if err := component.runOnMain(ctx, func() { view = component.detachLane() }); err != nil {
		return err
	}
	if view != nil {
		return view.Dispose()
	}
	return nil
}

// retireLane detaches the lane at a recovery checkpoint and clears its containers like Pi's closeLane (client-tui.ts:512-520). When the same checkpoint already queued a reattachment, Pi finishes openLane and its rebuild before any frame renders; staging here spans frames, so the turn's status is restored instead of exposing a cleared frame Pi never renders.
func (component *ExperimentalClientTui) retireLane() {
	view := component.detachLane()
	if view != nil {
		component.trackTask(func(context.Context) error { return view.Dispose() })
	}
	if len(component.recoveryQueue) > 0 && component.documentContainer != nil {
		component.rebuild()
	}
}

func (component *ExperimentalClientTui) handleConnectionState(serverId string, state services.ServerConnectionState) {
	if component.closed || !component.opened || component.selectedServerId != serverId {
		return
	}
	if state.Status == "connected" {
		if component.laneUnsubscribe == nil {
			component.busy = true
			component.status = "Reattaching Session…"
			component.rebuild()
		}
		return
	}
	component.laneGeneration++
	component.busy = true
	component.status = "Radius disconnected; retrying…"
	if state.Status == "connecting" {
		component.status = "Reconnecting to Radius…"
	}
	component.queueRecovery(func() func(context.Context) error { component.retireLane(); return nil })
	component.rebuild()
}

func (component *ExperimentalClientTui) handleAttachmentState(feature *clientTuiSessionFeature, state services.SessionAttachmentState) {
	if component.closed || !component.opened || component.selectedServerId != feature.serverId {
		return
	}
	if state.Status != "attached" || state.SessionID != component.sessionId {
		component.laneGeneration++
	}
	if state.Status == "attached" && state.SessionID == component.sessionId {
		generation := component.laneGeneration
		component.queueRecovery(func() func(context.Context) error {
			if component.laneGeneration != generation || component.session != feature {
				return nil
			}
			if component.laneUnsubscribe != nil {
				component.busy = false
				component.status = ""
				component.rebuild()
				return nil
			}
			return func(ctx context.Context) error {
				if err := component.populateLane(ctx, feature, generation); err != nil {
					return err
				}
				return component.runOnMain(ctx, func() {
					if component.closed || component.laneGeneration != generation {
						return
					}
					component.busy = false
					component.status = ""
					component.rebuild()
				})
			}
		})
	} else if state.Status == "attaching" && state.SessionID == component.sessionId {
		component.busy = true
		component.status = "Reattaching Session…"
		component.rebuild()
	}
}

// Recovery starts at a Promise continuation checkpoint. Fast detachment runs before its first await; resource retirement is independently owned off-loop rather than delaying the visible detachment.
func (component *ExperimentalClientTui) queueRecovery(prefix func() func(context.Context) error) {
	component.recoveryQueue = append(component.recoveryQueue, prefix)
	component.recordDeliveryError(component.queueMicrotask(component.ctx, component.advanceRecovery))
}

func (component *ExperimentalClientTui) advanceRecovery() {
	defer func() {
		if value := recover(); value != nil {
			component.recoveryRunning = false
			component.busy = true
			component.status = fmt.Sprintf("Reconnect error: %v", value)
			component.rebuild()
			component.recordDeliveryError(component.queueMicrotask(component.ctx, component.advanceRecovery))
		}
	}()
	if component.closed || component.recoveryRunning || len(component.recoveryQueue) == 0 {
		return
	}
	prefix := component.recoveryQueue[0]
	component.recoveryQueue[0] = nil
	component.recoveryQueue = component.recoveryQueue[1:]
	component.recoveryRunning = true
	continuation := prefix()
	if continuation == nil {
		component.recoveryRunning = false
		component.recordDeliveryError(component.queueMicrotask(component.ctx, component.advanceRecovery))
		return
	}
	component.startTask(func(ctx context.Context) error {
		err := continuation(ctx)
		return component.queueMicrotask(ctx, func() {
			component.recoveryRunning = false
			if err != nil && !errors.Is(err, errClientLaneReplaced) {
				component.busy = true
				component.status = "Reconnect error: " + err.Error()
				component.rebuild()
			}
			component.advanceRecovery()
		})
	})
}

func (component *ExperimentalClientTui) rebuild() {
	if component.closed {
		return
	}
	heading := ""
	if component.opened {
		heading = tui.ActiveTheme().FgText("dim", "Server: "+component.selectedServerId+"\nSession: "+component.sessionId)
	}
	component.sessionHeading.SetText(heading)
	component.statusContainer.Clear()
	if component.status != "" {
		component.statusContainer.Add(tui.NewPaddedText(tui.ActiveTheme().FgText("dim", component.status), 1, 0, nil))
	}
	if component.chatView != nil {
		component.statusContainer.Add(component.chatView.Status)
	}
	component.footerComponent.SetText(tui.ActiveTheme().FgText("dim", component.footer()))
	component.editorContainer.Clear()
	if selection := component.selection; selection != nil {
		component.chatInput.Focused = false
		selector := tui.NewContainer(tui.NewPaddedText("\x1b[1m"+selection.title+tui.SGRBoldDimReset, 1, 1, nil))
		labels, descriptions := make([]string, len(selection.items)), make([]string, len(selection.items))
		selected := -1
		for i, item := range selection.items {
			labels[i] = item.Label
			if labels[i] == "" {
				labels[i] = item.Value
			}
			if item.Description != nil {
				descriptions[i] = *item.Description
			}
			if selected < 0 && selection.selectedValue != nil && item.Value == *selection.selectedValue {
				selected = i
			}
		}
		list := tui.NewFilterableList("", labels)
		list.EnableSearch = false
		list.Descriptions = descriptions
		list.MaxVisible = min(max(len(labels), 1), 12)
		if selected >= 0 {
			list.SetCursor(selected)
		}
		component.selectList = list
		selector.Add(list)
		component.editorContainer.Add(selector)
	} else {
		component.selectList = nil
		component.chatInput.Focused = !component.busy
		component.editorContainer.Add(component.chatInput)
	}
	component.layoutRoot.Invalidate()
	component.requestRender()
}

func (component *ExperimentalClientTui) footer() string {
	const commands = "/model · /thinking · /compact · /reload"
	if component.snapshot == nil {
		return commands
	}
	snapshot := component.snapshot
	return fmt.Sprintf("%s/%s · thinking:%s · %d messages · %s", snapshot.Configuration.Model.Provider, snapshot.Configuration.Model.ModelID, snapshot.Configuration.ThinkingLevel, snapshot.Stats.MessageCount, commands)
}

func (component *ExperimentalClientTui) updateAutocomplete() {
	if component.closed {
		return
	}
	commands := make([]tui.SlashCommand, len(component.commands))
	for i, command := range component.commands {
		commands[i].Name = command.Name
		if command.Description != nil {
			commands[i].Description = *command.Description
		}
		if command.ArgumentHint != nil {
			commands[i].ArgumentHint = *command.ArgumentHint
		}
		if command.GetArgumentCompletions != nil {
			commands[i].AwaitArgumentCompletions = func(prefix string) ([]tui.AutocompleteItem, error) {
				items, err := command.GetArgumentCompletions(prefix)
				if err != nil || items == nil {
					return nil, err
				}
				result := make([]tui.AutocompleteItem, len(items))
				for i, item := range items {
					result[i] = tui.AutocompleteItem{Value: item.Value, Label: item.Label}
					if item.Description != nil {
						result[i].Description = *item.Description
					}
				}
				return result, nil
			}
		}
	}
	provider := tui.NewCombinedProvider(commands, component.cwd, component.fdPath)
	provider.SetAsyncFileSearch(true)
	component.chatInput.SetAutocomplete(provider)
	component.requestRender()
}

type clientPresentationUI struct{ component *ExperimentalClientTui }

func (ui clientPresentationUI) Select(ctx context.Context, title string, items []services.PresentationSelectItem, selectedValue *string) (*string, error) {
	selection := &pendingClientSelection{title: title, items: slices.Clone(items), result: make(chan *string, 1)}
	if selectedValue != nil {
		selection.selectedValue = new(*selectedValue)
	}
	for i, item := range selection.items {
		if item.Description != nil {
			selection.items[i].Description = new(*item.Description)
		}
	}
	if err := ui.component.runOnMain(ctx, func() {
		if ui.component.selection != nil {
			panic(errors.New("A slash command selector is already active"))
		}
		ui.component.selection = selection
		ui.component.rebuild()
	}); err != nil {
		return nil, err
	}
	extension.CallInitiated(ctx)
	select {
	case value := <-selection.result:
		return value, nil
	case <-ctx.Done():
		cleanup := ui.component.runOnMain(context.Background(), func() {
			if ui.component.selection == selection {
				ui.component.completeSelection(nil)
			}
		})
		return nil, errors.Join(ctx.Err(), cleanup)
	}
}

func (ui clientPresentationUI) ShowStatus(ctx context.Context, status string) error {
	return ui.component.runOnMain(ctx, func() {
		if !ui.component.closed {
			ui.component.status = status
			ui.component.rebuild()
		}
	})
}

func (component *ExperimentalClientTui) completeSelection(value *string) {
	selection := component.selection
	if selection == nil {
		return
	}
	component.selection = nil
	selection.result <- value
	component.rebuild()
}

func (component *ExperimentalClientTui) runPrompt(text string) {
	prompt := strings.TrimFunc(text, widthx.IsJSSpace)
	if prompt == "" {
		return
	}
	if strings.HasPrefix(prompt, "/") {
		name, args, _ := strings.Cut(prompt[1:], " ")
		component.executeSlashCommand(name, strings.TrimFunc(args, widthx.IsJSSpace))
		return
	}
	component.chatInput.SetText("")
	controller := component.controller
	if controller == nil {
		component.ShowError("No Session AgentController service is available")
		return
	}
	running := component.snapshot != nil && component.snapshot.Operation != nil
	component.status = "Running turn…"
	if running {
		component.status = "Queueing steering message…"
	}
	component.rebuild()
	initiator, _ := controller.(services.AgentControllerInitiator)
	request := services.AgentPromptRequest{Message: prompt, Images: nil}
	if running {
		startClientResultInvocation(component, func() (*chord.ServiceResultInvocation[services.AgentQueueResponse], error) {
			if initiator == nil {
				return nil, chord.ErrInvocationAdmissionUnavailable
			}
			return initiator.BeginSteer(context.Background(), request)
		}, func() (services.AgentQueueResponse, error) {
			return controller.Steer(context.Background(), request)
		}, component.reportQueue)
	} else {
		startClientResultInvocation(component, func() (*chord.ServiceResultInvocation[services.AgentOperationResponse], error) {
			if initiator == nil {
				return nil, chord.ErrInvocationAdmissionUnavailable
			}
			return initiator.BeginPrompt(context.Background(), request)
		}, func() (services.AgentOperationResponse, error) {
			return controller.Prompt(context.Background(), request)
		}, component.reportOperation)
	}
}

func (component *ExperimentalClientTui) executeSlashCommand(name, args string) {
	component.chatInput.SetText("")
	index := slices.IndexFunc(component.commands, func(command services.SlashCommandContribution) bool { return command.Name == name })
	if index < 0 {
		component.status = "Unknown slash command: /" + name
		component.rebuild()
		return
	}
	command := component.commands[index]
	component.startTask(func(ctx context.Context) error {
		result, err := awaitUncancellable(component, ctx, func() (services.SlashCommandRunResult, error) { return command.Run(context.Background(), args) })
		if err != nil {
			return err
		}
		if result == nil {
			return nil
		}
		return component.runOnMain(ctx, func() {
			switch response := result.(type) {
			case services.AgentQueueResponse:
				component.reportQueue(response)
			case services.AgentOperationResponse:
				component.reportOperation(response)
			}
		})
	})
}

func (component *ExperimentalClientTui) queueFollowUp(text string) {
	controller := component.controller
	if controller == nil {
		return
	}
	component.status = "Queueing follow-up…"
	component.rebuild()
	initiator, _ := controller.(services.AgentControllerInitiator)
	request := services.AgentPromptRequest{Message: text, Images: nil}
	startClientResultInvocation(component, func() (*chord.ServiceResultInvocation[services.AgentQueueResponse], error) {
		if initiator == nil {
			return nil, chord.ErrInvocationAdmissionUnavailable
		}
		return initiator.BeginFollowUp(context.Background(), request)
	}, func() (services.AgentQueueResponse, error) {
		return controller.FollowUp(context.Background(), request)
	}, component.reportQueue)
}

func (component *ExperimentalClientTui) reportOperation(response services.AgentOperationResponse) {
	component.status = ""
	if !response.Accepted {
		component.status = "Operation rejected: " + response.Error.Message
	} else if response.Error != nil {
		component.status = "Operation failed: " + response.Error.Message
	}
	component.rebuild()
}

func (component *ExperimentalClientTui) reportQueue(response services.AgentQueueResponse) {
	if response.Accepted {
		component.status = "Queued " + *response.EntryID + "."
	} else {
		component.status = "Message rejected: " + response.Error.Message
	}
	component.rebuild()
}

func (component *ExperimentalClientTui) interrupt() {
	if component.snapshot == nil || component.snapshot.Operation == nil || component.controller == nil {
		return
	}
	id, controller := component.snapshot.Operation.ID, component.controller
	component.status = "Aborting " + id + "…"
	component.rebuild()
	initiator, _ := controller.(services.AgentControllerInitiator)
	abortWithoutAdmission := func(ctx context.Context) error {
		_, err := awaitUncancellable(component, ctx, func() (struct{}, error) {
			return struct{}{}, controller.RequestAbort(context.Background(), id)
		})
		return err
	}
	component.startInvocation(func() (func(context.Context) error, error) {
		if initiator == nil {
			return abortWithoutAdmission, nil
		}
		operation, err := initiator.BeginRequestAbort(context.Background(), id)
		if errors.Is(err, chord.ErrInvocationAdmissionUnavailable) {
			return abortWithoutAdmission, nil
		}
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) error { _, err := operation.Wait(ctx); return err }, nil
	})
}

// errInvocationDroppedByClose marks an admission that had not started when Close began.
var errInvocationDroppedByClose = errors.New("invocation dropped by Close")

// startAdmission reports whether an admission may start. Pi issues every controller call on the input turn (client-tui.ts:563-572), before #close detaches the lane and disposes the host (client-tui.ts:339-366). Go cannot block the input loop on a transport, so an admission that has not started when Close begins is dropped: issuing it after Close would reach a disposed binding. An admission already inside its transport when Close begins is issued and is not joined, as Pi's close never awaits the call.
func (component *ExperimentalClientTui) startAdmission() bool {
	component.observeMu.Lock()
	defer component.observeMu.Unlock()
	return !component.observeClosed
}

// Admission is serialized in input order off-loop, on the detached group; waiting for completion is not. Pi calls the controller on the input turn and its close (client-tui.ts:339-366) never awaits that Promise (client-tui.ts:563-572), so Close neither joins a queued or blocked admission nor a no-admission call, and drops an admission that has not started (see startAdmission). Once begin returns, the observer joins the tracked tasks unless Close has started. Cancelling the observer never replaces BACKGROUND_CONTEXT on the operation.
func (component *ExperimentalClientTui) startInvocation(begin func() (func(context.Context) error, error)) {
	if component.ctx.Err() != nil {
		return
	}
	previous := component.invocationTail
	admitted := make(chan struct{})
	component.invocationTail = admitted
	component.detached.Go(func() {
		wait, err := func() (wait func(context.Context) error, err error) {
			defer close(admitted)
			defer recoverSourceError(&err)
			if previous != nil {
				<-previous
			}
			if !component.startAdmission() {
				return nil, errInvocationDroppedByClose
			}
			return begin()
		}()
		if errors.Is(err, errInvocationDroppedByClose) {
			return
		}
		component.observeMu.Lock()
		defer component.observeMu.Unlock()
		if component.observeClosed {
			return
		}
		component.trackTask(func(ctx context.Context) error {
			if err != nil {
				return err
			}
			return wait(ctx)
		})
	})
}

// startClientResultInvocation admits a selected transport operation in input order. A selected implementation without an admission boundary is awaited through its blocking method, as Pi awaits the Promise of any selected controller (client-tui.ts:563-572).
func startClientResultInvocation[T any](component *ExperimentalClientTui, begin func() (*chord.ServiceResultInvocation[T], error), call func() (T, error), report func(T)) {
	component.startInvocation(func() (func(context.Context) error, error) {
		operation, err := begin()
		if errors.Is(err, chord.ErrInvocationAdmissionUnavailable) {
			return func(ctx context.Context) error {
				response, err := awaitUncancellable(component, ctx, call)
				if err != nil {
					return err
				}
				return component.runOnMain(ctx, func() { report(response) })
			}, nil
		}
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) error {
			response, err := operation.Wait(ctx)
			if err != nil {
				return err
			}
			return component.runOnMain(ctx, func() { report(response) })
		}, nil
	})
}

// Close marks the UI closed, cancels pending selection and UI observation, joins recovery/reload/workers, detaches the lane and disposes facets. The owner executor must remain alive until this off-loop call returns.
func (component *ExperimentalClientTui) Close() error {
	component.closeOnce.Do(func() {
		var failures []error
		if err := component.runOnMain(context.Background(), func() {
			component.closed = true
			component.recoveryQueue = nil
			component.completeSelection(nil)
			if component.chatInput != nil {
				component.chatInput.AutocompleteCancel()
			}
			if component.transcriptScrollView != nil {
				component.transcriptScrollView.Dispose()
			}
			component.cancel()
		}); err != nil {
			failures = append(failures, err)
			component.cancel()
		}
		component.observeMu.Lock()
		component.observeClosed = true
		component.observeMu.Unlock()
		component.tasks.Wait()
		if err := component.closeLane(context.Background()); err != nil {
			failures = append(failures, err)
		}
		component.reloadMu.Lock()
		if component.facetHost != nil {
			if err := component.facetHost.Dispose(context.Background()); err != nil {
				failures = append(failures, err)
			}
			component.facetHost = nil
		}
		generations := []chord.LoadedFacets{component.sharedFacets}
		if component.presentationFacets != nil {
			generations = append([]chord.LoadedFacets{*component.presentationFacets}, generations...)
			component.presentationFacets = nil
		}
		results := make([]error, len(generations))
		var group sync.WaitGroup
		for i, generation := range generations {
			group.Go(func() {
				if generation.Dispose != nil {
					results[i] = generation.Dispose(context.Background())
				}
			})
		}
		group.Wait()
		component.reloadMu.Unlock()
		failures = append(failures, results...)
		component.failuresMu.Lock()
		failures = append(failures, component.failures...)
		component.failuresMu.Unlock()
		failures = slices.DeleteFunc(failures, func(err error) bool { return err == nil })
		switch len(failures) {
		case 0:
		case 1:
			component.closeError = failures[0]
		default:
			values := make([]any, len(failures))
			for i, failure := range failures {
				values[i] = failure
			}
			component.closeError = &services.AggregateError{Message: "Failed to dispose experimental TUI facets", Errors: values}
		}
	})
	return component.closeError
}

// RunClientTui opens the native experimental runtime and its shared fullscreen presentation. The terminal pump, UI executor, selected facets, source bindings and ResourceLoader all have joined shutdown ownership.
func RunClientTui(ctx context.Context, command ClientCommand, options RunClientTuiOptions) (err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	agentDir := codingagent.AgentDir()
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	// client-tui.ts:731-741 loads only theme resources before registering them. The caller resolves those paths with the stable CLI's resource precedence.
	registry := tui.NewThemeRegistry()
	codingagent.LoadThemePaths(registry, options.ThemePaths, nil)
	tui.SetThemeRegistry(registry)
	runtime, err := OpenClientRuntime(ctx, command, options.OpenClientRuntimeOptions)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, runtime.Dispose()) }()
	executor := NewClientTuiExecutor()
	defer executor.Close()
	uiCtx, cancelUI := context.WithCancel(ctx)
	defer cancelUI()
	ui := codingagent.CreateInteractiveTui(codingagent.InteractiveTuiOptions{TuiMode: "fullscreen", ShowHardwareCursor: new(settings.GetShowHardwareCursor()), LogDirectory: agentDir})
	alt := ui.(*tui.TuiAltScreen)
	terminal := tui.NewProcessTerminal(os.Stdin, os.Stdout)
	finished := make(chan struct{})
	var finishOnce sync.Once
	failures := make(chan error, 1)
	var failOnce sync.Once
	report := func(failure error) {
		if failure != nil {
			failOnce.Do(func() { failures <- failure })
		}
	}
	dispatch := func(apply func()) {
		if err := executor.RunOnMain(uiCtx, apply); err != nil && uiCtx.Err() == nil {
			report(err)
		}
	}
	ui.SetRenderDispatcher(dispatch)
	ui.SetOverlayCommandDispatcher(dispatch)
	alt.SetTickDispatcher(dispatch)
	ui.SetClearOnShrink(settings.GetClearOnShrink())
	var component *ExperimentalClientTui
	var theme *codingagent.InteractiveThemeController
	var themeWatcher *tui.ThemeWatcher
	var inputWorkers sync.WaitGroup
	terminalStarted, uiStarted := false, false
	defer func() {
		var cleanup []error
		if theme != nil {
			cleanup = append(cleanup, theme.Dispose())
		}
		// client-tui.ts:790 stops the custom-theme watcher after disposing the controller.
		if themeWatcher != nil {
			themeWatcher.Close()
		}
		cleanup = append(cleanup, executor.RunOnMain(context.Background(), func() {
			ui.CancelPendingRender()
			if uiStarted {
				ui.Stop()
				uiStarted = false
			}
		}))
		cancelUI()
		if terminalStarted {
			terminal.Stop()
		}
		inputWorkers.Wait()
		if component != nil {
			cleanup = append(cleanup, component.Close())
		}
		err = errors.Join(append([]error{err}, cleanup...)...)
	}()
	theme, err = codingagent.NewInteractiveThemeController(uiCtx, ui, codingagent.InteractiveThemeControllerOptions{
		GetSettingsManager: func() *codingagent.SettingsManager { return settings }, Output: os.Stdout,
		RunOnMain: executor.RunOnMain,
		ShowError: func(message string) {
			if component != nil {
				component.ShowError(message)
			}
		},
		OnChanged: func() {
			if component != nil {
				component.RefreshTheme()
			}
		},
	})
	if err != nil {
		return err
	}
	// The controller selects themes with Pi's enableWatcher=true (theme-controller.ts applyThemeName). No onThemeChange callback is registered in the client process, so a reloaded custom theme publishes on the owner loop and appears at the next render.
	themeWatcher = tui.StartThemeWatcher(uiCtx, filepath.Join(agentDir, "themes"), executor.RunOnMain)
	servers := make([]ClientTuiServer, len(runtime.Servers))
	for i, server := range runtime.Servers {
		servers[i] = ClientTuiServer{ServerId: server.Route.ServerId, Radius: server.Route.Transport == "radius", Server: server.Server, Session: server.Session}
	}
	component, err = CreateExperimentalClientTui(uiCtx, ExperimentalClientTuiOptions{
		Command: command, UI: ui, Servers: servers, FacetLoader: options.FacetLoader,
		RequestRender: ui.RequestRender, RunOnMain: executor.RunOnMain, QueueMicrotask: executor.QueueMicrotask,
		Finish: func() {
			theme.DisableAutoSync()
			if uiStarted {
				ui.Stop()
				uiStarted = false
			}
			finishOnce.Do(func() { close(finished) })
		},
	})
	if err != nil {
		return err
	}
	input := make(chan []byte)
	inputWorkers.Go(func() { report(runClientTuiInput(uiCtx, input, executor, alt, component, theme, finished)) })
	err = terminal.StartWithReadError(func(data []byte) {
		// upstream: packages/tui/src/terminal.ts:ProcessTerminal
		select {
		case input <- slices.Clone(data):
		case <-uiCtx.Done():
		}
	}, func() { dispatch(ui.RequestRender) }, report)
	if err != nil {
		return err
	}
	terminalStarted = true
	if err := executor.RunOnMain(uiCtx, func() {
		ui.Add(component)
		alt.SetLayoutRoot(component.LayoutRoot())
		ui.SetFocus(component)
		uiStarted = true
		ui.Start()
	}); err != nil {
		return err
	}
	if err := theme.ApplyFromSettings(ctx); err != nil {
		return err
	}
	select {
	case <-finished:
		return nil
	case failure := <-failures:
		return failure
	case <-ctx.Done():
		return ctx.Err()
	}
}

func runClientTuiInput(ctx context.Context, input <-chan []byte, executor *ClientTuiExecutor, ui *tui.TuiAltScreen, component *ExperimentalClientTui, theme *codingagent.InteractiveThemeController, finished <-chan struct{}) error {
	buffer := codingagent.NewStdinBuffer(codingagent.StdinBufferOptions{EscapeTimeout: time.Duration(tui.ResolveEscapeTimeoutMs(os.Getenv) * float64(time.Millisecond))})
	var timer *time.Timer
	var flush <-chan time.Time
	stopTimer := func() {
		if timer != nil {
			timer.Stop()
		}
		timer, flush = nil, nil
	}
	defer stopTimer()
	for {
		var chunks []string
		select {
		case <-ctx.Done():
			return nil
		case data, ok := <-input:
			if !ok {
				return io.EOF
			}
			stopTimer()
			chunks = buffer.ProcessTerminalBytes(data)
		case <-flush:
			stopTimer()
			chunks = buffer.Flush()
		}
		if len(chunks) > 0 {
			if err := executor.RunOnMain(ctx, func() {
				for _, data := range chunks {
					select {
					case <-finished:
						return
					default:
					}
					if theme.ConsumeInput(data) || !tui.ShouldDeliverKey(component, data) {
						continue
					}
					if ui.HandleViewportInput(data) {
						component.completeFinishedSelection()
						continue
					}
					if ui.ConsumeCellSizeResponse(data) || ui.HandleFocusedSearchInput(data) {
						continue
					}
					component.HandleInput(data)
				}
			}); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
		if buffer.HasPendingFlush() {
			timer = time.NewTimer(buffer.FlushTimeout())
			flush = timer.C
		}
	}
}

func (component *ExperimentalClientTui) completeFinishedSelection() {
	if component.selection == nil || component.selectList == nil || !component.selectList.Done() {
		return
	}
	var value *string
	if !component.selectList.Cancelled() {
		value = new(component.selection.items[component.selectList.SelectedIndex()].Value)
	}
	component.completeSelection(value)
}
