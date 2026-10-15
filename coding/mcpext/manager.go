package mcpext

// Ports the manager of packages/coding-agent/src/extensions/mcp/index.ts; the terminal view of ui.ts is package mcptui.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// McpMenuItem is one row of a [McpMenu]; it has the fields of tui.SelectItem so a terminal view converts it directly.
type McpMenuItem struct {
	Value       string
	Label       string
	Description string
}

// McpMenu is one menu of the manager view.
// upstream: packages/coding-agent/src/extensions/mcp/ui.ts:McpMenu
type McpMenu struct {
	Title string
	// Details is shown below the title.
	Details string
	// Error is shown below the details in the error color.
	Error string
	Items []McpMenuItem
	// Empty is shown when there are no items; a non-empty Empty or HasEmpty replaces the default "Nothing to show.", so an empty message can be shown.
	Empty    string
	HasEmpty bool
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
		return jsstring.LocaleCompare(a.entry.Name, b.entry.Name)
	})
	items := make([]McpMenuItem, len(servers))
	for i, s := range servers {
		scope := s.entry.Scope
		if scope == "" {
			scope = s.entry.Source
		}
		if s.entry.Override != "" {
			scope = "global, project override"
		}
		items[i] = McpMenuItem{
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
	var items []McpMenuItem
	var state ServerState
	if connection != nil {
		state = connection.State()
	}
	if !s.enabled() {
		items = append(items, McpMenuItem{Value: "enable", Label: "Enable", Description: saved})
		if inProject {
			items = append(items, McpMenuItem{Value: "enable-project", Label: "Enable in this project", Description: inProjectSaved})
		}
	} else {
		if state == StateNeedsAuth {
			items = append(items, McpMenuItem{Value: "signin", Label: "Sign in", Description: "opens the browser"})
		}
		if state == StateConnected {
			items = append(items, McpMenuItem{Value: "tools", Label: "Tools", Description: fmt.Sprintf("%d offered", len(connection.Tools()))})
		}
		if state == StateFailed || state == StateDisconnected || state == StateConnected || state == StateNeedsAuth {
			items = append(items, McpMenuItem{Value: "reconnect", Label: "Reconnect"})
		}
		if state == StateConnected && connection.OAuthURL() != "" {
			items = append(items, McpMenuItem{Value: "signout", Label: "Sign out", Description: "deletes the stored credentials"})
		}
		items = append(items,
			McpMenuItem{Value: "exposure", Label: "Exposure", Description: string(exposureOf(entry))},
			McpMenuItem{Value: "disable", Label: "Disable", Description: saved})
		if inProject {
			items = append(items, McpMenuItem{Value: "disable-project", Label: "Disable in this project", Description: inProjectSaved})
		}
	}
	scope := entry.Scope
	if scope == "" {
		scope = "config"
	}
	details := []string{describeTransport(entry)}
	// pig divergence (D156): the server connects to the expansion of its url, so the menu shows it under the url as written.
	if entry.Config.IsHTTP() {
		if expanded := extension.ExpandMcpURL(entry.Config.URL); expanded != entry.Config.URL {
			details = append(details, "connects to: "+expanded)
		}
	}
	details = append(details, scope+": "+entry.Source)
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
		var items []McpMenuItem
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
				items = append(items, McpMenuItem{Value: tool.Name, Label: tool.Name, Description: description})
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
		items := make([]McpMenuItem, len(exposureDescriptions))
		for i, d := range exposureDescriptions {
			mark := "  "
			if d.exposure == current {
				mark = "✓ "
			}
			items[i] = McpMenuItem{Value: string(d.exposure), Label: mark + string(d.exposure), Description: d.description}
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

func (p *managerSignIn) ShowAuthorizationURL(u oauth.URL) {
	p.url = u.Href()
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
