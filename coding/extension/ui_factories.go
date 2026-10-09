package extension

import "github.com/MichaelKinsy/PiG/tui"

// ReadonlyFooterDataProvider is the footer data an extension's footer reads (footer-data-provider.ts:385
// Pick<FooterDataProvider, "getGitBranch" | "getExtensionStatuses" | "getAvailableProviderCount" | "onBranchChange">).
type ReadonlyFooterDataProvider interface {
	// GetGitBranch is the current git branch, empty outside a repository or on a detached HEAD without a branch name.
	GetGitBranch() string
	// GetExtensionStatuses are the statuses extensions set with setStatus, by key.
	GetExtensionStatuses() map[string]string
	// GetAvailableProviderCount is the number of providers with usable credentials.
	GetAvailableProviderCount() int
	// OnBranchChange subscribes to branch changes and returns the unsubscribe function.
	OnBranchChange(callback func()) func()
}

// DisposableComponent is `Component & { dispose?(): void }`: a component whose Dispose runs when the surface that shows it is replaced or
// restored. A component with nothing to release implements Dispose as a no-op.
type DisposableComponent interface {
	tui.Component
	Dispose()
}

// HeaderFactory builds an extension's header component (types.ts ExtensionUIContext.setHeader:
// `(tui: TUI, theme: Theme) => Component & { dispose?(): void }`). A component that has a Dispose method is disposed when the header
// is replaced or restored.
type HeaderFactory func(host tui.TUI, theme *tui.Theme) DisposableComponent

// FooterFactory builds an extension's footer component (types.ts ExtensionUIContext.setFooter:
// `(tui: TUI, theme: Theme, footerData: ReadonlyFooterDataProvider) => Component & { dispose?(): void }`).
type FooterFactory func(host tui.TUI, theme *tui.Theme, footerData ReadonlyFooterDataProvider) DisposableComponent

// Render and Invalidate make a frame a component: it renders the lines the extension rendered, whatever the width it is asked for,
// because the extension process rendered them at Width.
func (w WidthLines) Render(int) []string { return w.Lines }

// Invalidate is a no-op: a frame is static until the extension sends another.
func (WidthLines) Invalidate() {}

// Dispose is a no-op: a frame holds nothing to release.
func (WidthLines) Dispose() {}

// FrameHeader is the header factory of an extension process: the component it builds is the frame the process rendered, at the width it
// rendered it.
func FrameHeader(lines []string, width int) HeaderFactory {
	frame := WidthLines{Lines: append([]string(nil), lines...), Width: width}
	return func(tui.TUI, *tui.Theme) DisposableComponent { return frame }
}

// FrameFooter is the footer factory of an extension process.
func FrameFooter(lines []string, width int) FooterFactory {
	frame := WidthLines{Lines: append([]string(nil), lines...), Width: width}
	return func(tui.TUI, *tui.Theme, ReadonlyFooterDataProvider) DisposableComponent { return frame }
}

// ThemeSelection is `string | Theme`, the argument of setTheme: a theme by name or a theme object.
type ThemeSelection interface {
	themeSelection()
}

// ThemeName selects a theme by name.
type ThemeName string

// ThemeInstance selects a theme object, which has no name to look up and nothing to watch.
type ThemeInstance struct{ Theme *tui.Theme }

func (ThemeName) themeSelection()     {}
func (ThemeInstance) themeSelection() {}

// EditorFactory builds an extension's editor component (types.ts:143
// `(tui: TUI, theme: EditorTheme, keybindings: KeybindingsManager) => EditorComponent`). keybindings is the merged keybinding table
// ([tui.GetTUIKeybindings]), as for [CustomFactory].
type EditorFactory func(host tui.TUI, theme tui.EditorTheme, keybindings KeybindingsManager) tui.EditorComponent

// RemoteEditorFactory is the editor factory of an editor that runs in an extension's process. The component it builds is the editor
// itself seen from the host: the host installs the editor's frames, text and callbacks through [RemoteEditor] and never renders or types
// into the component, so Render, Text and HandleInput act on the process only for a caller that is not the host.
func RemoteEditorFactory(editor RemoteEditor) EditorFactory {
	component := &RemoteEditorComponent{editor: editor}
	return func(tui.TUI, tui.EditorTheme, KeybindingsManager) tui.EditorComponent { return component }
}

// RemoteEditorComponent is the [tui.EditorComponent] [RemoteEditorFactory] builds.
type RemoteEditorComponent struct {
	editor RemoteEditor
	text   string
}

var _ tui.EditorComponent = (*RemoteEditorComponent)(nil)

// Editor is the editor in the extension's process.
func (c *RemoteEditorComponent) Editor() RemoteEditor { return c.editor }

// RemoteEditorOf is the editor in an extension's process that component stands for, when [RemoteEditorFactory] built it.
func RemoteEditorOf(component tui.EditorComponent) (RemoteEditor, bool) {
	remote, ok := component.(*RemoteEditorComponent)
	if !ok {
		return nil, false
	}
	return remote.editor, true
}

// Render is empty: the process draws the editor and the host shows its frames.
func (*RemoteEditorComponent) Render(int) []string { return nil }

// Invalidate is a no-op: the process redraws on its own.
func (*RemoteEditorComponent) Invalidate() {}

// Text is the text the host last set; the editor's typing reaches the host through [RemoteEditorHost.EditorChanged].
func (c *RemoteEditorComponent) Text() string { return c.text }

// SetText replaces the editor's text.
func (c *RemoteEditorComponent) SetText(text string) { c.text = text; c.editor.SetText(text) }

// HandleInput delivers a keystroke to the editor.
func (c *RemoteEditorComponent) HandleInput(data string) { c.editor.Input(data) }

// SetOnSubmit and SetOnChange are no-ops: the host binds its callbacks through [RemoteEditor.Bind].
func (*RemoteEditorComponent) SetOnSubmit(func(string)) {}
func (*RemoteEditorComponent) SetOnChange(func(string)) {}

// WidgetFactory builds an extension's widget component (types.ts:189 setWidget's second overload:
// `(tui: TUI, theme: Theme) => Component & { dispose?(): void }`). Its Dispose runs when the widget is replaced, removed or cleared.
type WidgetFactory func(host tui.TUI, theme *tui.Theme) DisposableComponent
