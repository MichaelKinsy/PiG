package codingagent

import (
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts setCustomEditorComponent and theme.ts getEditorTheme.

// editorTheme is theme.ts:1145 getEditorTheme: the border in borderMuted and the select list theme.
func editorTheme() tui.EditorTheme {
	return tui.EditorTheme{
		BorderColor: func(text string) string {
			if theme := tui.ActiveTheme(); theme != nil {
				return theme.Fg("borderMuted", text)
			}
			return text
		},
		SelectList: tui.GetSelectListTheme(),
	}
}

// setCustomEditorComponent installs the editor factory builds, or with nil restores the default editor: it builds the component with
// (tui, editorTheme, keybindings) and puts it in the editor's place with the editor's text and callbacks. It runs on the owner loop;
// [ExtUIContext.SetEditorComponent] has recorded the factory already, as Pi's setCustomEditorComponent does first.
func (m *InteractiveMode) setCustomEditorComponent(factory extension.EditorFactory) {
	if factory == nil {
		m.setRemoteEditor(nil)
		return
	}
	component := factory(m.tuiInst, editorTheme(), tui.GetTUIKeybindings())
	if component == nil {
		m.setRemoteEditor(nil)
		return
	}
	if remote, ok := extension.RemoteEditorOf(component); ok {
		m.setRemoteEditor(remote)
		return
	}
	m.setRemoteEditor(&componentEditor{m: m, component: component})
}

// componentEditor presents an editor component of the host process as the editor [setRemoteEditor] installs: the editor forwards keys and text to
// it and shows the frames it renders, and the component's callbacks become the host's. A frame is rendered after each key, text operation,
// configuration and change, at the terminal's width, and again at every render of the editor's row.
type componentEditor struct {
	m         *InteractiveMode
	component tui.EditorComponent
	host      extension.RemoteEditorHost
}

var (
	_ extension.RemoteEditor     = (*componentEditor)(nil)
	_ extension.LiveRemoteEditor = (*componentEditor)(nil)
)

func (c *componentEditor) width() int {
	if c.m.tuiInst != nil {
		if terminal := c.m.tuiInst.Terminal(); terminal != nil {
			return terminal.Columns()
		}
	}
	return 80
}

func (c *componentEditor) frame() {
	if c.host == nil {
		return
	}
	width := c.width()
	c.host.EditorFrame(c.component.Render(width), width, false)
}

// RenderFrame renders the component at the width of the editor's row: a component that changes on its own (it asked the TUI to render) shows the
// change without a key.
func (c *componentEditor) RenderFrame(width int) []string { return c.component.Render(width) }

func (c *componentEditor) Input(data string) {
	c.component.HandleInput(data)
	c.frame()
	if c.host != nil {
		c.host.EditorInputDone()
	}
}

func (c *componentEditor) SetText(text string) {
	c.component.SetText(text)
	c.frame()
}

func (c *componentEditor) InsertTextAtCursor(text string) {
	if inserter, ok := c.component.(tui.EditorWithTextInsertion); ok {
		inserter.InsertTextAtCursor(text)
		c.frame()
	}
}

func (c *componentEditor) AddToHistory(text string) {
	if history, ok := c.component.(tui.EditorWithHistory); ok {
		history.AddToHistory(text)
	}
}

// Mouse is ignored: tui.EditorComponent has no mouse handler.
func (*componentEditor) Mouse(extension.RemoteMouseEvent) {}

func (c *componentEditor) Configure(config extension.RemoteEditorConfig) {
	if appearance, ok := c.component.(tui.EditorWithAppearance); ok {
		appearance.SetPaddingX(config.PaddingX)
		appearance.SetAutocompleteMaxVisible(config.AutocompleteMaxVisible)
	}
	c.frame()
}

func (*componentEditor) EmbedWorkingStatus() bool { return false }

func (c *componentEditor) Bind(host extension.RemoteEditorHost) {
	c.host = host
	c.component.SetOnSubmit(func(text string) { host.EditorSubmit(text, func() {}) })
	c.component.SetOnChange(func(text string) {
		expanded := text
		if source, ok := c.component.(tui.EditorWithExpandedText); ok {
			expanded = source.GetExpandedText()
		}
		host.EditorChanged(text, expanded)
	})
	c.frame()
}

func (c *componentEditor) Close() {
	c.component.SetOnSubmit(nil)
	c.component.SetOnChange(nil)
	c.host = nil
}
