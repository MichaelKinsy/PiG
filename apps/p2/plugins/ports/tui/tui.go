// Package tui is the ports tool's card in the TUI, after the web card (src/web/Chat.tsx `ports`): action, port, name and public flag, the tool's text, and the
// forward's addresses as text once opened. Opening them in a browser is a launcher capability (p2-tui-port.md section 3.5); OSC 8 hyperlinks stay stripped.
// `/ports` (P11f) is the web's ports panel.
//
// Ports apps/p2/plugins/ports/tui.ts.
package tui

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/ports/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/google/uuid"
)

// forwardPattern: only Radius forward addresses (the web card's pattern): never arbitrary server-reported URLs.
var forwardPattern = regexp.MustCompile(`^https?://(?:fwd|pub)--[0-9a-f]{32}--[a-z0-9]+\.[^/\s]+/$`)

func forwardURL(value any) string {
	if text, ok := value.(string); ok && forwardPattern.MatchString(text) {
		return text
	}
	return ""
}

// Renderers is the ports tool's card.
var Renderers = &tools.ToolRenderers{
	RenderCall: func(args tools.ToolArgs, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		text, ok := context.LastComponent.(*pitui.Text)
		if !ok {
			text = pitui.NewPaddedText("", 0, 0, nil)
		}
		action, _ := tools.Str(args.Lookup("action"))
		name, _ := tools.Str(args.Lookup("name"))
		port := ""
		if n, ok := tools.Num(args.Lookup("port")); ok {
			port = jsvalue.String(n)
		}
		public := ""
		if args.Lookup("public") == true {
			public = "public"
		}
		var parts []string
		for _, part := range []string{action, port, name, public} {
			if part != "" {
				parts = append(parts, part)
			}
		}
		text.SetText(t.Fg("toolTitle", t.Bold("ports")) + " " + t.Fg("accent", render.LineText(strings.Join(parts, " · "))))
		return text
	},
	RenderResult: func(result tools.ToolResultView, _ tools.ToolRenderResultOptions, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		text, ok := context.LastComponent.(*pitui.Text)
		if !ok {
			text = pitui.NewPaddedText("", 0, 0, nil)
		}
		output := strings.TrimSpace(tools.GetTextOutput(&result))
		details, _ := result.Details.(*jsvalue.Object)
		forward, _ := details.Lookup("forward").(*jsvalue.Object)
		url, publicURL := forwardURL(forward.Lookup("url")), forwardURL(forward.Lookup("publicUrl"))
		var lines []string
		if output != "" {
			color := "toolOutput"
			if context.IsError {
				color = "error"
			}
			lines = append(lines, t.Fg(color, output))
		}
		if !context.IsError && url != "" {
			lines = append(lines, t.Fg("muted", "Open: ")+t.Fg("mdLink", url))
		}
		if !context.IsError && publicURL != "" {
			lines = append(lines, t.Fg("muted", "Public: ")+t.Fg("mdLink", publicURL))
		}
		if len(lines) == 0 {
			text.SetText("")
		} else {
			text.SetText("\n" + strings.Join(lines, "\n"))
		}
		return text
	},
}

type forward struct {
	ID, Name, Session, SessionTitle, URL, PublicURL string
	Port                                            int
	Public                                          bool
}

func forwardsOf(value any) []forward {
	var forwards []forward
	for _, each := range p2tui.JList(value) {
		forwards = append(forwards, forward{
			ID: p2tui.JStr(p2tui.JField(each, "id")), Port: int(p2tui.JNum(p2tui.JField(each, "port"))), Name: p2tui.JStr(p2tui.JField(each, "name")),
			Public: p2tui.JBool(p2tui.JField(each, "public")), Session: p2tui.JStr(p2tui.JField(each, "session")),
			SessionTitle: p2tui.JStr(p2tui.JField(each, "sessionTitle")), URL: p2tui.JStr(p2tui.JField(each, "url")), PublicURL: p2tui.JStr(p2tui.JField(each, "publicUrl")),
		})
	}
	return forwards
}

// guess is a change of the list while its call is in flight; an added forward's id and addresses are the server's.
type guess struct {
	kind   string // "add", "remove" or "public"
	id     string
	port   int
	name   string
	public bool
}

func applyPort(forwards []forward, g guess) []forward {
	switch g.kind {
	case "add":
		return append(append([]forward(nil), forwards...), forward{ID: g.id, Port: g.port, Name: g.name, Public: g.public})
	case "remove":
		var kept []forward
		for _, each := range forwards {
			if each.ID != g.id {
				kept = append(kept, each)
			}
		}
		return kept
	}
	out := append([]forward(nil), forwards...)
	for i := range out {
		if out[i].ID == g.id {
			out[i].Public = g.public
		}
	}
	return out
}

func publicWarning(port int) []string {
	return []string{fmt.Sprintf("Make port %d public?", port), "Anyone with its public address can open it without signing in."}
}

// openPorts is `/ports [open <port> [name] [public]]` (P11f), after the web's ports panel: the owner's forwards with their addresses; Ctrl+P makes one public or
// private (public only after a confirmation: anyone with the address can open it), Ctrl+D closes one. `open` adds a forward; a public one is confirmed first.
func openPorts(ctx runtime.TuiContext, args string) error {
	scope := ctx.Scope()
	ref, err := scope.Hold(contract.Ports, delta.JsonObjectOf())
	if err != nil {
		scope.Dispose()
		return err
	}
	read := chordtui.WatchMember(scope, ref.Member("forwards"))
	// Adding, closing and the public switch show at once; a refused one rolls back with its error.
	forwards, changing, _ := p2tui.Optimistic(scope, func() []forward {
		value, _ := read()
		return forwardsOf(value)
	}, applyPort)
	change := func(g guess, call func() error) {
		go func() {
			err := p2tui.AddCall(changing, g, func() error {
				// `/ports open` runs before the list arrived: the call waits for the reference.
				if err := p2runtime.WhenLive(context.Background(), ref); err != nil {
					return err
				}
				if variant, _, _ := ref.Variant(); variant != "owner" {
					return fmt.Errorf("Ports are the owner's")
				}
				return call()
			})
			if err != nil {
				ctx.Post(func() { ctx.Fail(err) })
			}
		}()
	}
	byRow := func(row *components.ListRow) *forward {
		if row == nil {
			return nil
		}
		for _, each := range forwards() {
			if each.ID == row.Key {
				each := each
				return &each
			}
		}
		return nil
	}
	var overlay runtime.TuiOverlayHandle
	setPublic := func(f *forward, open bool) {
		change(guess{kind: "public", id: f.ID, public: open}, func() error {
			_, err := ref.Call("setPublic", delta.JsonObjectOf("id", f.ID, "public", open), context.Background())
			return err
		})
	}
	overlay = ctx.Overlay("ports", components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title: func() string { return fmt.Sprintf("Ports (%d)", len(forwards())) },
		Rows: func() []components.ListRow {
			var rows []components.ListRow
			for _, f := range forwards() {
				name := f.Name
				if name == "" {
					name = fmt.Sprintf("Port %d", f.Port)
				}
				label := fmt.Sprintf("%s  localhost:%d", name, f.Port)
				if f.Public {
					label += "  public"
				}
				detail := ""
				if f.Session != "" {
					title := f.SessionTitle
					if title == "" {
						title = "another session"
					}
					detail = "by " + title
				}
				// The card's rule: only Radius forward addresses are shown, never other server-reported URLs.
				var addresses []string
				if u := forwardURL(f.URL); u != "" {
					addresses = append(addresses, u)
				}
				if forwardURL(f.PublicURL) != "" {
					addresses = append(addresses, "public: "+f.PublicURL)
				}
				description := strings.Join(addresses, "  ·  ")
				if description == "" {
					description = "no address yet (Radius)"
				}
				row := components.ListRow{Key: f.ID, Label: label, Detail: detail, Description: description}
				if f.Public {
					row.Color = "warning"
				}
				rows = append(rows, row)
			}
			return rows
		},
		Actions: []components.ListAction{
			{Key: "ctrl+p", Label: "public/private", Run: func(row *components.ListRow) {
				f := byRow(row)
				if f == nil || strings.HasPrefix(f.ID, "adding-") {
					return
				}
				if f.Public {
					setPublic(f, false)
					return
				}
				components.Confirm(runtime.Selectors(ctx), publicWarning(f.Port), func(yes bool) {
					if yes {
						setPublic(f, true)
					}
				})
			}},
			{Key: "ctrl+d", Label: "close", Run: func(row *components.ListRow) {
				f := byRow(row)
				if f != nil && !strings.HasPrefix(f.ID, "adding-") {
					change(guess{kind: "remove", id: f.ID}, func() error {
						_, err := ref.Call("remove", delta.JsonObjectOf("id", f.ID), context.Background())
						return err
					})
				}
			}},
		},
		Footer: func() []string {
			return []string{ctx.Theme().Current().Fg("dim", "  /ports open <port> [name] [public] adds one")}
		},
		OnClose: func() { scope.Dispose(); overlay.Close() },
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	// After the overlay: a confirmation opens on top of it.
	words := strings.Fields(args)
	if len(words) > 0 && words[0] == "open" {
		port := -1
		if len(words) > 1 {
			if n, err := strconv.Atoi(words[1]); err == nil {
				port = n
			}
		}
		isPublic, name := false, ""
		for i, word := range words {
			if word == "public" {
				isPublic = true
			} else if i >= 2 && name == "" {
				name = word
			}
		}
		if port < 1 || port > 65535 {
			ctx.Notify("Usage: /ports open <port> [name] [public]", runtime.NoticeInfo)
			return nil
		}
		add := func() {
			pairs := []any{"port", float64(port)}
			if name != "" {
				pairs = append(pairs, "name", name)
			}
			if isPublic {
				pairs = append(pairs, "public", true)
			}
			params := delta.JsonObjectOf(pairs...)
			change(guess{kind: "add", id: "adding-" + uuid.NewString(), port: port, name: name, public: isPublic}, func() error {
				if _, err := ref.Call("add", params, context.Background()); err != nil {
					return err
				}
				ctx.Post(func() { ctx.Notify(fmt.Sprintf("Forwarding port %d", port), runtime.NoticeInfo) })
				return nil
			})
		}
		if isPublic {
			components.Confirm(runtime.Selectors(ctx), publicWarning(port), func(yes bool) {
				if yes {
					add()
				}
			})
		} else {
			add()
		}
	}
	return nil
}

var commands = p2runtime.Keyed[runtime.TuiCommand]{
	p2runtime.Entry("p2.ports", runtime.TuiCommand{
		Title: "Ports", Category: "Owner", When: "owner && !overlay",
		Slash: &runtime.TuiCommandSlash{Name: "ports", ArgumentHint: "[open <port> [name] [public]]"},
		Run:   func(ctx runtime.TuiContext, args string) error { return openPorts(ctx, args) },
	}),
}

// Facet is the plugin's TUI facet.
var Facet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-ports", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, runtime.TuiToolRenderers, p2runtime.Entry("ports", Renderers))
		p2runtime.ContributeKeyed(self, runtime.TuiCommands, commands...)
		return nil
	})
})
