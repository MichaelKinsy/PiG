package mcpext

// Ports packages/coding-agent/src/extensions/mcp/ui.ts (the manager view) and the manager of index.ts.

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// McpMenu is one menu of the manager view.
// upstream: packages/coding-agent/src/extensions/mcp/ui.ts:McpMenu
type McpMenu struct {
	Title string
	// Details is shown below the title.
	Details string
	// Error is shown below the details in the error color.
	Error string
	Items []tui.SelectItem
	// Empty is shown when there are no items.
	Empty string
	// Selected is the value of the item selected when the menu opens.
	Selected string
	// ConfirmLabel and CancelLabel are what the confirm and cancel keys do, for the key hint.
	ConfirmLabel string
	CancelLabel  string
}

// McpUi is what the manager needs from its view.
// upstream: packages/coding-agent/src/extensions/mcp/ui.ts:McpUi
type McpUi interface {
	// Menu shows a menu and returns the chosen item's value; ok is false when the user cancelled or ctx ended.
	// subscribe rebuilds the menu on every change, keeping the selected item.
	Menu(ctx context.Context, build func() McpMenu, subscribe func(listener func()) (unsubscribe func())) (value string, ok bool)
	// Status shows a message while an operation runs. With onCancel, the cancel key calls it.
	Status(title, message string, onCancel func())
	// RedirectURL shows the authorization URL and waits for a pasted redirect URL; ok is false when the user
	// cancelled or ctx ended (the browser reached the callback).
	RedirectURL(ctx context.Context, title, authorizationURL string) (value string, ok bool)
}

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
func (v *McpManagerView) Menu(ctx context.Context, build func() McpMenu, subscribe func(listener func()) (unsubscribe func())) (string, bool) {
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
			if empty == "" {
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
		if index := slices.IndexFunc(menu.Items, func(item tui.SelectItem) bool { return item.Value == wanted }); index != -1 {
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

// exposureDescriptions are the descriptions of the exposures a server can be set to, in menu order.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:91-96 (EXPOSURE_DESCRIPTIONS)
var exposureDescriptions = []struct {
	exposure    extension.McpExposure
	description string
}{
	{extension.McpExposureCodemode, "called from codemode scripts, which find them with searchTools()"},
	{extension.McpExposureDeferred, "not declared until tool_search loads them, then called directly; no codemode needed"},
	{extension.McpExposureDirect, "declared to the model like built-in tools"},
}

func exposureDescription(exposure extension.McpExposure) string {
	for _, d := range exposureDescriptions {
		if d.exposure == exposure {
			return d.description
		}
	}
	return ""
}

// attentionRank orders the servers that need the user first.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:138-152
func (e *Extension) attentionRank(s *server) int {
	if !s.enabled() {
		return 5
	}
	switch e.connectionState(s) {
	case StateNeedsAuth:
		return 0
	case StateFailed:
		return 1
	case StateDisconnected:
		return 2
	case StateConnected:
		return 4
	default:
		return 3
	}
}

// describeTransport is the command line or URL of a server.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:154-158
func describeTransport(entry McpServerEntry) string {
	if entry.Config.IsHTTP() {
		return entry.Config.URL
	}
	return strings.Join(append([]string{entry.Config.Command}, entry.Config.Args...), " ")
}

// notices are the config errors and overridden registrations.
func (e *Extension) notices() string { return strings.Join(e.Notices(), "\n") }

// serversMenu lists every server.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:526-544
func (e *Extension) serversMenu() McpMenu {
	e.mu.Lock()
	servers := slices.Clone(e.servers)
	e.mu.Unlock()
	slices.SortStableFunc(servers, func(a, b *server) int {
		if rank := e.attentionRank(a) - e.attentionRank(b); rank != 0 {
			return rank
		}
		return localeCompare(a.entry.Name, b.entry.Name)
	})
	items := make([]tui.SelectItem, len(servers))
	for i, s := range servers {
		scope := s.entry.Scope
		if scope == "" {
			scope = s.entry.Source
		}
		if s.entry.Override != "" {
			scope = "global, project override"
		}
		items[i] = tui.SelectItem{
			Value: s.entry.Name, Label: s.entry.Name,
			Description: fmt.Sprintf("%s · %s · %s", describeState(s, true), exposureOf(s.entry), scope),
		}
	}
	return McpMenu{
		Title: "MCP servers", Error: e.notices(), Items: items,
		Empty:        e.noServersMessage(),
		ConfirmLabel: "manage", CancelLabel: "close",
	}
}

// serverMenu lists what can be done with one server.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:546-597
func (e *Extension) serverMenu(name string) McpMenu {
	s := e.findServer(name)
	if s == nil {
		return McpMenu{Title: name, Empty: "This server is no longer configured.", CancelLabel: "back"}
	}
	e.mu.Lock()
	entry, connection, message, projectConfig := s.entry, s.connection.Load(), s.message, e.projectConfig
	e.mu.Unlock()
	saved := "saved to mcp.json"
	switch {
	case entry.Scope == "extension":
		saved = "for this session"
	case entry.Override != "":
		saved = "saved to the project mcp.json"
	case entry.Scope != "":
		saved = "saved to the " + entry.Scope + " mcp.json"
	}
	// Global servers without an override can be turned on or off for the trusted project alone.
	inProject := entry.Scope == "global" && entry.Override == "" && projectConfig != ""
	const inProjectSaved = "saved to the project mcp.json"
	var items []tui.SelectItem
	var state ServerState
	if connection != nil {
		state = connection.State()
	}
	if !s.enabled() {
		items = append(items, tui.SelectItem{Value: "enable", Label: "Enable", Description: saved})
		if inProject {
			items = append(items, tui.SelectItem{Value: "enable-project", Label: "Enable in this project", Description: inProjectSaved})
		}
	} else {
		if state == StateNeedsAuth {
			items = append(items, tui.SelectItem{Value: "signin", Label: "Sign in", Description: "opens the browser"})
		}
		if state == StateConnected {
			items = append(items, tui.SelectItem{Value: "tools", Label: "Tools", Description: fmt.Sprintf("%d offered", len(connection.Tools()))})
		}
		if state == StateFailed || state == StateDisconnected || state == StateConnected || state == StateNeedsAuth {
			items = append(items, tui.SelectItem{Value: "reconnect", Label: "Reconnect"})
		}
		if state == StateConnected && connection.OAuthURL() != "" {
			items = append(items, tui.SelectItem{Value: "signout", Label: "Sign out", Description: "deletes the stored credentials"})
		}
		items = append(items,
			tui.SelectItem{Value: "exposure", Label: "Exposure", Description: string(exposureOf(entry))},
			tui.SelectItem{Value: "disable", Label: "Disable", Description: saved})
		if inProject {
			items = append(items, tui.SelectItem{Value: "disable-project", Label: "Disable in this project", Description: inProjectSaved})
		}
	}
	scope := entry.Scope
	if scope == "" {
		scope = "config"
	}
	details := []string{describeTransport(entry), scope + ": " + entry.Source}
	if entry.Override != "" {
		details = append(details, "project override: "+entry.Override)
	}
	details = append(details, "State: "+describeState(s, false))
	var errorLines []string
	if message != "" {
		errorLines = append(errorLines, message)
	}
	if connection != nil && state != StateConnected && connection.Error() != "" {
		errorLines = append(errorLines, connection.Error())
	}
	menu := McpMenu{
		Title: "MCP server " + name, Details: strings.Join(details, "\n"), Error: strings.Join(errorLines, "\n"),
		Items: items, ConfirmLabel: "select", CancelLabel: "back",
	}
	if len(items) > 0 {
		menu.Selected = items[0].Value
	}
	return menu
}

// showTools lists the tools a server offers.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:599-618
func (e *Extension) showTools(ctx context.Context, ui McpUi, s *server) {
	exposure := exposureOf(s.entry)
	overridden := s.entry.Config.ToolExposure != nil && len(s.entry.Config.ToolExposure.Keys()) > 0
	ui.Menu(ctx, func() McpMenu {
		details := "Exposure " + string(exposure) + ": unreachable"
		if exposure != extension.McpExposureHidden {
			details = "Exposure " + string(exposure) + ": " + exposureDescription(exposure)
		}
		if overridden {
			details += "\nSome tools override it with toolExposure."
		}
		var items []tui.SelectItem
		e.mu.Lock()
		connection := s.connection.Load()
		e.mu.Unlock()
		if connection != nil {
			for _, tool := range connection.Tools() {
				toolExposure := extension.GetMcpToolExposure(s.entry.Config, tool.Name)
				description := firstLine(tool.Description)
				if toolExposure != exposure {
					description = "[" + string(toolExposure) + "] " + description
				}
				items = append(items, tui.SelectItem{Value: tool.Name, Label: tool.Name, Description: description})
			}
		}
		return McpMenu{Title: "Tools of " + s.entry.Name, Details: details, Items: items, Empty: "The server offers no tools.", ConfirmLabel: "back", CancelLabel: "back"}
	}, nil)
}

// chooseExposure asks for the exposure of a server and saves a different choice. It returns a message for a failure.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:620-641
func (e *Extension) chooseExposure(ctx context.Context, ui McpUi, s *server) string {
	current := exposureOf(s.entry)
	choice, ok := ui.Menu(ctx, func() McpMenu {
		target := s.entry.Source
		if s.entry.Override != "" {
			target = s.entry.Override
		}
		details := "Saved to " + target + "."
		if s.entry.Scope == "extension" {
			details = "Applies to this session; the server is registered by " + s.entry.Source + "."
		}
		items := make([]tui.SelectItem, len(exposureDescriptions))
		for i, d := range exposureDescriptions {
			mark := "  "
			if d.exposure == current {
				mark = "✓ "
			}
			items[i] = tui.SelectItem{Value: string(d.exposure), Label: mark + string(d.exposure), Description: d.description}
		}
		return McpMenu{Title: "Exposure of " + s.entry.Name, Details: details, Items: items, Selected: string(current), ConfirmLabel: "save", CancelLabel: "back"}
	}, nil)
	if !ok || choice == string(current) {
		return ""
	}
	return e.SetExposure(s.entry.Name, extension.McpExposure(choice))
}

// runInBackground keeps the subscribed menu usable while a connection opens or closes. begin runs at once; the
// function it returns runs on a goroutine the extension owns and drains in [Extension.SessionShutdown]. Its message
// shows in the manager unless the session ended or the server's attempt was replaced meanwhile.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:runInBackground
func (e *Extension) runInBackground(ec EventContext, s *server, begin func() func() string) {
	wait := begin()
	e.mu.Lock()
	attempt, session := s.attempt, e.session
	e.mu.Unlock()
	e.background.Go(func() {
		message := wait()
		e.mu.Lock()
		stale := session.Err() != nil || s.attempt != attempt || !slices.Contains(e.servers, s)
		if !stale {
			s.message = message
		}
		e.mu.Unlock()
		if stale {
			return
		}
		e.ensureDiscoveryActive(ec)
		e.emitChange()
	})
}

// runAction runs one action of the server menu. It returns what upstream's runAction throws: a sign-out whose
// credentials could not be removed, which ends the manager.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:runAction
func (e *Extension) runAction(ctx context.Context, ui McpUi, ec EventContext, s *server, action string) error {
	name := s.entry.Name
	e.mu.Lock()
	session := e.session
	e.mu.Unlock()
	var message string
	switch action {
	case "signin":
		message = e.signInWithUI(ctx, ui, name)
	case "reconnect":
		// Connection state and error already report failures, including required sign-ins.
		e.runInBackground(ec, s, func() func() string {
			wait := e.beginReconnect(name)
			return func() string {
				wait(session)
				return ""
			}
		})
	case "signout":
		if _, err := e.SignOut(name); err != nil {
			return err
		}
	case "tools":
		e.showTools(ctx, ui, s)
	case "exposure":
		message = e.chooseExposure(ctx, ui, s)
	case "enable", "disable", "enable-project", "disable-project":
		enable := strings.HasPrefix(action, "enable")
		e.runInBackground(ec, s, func() func() string {
			return e.beginSetEnabled(ec, name, enable, strings.HasSuffix(action, "-project"))
		})
	}
	// The session ended meanwhile (for example during a sign-in), which made the event context stale.
	if session.Err() != nil {
		return nil
	}
	e.SetMessage(name, message)
	e.EnsureDiscoveryActive(ec)
	e.emitChange()
	return nil
}

// signInWithUI signs in with the manager view's sign-in screen, which shows the URL with a copy key and cancels with
// the cancel key at every step.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:signInWithUi
func (e *Extension) signInWithUI(ctx context.Context, ui McpUi, name string) string {
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	prompt := &managerSignIn{ui: ui, title: "Sign in to " + name, cancel: cancel, e: e}
	prompt.status("Contacting the authorization server…")
	return e.SignIn(cancelCtx, name, prompt)
}

// managerSignIn is the sign-in prompt of the manager: the authorization URL goes to the view, and a pasted redirect URL comes from it.
type managerSignIn struct {
	e      *Extension
	ui     McpUi
	title  string
	cancel context.CancelFunc
	url    string
}

func (p *managerSignIn) status(message string) { p.ui.Status(p.title, message, p.cancel) }

func (p *managerSignIn) ShowAuthorizationURL(u *url.URL) {
	p.url = u.String()
	p.e.openURL(p.url)
}

func (p *managerSignIn) PromptForRedirectURL(ctx context.Context) (string, error) {
	value, _ := p.ui.RedirectURL(ctx, p.title, p.url)
	p.status("Connecting…")
	return value, nil
}

// Manage runs the manager in ui until the user closes it.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:687-701 (manage)
func (e *Extension) Manage(ctx context.Context, ui McpUi, ec EventContext) error {
	for {
		name, ok := ui.Menu(ctx, e.serversMenu, e.Subscribe)
		if !ok {
			return nil
		}
		for {
			action, ok := ui.Menu(ctx, func() McpMenu { return e.serverMenu(name) }, e.Subscribe)
			s := e.findServer(name)
			if !ok || s == nil {
				break
			}
			if err := e.runAction(ctx, ui, ec, s, action); err != nil {
				return err
			}
		}
	}
}

// Dispose releases nothing: the view's work runs in the manage goroutine that Custom waits for.
func (*McpManagerView) Dispose() {}
