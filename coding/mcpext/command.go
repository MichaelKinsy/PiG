package mcpext

// Ports the `/mcp` command of packages/coding-agent/src/extensions/mcp/index.ts (registerCommand "mcp", pickServer,
// loginCommand, MCP_USAGE).

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// McpUsage is MCP_USAGE.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:160
const McpUsage = "Usage: /mcp, /mcp login [server], /mcp logout [server], /mcp reconnect [server]"

// CommandContext is the part of ExtensionCommandContext the `/mcp` handler uses.
type CommandContext struct {
	EventContext
	// Mode is the mode the command runs in; the manager view opens only in the terminal UI.
	Mode extension.ExtensionMode
	// HasUI reports whether the mode can ask the user.
	HasUI bool
	// Select shows a selector (ctx.ui.select) and returns the choice; ok is false when the user cancelled.
	Select func(ctx context.Context, title string, options []string) (choice string, ok bool)
	// Input shows a text input (ctx.ui.input) that ends when ctx is done; ok is false when the user cancelled.
	Input func(ctx context.Context, title, placeholder string) (value string, ok bool)
	// ShowManager runs manage in the manager view (showMcpManager) and returns when it does.
	ShowManager func(ctx context.Context, manage func(ui McpUi) error) error
}

// CompleteCommand is the argument completion of `/mcp`; it returns nil where upstream returns null.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:904-924
func (e *Extension) CompleteCommand(prefix string) []extension.AutocompleteItem {
	trimmed := strings.TrimLeftFunc(prefix, isJSWhitespace)
	words := strings.FieldsFunc(trimmed, isJSWhitespace)
	// JavaScript's split(/\s+/) keeps the empty word after trailing whitespace, and yields one empty word for "".
	if last, _ := utf8.DecodeLastRuneInString(trimmed); trimmed == "" || isJSWhitespace(last) {
		words = append(words, "")
	}
	if len(words) > 2 {
		return nil
	}
	action := words[0]
	if len(words) == 1 {
		var items []extension.AutocompleteItem
		for _, item := range []string{"login", "logout", "reconnect"} {
			if strings.HasPrefix(item, action) {
				items = append(items, extension.AutocompleteItem{Value: item + " ", Label: item})
			}
		}
		return items
	}
	if action != "login" && action != "logout" && action != "reconnect" {
		return nil
	}
	name := words[1]
	e.mu.Lock()
	servers := slices.Clone(e.servers)
	e.mu.Unlock()
	var items []extension.AutocompleteItem
	for _, s := range servers {
		eligible := e.usesOAuth(s)
		if action == "reconnect" {
			eligible = e.hasConnection(s)
		}
		if !eligible || !strings.HasPrefix(s.entry.Name, name) {
			continue
		}
		items = append(items, extension.AutocompleteItem{
			Value: action + " " + s.entry.Name, Label: s.entry.Name, Description: describeState(s, true),
		})
	}
	return items
}

// usesOAuth reports whether the server has a connection that signs in with OAuth.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:758
func (e *Extension) usesOAuth(s *server) bool {
	e.mu.Lock()
	connection := s.connection
	e.mu.Unlock()
	return connection != nil && connection.OAuthURL() != ""
}

func (e *Extension) hasConnection(s *server) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.connection != nil
}

func (e *Extension) connectionState(s *server) ServerState {
	e.mu.Lock()
	connection := s.connection
	e.mu.Unlock()
	if connection == nil {
		return ""
	}
	return connection.State()
}

// pick describes which servers a subcommand accepts.
type pick struct {
	eligible  func(*server) bool
	preferred func(*server) bool
	none      string
}

func (e *Extension) oauthPick() pick {
	return pick{
		eligible:  e.usesOAuth,
		preferred: func(s *server) bool { return e.connectionState(s) == StateNeedsAuth },
		none:      "No enabled MCP server uses OAuth. Only HTTP servers without an Authorization header do.",
	}
}

// pickServer resolves the server for a subcommand, asking when the name is omitted and ambiguous.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:729-756
func (e *Extension) pickServer(ctx context.Context, name string, c CommandContext, options pick) *server {
	if name != "" {
		s := e.findServer(name)
		switch {
		case s == nil:
			c.notify(fmt.Sprintf(`No MCP server named "%s".`, name), "error")
		case !options.eligible(s):
			c.notify(options.none, "error")
		default:
			return s
		}
		return nil
	}
	e.mu.Lock()
	servers := slices.Clone(e.servers)
	e.mu.Unlock()
	candidates := slices.DeleteFunc(servers, func(s *server) bool { return !options.eligible(s) })
	if len(candidates) == 0 {
		c.notify(options.none, "info")
		return nil
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	if preferred := slices.DeleteFunc(slices.Clone(candidates), func(s *server) bool { return !options.preferred(s) }); len(preferred) == 1 {
		return preferred[0]
	}
	names := make([]string, len(candidates))
	for i, s := range candidates {
		names[i] = s.entry.Name
	}
	choice, ok := "", false
	if c.Select != nil {
		choice, ok = c.Select(ctx, "MCP server", names)
	}
	if !ok {
		return nil
	}
	for _, s := range candidates {
		if s.entry.Name == choice {
			return s
		}
	}
	return nil
}

// RunCommand is the handler of `/mcp`. It returns what upstream's handler throws: a manager view that could not be
// shown and a sign-out whose credentials could not be removed. The runner reports it as a command error.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:925-977
func (e *Extension) RunCommand(ctx context.Context, args string, c CommandContext) error {
	e.Pending()
	words := strings.FieldsFunc(args, isJSWhitespace)
	if len(words) == 0 {
		if c.Mode == extension.ModeTUI && c.ShowManager != nil {
			return c.ShowManager(ctx, func(ui McpUi) error { return e.Manage(ctx, ui, c.EventContext) })
		}
		c.notify(e.FormatStatus(), "info")
		return nil
	}
	if len(words) > 2 {
		c.notify(McpUsage, "warning")
		return nil
	}
	action, name := words[0], ""
	if len(words) == 2 {
		name = words[1]
	}
	switch action {
	case "login":
		if s := e.pickServer(ctx, name, c, e.oauthPick()); s != nil {
			e.loginCommand(ctx, s, c)
		}
	case "logout":
		s := e.pickServer(ctx, name, c, e.oauthPick())
		if s == nil {
			return nil
		}
		removed, err := e.SignOut(s.entry.Name)
		switch {
		case err != nil:
			return err
		case removed:
			c.notify(fmt.Sprintf(`Signed out of MCP server "%s".`, s.entry.Name), "info")
		default:
			c.notify(fmt.Sprintf(`No stored credentials for MCP server "%s".`, s.entry.Name), "info")
		}
	case "reconnect":
		s := e.pickServer(ctx, name, c, pick{
			eligible: e.hasConnection,
			preferred: func(s *server) bool {
				state := e.connectionState(s)
				return state == StateFailed || state == StateDisconnected
			},
			none: "No enabled MCP server to reconnect.",
		})
		if s == nil {
			return nil
		}
		if failure := e.Reconnect(ctx, s.entry.Name); failure != "" {
			c.notify(failure, "error")
			return nil
		}
		e.EnsureDiscoveryActive(c.EventContext)
		c.notify(fmt.Sprintf(`Reconnected to MCP server "%s" (%s).`, s.entry.Name, describeState(s, true)), "info")
	default:
		c.notify(McpUsage, "warning")
	}
	return nil
}

// loginCommand signs in to a server from the command line, showing the URL as a notice and asking for the redirect URL in an input.
// upstream: packages/coding-agent/src/extensions/mcp/index.ts:765-790
func (e *Extension) loginCommand(ctx context.Context, s *server, c CommandContext) {
	name := s.entry.Name
	if !c.HasUI {
		c.notify(fmt.Sprintf(`Signing in to MCP server "%s" requires interactive mode.`, name), "error")
		return
	}
	failure := e.SignIn(ctx, name, &commandSignIn{e: e, ctx: c, name: name})
	if failure != "" {
		level := "error"
		if failure == "Sign-in cancelled." {
			level = "info"
		}
		c.notify(failure, level)
		return
	}
	e.EnsureDiscoveryActive(c.EventContext)
	e.mu.Lock()
	connection := s.connection
	e.mu.Unlock()
	tools := 0
	if connection != nil {
		tools = len(connection.Tools())
	}
	c.notify(fmt.Sprintf(`Signed in to MCP server "%s" (%d tools).`, name, tools), "info")
}

type commandSignIn struct {
	e    *Extension
	ctx  CommandContext
	name string
}

func (p *commandSignIn) ShowAuthorizationURL(u *url.URL) {
	p.ctx.notify(fmt.Sprintf("Sign in to MCP server \"%s\" in your browser:\n%s", p.name, u.String()), "info")
	p.e.openURL(u.String())
}

func (p *commandSignIn) PromptForRedirectURL(ctx context.Context) (string, error) {
	if p.ctx.Input == nil {
		return "", nil
	}
	value, _ := p.ctx.Input(ctx, fmt.Sprintf(`Waiting for sign-in to "%s". If the browser cannot reach this machine, paste the URL it was redirected to.`, p.name), "http://127.0.0.1:.../callback?code=...")
	return value, nil
}

// openURL opens an authorization URL in the browser the host supplies.
func (e *Extension) openURL(u string) {
	if e.options.OpenURL != nil {
		e.options.OpenURL(u)
	}
}
