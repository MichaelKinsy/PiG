// Package tui is sharing in the TUI (p2-tui-port.md P11a), after the web's share sheet. A TUI guest counts as connected while its worker holds GuestPresence, as
// a guest's web page does (section 4.1; the owner's variant does nothing), held from a slot surface (after the link is live), not during facet load. The guest's
// name and access show in the header and, read-only, in place of the editor (the chat surface).
//
// `/share` (owner): the session's two links and the guests connected now. Enter copies a link (creating it first) through the launcher's `copy` (its consent
// screen shows the link); a first read/write link on a remote environment is confirmed first. Ctrl+R resets a link (new token, its guests lose access), Ctrl+D
// removes a guest; both confirmed.
//
// While the brain is connected to Radius, `/share <email> [full]` shares the session with a person, who opens it in Radius signed in with that email. The
// overlay lists them; Enter copies the session's Radius address to send them.
//
// Ports apps/p2/plugins/sharing/tui.ts.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
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

var label = map[string]string{"view": "Read-only", "full": "Read/write"}

type link struct{ mode, token, url string }
type guest struct{ id, name, mode string }

func linksOf(value any) []link {
	var out []link
	for _, each := range p2tui.JList(value) {
		out = append(out, link{p2tui.JStr(p2tui.JField(each, "mode")), p2tui.JStr(p2tui.JField(each, "token")), p2tui.JStr(p2tui.JField(each, "url"))})
	}
	return out
}

func guestsOf(value any) []guest {
	var out []guest
	for _, each := range p2tui.JList(value) {
		out = append(out, guest{p2tui.JStr(p2tui.JField(each, "id")), p2tui.JStr(p2tui.JField(each, "name")), p2tui.JStr(p2tui.JField(each, "mode"))})
	}
	return out
}

func call(ref *host.Reference, method string, args *delta.JsonObject) error {
	_, err := ref.Call(method, args, context.Background())
	return err
}

func accessText(mode string) string {
	if mode == "full" {
		return "read/write"
	}
	return "read-only"
}

func openShare(ctx runtime.TuiContext) error {
	session := ctx.Identity().Session
	if session == "" {
		return nil
	}
	scope := ctx.Scope()
	sharing, err := scope.Hold(contract.Sharing, delta.JsonObjectOf("session", session))
	if err != nil {
		scope.Dispose()
		return err
	}
	linksRead := chordtui.WatchMember(scope, sharing.Member("links"))
	guestsRead := chordtui.WatchMember(scope, sharing.Member("guests"))
	envRead := chordtui.WatchMember(scope, sharing.Member("env"))
	peopleRead := chordtui.WatchMember(scope, sharing.Member("people"))
	peopleURLRead := chordtui.WatchMember(scope, sharing.Member("peopleUrl"))
	status := ""
	links := func() ([]link, bool) {
		value, ok := linksRead()
		return linksOf(value), ok && value != nil
	}
	guests := func() []guest { value, _ := guestsRead(); return guestsOf(value) }
	people := func() []guest { value, _ := peopleRead(); return guestsOf(value) }
	env := func() string { value, _ := envRead(); text, _ := value.(string); return text }
	peopleURL := func() string { value, _ := peopleURLRead(); text, _ := value.(string); return text }
	owner := func() *host.Reference {
		if variant, _, _ := sharing.Variant(); variant == "owner" && p2runtime.StatusOf(sharing) == host.RefStateLive {
			return sharing
		}
		return nil
	}
	fail := func(err error) { ctx.Post(func() { status = err.Error(); ctx.RequestRender() }) }
	go func() {
		if err := p2runtime.WhenLive(context.Background(), sharing); err != nil {
			fail(err)
		}
	}()
	confirm := func(lines []string, then func()) {
		components.Confirm(runtime.Selectors(ctx), lines, func(yes bool) {
			if yes {
				then()
			}
		})
	}
	// create makes the link of mode (the answer follows its state: the new link is in `links` when the call resolves) and passes its address on, off the loop.
	create := func(mode string, remote bool, then func(url string)) {
		go func() {
			pairs := []any{"mode", mode}
			if remote {
				pairs = append(pairs, "remoteConfirmed", true)
			}
			if err := call(sharing, "link", delta.JsonObjectOf(pairs...)); err != nil {
				fail(err)
				return
			}
			value, err := sharing.Member("links").Value()
			if err != nil {
				fail(err)
				return
			}
			for _, each := range linksOf(value) {
				if each.mode == mode {
					then(each.url)
					return
				}
			}
		}()
	}
	grantCopy := func(text, labelText, done string) {
		go func() {
			result := ctx.Grant("copy", map[string]any{"text": text, "label": labelText})
			ctx.Post(func() { p2tui.GrantNotice(ctx, done, result) })
		}()
	}
	// copyLink copies the link of mode, created first when missing (and confirmed for read/write on a remote environment).
	copyLink := func(mode string) {
		status = ""
		if owner() == nil {
			return
		}
		done := func(url string) { grantCopy(url, label[mode]+" share link", label[mode]+" link copied") }
		current, _ := links()
		for _, each := range current {
			if each.mode == mode && each.url != "" {
				done(each.url)
				return
			}
		}
		remote := mode == "full" && env() != ""
		if remote {
			confirm([]string{"Create a read/write link?", fmt.Sprintf("Guests can make the agent run commands and change files on the remote environment %q.", render.LineText(env()))}, func() { create(mode, true, done) })
			return
		}
		create(mode, false, done)
	}
	reset := func(mode string) {
		service := owner()
		exists := false
		current, _ := links()
		for _, each := range current {
			exists = exists || each.mode == mode
		}
		if service == nil || !exists {
			return
		}
		confirm([]string{fmt.Sprintf("New %s link?", strings.ToLower(label[mode])), "Guests who joined through the old one lose access."}, func() {
			go func() {
				pairs := []any{"mode", mode, "reset", true}
				if env() != "" && mode == "full" {
					pairs = append(pairs, "remoteConfirmed", true)
				}
				if err := call(service, "link", delta.JsonObjectOf(pairs...)); err != nil {
					fail(err)
					return
				}
				ctx.Post(func() { ctx.Notify(label[mode]+" link reset", runtime.NoticeInfo) })
			}()
		})
	}
	remove := func(g guest, person bool) {
		service := owner()
		if service == nil {
			return
		}
		why := "This identity is blocked; the link keeps working for others."
		if person {
			why = "They lose access to this session; share it again to give it back."
		}
		confirm([]string{fmt.Sprintf("Remove %s?", render.LineText(g.name)), why}, func() {
			go func() {
				if err := call(service, "remove", delta.JsonObjectOf("guestId", g.id)); err != nil {
					fail(err)
					return
				}
				ctx.Post(func() { ctx.Notify("Removed "+render.LineText(g.name), runtime.NoticeInfo) })
			}()
		})
	}
	linkMode := func(row *components.ListRow) string {
		switch {
		case row != nil && row.Key == "link:view":
			return "view"
		case row != nil && row.Key == "link:full":
			return "full"
		}
		return ""
	}
	find := func(list []guest, prefix string, row *components.ListRow) *guest {
		if row == nil {
			return nil
		}
		for _, each := range list {
			if prefix+each.id == row.Key {
				each := each
				return &each
			}
		}
		return nil
	}
	var overlay runtime.TuiOverlayHandle
	overlay = ctx.Overlay("share", components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title: func() string {
			plural := "s"
			if len(guests()) == 1 {
				plural = ""
			}
			return fmt.Sprintf("Share · %d guest%s connected", len(guests()), plural)
		},
		Rows: func() []components.ListRow {
			var rows []components.ListRow
			current, known := links()
			for _, mode := range []string{"view", "full"} {
				exists := false
				for _, each := range current {
					exists = exists || each.mode == mode
				}
				detail := "enter creates and copies"
				switch {
				case !known:
					detail = "…"
				case exists:
					detail = "enter copies"
				}
				row := components.ListRow{Key: "link:" + mode, Label: label[mode] + " link", Detail: detail}
				if mode == "full" {
					row.Color = "warning"
				}
				rows = append(rows, row)
			}
			listed := people()
			for _, person := range listed {
				row := components.ListRow{Key: "person:" + person.id, Label: person.name, Detail: accessText(person.mode) + " · enter copies the Radius address"}
				if person.mode == "full" {
					row.Color = "warning"
				}
				rows = append(rows, row)
			}
			// People are listed above; the connected guests here joined through links.
			for _, g := range guests() {
				isPerson := false
				for _, person := range listed {
					isPerson = isPerson || person.id == g.id
				}
				if !isPerson {
					rows = append(rows, components.ListRow{Key: "guest:" + g.id, Label: g.name + " (unverified name)", Detail: accessText(g.mode), Color: "muted"})
				}
			}
			return rows
		},
		OnSelect: func(row components.ListRow) {
			if mode := linkMode(&row); mode != "" {
				copyLink(mode)
			} else if find(people(), "person:", &row) != nil && peopleURL() != "" {
				grantCopy(peopleURL(), "Radius address of this session", "Radius address copied")
			}
		},
		Actions: []components.ListAction{
			{Key: "ctrl+r", Label: "reset link", Run: func(row *components.ListRow) {
				if mode := linkMode(row); mode != "" {
					reset(mode)
				}
			}},
			{Key: "ctrl+d", Label: "remove guest", Run: func(row *components.ListRow) {
				if person := find(people(), "person:", row); person != nil {
					remove(*person, true)
				} else if g := find(guests(), "guest:", row); g != nil {
					remove(*g, false)
				}
			}},
		},
		Footer: func() []string {
			t := ctx.Theme().Current()
			where := "this host"
			if env() != "" {
				where = fmt.Sprintf("the remote environment %q", render.LineText(env()))
			}
			radius := "  Connect this brain to Radius to share sessions with people by email."
			if peopleURL() != "" {
				radius = "  /share <email> [full] shares it with a person, who opens it in Radius signed in with that email."
			}
			lines := []string{
				t.Fg("dim", "  Anyone with a link joins this session and its subagents under a name they choose. Read/write lets them make the agent run commands and change files on "+where+"."),
				t.Fg("dim", radius),
			}
			if status != "" {
				lines = append(lines, t.Fg("error", "  "+render.LineText(status)))
			}
			return lines
		},
		OnClose: func() { scope.Dispose(); overlay.Close() },
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	return nil
}

// shareWith is `/share <email> [full]`: shares the session with a person through Radius (read-only unless `full`).
func shareWith(ctx runtime.TuiContext, args string) error {
	session := ctx.Identity().Session
	if session == "" {
		return nil
	}
	words := strings.Fields(args)
	email, mode := "", "view"
	if len(words) > 0 {
		email = words[0]
	}
	if len(words) > 1 {
		mode = words[1]
	}
	if (mode != "view" && mode != "full") || len(words) > 2 {
		ctx.Notify("Usage: /share <email> [full]", runtime.NoticeInfo)
		return nil
	}
	sharing, err := ctx.Host().Hold(context.Background(), contract.Sharing, delta.JsonObjectOf("session", session), host.GetOptions{})
	if err != nil {
		return err
	}
	notify := func(message string) { ctx.Post(func() { ctx.Notify(message, runtime.NoticeInfo) }) }
	share := func(remote bool) {
		pairs := []any{"email", email, "mode", mode}
		if remote {
			pairs = append(pairs, "remoteConfirmed", true)
		}
		if err := call(sharing, "share", delta.JsonObjectOf(pairs...)); err != nil {
			notify(err.Error())
		} else {
			notify(fmt.Sprintf("Shared with %s; they find it in Radius", render.LineText(email)))
		}
		sharing.Release()
	}
	go func() {
		if err := p2runtime.WhenLive(context.Background(), sharing); err != nil {
			notify(err.Error())
			sharing.Release()
			return
		}
		if variant, _, _ := sharing.Variant(); variant != "owner" {
			sharing.Release()
			return
		}
		peopleURL, _ := sharing.Member("peopleUrl").Value()
		if text, _ := peopleURL.(string); text == "" {
			notify("Connect this brain to Radius and open it there once to share with people by email")
			sharing.Release()
			return
		}
		envValue, _ := sharing.Member("env").Value()
		env, hasEnv := envValue.(string)
		remote := mode == "full" && hasEnv
		if !remote {
			share(false)
			return
		}
		ctx.Post(func() {
			components.Confirm(runtime.Selectors(ctx), []string{
				fmt.Sprintf("Share read/write with %s?", render.LineText(email)),
				fmt.Sprintf("They can make the agent run commands and change files on the remote environment %q.", render.LineText(env)),
			}, func(yes bool) {
				if !yes {
					sharing.Release()
					return
				}
				go share(true)
			})
		})
	}()
	return nil
}

var commands = p2runtime.Keyed[runtime.TuiCommand]{
	p2runtime.Entry("p2.share", runtime.TuiCommand{
		Title: "Share this session", Category: "Session", When: "owner && chat && !overlay",
		Slash: &runtime.TuiCommandSlash{Name: "share", ArgumentHint: "[email [full]]"},
		Run: func(ctx runtime.TuiContext, args string) error {
			if args = strings.TrimSpace(args); args != "" {
				return shareWith(ctx, args)
			}
			return openShare(ctx)
		},
	}),
}

// presence holds GuestPresence while the surface lives.
type presence struct{ scope *chordtui.TuiScope }

func (presence) Render(int) []string { return nil }
func (presence) Invalidate()         {}
func (p presence) Dispose()          { p.scope.Dispose() }

// Facet is the plugin's TUI facet.
var Facet = runtime.TuiFacet("widget-above:sharing", func(rt *runtime.TuiRuntime) runtime.TuiSurface {
	scope := chordtui.NewTuiScope(context.Background(), rt.Host, rt.Ctx.RequestRender)
	_, _ = scope.Hold(contract.GuestPresence, delta.JsonObjectOf())
	return presence{scope}
}, commands, nil)
