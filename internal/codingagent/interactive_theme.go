package codingagent

// Ports packages/coding-agent/src/modes/interactive/theme/theme-controller.ts
// Ports packages/tui/src/tui.ts

import (
	"context"
	"io"
	"os"

	"github.com/MichaelKinsy/PiG/tui"
)

// theme binds the shared theme-controller state machine to this mode's settings, renderer and output.
func (m *InteractiveMode) theme() presentationTheme {
	return presentationTheme{
		state:      &m.themeState,
		getSetting: m.settingsThemeSelection,
		output:     m.themeOutput,
		renderer:   func() tui.TUI { return m.tuiInst },
		showError:  m.showError,
		post:       m.postToMain,
		spawn:      func(task func()) { m.backgroundTasks.Go(task) },
		ctx:        func() context.Context { return m.backgroundCtx },
	}
}

// settingsThemeSelection reads the current manager, including overrides and manager replacement.
// upstream: packages/coding-agent/src/modes/interactive/theme/theme-controller.ts:applyFromSettings
func (m *InteractiveMode) settingsThemeSelection() *string {
	if manager := m.opts.SettingsManager; manager != nil {
		return manager.GetThemeSetting()
	}
	return m.opts.Settings.themeSetting()
}

// getThemeSelection preserves an initial or explicit selection; otherwise it reads the current manager, then the active theme. nil means no selection; an empty name is a selection.
func (m *InteractiveMode) getThemeSelection() *string { return m.theme().getThemeSelection() }

func (m *InteractiveMode) themeOutput() io.Writer {
	if m.themeState.output != nil {
		return m.themeState.output
	}
	// pig additive (D91): while a frontend session draws, PiG writes to the
	// terminal in turn with it.
	if m.surface != nil {
		return m.surface.TerminalOut()
	}
	if m.rendererOut != nil {
		return m.rendererOut
	}
	return os.Stdout
}

func (m *InteractiveMode) writeThemeNotifications(enabled bool) {
	m.theme().writeThemeNotifications(enabled)
}

func (m *InteractiveMode) previewTheme(setting string) { m.theme().previewTheme(setting) }

// consumeTerminalThemeInput precedes extension listeners, viewport input, and focused components.
func (m *InteractiveMode) consumeTerminalThemeInput(data string) bool {
	return m.theme().consumeInput(data)
}

// applyThemeFromSettings applies the theme setting now and starts the terminal color query without waiting for it (theme-controller.ts applyFromSettings). Its replies apply on the owner loop.
func (m *InteractiveMode) applyThemeFromSettings(context.Context) { m.theme().applyFromSettings() }

func (m *InteractiveMode) disposeTheme() { m.theme().dispose() }

// initTheme applies the initial theme selection, as the theme controller's constructor does.
func (m *InteractiveMode) initTheme() { m.theme().initTheme() }

// initStartupTheme registers the resource themes and then applies the initial theme, as the InteractiveMode constructor calls setRegisteredThemes before constructing the theme controller (interactive-mode.ts:631-637). The system theme renders in grayscale until the terminal reports its colors.
func (m *InteractiveMode) initStartupTheme() {
	m.loadThemes()
	m.initTheme()
}

// waitForTerminalColors serves the owner loop until the latest terminal color query completed and its colors applied. Content that bakes theme colors into strings, such as the startup header, is built after this. Terminals answer the DA1 request right after the color replies, so this only takes the full timeout when a terminal answers nothing.
func (m *InteractiveMode) waitForTerminalColors(ctx context.Context) error {
	done := m.themeState.colorQuery
	if done == nil {
		return nil
	}
	for {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case err := <-m.inputErrCh:
			return err
		case input, ok := <-m.inputReadCh:
			if !ok {
				return io.EOF
			}
			// Pi installs application and submit handlers only after startup setup (interactive-mode.ts:954-1028). Early input can edit text but cannot dispatch a command before session_start.
			m.handleStartupInput(input, false)
		case fn := <-m.uiTaskCh:
			fn()
		}
	}
}
