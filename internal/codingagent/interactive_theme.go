package codingagent

// Ports packages/coding-agent/src/modes/interactive/theme/theme-controller.ts
// Ports packages/tui/src/tui.ts

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// theme binds the shared theme-controller state machine to this mode's settings, renderer and output.
func (m *InteractiveMode) theme() presentationTheme {
	return presentationTheme{
		state:      &m.themeState,
		getSetting: m.settingsThemeSelection,
		persist: func(name string) {
			if sm := m.opts.SettingsManager; sm != nil {
				// upstream: packages/coding-agent/src/core/settings-manager.ts:enqueueWrite
				_ = sm.SetTheme(name)
			}
			m.opts.Settings.Theme = name
		},
		output:    m.themeOutput,
		renderer:  func() tui.Renderer { return m.tuiInst },
		showError: m.showError,
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

// getThemeSelection preserves an initial or explicit selection; otherwise it reads the current manager. nil means no selection; an empty name is a selection.
func (m *InteractiveMode) getThemeSelection() *string { return m.theme().getThemeSelection() }

func (m *InteractiveMode) themeOutput() io.Writer {
	if m.themeState.output != nil {
		return m.themeState.output
	}
	if m.rendererOut != nil {
		return m.rendererOut
	}
	return os.Stdout
}

func (m *InteractiveMode) setAutoSync(enabled bool) { m.theme().setAutoSync(enabled) }

func (m *InteractiveMode) writeThemeNotifications(enabled bool) {
	m.theme().writeThemeNotifications(enabled)
}

func (m *InteractiveMode) previewTheme(setting string) { m.theme().previewTheme(setting) }

func (m *InteractiveMode) beginThemeDetection(output io.Writer) *interactiveThemeQuery {
	return m.theme().beginThemeDetection(output)
}

func (m *InteractiveMode) finishThemeDetection(q *interactiveThemeQuery) {
	m.theme().finishThemeDetection(q)
}

// consumeTerminalThemeInput precedes extension listeners, viewport input, and focused components.
func (m *InteractiveMode) consumeTerminalThemeInput(data string) bool {
	return m.theme().consumeInput(data)
}

// initializeTerminalTheme awaits initial appearance before session_start, using the same decoder as the main loop.
func (m *InteractiveMode) initializeTerminalTheme(ctx context.Context, output io.Writer) error {
	q := m.beginThemeDetection(output)
	if q == nil {
		return nil
	}
	timer := time.NewTimer(startupThemeQueryTimeout)
	defer timer.Stop()
	for {
		select {
		case <-q.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case err := <-m.inputErrCh:
			return err
		case <-timer.C:
			q.detection.timeout()
			m.finishThemeDetection(q)
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

// applyThemeFromSettings leaves the input loop free while the terminal answers.
// The Run lifetime owns and joins the deadline worker; all theme mutation runs on the owner loop.
func (m *InteractiveMode) applyThemeFromSettings(ctx context.Context) {
	q := m.beginThemeDetection(m.themeOutput())
	if q == nil {
		return
	}
	if m.backgroundCtx != nil {
		ctx = m.backgroundCtx
	}
	m.backgroundTasks.Go(func() {
		timer := time.NewTimer(startupThemeQueryTimeout)
		defer timer.Stop()
		select {
		case <-q.done:
		case <-ctx.Done():
		case <-timer.C:
			m.runOnMain(ctx, func() {
				if m.tuiTornDown {
					return
				}
				q.detection.timeout()
				m.finishThemeDetection(q)
			})
		}
	})
}

func (m *InteractiveMode) disposeTheme() { m.theme().dispose() }
