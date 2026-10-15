package mcptui

// Ports packages/coding-agent/src/extensions/mcp/ui.ts (the manager view).

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ManagerHost is the terminal the manager view asks to draw again.
type ManagerHost interface{ RequestRender(force ...bool) }

// maxVisibleItems is the number of menu items shown before the list scrolls.
// upstream: packages/coding-agent/src/extensions/mcp/ui.ts:MAX_VISIBLE_ITEMS
const maxVisibleItems = 12

// McpManagerView is the `/mcp` manager view: menus that rebuild while servers connect, a read-only status screen, and
// the sign-in screen that accepts a pasted redirect URL. The manager calls Menu, Status and RedirectURL from its own
// goroutine; Render and HandleInput run on the interactive loop.
// upstream: packages/coding-agent/src/extensions/mcp/ui.ts:McpManagerView
type McpManagerView struct {
	host        ManagerHost
	theme       *tui.Theme
	keybindings *tui.TUIKeybindingsManager

	mu           sync.Mutex
	content      *tui.Container
	inputHandler func(data string)
	inputTarget  interface{ SetFocused(bool) }
	focused      bool
	// copyToClipboard copies the sign-in URL on `app.message.copy`.
	copyToClipboard func(text string) error
}

// NewMcpManagerView returns the view, showing "Loading…" until the manager shows its first menu.
func NewMcpManagerView(host ManagerHost, theme *tui.Theme, keybindings *tui.TUIKeybindingsManager) *McpManagerView {
	v := &McpManagerView{host: host, theme: theme, keybindings: keybindings}
	v.content = v.frame("MCP servers", []tui.Component{tui.NewPaddedText(theme.Fg("muted", "Loading…"), 1, 1, nil)}, "")
	return v
}

func bold(text string) string { return "\x1b[1m" + text + "\x1b[22m" }

// frame is a bordered container with a bold title, the body, and an optional footer.
func (v *McpManagerView) frame(title string, body []tui.Component, footer string) *tui.Container {
	container := tui.NewContainer()
	container.Add(tui.NewDynamicBorder(func(text string) string { return v.theme.Fg("accent", text) }))
	container.Add(tui.NewPaddedText(v.theme.Fg("accent", bold(title)), 1, 0, nil))
	for _, child := range body {
		container.Add(child)
	}
	if footer != "" {
		container.Add(tui.NewSpacer(1))
		container.Add(tui.NewPaddedText(v.theme.Fg("dim", footer), 1, 0, nil))
	}
	container.Add(tui.NewDynamicBorder(func(text string) string { return v.theme.Fg("accent", text) }))
	return container
}

// keyHint is keyHint(action, description) of the coding agent: the keys bound to a select action, then the description.
func (v *McpManagerView) keyHint(action tui.TUIKeybinding, description string) string {
	return tui.KeyHint(tui.FormatKeyText(strings.Join(v.keybindings.GetKeys(action), "/"), false), description)
}

func (v *McpManagerView) setContent(content *tui.Container, inputHandler func(string), target interface{ SetFocused(bool) }) {
	v.mu.Lock()
	if v.inputTarget != nil {
		v.inputTarget.SetFocused(false)
	}
	v.content, v.inputHandler, v.inputTarget = content, inputHandler, target
	if target != nil {
		target.SetFocused(v.focused)
	}
	v.mu.Unlock()
	v.host.RequestRender()
}

// SetFocused records whether the view holds focus, and passes it on to the input that is showing.
func (v *McpManagerView) SetFocused(focused bool) {
	v.mu.Lock()
	v.focused = focused
	if v.inputTarget != nil {
		v.inputTarget.SetFocused(focused)
	}
	v.mu.Unlock()
}

// Menu shows a menu, rebuilding it on every change and keeping the selected item.
func (v *McpManagerView) Menu(ctx context.Context, build func() mcpext.McpMenu, subscribe func(listener func()) (unsubscribe func())) (string, bool) {
	type answer struct {
		value string
		ok    bool
	}
	result := make(chan answer, 1)
	var (
		settled     bool
		selected    string
		hasSelected bool
		unsubscribe func()
	)
	finish := func(value string, ok bool) {
		v.mu.Lock()
		if settled {
			v.mu.Unlock()
			return
		}
		settled = true
		stop := unsubscribe
		v.mu.Unlock()
		if stop != nil {
			stop()
		}
		result <- answer{value, ok}
	}
	render := func() {
		menu := build()
		v.mu.Lock()
		wanted := menu.Selected
		if hasSelected {
			wanted = selected
		}
		v.mu.Unlock()
		var body []tui.Component
		if menu.Details != "" {
			body = append(body, tui.NewPaddedText(v.theme.Fg("muted", menu.Details), 1, 0, nil))
		}
		if menu.Error != "" {
			body = append(body, tui.NewPaddedText(v.theme.Fg("error", menu.Error), 1, 0, nil))
		}
		body = append(body, tui.NewSpacer(1))
		if len(menu.Items) == 0 {
			empty := menu.Empty
			if empty == "" && !menu.HasEmpty {
				empty = "Nothing to show."
			}
			body = append(body, tui.NewPaddedText(v.theme.Fg("muted", empty), 1, 0, nil))
			v.setContent(v.frame(menu.Title, body, v.keyHint(tui.KBSelectCancel, menu.CancelLabel)), func(data string) {
				if v.keybindings.Matches(data, tui.KBSelectCancel) {
					finish("", false)
				}
			}, nil)
			return
		}
		footer := v.keyHint(tui.KBSelectConfirm, menu.ConfirmLabel) + " • " + v.keyHint(tui.KBSelectCancel, menu.CancelLabel)
		labels := make([]string, len(menu.Items))
		descriptions := make([]string, len(menu.Items))
		for i, item := range menu.Items {
			labels[i], descriptions[i] = item.Label, item.Description
		}
		list := tui.NewFilterableList("", labels)
		list.EnableSearch = false
		list.Descriptions = descriptions
		list.MaxVisible = min(len(menu.Items), maxVisibleItems)
		if index := slices.IndexFunc(menu.Items, func(item mcpext.McpMenuItem) bool { return item.Value == wanted }); index != -1 {
			list.SetCursor(index)
		}
		v.mu.Lock()
		selected, hasSelected = menu.Items[list.CursorIndex()].Value, true
		v.mu.Unlock()
		body = append(body, list)
		v.setContent(v.frame(menu.Title, body, footer), func(data string) {
			list.HandleInput(data)
			switch {
			case list.Cancelled():
				finish("", false)
			case list.Done():
				finish(menu.Items[list.SelectedIndex()].Value, true)
			default:
				v.mu.Lock()
				selected = menu.Items[list.CursorIndex()].Value
				v.mu.Unlock()
			}
		}, nil)
	}
	render()
	if subscribe != nil {
		stop := subscribe(func() {
			v.mu.Lock()
			done := settled
			v.mu.Unlock()
			if !done {
				render()
			}
		})
		v.mu.Lock()
		unsubscribe = stop
		done := settled
		v.mu.Unlock()
		if done && stop != nil {
			stop()
		}
	}
	select {
	case got := <-result:
		return got.value, got.ok
	case <-ctx.Done():
		finish("", false)
		return "", false
	}
}

// Status shows a message while an operation runs. With onCancel, the cancel key calls it.
func (v *McpManagerView) Status(title, message string, onCancel func()) {
	body := []tui.Component{tui.NewSpacer(1), tui.NewPaddedText(v.theme.Fg("muted", message), 1, 0, nil)}
	if onCancel == nil {
		v.setContent(v.frame(title, body, ""), nil, nil)
		return
	}
	v.setContent(v.frame(title, body, v.keyHint(tui.KBSelectCancel, "cancel")), func(data string) {
		if v.keybindings.Matches(data, tui.KBSelectCancel) {
			onCancel()
		}
	}, nil)
}

// SetCopyToClipboard supplies the clipboard writer `app.message.copy` uses for the authorization URL on the sign-in
// screen. Without one, the key is ignored.
func (v *McpManagerView) SetCopyToClipboard(copyToClipboard func(text string) error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.copyToClipboard = copyToClipboard
}

// RedirectURL shows the authorization URL and waits for a pasted redirect URL.
func (v *McpManagerView) RedirectURL(ctx context.Context, title, authorizationURL string) (string, bool) {
	if ctx.Err() != nil {
		return "", false
	}
	type answer struct {
		value string
		ok    bool
	}
	result := make(chan answer, 1)
	var once sync.Once
	finish := func(value string, ok bool) { once.Do(func() { result <- answer{value, ok} }) }
	input := tui.NewInput(tui.InputOptions{})
	v.mu.Lock()
	copyToClipboard := v.copyToClipboard
	v.mu.Unlock()
	link := tui.NewAuthURL(authorizationURL, copyToClipboard, func() {
		v.Invalidate()
		v.host.RequestRender()
	})
	body := []tui.Component{
		tui.NewSpacer(1),
		tui.NewPaddedText(v.theme.Fg("muted", "Approve access in your browser. If it did not open, visit:"), 1, 0, nil),
		link,
		tui.NewSpacer(1),
		tui.NewPaddedText(v.theme.Fg("muted", "If the browser runs on another machine, paste the URL it was redirected to:"), 1, 0, nil),
		input,
	}
	footer := v.keyHint(tui.KBSelectConfirm, "submit") + " • " + v.keyHint(tui.KBSelectCancel, "cancel")
	v.setContent(v.frame(title, body, footer), func(data string) {
		switch {
		case v.keybindings.Matches(data, tui.KBSelectConfirm):
			if value := strings.TrimFunc(input.GetValue(), isJSWhitespace); value != "" {
				finish(value, true)
			}
		case v.keybindings.Matches(data, tui.KBSelectCancel):
			finish("", false)
		case v.keybindings.Matches(data, "app.message.copy"):
			link.Copy()
		default:
			input.HandleInput(data)
		}
	}, input)
	select {
	case got := <-result:
		return got.value, got.ok
	case <-ctx.Done():
		return "", false
	}
}

// HandleInput passes a key to what the view is showing.
func (v *McpManagerView) HandleInput(data string) {
	v.mu.Lock()
	handler := v.inputHandler
	v.mu.Unlock()
	if handler != nil {
		handler(data)
	}
	v.host.RequestRender()
}

// Render draws the current content, cutting rows to the width.
func (v *McpManagerView) Render(width int) []string {
	v.mu.Lock()
	content := v.content
	v.mu.Unlock()
	lines := content.Render(width)
	out := make([]string, len(lines))
	for i, line := range lines {
		if widthx.VisibleWidth(line) > width {
			line = widthx.TruncateToWidth(line, width, "", false)
		}
		out[i] = line
	}
	return out
}

// Invalidate clears what the content cached.
func (v *McpManagerView) Invalidate() {
	v.mu.Lock()
	content := v.content
	v.mu.Unlock()
	content.Invalidate()
}

// ShowManager runs manage in the manager view until it returns (showMcpManager).
// upstream: packages/coding-agent/src/extensions/mcp/ui.ts:showMcpManager
func ShowManager(ctx context.Context, ui extension.UIContext, events mcpext.EventContext, copyToClipboard func(text string) error, manage func(mcpext.McpUi) error) error {
	var running sync.WaitGroup
	defer running.Wait()
	_, err := ui.Custom(ctx, extension.CustomFactory(func(host extension.TUI, theme *tui.Theme, keybindings extension.KeybindingsManager, done func(any)) (extension.DisposableComponent, error) {
		view := NewMcpManagerView(host, themeOf(theme), keybindingsOf(keybindings))
		view.SetCopyToClipboard(copyToClipboard)
		running.Go(func() {
			if err := manage(view); err != nil {
				if events.Notify != nil {
					events.Notify(err.Error(), "error")
				}
			}
			done(nil)
		})
		return view, nil
	}), nil)
	return err
}

func keybindingsOf(keybindings extension.KeybindingsManager) *tui.TUIKeybindingsManager {
	if keybindings != nil {
		return keybindings
	}
	return tui.GetTUIKeybindings()
}

// Dispose releases nothing: the view's work runs in the manage goroutine that Custom waits for.
func (*McpManagerView) Dispose() {}
