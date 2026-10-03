package experimental

// Ports packages/coding-agent/src/experimental/durable/tui.ts (runDurableTui) and packages/coding-agent/src/experimental/vacation/tui.ts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/experimental/durableagent"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// durableCommands are what the prompt line and the selectors ask of the controller, and the selectors they open.
type durableCommands struct {
	source     durableagent.DurableViewSource
	controller durableagent.DurableController
	view       func() *durableTui
}

func (commands durableCommands) selectModel() {
	snapshot := commands.source.Current()
	current := durableagent.AgentOf(snapshot.Conversation).Model
	isCurrent := func(model durableagent.ModelSummary) bool {
		return current != nil && model.Provider == current.Provider && model.ModelId == current.ModelId
	}
	models := slices.Clone(snapshot.Models)
	slices.SortStableFunc(models, func(left, right durableagent.ModelSummary) int {
		return boolNumber(isCurrent(right)) - boolNumber(isCurrent(left))
	})
	items := make([]durableSelectItem, len(models))
	for index, model := range models {
		items[index] = durableSelectItem{Value: model.Provider + "/" + model.ModelId, Label: model.ModelId, Description: model.Provider}
	}
	view := commands.view()
	view.Mount(newDurableListSelector("Select model:", items, func(value string) {
		view.RestoreEditor()
		separator := strings.Index(value, "/")
		commands.controller.SetModel(durable.ModelRef{Provider: value[:separator], ModelId: value[separator+1:]})
	}, view.RestoreEditor))
}

func boolNumber(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (commands durableCommands) selectConversation() {
	snapshot := commands.source.Current()
	items := make([]durableSelectItem, 0, len(snapshot.Conversations))
	for _, candidate := range slices.Backward(snapshot.Conversations) {
		description := ""
		if candidate.ID == snapshot.Conversation.Conversation.Id {
			description = "(shown) "
		}
		if candidate.Title != nil {
			description += *candidate.Title
		}
		items = append(items, durableSelectItem{Value: strconv.FormatInt(int64(candidate.ID), 10), Label: candidate.Label, Description: description})
	}
	view := commands.view()
	view.Mount(newDurableListSelector("Switch to:", items, func(value string) {
		view.RestoreEditor()
		id, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			commands.controller.SwitchConversation(durable.ConversationId(id))
		}
	}, view.RestoreEditor))
}

// submit handles a line of the editor: a slash command, or a prompt that steers a busy conversation.
func (commands durableCommands) submit(text string) {
	trimmed := strings.TrimFunc(text, widthx.IsJSSpace)
	switch {
	case trimmed == "":
	case trimmed == "/model":
		commands.selectModel()
	case trimmed == "/tasks":
		commands.controller.ToggleTasks()
	case trimmed == "/agents":
		commands.selectConversation()
	case trimmed == "/compact" || strings.HasPrefix(trimmed, "/compact "):
		instructions := strings.TrimFunc(trimmed[len("/compact"):], widthx.IsJSSpace)
		if instructions == "" {
			commands.controller.Compact(nil)
			return
		}
		commands.controller.Compact(&instructions)
	default:
		commands.controller.Submit(trimmed, durable.WhenBusySteer)
	}
}

// RunDurableTui shows source's view and sends the user's input to controller until the user exits. The theme is pi's: the theme setting, also light and dark pairs, resolved against the terminal's colors.
func RunDurableTui(ctx context.Context, source durableagent.DurableViewSource, controller durableagent.DurableController, settings *codingagent.SettingsManager) (err error) {
	tui.SetCapabilityOverrides(settings.GetTerminalCapabilityOverrides())
	agentDir := codingagent.AgentDir()
	executor := NewClientTuiExecutor()
	defer executor.Close()
	uiCtx, cancelUI := context.WithCancel(ctx)
	defer cancelUI()
	wheel := settings.GetFullscreenWheelScrollLines()
	wheelScrollLines := tui.WheelScrollLines{Auto: wheel.Auto, Lines: wheel.Lines}
	ui := codingagent.CreateInteractiveTui(codingagent.InteractiveTuiOptions{TuiMode: "fullscreen", ShowHardwareCursor: new(settings.GetShowHardwareCursor()), LogDirectory: agentDir, FullscreenWheelScrollLines: &wheelScrollLines})
	alt := ui.(*tui.TuiAltScreen)
	terminal := tui.NewProcessTerminal(os.Stdin, os.Stdout)
	finished := make(chan struct{})
	var finishOnce sync.Once
	exit := func() { finishOnce.Do(func() { close(finished) }) }
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

	var view *durableTui
	commands := durableCommands{source: source, controller: controller, view: func() *durableTui { return view }}
	keybindings := codingagent.NewKeybindingsManager(agentDir)
	view = newDurableTui(uiCtx, durableTuiOptions{
		CWD: source.Current().Session.CWD, UI: ui, Keybindings: keybindings,
		Handlers: durableTuiHandlers{
			submit:        commands.submit,
			followUp:      func(text string) { controller.Submit(text, durable.WhenBusyFollowUp) },
			abort:         func() { controller.Abort() },
			exit:          exit,
			selectModel:   commands.selectModel,
			cycleThinking: func() { controller.CycleThinking() },
		},
		RequestRender: ui.RequestRender, RunOnMain: executor.RunOnMain,
	})
	// pi's theme handling: the theme setting resolved against the terminal's reported colors.
	theme, err := codingagent.NewInteractiveThemeController(uiCtx, ui, codingagent.InteractiveThemeControllerOptions{
		GetSettingsManager: func() *codingagent.SettingsManager { return settings }, Output: os.Stdout,
		RunOnMain: executor.RunOnMain,
		ShowError: func(message string) { fmt.Fprintln(os.Stderr, message) },
		OnChanged: ui.RequestRender,
	})
	if err != nil {
		return err
	}
	themeWatcher := tui.StartThemeWatcher(uiCtx, filepath.Join(agentDir, "themes"), executor.RunOnMain)
	var inputWorkers sync.WaitGroup
	terminalStarted, uiStarted := false, false
	unsubscribe := func() {}
	defer func() {
		unsubscribe()
		cleanup := []error{theme.Dispose()}
		themeWatcher.Close()
		cleanup = append(cleanup, executor.RunOnMain(context.Background(), func() {
			ui.CancelPendingRender()
			if uiStarted {
				ui.Stop()
				uiStarted = false
			}
			view.Stop()
		}))
		cancelUI()
		if terminalStarted {
			terminal.Stop()
		}
		inputWorkers.Wait()
		err = errors.Join(append([]error{err}, cleanup...)...)
	}()

	apply := func() {
		if failure := view.Apply(source.Current()); failure != nil && uiCtx.Err() == nil {
			report(failure)
		}
	}
	unsubscribe = source.Subscribe(func() { dispatch(apply) })
	input := make(chan []byte)
	inputWorkers.Go(func() { report(runDurableTuiInput(uiCtx, input, executor, alt, view, theme, finished)) })
	err = terminal.StartWithReadError(func(data []byte) {
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
		ui.Add(view)
		alt.SetLayoutRoot(view.LayoutRoot())
		ui.SetFocus(view)
		uiStarted = true
		ui.Start()
	}); err != nil {
		return err
	}
	if err := theme.ApplyFromSettings(ctx); err != nil {
		return err
	}
	dispatch(apply)
	select {
	case <-finished:
		theme.DisableAutoSync()
		return nil
	case failure := <-failures:
		return failure
	case <-ctx.Done():
		return ctx.Err()
	}
}

func runDurableTuiInput(ctx context.Context, input <-chan []byte, executor *ClientTuiExecutor, ui *tui.TuiAltScreen, view *durableTui, theme *codingagent.InteractiveThemeController, finished <-chan struct{}) error {
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
					if theme.ConsumeInput(data) || !tui.ShouldDeliverKey(view, data) {
						continue
					}
					if ui.HandleViewportInput(data) || ui.ConsumeCellSizeResponse(data) || ui.HandleFocusedSearchInput(data) {
						continue
					}
					view.HandleInput(data)
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
