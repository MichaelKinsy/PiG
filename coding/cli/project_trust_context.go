package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports packages/coding-agent/src/cli/project-trust.ts createProjectTrustContext.

// projectTrustCLIUI is the UI of a project_trust handler's context. A replacement that interactive mode drives hands its live UI
// through unchanged (interactive-mode.ts:2609-2621 createProjectTrustContext). Otherwise the context is cli/project-trust.ts
// createProjectTrustContext: only an interactive run with a UI prompts, a dismissed startup dialog answers undefined (empty, false,
// empty), every other run answers as a no-UI context does, and notify prints the message to stderr, colored as chalk would color it
// there, except in an interactive run, where it does nothing.
type projectTrustCLIUI struct {
	ui          extension.UIContext
	prompts     bool
	interactive bool
	live        bool
	stderr      io.Writer
	color       bool
}

// dismissed reports a startup dialog the user closed without an answer: showStartupSelector and showStartupInput resolve undefined
// (startup-ui.ts), so the handler sees no answer rather than an error.
func (ui *projectTrustCLIUI) dismissed(ctx context.Context, err error) bool {
	return !ui.live && errors.Is(err, context.Canceled) && ctx.Err() == nil
}

func (ui *projectTrustCLIUI) Select(ctx context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions) (string, error) {
	if !ui.prompts {
		return "", nil
	}
	selected, err := ui.ui.Select(ctx, title, options, opts)
	if ui.dismissed(ctx, err) {
		return "", nil
	}
	return selected, err
}

func (ui *projectTrustCLIUI) Confirm(ctx context.Context, title, message string, opts extension.ExtensionUIDialogOptions) (bool, error) {
	if !ui.prompts {
		return false, nil
	}
	confirmed, err := ui.ui.Confirm(ctx, title, message, opts)
	if ui.dismissed(ctx, err) {
		return false, nil
	}
	return confirmed, err
}

func (ui *projectTrustCLIUI) Input(ctx context.Context, title, placeholder string, opts extension.ExtensionUIDialogOptions) (string, error) {
	if !ui.prompts {
		return "", nil
	}
	typed, err := ui.ui.Input(ctx, title, placeholder, opts)
	if ui.dismissed(ctx, err) {
		return "", nil
	}
	return typed, err
}

func (ui *projectTrustCLIUI) Notify(message, kind string) {
	if ui.live {
		ui.ui.Notify(message, kind)
		return
	}
	if ui.interactive {
		return
	}
	if ui.color {
		switch kind {
		case "error":
			message = "\x1b[31m" + message + "\x1b[39m"
		case "warning":
			message = "\x1b[33m" + message + "\x1b[39m"
		default:
			message = "\x1b[36m" + message + "\x1b[39m"
		}
	}
	_, _ = fmt.Fprintln(ui.stderr, message)
}

// newProjectTrustContext builds the context a project_trust handler receives (cli/project-trust.ts:7-58). mode is the app mode the
// prompt runs in and hasUI whether it can prompt; ui serves an interactive run. live marks ui as the live UI of an interactive-mode
// replacement (interactive-mode.ts:2609).
func newProjectTrustContext(cwd string, mode appMode, hasUI bool, ui extension.UIContext, live bool) extension.ProjectTrustContext {
	color := chalkColorLevel(environMap(os.Environ()), os.Args[1:], term.IsTerminal(int(os.Stderr.Fd()))) > 0
	return buildProjectTrustContext(cwd, mode, hasUI, ui, live, os.Stderr, color)
}

func buildProjectTrustContext(cwd string, mode appMode, hasUI bool, ui extension.UIContext, live bool, stderr io.Writer, color bool) extension.ProjectTrustContext {
	if ui == nil {
		ui = extension.NoopUIContext
	}
	extensionMode := extension.ExtensionMode(mode)
	if mode == appModeInteractive {
		extensionMode = extension.ModeTUI
	}
	return extension.ProjectTrustContext{
		Cwd:   cwd,
		Mode:  extensionMode,
		HasUI: hasUI,
		UI: &projectTrustCLIUI{
			ui:          ui,
			prompts:     hasUI && mode == appModeInteractive,
			interactive: mode == appModeInteractive,
			live:        live,
			stderr:      stderr,
			color:       color,
		},
	}
}

// projectTrustPromptMode is the mode and UI availability of a project_trust prompt: the startup prompt runs as print for --help and
// --list-models (main.ts:723) and has a UI only when it is interactive (main.ts:770); a replacement the interactive mode drives
// carries its live UI as interactive-mode.ts createProjectTrustContext (mode "tui", hasUI true), which live reports.
func projectTrustPromptMode(mode appMode, helpOrListModels, interactiveStartup, liveUI bool) (promptMode appMode, hasUI, live bool) {
	if helpOrListModels {
		mode = appModePrint
	}
	if liveUI && !interactiveStartup {
		return appModeInteractive, true, true
	}
	return mode, interactiveStartup, false
}
