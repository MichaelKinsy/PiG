package codingagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi does not await cycleModel or selectModel in the handlers that start them (interactive-mode.ts:3083-3084, 5334-5340), and
// their awaits do not block its event loop (4498-4500, 5313-5320). Keys typed while model_select handlers run reach the focused
// component, a submitted prompt starts at once (1244-1251), and a second change applies its model before its own await and
// overlaps the first one's handlers (agent-session.ts:2430-2449, 2482-2518, 2523-2550).
//
// liveInputModelHandle applies each change on the owner loop, as the Session's OnMain split does, and then plays the
// model_select handlers off the loop: the i-th notification routes keys[i] through the terminal reader's path, runs afterKeys,
// records probe's view of the owner loop, and waits for hold(i). With blockUntilDone a notification then waits until the
// context the mode passed to the OnMain call ends, as a handler that honours cancellation does.
type liveInputModelHandle struct {
	recordingCompactHandle
	mode      *InteractiveMode
	ctx       context.Context
	keys      [][]string
	afterKeys func()
	probe     func() string
	hold      func(index int)
	// blockUntilDone makes every notification wait for its context.
	blockUntilDone bool

	// applied lists each change's previous and next model, in the order the owner loop applied them.
	applied []string

	mu                               sync.Mutex
	seen                             []string
	notifications, active, maxActive int
}

func (h *liveInputModelHandle) apply(model *ai.Model) {
	h.applied = append(h.applied, h.agent.Model().ID+"->"+model.ID)
	h.agent.SetModel(model)
}

func (h *liveInputModelHandle) notify(ctx context.Context) {
	h.mu.Lock()
	index := h.notifications
	h.notifications++
	h.active++
	h.maxActive = max(h.maxActive, h.active)
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.active--
		h.mu.Unlock()
	}()
	if index < len(h.keys) {
		for _, key := range h.keys[index] {
			if ticket := h.mode.routeInputChunk(h.ctx, []byte(key), h.mode.inputReadCh); ticket != nil {
				select {
				case <-ticket.done:
				case <-h.ctx.Done():
				}
			}
		}
		if h.afterKeys != nil {
			h.afterKeys()
		}
		if h.probe != nil {
			seen := make(chan string, 1)
			h.mode.runOnMain(h.ctx, func() { seen <- h.probe() })
			select {
			case value := <-seen:
				h.mu.Lock()
				h.seen = append(h.seen, value)
				h.mu.Unlock()
			case <-h.ctx.Done():
			}
		}
	}
	if h.hold != nil {
		h.hold(index)
	}
	if h.blockUntilDone {
		<-ctx.Done()
	}
}

func (h *liveInputModelHandle) BeginModelCycleOnMain(ctx context.Context, direction string, options ModelMutationOptions) (*ModelCycleResult, func(), error) {
	result, _, err := h.recordingCompactHandle.BeginModelCycleOnMain(ctx, direction, options)
	if result == nil {
		return result, nil, err
	}
	h.apply(result.Model)
	return result, func() { h.notify(ctx) }, err
}

func (h *liveInputModelHandle) SetModelOnMain(ctx context.Context, model *ai.Model, options ModelMutationOptions, dispatch func(func() error) error) error {
	if err := dispatch(func() error {
		h.apply(model)
		return h.SetModel(model, options)
	}); err != nil {
		return err
	}
	h.notify(ctx)
	return nil
}

func (h *liveInputModelHandle) results() (seen []string, notifications, maxActive int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seen...), h.notifications, h.maxActive
}

// liveInputModelMode is a mode whose terminal input reaches the owner loop through its read channel, with the editor
// focused in the layout's editor slot and the main loop awaiting the next prompt.
func liveInputModelMode(t *testing.T) (*InteractiveMode, *liveInputModelHandle, context.Context) {
	m := modelPickerTestMode(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	m.setModalInputChannel(nil)
	m.keybindings = DefaultKeybindingsManager()
	m.inputReadCh = make(chan inputChunk)
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.editorContainer = tui.NewContainer(m.editor)
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	m.tuiInst.SetFocus(m.editor)
	m.setupEditorSubmitHandler(ctx)
	m.slashRegistry = NewSlashRegistry()
	h := &liveInputModelHandle{mode: m, ctx: ctx, recordingCompactHandle: recordingCompactHandle{agent: m.agent}}
	m.opts.SessionHandle = h
	return m, h, ctx
}

// serveOwnerUntil stands in for the main loop until done reports true: it awaits the next prompt while none runs and starts a
// resolved one first, then dispatches terminal input as the loop dispatches it and runs posted tasks, renders and agent events.
func serveOwnerUntil(t *testing.T, m *InteractiveMode, ctx context.Context, done func() bool, failure func() string) {
	t.Helper()
	readCh, errCh := m.inputReadCh, (<-chan error)(m.inputErrCh)
	var userInput <-chan string
	for !done() {
		if userInput == nil && m.initialMessagesDone == nil && !m.hasActiveAgentTurn() {
			userInput = m.getUserInput()
		}
		select {
		case text := <-userInput:
			userInput = nil
			m.handleSubmit(ctx, text)
			continue
		default:
		}
		select {
		case <-ctx.Done():
			t.Fatal(failure())
		case text := <-userInput:
			userInput = nil
			m.handleSubmit(ctx, text)
		case chunk, ok := <-readCh:
			if !ok {
				readCh = nil
			} else if err := m.dispatchTerminalRead(ctx, chunk, &readCh, errCh); err != nil {
				t.Fatal(err)
			}
		case task := <-m.uiTaskCh:
			task()
		case <-m.renderWakeCh:
			m.runScheduledRender()
		case event, ok := <-m.eventCh:
			if !ok {
				m.eventCh = nil
			} else {
				m.handleAgentEvent(event)
			}
		}
	}
}

// settleSwitchModel starts sc.SwitchModel and runs owner-loop tasks until the change settles.
func settleSwitchModel(t testing.TB, m *InteractiveMode, sc *SlashContext, spec string) error {
	t.Helper()
	var result error
	settled := false
	sc.SwitchModel(spec, func(err error) { result, settled = err, true })
	runOwnerTasksUntil(t, m, func() bool { return settled })
	return result
}

// runOwnerTasksUntil runs posted owner-loop tasks, as the owner loop would, until done reports true. A key handler does not
// await a cycle (interactive-mode.ts:3083-3084), so its status follows on the owner loop.
func runOwnerTasksUntil(t testing.TB, m *InteractiveMode, done func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !done() {
		select {
		case task := <-m.uiTaskCh:
			task()
		case <-deadline:
			t.Fatal("the owner loop never finished the model change")
		}
	}
}

// focusedModelSearch reports the search text of the focused model selector.
func focusedModelSearch(m *InteractiveMode) string {
	focused := m.tuiInst.GetFocusedComponent()
	selector, ok := focused.(*tui.ModelSelectorComponent)
	if !ok {
		return fmt.Sprintf("focus is %T", focused)
	}
	return "search " + selector.GetSearchInput().Text()
}

func TestInteractiveModelChangeKeepsInputLive(t *testing.T) {
	t.Run("cycle", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, h, ctx := liveInputModelMode(t)
			next := &ai.Model{ID: "next", ProviderMeta: ai.ProviderMetadata{ProviderID: "capture"}}
			h.cycleResults = []*ModelCycleResult{{Model: next}}
			h.keys = [][]string{{"x"}}
			h.probe = func() string { return "editor " + m.editor.Text() }
			m.cycleModel(true)
			if m.opts.Model != next {
				t.Fatalf("model %v after Ctrl+P, want the cycled model before model_select settles", m.opts.Model)
			}
			serveOwnerUntil(t, m, ctx, func() bool { return m.lastStatusMessage == "Switched to next" }, func() string { return "the cycle never finished" })
			if seen, _, _ := h.results(); len(seen) != 1 || seen[0] != "editor x" {
				t.Fatalf("while the cycle awaited its handlers the owner saw %q, want the key in the editor", seen)
			}
		})
	})

	t.Run("selection", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, h, ctx := liveInputModelMode(t)
			useCaptureModels(t, m)
			h.keys = [][]string{{"x"}}
			h.probe = func() string { return focusedModelSearch(m) }
			go m.routeInputChunk(ctx, []byte("\r"), m.inputReadCh)
			m.handleModelPicker()
			serveOwnerUntil(t, m, ctx, func() bool { return m.lastStatusMessage == "Model: model-one" }, func() string { return "the selection never settled" })
			if seen, _, _ := h.results(); len(seen) != 1 || seen[0] != "search x" {
				t.Fatalf("while the selection awaited its handlers the owner saw %q, want the key in the focused selector", seen)
			}
			if len(h.switched) != 1 {
				t.Fatalf("switched %v after the selection", h.switched)
			}
			if m.tuiInst.GetFocusedComponent() != m.editor || len(m.editorContainer.Children()) != 1 || m.editorContainer.Children()[0] != m.editor {
				t.Fatal("the selector did not return the editor slot and focus once the selection settled")
			}
		})
	})

	// A model_select handler's dialog takes the editor slot and disposes the pending selector, as Pi's dialogs dispose the
	// active selector (interactive-mode.ts:2714, 4833-4838); once the dialog is answered, keys reach the editor.
	t.Run("selection dialog", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, h, ctx := liveInputModelMode(t)
			useCaptureModels(t, m)
			ui := &ExtUIContext{m: m}
			h.keys = [][]string{{}}
			h.afterKeys = func() {
				answered := make(chan error, 1)
				go func() {
					_, err := ui.Confirm(ctx, "Allow?", "Switch the model?", extension.ExtensionUIDialogOptions{})
					answered <- err
				}()
				synctest.Wait()
				for _, key := range []string{"\r", "z"} {
					if ticket := m.routeInputChunk(ctx, []byte(key), m.inputReadCh); ticket != nil {
						<-ticket.done
					}
					if key == "\r" {
						if err := <-answered; err != nil {
							t.Error(err)
						}
					}
				}
			}
			h.probe = func() string {
				return fmt.Sprintf("slot %T focus %T editor %q", m.editorContainer.Children()[0], m.tuiInst.GetFocusedComponent(), m.editor.Text())
			}
			go m.routeInputChunk(ctx, []byte("\r"), m.inputReadCh)
			m.handleModelPicker()
			serveOwnerUntil(t, m, ctx, func() bool { return m.lastStatusMessage == "Model: model-one" }, func() string { return "the selection never settled" })
			if seen, _, _ := h.results(); len(seen) != 1 || seen[0] != `slot *tui.Editor focus *tui.Editor editor "z"` {
				t.Fatalf("after a handler's dialog during the selection the owner saw %q, want the key in the focused editor", seen)
			}
			if len(h.switched) != 1 || m.tuiInst.GetFocusedComponent() != m.editor || m.editorContainer.Children()[0] != m.editor {
				t.Fatalf("switched %v, focus %T after the selection settled", h.switched, m.tuiInst.GetFocusedComponent())
			}
		})
	})

	// The selector's done restores the editor only while it is still the active selector (interactive-mode.ts:4849-4857): a
	// dialog opened during the selection keeps the editor slot after the selection settles.
	t.Run("dialog outlives selection", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, h, ctx := liveInputModelMode(t)
			useCaptureModels(t, m)
			ui := &ExtUIContext{m: m}
			answered := make(chan error, 1)
			h.keys = [][]string{{}}
			h.afterKeys = func() {
				go func() {
					_, err := ui.Confirm(ctx, "Allow?", "Keep the model?", extension.ExtensionUIDialogOptions{})
					answered <- err
				}()
				synctest.Wait()
			}
			go m.routeInputChunk(ctx, []byte("\r"), m.inputReadCh)
			m.handleModelPicker()
			serveOwnerUntil(t, m, ctx, func() bool { return m.lastStatusMessage == "Model: model-one" }, func() string { return "the selection never settled" })
			if m.shownExtensionDialog() == nil || m.editorContainer.Children()[0] != m.shownExtensionDialog().component {
				t.Fatalf("slot %T after the selection settled, want the dialog it opened still shown", m.editorContainer.Children()[0])
			}
			go m.routeInputChunk(ctx, []byte("\r"), m.inputReadCh)
			serveOwnerUntil(t, m, ctx, func() bool { return m.shownExtensionDialog() == nil }, func() string { return "the dialog was never answered" })
			if err := <-answered; err != nil || m.editorContainer.Children()[0] != m.editor {
				t.Fatalf("dialog answer %v, slot %T after it closed", err, m.editorContainer.Children()[0])
			}
		})
	})

	// The second Ctrl+P applies its model at once, its handlers overlap the first's, and a prompt submitted meanwhile starts
	// at once on the model the second cycle set.
	t.Run("second cycle and prompt", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, h, ctx := liveInputModelMode(t)
			prompted := make(chan capturedStreamRequest, 1)
			second := &ai.Model{ID: "second", ProviderMeta: ai.ProviderMetadata{ProviderID: "capture"}, Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
			third := &ai.Model{ID: "third", Provider: captureStreamOptionsProvider{seen: prompted}, ProviderMeta: ai.ProviderMetadata{ProviderID: "capture"}, Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
			h.cycleResults = []*ModelCycleResult{{Model: second}, {Model: third}}
			h.keys = [][]string{{"\x10", "h", "i", "\r"}}
			var promptModel string
			firstFinished := make(chan struct{})
			h.hold = func(index int) {
				if index == 0 {
					// The first cycle's handlers settle only once the prompt has reached a model.
					select {
					case <-prompted:
						promptModel = "third"
					case <-ctx.Done():
					}
					return
				}
				// The second cycle's handlers settle once the first cycle finished on the owner loop.
				<-firstFinished
			}
			m.cycleModel(true)
			var statuses []string
			serveOwnerUntil(t, m, ctx, func() bool {
				if status := m.lastStatusMessage; status != "" && (len(statuses) == 0 || statuses[len(statuses)-1] != status) {
					statuses = append(statuses, status)
					if status == "Switched to second" {
						close(firstFinished)
					}
				}
				return m.lastStatusMessage == "Switched to third"
			}, func() string {
				_, notifications, _ := h.results()
				return fmt.Sprintf("the second cycle never finished (notifications: %d, applied %v)", notifications, h.applied)
			})
			if promptModel != "third" {
				t.Fatal("the prompt submitted during the first cycle's handlers did not reach the second cycle's model")
			}
			if want := []string{"current->second", "second->third"}; !slices.Equal(h.applied, want) {
				t.Fatalf("applied %v, want %v", h.applied, want)
			}
			if _, notifications, maxActive := h.results(); notifications != 2 || maxActive != 2 {
				t.Fatalf("notifications = %d with %d at once, want the second overlapping the first", notifications, maxActive)
			}
			if want := []string{"Switched to second", "Switched to third"}; !slices.Equal(statuses, want) {
				t.Fatalf("statuses %v, want %v", statuses, want)
			}
			serveOwnerUntil(t, m, ctx, func() bool { return !m.hasActiveAgentTurn() }, func() string { return "the prompt never settled" })
		})
	})

	// Escape runs Pi's onCancel, which closes the selector at once (interactive-mode.ts:5335-5338); later keys reach the
	// editor, and a prompt submitted there starts while setModel still awaits. Its status follows when it settles.
	t.Run("selection cancel and prompt", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, h, ctx := liveInputModelMode(t)
			prompted := useCaptureModels(t, m)
			h.keys = [][]string{{"\x1b", "y", "\r"}}
			h.probe = func() string { return fmt.Sprintf("%T", m.tuiInst.GetFocusedComponent()) }
			prompt := ""
			h.afterKeys = func() {
				select {
				case request := <-prompted:
					prompt = lastUserMessageText(t, request.Messages)
				case <-ctx.Done():
				}
			}
			go m.routeInputChunk(ctx, []byte("\r"), m.inputReadCh)
			m.handleModelPicker()
			serveOwnerUntil(t, m, ctx, func() bool { return m.lastStatusMessage == "Model: model-one" }, func() string { return "the selection never settled" })
			if seen, _, _ := h.results(); len(seen) != 1 || seen[0] != "*tui.Editor" || prompt != "y" {
				t.Fatalf("after Escape during the selection the owner focused %q and prompted %q, want the editor and the prompt started", seen, prompt)
			}
			if len(h.switched) != 1 {
				t.Fatalf("switched %v after the cancelled selector settled", h.switched)
			}
			serveOwnerUntil(t, m, ctx, func() bool { return !m.hasActiveAgentTurn() }, func() string { return "the prompt never settled" })
		})
	})

	// Another confirm in the still focused selector runs Pi's selectModel again: its setModel applies and notifies while the
	// first one's handlers run, and the selector closes once the first settles.
	t.Run("second selection", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, h, ctx := liveInputModelMode(t)
			useCaptureModels(t, m)
			h.keys = [][]string{{"\x1b[B", "\r"}}
			h.probe = func() string { return fmt.Sprintf("%T", m.tuiInst.GetFocusedComponent()) }
			secondStarted, firstFinished := make(chan struct{}), make(chan struct{})
			h.hold = func(index int) {
				if index == 0 {
					<-secondStarted
					return
				}
				close(secondStarted)
				// The second selection's handlers settle once the first selection finished on the owner loop.
				<-firstFinished
			}
			go m.routeInputChunk(ctx, []byte("\r"), m.inputReadCh)
			m.handleModelPicker()
			var atFirst string
			serveOwnerUntil(t, m, ctx, func() bool {
				if atFirst == "" && m.lastStatusMessage == "Model: model-one" {
					atFirst = fmt.Sprintf("focus %T model %s", m.tuiInst.GetFocusedComponent(), m.opts.Model.ID)
					close(firstFinished)
				}
				return m.lastStatusMessage == "Model: model-two"
			}, func() string {
				return fmt.Sprintf("the second confirm never settled (applied %v, status %q)", h.applied, m.lastStatusMessage)
			})
			if atFirst != "focus *tui.Editor model model-two" {
				t.Fatalf("when the first selection settled: %s; want the editor focused and the second selection, applied before it", atFirst)
			}
			if seen, notifications, maxActive := h.results(); len(seen) != 1 || seen[0] != "*tui.ModelSelectorComponent" || notifications != 2 || maxActive != 2 {
				t.Fatalf("owner saw %q; notifications = %d with %d at once, want the second overlapping the first from the focused selector", seen, notifications, maxActive)
			}
			if want := []string{"model-one->model-one", "model-one->model-two"}; !slices.Equal(h.applied, want) {
				t.Fatalf("applied %v, want %v", h.applied, want)
			}
		})
	})

	// Pi's editor does not await onSubmit (interactive-mode.ts:3180, 3195-3199), and handleModelCommand awaits setModel only
	// for its own status (5163-5180). A typed /model returns to the loop at once, and a second one typed while the first's
	// handlers run applies and notifies at once too; each status follows its own handlers.
	t.Run("typed model commands", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, h, ctx := liveInputModelMode(t)
			useCaptureModels(t, m)
			release := make(chan struct{})
			// The first notification reports to the owner loop before it holds.
			h.keys = [][]string{{}}
			h.probe = func() string { return m.opts.Model.ID }
			h.hold = func(index int) {
				if index == 0 {
					<-release
				}
			}
			m.dispatchSlash(ctx, "/model capture/model-two")
			serveOwnerUntil(t, m, ctx, func() bool { _, notifications, _ := h.results(); return notifications == 1 }, func() string {
				return "the first /model never reached its handlers"
			})
			m.dispatchSlash(ctx, "/model capture/model-one")
			var statuses []string
			serveOwnerUntil(t, m, ctx, func() bool {
				if status := m.lastStatusMessage; status != "" && (len(statuses) == 0 || statuses[len(statuses)-1] != status) {
					statuses = append(statuses, status)
					if status == "Model: model-one" {
						close(release)
					}
				}
				return m.lastStatusMessage == "Model: model-two"
			}, func() string {
				return fmt.Sprintf("the typed commands never settled (applied %v, statuses %v)", h.applied, statuses)
			})
			if want := []string{"model-one->model-two", "model-two->model-one"}; !slices.Equal(h.applied, want) {
				t.Fatalf("applied %v, want %v", h.applied, want)
			}
			if _, notifications, maxActive := h.results(); notifications != 2 || maxActive != 2 {
				t.Fatalf("notifications = %d with %d at once, want the second overlapping the first", notifications, maxActive)
			}
			if want := []string{"Model: model-one", "Model: model-two"}; !slices.Equal(statuses, want) || m.opts.Model.ID != "model-one" {
				t.Fatalf("statuses %v model %s, want completion-order statuses and the last applied model", statuses, m.opts.Model.ID)
			}
		})
	})

	// As awaitReloadStep does, cancellation reports its cause once the awaited handlers returned; both cycles' handlers are
	// still running when the mode stops.
	t.Run("cancelled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, h, ctx := liveInputModelMode(t)
			runCtx, cancel := context.WithCancelCause(ctx)
			m.runCtx = runCtx
			stopped := errors.New("interactive mode stopped")
			second := &ai.Model{ID: "second", ProviderMeta: ai.ProviderMetadata{ProviderID: "capture"}}
			third := &ai.Model{ID: "third", ProviderMeta: ai.ProviderMetadata{ProviderID: "capture"}}
			h.cycleResults = []*ModelCycleResult{{Model: second}, {Model: third}}
			h.keys = [][]string{{"\x10"}}
			h.afterKeys = func() { cancel(stopped) }
			h.hold = func(index int) {
				if index == 1 {
					<-runCtx.Done()
				}
			}
			m.cycleModel(true)
			serveOwnerUntil(t, m, ctx, func() bool {
				return strings.Count(strings.Join(m.chatContainer.Render(100), "\n"), stopped.Error()) == 2
			}, func() string {
				return fmt.Sprintf("the cancellation's cause was not reported for both cycles (applied %v)", h.applied)
			})
			if strings.Contains(strings.Join(m.chatContainer.Render(100), "\n"), "Switched to") {
				t.Fatal("a cancelled cycle reported success")
			}
		})
	})

	// Pi's shutdown exits without awaiting a pending model_select (interactive-mode.ts:4251-4291). Stop cancels the mode's
	// background context and then joins its tasks (Init's teardown), so a handler that honours its context cannot hold it.
	t.Run("stop", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, h, ctx := liveInputModelMode(t)
			useCaptureModels(t, m)
			m.backgroundCtx, m.backgroundCancel = context.WithCancel(ctx)
			h.cycleResults = []*ModelCycleResult{{Model: &ai.Model{ID: "second", ProviderMeta: ai.ProviderMetadata{ProviderID: "capture"}}}}
			h.blockUntilDone = true
			// Each notification reports to the owner loop before it blocks; past its report it waits only for its context.
			h.keys = [][]string{{}, {}}
			reported := 0
			h.probe = func() string { reported++; return "" }
			m.cycleModel(true)
			m.dispatchSlash(ctx, "/model capture/model-two")
			serveOwnerUntil(t, m, ctx, func() bool { return reported == 2 }, func() string {
				return "the changes never reached their handlers"
			})
			stopped := time.Now()
			m.backgroundCancel()
			m.backgroundTasks.Wait()
			// Inside the bubble time only advances while every goroutine waits, here for the mode's own deadline.
			if waited := time.Since(stopped); waited != 0 {
				t.Fatalf("Stop waited %v for model_select handlers", waited)
			}
		})
	})
}

// useCaptureModels gives the picker two available models, with model-one current. Their requests reach the returned channel.
func useCaptureModels(t *testing.T, m *InteractiveMode) chan capturedStreamRequest {
	t.Helper()
	models := `{"providers":{"capture":{"baseUrl":"http://127.0.0.1:1/v1","api":"openai-completions","apiKey":"fixture-key","models":[{"id":"model-one","name":"Model One"},{"id":"model-two","name":"Model Two"}]}}}`
	if err := os.WriteFile(filepath.Join(m.opts.AgentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	m.opts.ModelRegistry.Refresh()
	prompted := make(chan capturedStreamRequest, 4)
	build := func(id string) *ai.Model {
		return &ai.Model{ID: id, Provider: captureStreamOptionsProvider{seen: prompted}, ProviderMeta: ai.ProviderMetadata{ProviderID: "capture"}, Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	}
	m.opts.Model = build("model-one")
	m.agent = mustNewAgent(agent.AgentOptions{Model: m.opts.Model})
	m.opts.SessionHandle.(*liveInputModelHandle).agent = m.agent
	m.opts.ModelBuilder = func(spec string) (*ai.Model, error) {
		_, id, _ := strings.Cut(spec, "/")
		return build(id), nil
	}
	return prompted
}
