package codingagent

import (
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts setExtensionWidget and clearExtensionWidgets.

// maxWidgetLines is interactive-mode.ts MAX_WIDGET_LINES: a widget of text lines shows this many.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:MAX_WIDGET_LINES
const maxWidgetLines = 10

// inprocessWidget is a widget a host-process extension set, in the order it was set.
type inprocessWidget struct {
	key       string
	component tui.Component
	placement extension.WidgetPlacement
}

// SetWidget sets a widget of text lines above or below the editor; nil lines remove it (types.ts:188 setWidget).
func (u *ExtUIContext) SetWidget(key string, content []string, opts *extension.ExtensionWidgetOptions) {
	u.m.runOnMain(u.m.backgroundCtx, func() {
		var component tui.Component
		if content != nil {
			component = widgetLines(content)
		}
		u.m.setExtensionWidget(key, component, opts)
	})
}

// SetWidgetFactory sets the widget factory builds above or below the editor; a nil factory removes it (types.ts:189 setWidget's second overload).
func (u *ExtUIContext) SetWidgetFactory(key string, factory extension.WidgetFactory, opts *extension.ExtensionWidgetOptions) {
	var build func() tui.Component
	if factory != nil {
		build = func() tui.Component { return factory(u.m.tuiInst, tui.ActiveTheme()) }
	}
	u.m.runOnMain(u.m.backgroundCtx, func() {
		var component tui.Component
		if build != nil {
			component = build()
		}
		u.m.setExtensionWidget(key, component, opts)
	})
}

// widgetLines wraps lines in a container of one-row texts, as setExtensionWidget does for a string array: the first maxWidgetLines lines, then a
// muted truncation notice.
func widgetLines(lines []string) tui.Component {
	container := tui.NewContainer()
	for _, line := range lines[:min(len(lines), maxWidgetLines)] {
		container.Add(tui.NewPaddedText(line, 1, 0, nil))
	}
	if len(lines) > maxWidgetLines {
		container.Add(tui.NewThemedText(func() string { return tui.ActiveTheme().Fg("muted", "... (widget truncated)") }, 1, 0))
	}
	return container
}

// setExtensionWidget replaces the host-process widget key with component (nil removes it), disposing the one it replaces, and lays the widgets out.
func (m *InteractiveMode) setExtensionWidget(key string, component tui.Component, opts *extension.ExtensionWidgetOptions) {
	placement := extension.WidgetPlacementAboveEditor
	if opts != nil && opts.Placement != "" {
		placement = opts.Placement
	}
	m.widgetMu.Lock()
	defer m.widgetMu.Unlock()
	for i, widget := range m.inprocessWidgets {
		if widget.key == key {
			disposeWidget(widget.component)
			m.inprocessWidgets = append(m.inprocessWidgets[:i], m.inprocessWidgets[i+1:]...)
			break
		}
	}
	if component != nil {
		m.inprocessWidgets = append(m.inprocessWidgets, inprocessWidget{key: key, component: component, placement: placement})
	}
	m.renderWidgetsLocked()
}

// clearExtensionWidgets disposes and removes every host-process widget (interactive-mode.ts clearExtensionWidgets).
func (m *InteractiveMode) clearExtensionWidgets() {
	m.widgetMu.Lock()
	defer m.widgetMu.Unlock()
	for _, widget := range m.inprocessWidgets {
		disposeWidget(widget.component)
	}
	m.inprocessWidgets = nil
	m.renderWidgetsLocked()
}

func disposeWidget(component tui.Component) {
	if disposable, ok := component.(interface{ Dispose() }); ok {
		disposable.Dispose()
	}
}
