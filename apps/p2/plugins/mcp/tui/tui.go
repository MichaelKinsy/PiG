// Package tui is `/mcp` in the TUI (owners), after the web's MCP sheet: the running servers with status, place, tools and errors; Ctrl+R restarts one, Ctrl+S
// signs in to an HTTP server using OAuth (the authorization page opens through the launcher's `openUrl`, confirmed in its own screen), Ctrl+O signs out. The
// sign-in returns to a loopback callback on the p2 host; a browser elsewhere ends on a page that does not load, whose address `/mcp paste <url>` completes.
//
// Ports apps/p2/plugins/mcp/tui.ts.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

var statusText = map[string]string{"starting": "starting", "connected": "connected", "sign-in": "sign-in needed", "failed": "failed"}

var statusColor = map[string]string{"starting": "muted", "connected": "success", "sign-in": "warning", "failed": "error"}

type instance struct {
	id, server, status, auth, runsOnEnv, errorText string
	location                                       *p2runtime.Location
	tools                                          int
}

func instanceOf(value any) instance {
	i := instance{
		id: p2tui.JStr(p2tui.JField(value, "id")), server: p2tui.JStr(p2tui.JField(value, "server")), status: p2tui.JStr(p2tui.JField(value, "status")),
		auth: p2tui.JStr(p2tui.JField(value, "auth")), errorText: p2tui.JStr(p2tui.JField(value, "error")), tools: len(p2tui.JList(p2tui.JField(value, "tools"))),
	}
	if location := p2tui.JField(value, "location"); location != nil {
		l := p2runtime.Location{Cwd: p2tui.JStr(p2tui.JField(location, "cwd"))}
		if env, ok := p2tui.JField(location, "env").(string); ok {
			l.Env = &env
		}
		i.location = &l
	}
	if runsOn := p2tui.JField(value, "runsOn"); runsOn != nil {
		if _, host := runsOn.(string); !host {
			i.runsOnEnv = p2tui.JStr(p2tui.JField(runsOn, "env"))
		}
	}
	return i
}

type statusGuess struct{ instance, status string }

// openMcp is `/mcp [paste <url>]`: the overlay, or a pasted redirect URL for the sign-in waiting for it.
func openMcp(ctx runtime.TuiContext, args string) error {
	words := strings.Fields(args)
	if len(words) > 0 && words[0] == "paste" {
		ref, err := ctx.Host().Hold(context.Background(), contract.Mcp, delta.JsonObjectOf(), host.GetOptions{})
		if err != nil {
			return err
		}
		url := ""
		if len(words) > 1 {
			url = words[1]
		}
		go func() {
			defer ref.Release()
			paste(ctx, ref, url)
		}()
		return nil
	}
	scope := ctx.Scope()
	held, err := scope.Hold(contract.Mcp, delta.JsonObjectOf())
	if err != nil {
		scope.Dispose()
		return err
	}
	instancesRead := chordtui.WatchMember(scope, held.Member("instances"))
	signInsRead := chordtui.WatchMember(scope, held.Member("signIns"))
	status := ""
	// A restart shows the server starting and a sign-out signed out at once; the status line follows the call.
	instances, changing, _ := chordtui.Optimistic(scope, func() []instance {
		value, _ := instancesRead()
		var out []instance
		for _, each := range p2tui.JList(value) {
			out = append(out, instanceOf(each))
		}
		return out
	}, func(list []instance, guess statusGuess) []instance {
		out := append([]instance(nil), list...)
		for i := range out {
			if out[i].id == guess.instance {
				out[i].status = guess.status
			}
		}
		return out
	})
	owner := func() *host.Reference {
		if variant, _, _ := held.Variant(); variant == "owner" && p2runtime.StatusOf(held) == host.RefStateLive {
			return held
		}
		return nil
	}
	run := func(label string, action func(view *host.Reference) error, guess *statusGuess) {
		view := owner()
		if view == nil {
			return
		}
		status = label + "…"
		call := func() error {
			err := action(view)
			ctx.Post(func() {
				if err != nil {
					status = label + ": " + err.Error()
				} else {
					status = label + ": done"
				}
				ctx.RequestRender()
			})
			return err
		}
		// The status line shows how it ended, a failure included.
		go func() {
			if guess != nil {
				_ = changing(*guess, call)
			} else {
				_ = call()
			}
		}()
		ctx.RequestRender()
	}
	byRow := func(row *components.ListRow) *instance {
		if row == nil {
			return nil
		}
		for _, i := range instances() {
			if i.id == row.Key {
				i := i
				return &i
			}
		}
		return nil
	}
	signIn := func(view *host.Reference, server string) error {
		start, err := view.Call("signIn", delta.JsonObjectOf("server", server), context.Background())
		if err != nil {
			return err
		}
		if p2tui.JStr(p2tui.JField(start, "kind")) == "authorized" {
			return nil
		}
		opened := ctx.Grant("openUrl", map[string]any{"kind": "external", "url": p2tui.JStr(p2tui.JField(start, "url"))})
		ctx.Post(func() {
			if opened.OK {
				status = fmt.Sprintf("Signing in to %s: finish in the browser; if it ends on a page that does not load, /mcp paste <its address>", server)
			} else {
				status = fmt.Sprintf("Open the sign-in page in a browser (%s); /mcp paste <final address> if it does not return", opened.Error)
			}
		})
		return nil
	}
	var overlay runtime.TuiOverlayHandle
	overlay = ctx.Overlay("mcp", components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title: func() string { return fmt.Sprintf("MCP servers (%d)", len(instances())) },
		Rows: func() []components.ListRow {
			signIns, _ := signInsRead()
			var rows []components.ListRow
			for _, i := range instances() {
				var flow any
				for _, each := range p2tui.JList(signIns) {
					if p2tui.JStr(p2tui.JField(each, "server")) == i.server {
						flow = each
						break
					}
				}
				var place []string
				if i.location != nil {
					place = append(place, p2runtime.LocationText(*i.location))
				}
				if i.runsOnEnv != "" {
					place = append(place, "on "+i.runsOnEnv)
				}
				placeText := strings.Join(place, " · ")
				detail := placeText
				if i.status == "connected" {
					plural := "s"
					if i.tools == 1 {
						plural = ""
					}
					detail = fmt.Sprintf("%d tool%s", i.tools, plural)
				}
				description := placeText
				switch {
				case p2tui.JStr(p2tui.JField(flow, "status")) == "waiting":
					description = "sign-in waiting for the browser (/mcp paste <url> completes it)"
				case p2tui.JStr(p2tui.JField(flow, "status")) == "failed":
					description = "sign-in failed: " + p2tui.JStr(p2tui.JField(flow, "error"))
				case i.errorText != "":
					description = i.errorText
				}
				rows = append(rows, components.ListRow{Key: i.id, Label: render.LineText(i.server + "  " + statusText[i.status]), Detail: render.LineText(detail), Description: render.LineText(description), Color: statusColor[i.status]})
			}
			return rows
		},
		Actions: []components.ListAction{
			{Key: "ctrl+r", Label: "restart", Run: func(row *components.ListRow) {
				if i := byRow(row); i != nil {
					run("Restarting "+i.server, func(view *host.Reference) error {
						_, err := view.Call("restart", delta.JsonObjectOf("instance", i.id), context.Background())
						return err
					}, &statusGuess{i.id, "starting"})
				}
			}},
			{Key: "ctrl+s", Label: "sign in", Run: func(row *components.ListRow) {
				if i := byRow(row); i != nil && i.auth == "oauth" {
					run("Signing in to "+i.server, func(view *host.Reference) error { return signIn(view, i.server) }, nil)
				}
			}},
			{Key: "ctrl+o", Label: "sign out", Run: func(row *components.ListRow) {
				if i := byRow(row); i != nil && i.auth == "oauth" {
					run("Signing out of "+i.server, func(view *host.Reference) error {
						_, err := view.Call("signOut", delta.JsonObjectOf("server", i.server), context.Background())
						return err
					}, &statusGuess{i.id, "sign-in"})
				}
			}},
		},
		Footer: func() []string {
			if status == "" {
				return nil
			}
			return []string{ctx.Theme().Current().Fg("muted", "  "+render.LineText(status))}
		},
		OnClose: func() { scope.Dispose(); overlay.Close() },
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	return nil
}

// paste completes the waiting sign-in whose redirect URL url is (each waiting one is tried: the URL's state picks it). It runs off the owner loop.
func paste(ctx runtime.TuiContext, ref *host.Reference, url string) {
	notify := func(message string, kind runtime.NoticeKind) { ctx.Post(func() { ctx.Notify(message, kind) }) }
	if url == "" {
		notify("Usage: /mcp paste <final redirect URL>", runtime.NoticeInfo)
		return
	}
	if err := p2runtime.WhenLive(context.Background(), ref); err != nil {
		notify(err.Error(), runtime.NoticeError)
		return
	}
	if variant, _, _ := ref.Variant(); variant != "owner" {
		notify("MCP servers are for the owner", runtime.NoticeError)
		return
	}
	signIns, err := ref.Member("signIns").Value()
	if err != nil {
		notify(err.Error(), runtime.NoticeError)
		return
	}
	var waiting []any
	for _, each := range p2tui.JList(signIns) {
		if p2tui.JStr(p2tui.JField(each, "status")) == "waiting" {
			waiting = append(waiting, each)
		}
	}
	if len(waiting) == 0 {
		notify("No MCP sign-in is waiting", runtime.NoticeError)
		return
	}
	var failure error
	for _, entry := range waiting {
		_, err := ref.Call("paste", delta.JsonObjectOf("id", p2tui.JStr(p2tui.JField(entry, "id")), "redirectUrl", url), context.Background())
		if err == nil {
			notify("Signed in to "+p2tui.JStr(p2tui.JField(entry, "server")), runtime.NoticeInfo)
			return
		}
		failure = err
	}
	notify(failure.Error(), runtime.NoticeError)
}

var commands = p2runtime.Keyed[runtime.TuiCommand]{
	p2runtime.Entry("p2.mcp", runtime.TuiCommand{
		Title: "MCP servers", Category: "Owner", When: "owner && !overlay", Slash: &runtime.TuiCommandSlash{Name: "mcp", ArgumentHint: "[paste <url>]"},
		Run: func(ctx runtime.TuiContext, args string) error { return openMcp(ctx, args) },
	}),
}

// Facet is the plugin's TUI facet.
var Facet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-mcp", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, runtime.TuiCommands, commands...)
		return nil
	})
})
