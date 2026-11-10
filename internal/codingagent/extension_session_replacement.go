package codingagent

import (
	"context"
	"errors"
	"os"

	"github.com/MichaelKinsy/PiG/coding/extension"
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
	statusResumeCancelled = "Resume cancelled"
)

// resumeHost is where a resume runs. The /resume command runs on the owner loop, so its replacement is awaited while that loop serves extension dialogs and its missing-cwd selector is a stacked dialog that the same loop awaits (interactive-mode.ts promptForMissingSessionCwd). An extension's ctx.switchSession runs on the extension host's call goroutine, so its replacement runs there and its selector is an extension dialog.
type resumeHost struct {
	await func(ctx context.Context, work func(context.Context) error) error
	// selectOption is showExtensionSelector: the chosen option, or ok false when it was dismissed.
	selectOption func(ctx context.Context, title string, options []string) (string, bool)
}

func (m *InteractiveMode) commandResumeHost() resumeHost {
	return resumeHost{
		await: m.awaitExtensionUI,
		selectOption: func(ctx context.Context, title string, options []string) (string, bool) {
			return m.askSelectorCommit(ctx, title, options, nil)
		},
	}
}

func (m *InteractiveMode) extensionResumeHost() resumeHost {
	return resumeHost{
		await: func(ctx context.Context, work func(context.Context) error) error { return work(ctx) },
		selectOption: func(ctx context.Context, title string, options []string) (string, bool) {
			choice, err := (&ExtUIContext{m: m}).Select(ctx, title, options, extension.ExtensionUIDialogOptions{})
			return choice, err == nil
		},
	}
}

// projectTrustUI is Pi's createProjectTrustContext (interactive-mode.ts:2521-2534): the live extension UI serves the project trust prompt of a resumed Session's destination.
func (m *InteractiveMode) projectTrustUI(cwd string) extension.ProjectTrustContext {
	return extension.ProjectTrustContext{Cwd: cwd, Mode: extension.ModeTUI, HasUI: true, UI: &ExtUIContext{m: m}}
}

// resumeThroughRuntime is Pi's handleResumeSession: the switch prompts for trust in the destination project, and a missing stored cwd offers the current directory. A missing stored cwd offers Continue, Fork or Cancel (promptForMissingSessionCwd). It returns the status Pi shows: empty when a session_before_switch handler cancelled, statusResumeCancelled when the user cancelled the prompt, "Resumed session in <cwd>" after Continue, "Forked into new session <id> in <cwd>" after Fork, statusResumed otherwise.
func (m *InteractiveMode) resumeThroughRuntime(ctx context.Context, host resumeHost, path string, options *extension.SwitchSessionOptions) (extension.CancelledResult, string, error) {
	result, err := m.switchThroughRuntime(ctx, host, path, "", options)
	if missing, ok := errors.AsType[*MissingSessionCwdError](err); ok {
		switch m.promptForMissingSessionCwd(ctx, host, missing.Issue) {
		case MissingSessionCwdContinue:
			result, err = m.switchThroughRuntime(ctx, host, path, missing.Issue.FallbackCwd, options)
			if err != nil || result.Cancelled {
				return result, "", err
			}
			return result, "Resumed session in " + missing.Issue.FallbackCwd, nil
		case MissingSessionCwdFork:
			return m.switchToForkedSession(ctx, host, path, missing.Issue.FallbackCwd, options)
		default:
			return extension.CancelledResult{Cancelled: true}, statusResumeCancelled, nil
		}
	}
	if err != nil || result.Cancelled {
		return result, "", err
	}
	return result, statusResumed, nil
}

// switchThroughRuntime is runtimeHost.switchSession with the project trust UI of the destination.
func (m *InteractiveMode) switchThroughRuntime(ctx context.Context, host resumeHost, path, cwdOverride string, options *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
	var result extension.CancelledResult
	err := host.await(ctx, func(ctx context.Context) error {
		var err error
		result, err = m.runtimeHost.SwitchSession(ctx, path, cwdOverride, m.projectTrustUI, options)
		return err
	})
	return result, err
}

// promptForMissingSessionCwd is Pi's promptForMissingSessionCwd: the choice, or "" when the user cancelled.
func (m *InteractiveMode) promptForMissingSessionCwd(ctx context.Context, host resumeHost, issue SessionCwdIssue) MissingSessionCwdChoice {
	options := GetMissingSessionCwdOptions(issue)
	labels := make([]string, len(options))
	for i, option := range options {
		labels[i] = option.Label
	}
	label, ok := host.selectOption(ctx, FormatMissingSessionCwdPrompt(issue), labels)
	if !ok {
		return ""
	}
	for _, option := range options {
		if option.Label == label {
			return option.Value
		}
	}
	return ""
}

// switchToForkedSession is Pi's switchToForkedSession: fork a session whose stored cwd is gone into cwd and switch to the fork, like `--session <id>` from another project (Pi #11060). A switch an extension vetoed drops the fork, so cancelling leaves no copy behind.
func (m *InteractiveMode) switchToForkedSession(ctx context.Context, host resumeHost, sourcePath, cwd string, options *extension.SwitchSessionOptions) (extension.CancelledResult, string, error) {
	fork, err := NewSessionManagerWithDir(cwd, m.currentSession().GetSessionDir()).ForkFromFile(sourcePath)
	if err != nil {
		return extension.CancelledResult{}, "", err
	}
	result, err := m.switchThroughRuntime(ctx, host, fork.Path(), "", options)
	if err != nil {
		return result, "", err
	}
	if result.Cancelled {
		_ = os.Remove(fork.Path())
		return result, "", nil
	}
	return result, "Forked into new session " + fork.ID() + " in " + cwd, nil
}

// replaceThroughRuntime replaces the Session through the runtime host, as Pi's /new, /resume, /fork and /clone do with runtimeHost, while servicing extension dialogs and event barriers on the input loop.
func (m *InteractiveMode) replaceThroughRuntime(ctx context.Context, operation, target string) error {
	rt := m.runtimeHost
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

// replaceSessionFromCommand awaits the same Session owner used by extension commands while servicing extension dialogs and event barriers on the input loop.
func (m *InteractiveMode) replaceSessionFromCommand(ctx context.Context, operation, target string) error {
	if m.runtimeHost != nil {
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
		if entry, ok := m.currentSession().GetEntry(target); ok {
			if message, ok := asMessage(entry); ok {
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
	if rt := m.runtimeHost; rt != nil {
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
			if entry, ok := m.currentSession().GetEntry(id); ok {
				if message, ok := asMessage(entry); ok {
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
		if m.runtimeHost == nil {
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
