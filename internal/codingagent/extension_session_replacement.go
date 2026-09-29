package codingagent

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// extensionSessionController supplies the Session-owned replacement operations
// without making the terminal own persistence or importing coding.Session.
type extensionSessionController interface {
	ExtensionCommandActions() extension.CommandActions
}

// sessionImporter supplies the Session-owned /import replacement.
type sessionImporter interface {
	ImportFromJsonl(ctx context.Context, inputPath, cwdOverride string) (extension.CancelledResult, error)
}

type sessionRebinder interface {
	SetBeforeSessionReplacement(func(context.Context) error)
	SetRebindSession(func(context.Context, extension.SessionStartEvent) error)
}

// Statuses of Pi's handleResumeSession (interactive-mode.ts:5590-5626).
const (
	statusResumed         = "Resumed session"
	statusResumedInCWD    = "Resumed session in current cwd"
	statusResumeCancelled = "Resume cancelled"
)

// resumeHost is where a resume runs. The /resume command runs on the owner loop, so its replacement is awaited while that loop serves extension dialogs and its confirmation is an editor-slot selector. An extension's ctx.switchSession runs on the extension host's call goroutine, so its replacement runs there and its confirmation is an extension dialog.
type resumeHost struct {
	await   func(ctx context.Context, work func(context.Context) error) error
	confirm func(ctx context.Context, title, message string) bool
}

func (m *InteractiveMode) commandResumeHost() resumeHost {
	return resumeHost{
		await:   m.awaitExtensionUI,
		confirm: func(_ context.Context, title, message string) bool { return m.confirmSelection(title, message) },
	}
}

func (m *InteractiveMode) extensionResumeHost() resumeHost {
	return resumeHost{
		await: func(ctx context.Context, work func(context.Context) error) error { return work(ctx) },
		confirm: func(ctx context.Context, title, message string) bool {
			confirmed, err := (&ExtUIContext{m: m}).Confirm(ctx, title, message, nil)
			return err == nil && confirmed
		},
	}
}

// projectTrustUI is Pi's createProjectTrustContext (interactive-mode.ts:2521-2534): the live extension UI serves the project trust prompt of a resumed Session's destination.
func (m *InteractiveMode) projectTrustUI(string) extension.UIContext { return &ExtUIContext{m: m} }

// resumeThroughRuntime is Pi's handleResumeSession: the switch prompts for trust in the destination project, and a missing stored cwd offers the current directory. It returns the status Pi shows: empty when a session_before_switch handler cancelled, statusResumeCancelled when the user declined the current directory, statusResumedInCWD after a retry with it, statusResumed otherwise.
func (m *InteractiveMode) resumeThroughRuntime(ctx context.Context, host resumeHost, path string, options *extension.SwitchSessionOptions) (extension.CancelledResult, string, error) {
	rt := m.opts.Runtime
	run := func(cwdOverride string) (extension.CancelledResult, error) {
		var result extension.CancelledResult
		err := host.await(ctx, func(ctx context.Context) error {
			var err error
			result, err = rt.SwitchSession(ctx, path, cwdOverride, m.projectTrustUI, options)
			return err
		})
		return result, err
	}
	result, err := run("")
	if missing, ok := errors.AsType[*MissingSessionCwdError](err); ok {
		if !host.confirm(ctx, "Session cwd not found", FormatMissingSessionCwdPrompt(missing.Issue)) {
			return extension.CancelledResult{Cancelled: true}, statusResumeCancelled, nil
		}
		result, err = run(missing.Issue.FallbackCwd)
		if err != nil || result.Cancelled {
			return result, "", err
		}
		return result, statusResumedInCWD, nil
	}
	if err != nil || result.Cancelled {
		return result, "", err
	}
	return result, statusResumed, nil
}

// replaceThroughRuntime replaces the Session through the runtime host, as Pi's /new, /resume, /fork and /clone do with runtimeHost, while servicing extension dialogs and event barriers on the input loop.
func (m *InteractiveMode) replaceThroughRuntime(ctx context.Context, operation, target string) error {
	rt := m.opts.Runtime
	var result extension.CancelledResult
	var fork InteractiveForkResult
	var err error
	if operation == "resume" {
		var status string
		result, status, err = m.resumeThroughRuntime(ctx, m.commandResumeHost(), target, nil)
		switch {
		case err != nil:
			return err
		case status == statusResumeCancelled:
			m.showStatus(status)
		default:
			m.resumeStatus = status
		}
	} else {
		err = m.awaitExtensionUI(ctx, func(ctx context.Context) error {
			var err error
			switch operation {
			case "new":
				result, err = rt.NewSession(ctx, nil)
			case "fork", "clone":
				position := "before"
				if operation == "clone" {
					position = "at"
				}
				fork, err = rt.Fork(ctx, target, &extension.ForkOptions{Position: position})
				result = extension.CancelledResult{Cancelled: fork.Cancelled}
			}
			return err
		})
	}
	if err != nil {
		return err
	}
	if result.Cancelled {
		return errSessionReplacementCancelled
	}
	if operation == "fork" {
		text := ""
		if fork.SelectedText != nil {
			text = *fork.SelectedText
		}
		m.editor.SetText(text)
	}
	return nil
}

// confirmSelection asks a yes/no question in the editor slot, as upstream showExtensionConfirm does.
func (m *InteractiveMode) confirmSelection(title, message string) bool {
	sel := tui.NewExtensionSelector(title+"\n"+message, []string{"Yes", "No"})
	idx, ok := m.runEditorSlotExtensionSelector(sel)
	return ok && idx == 0
}

// replaceSessionFromCommand awaits the same Session owner used by extension commands while servicing extension dialogs and event barriers on the input loop.
func (m *InteractiveMode) replaceSessionFromCommand(ctx context.Context, operation, target string) error {
	if m.opts.Runtime != nil {
		return m.replaceThroughRuntime(ctx, operation, target)
	}
	handle, ok := m.opts.SessionHandle.(extensionSessionController)
	if !ok {
		return errors.New("session replacement is unavailable")
	}
	m.bindSessionRebind()
	actions := handle.ExtensionCommandActions()
	var result extension.CancelledResult
	text := ""
	if operation == "fork" {
		if entry, ok := m.currentSession().EntryByID(target); ok {
			if message, ok := entry.AsMessage(); ok {
				text = extractMessageText(message)
			}
		}
	}
	err := m.awaitExtensionUI(ctx, func(ctx context.Context) error {
		var err error
		switch operation {
		case "new":
			result, err = actions.NewSessionContext(ctx, nil)
		case "resume":
			result, err = actions.SwitchSessionContext(ctx, target, nil)
		case "fork", "clone":
			position := "before"
			if operation == "clone" {
				position = "at"
			}
			result, err = actions.ForkContext(ctx, target, &extension.ForkOptions{Position: position})
		}
		return err
	})
	if err != nil {
		return err
	}
	if result.Cancelled {
		return errSessionReplacementCancelled
	}
	if operation == "fork" {
		m.editor.SetText(text)
	}
	return nil
}

func (m *InteractiveMode) bindSessionRebind() {
	if handle, ok := m.opts.SessionHandle.(sessionRebinder); ok {
		handle.SetBeforeSessionReplacement(func(ctx context.Context) error {
			return m.runOnMainAndWait(ctx, m.settleActiveRun)
		})
		handle.SetRebindSession(func(ctx context.Context, event extension.SessionStartEvent) error {
			if err := m.runOnMainAndWait(ctx, func() error {
				m.opts.SessionStartEvent = &event
				if event.Reason == "new" {
					m.restoreBuiltInHeader()
				}
				return nil
			}); err != nil {
				return err
			}
			return m.rebindCurrentSession(ctx, true)
		})
	}
}

func (m *InteractiveMode) extensionReplacementActions() extension.CommandActions {
	var actions extension.CommandActions
	if rt := m.opts.Runtime; rt != nil {
		actions = rt.ExtensionCommandActions(m.opts.SessionHandle)
	} else {
		handle, ok := m.opts.SessionHandle.(extensionSessionController)
		if !ok {
			return extension.CommandActions{}
		}
		m.bindSessionRebind()
		actions = handle.ExtensionCommandActions()
	}
	newSession := func(ctx context.Context, opts *extension.NewSessionOptions) (extension.CancelledResult, error) {
		if err := m.prepareExtensionSessionUI(ctx); err != nil {
			return extension.CancelledResult{}, err
		}
		result, err := actions.NewSessionContext(ctx, opts)
		return m.finishExtensionSessionUI(ctx, result, err, "new", "")
	}
	fork := func(ctx context.Context, id string, opts *extension.ForkOptions) (extension.CancelledResult, error) {
		text := ""
		if opts == nil || opts.Position != "at" {
			if entry, ok := m.currentSession().EntryByID(id); ok {
				if message, ok := entry.AsMessage(); ok {
					text = extractMessageText(message)
				}
			}
		}
		result, err := actions.ForkContext(ctx, id, opts)
		return m.finishExtensionSessionUI(ctx, result, err, "fork", text)
	}
	switchSession := func(ctx context.Context, path string, opts *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
		if err := m.prepareExtensionSessionUI(ctx); err != nil {
			return extension.CancelledResult{}, err
		}
		if m.opts.Runtime == nil {
			result, err := actions.SwitchSessionContext(ctx, path, opts)
			return m.finishExtensionSessionUI(ctx, result, err, "resume", statusResumed)
		}
		// Pi binds ctx.switchSession to handleResumeSession (interactive-mode.ts:1962).
		result, status, err := m.resumeThroughRuntime(ctx, m.extensionResumeHost(), path, opts)
		if err == nil && status == statusResumeCancelled {
			return result, m.extensionSessionUIOnMain(ctx, func() error {
				m.showStatus(status)
				return nil
			})
		}
		return m.finishExtensionSessionUI(ctx, result, err, "resume", status)
	}
	return extension.CommandActions{
		NewSession: func(opts *extension.NewSessionOptions) (extension.CancelledResult, error) {
			return newSession(context.Background(), opts)
		},
		NewSessionContext: newSession,
		Fork: func(id string, opts *extension.ForkOptions) (extension.CancelledResult, error) {
			return fork(context.Background(), id, opts)
		},
		ForkContext: fork,
		SwitchSession: func(path string, opts *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
			return switchSession(context.Background(), path, opts)
		},
		SwitchSessionContext: switchSession,
	}
}

func (m *InteractiveMode) prepareExtensionSessionUI(ctx context.Context) error {
	if m.tuiInst == nil {
		return nil
	}
	return m.extensionSessionUIOnMain(ctx, func() error {
		m.clearStatusIndicator("")
		return nil
	})
}

// finishExtensionSessionUI shows the outcome of an extension-driven replacement on the owner loop. text is the fork prefill, or the status a resume earned.
func (m *InteractiveMode) finishExtensionSessionUI(ctx context.Context, result extension.CancelledResult, err error, reason, text string) (extension.CancelledResult, error) {
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if result.Cancelled || m.tuiInst == nil {
		return result, err
	}
	return result, m.extensionSessionUIOnMain(ctx, func() error {
		if err != nil {
			prefix := "Failed to create session"
			switch reason {
			case "fork":
				prefix = "Failed to fork session"
			case "resume":
				prefix = "Failed to resume session"
			}
			return m.handleFatalRuntimeError(prefix, err)
		}
		switch reason {
		case "fork":
			m.editor.SetText(text)
			m.showStatus("Forked to new session")
		case "resume":
			m.showStatus(text)
		}
		m.tuiInst.Render()
		return nil
	})
}

func (m *InteractiveMode) extensionSessionUIOnMain(ctx context.Context, action func() error) error {
	done := make(chan error, 1)
	if err := m.postToMain(ctx, func() {
		if err := ctx.Err(); err != nil {
			done <- err
			return
		}
		done <- action()
	}); err != nil {
		return err
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
