// Package tui is `/envs` in the TUI (p2-tui-port.md P11e, section 10.6): the one environment screen. This device and the owner's SSH environments, the viewed
// session's one marked; Enter moves the session to the selected one (the sessions plugin's place: the directory prompt there, then `Sessions.relocate`).
// Management stays on secondary keys, after the web's environments panel: Ctrl+T tests, Ctrl+K reviews a host key the test could not verify (accepted only
// after a confirmation that shows its fingerprints), Ctrl+F forgets a trusted one. Editing configurations stays on the web.
//
// Ports apps/p2/plugins/envs/tui.ts.
package tui

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
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

type hostKey struct {
	state        string
	fingerprints []string
	lines        []any
}

type envTest struct {
	ok                    bool
	platform, arch, error string
	latencyMs             float64
	hostKey               *hostKey
}

type envSummary struct {
	name, typ, host, user string
	port                  float64
	sessions              float64
	test                  *envTest
}

func summaryOf(value any) envSummary {
	config := p2tui.JField(value, "config")
	env := envSummary{
		name: p2tui.JStr(p2tui.JField(value, "name")), typ: p2tui.JStr(p2tui.JField(config, "type")), host: p2tui.JStr(p2tui.JField(config, "host")),
		user: p2tui.JStr(p2tui.JField(config, "user")), port: p2tui.JNum(p2tui.JField(config, "port")), sessions: p2tui.JNum(p2tui.JField(value, "sessions")),
	}
	if test := p2tui.JField(value, "test"); test != nil {
		t := &envTest{ok: p2tui.JBool(p2tui.JField(test, "ok")), platform: p2tui.JStr(p2tui.JField(test, "platform")), arch: p2tui.JStr(p2tui.JField(test, "arch")),
			error: p2tui.JStr(p2tui.JField(test, "error")), latencyMs: p2tui.JNum(p2tui.JField(test, "latencyMs"))}
		if key := p2tui.JField(test, "hostKey"); key != nil {
			k := &hostKey{state: p2tui.JStr(p2tui.JField(key, "state")), lines: p2tui.JList(p2tui.JField(key, "lines"))}
			for _, f := range p2tui.JList(p2tui.JField(key, "fingerprints")) {
				k.fingerprints = append(k.fingerprints, p2tui.JStr(f))
			}
			t.hostKey = k
		}
		env.test = t
	}
	return env
}

func detail(env envSummary) string {
	test := env.test
	switch {
	case test == nil:
		return "untested"
	case test.ok:
		return fmt.Sprintf("ok · %s %s · %s ms", test.platform, test.arch, formatNumber(test.latencyMs))
	case test.hostKey != nil:
		return "host key " + test.hostKey.state
	}
	return "failed"
}

func formatNumber(n float64) string { return fmt.Sprintf("%v", n) }

// local is the row of this device (environment names never contain NUL).
const local = "\x00local"

func hostText(env envSummary) string {
	if env.typ == "gondolin" {
		return "sandbox on " + env.host
	}
	if env.user != "" {
		return env.user + "@" + env.host
	}
	return env.host
}

func openEnvs(ctx runtime.TuiContext) error {
	scope := ctx.Scope()
	ref, err := scope.Hold(contract.Envs, delta.JsonObjectOf())
	if err != nil {
		scope.Dispose()
		return err
	}
	// Moving needs the sessions plugin; without it the screen only manages environments.
	places := chordtui.Registry(scope, runtime.TuiSessionPlaces.Registry)
	envsRead := chordtui.WatchMember(scope, ref.Member("envs"))
	failureRead := chordtui.WatchMember(scope, ref.Member("failure"))
	status := ""
	place := func() *runtime.TuiSessionPlace {
		value, ok := places()
		table, _ := value.(*p2runtime.KeyedTable[runtime.TuiSessionPlace])
		if !ok || table == nil || table.Len() == 0 {
			return nil
		}
		first, _ := table.Get(table.Keys()[0])
		return &first
	}
	envs := func() []envSummary {
		value, _ := envsRead()
		var out []envSummary
		for _, each := range p2tui.JList(value) {
			out = append(out, summaryOf(each))
		}
		return out
	}
	here := func() *runtime.SessionWhere {
		if p := place(); p != nil && p.Where != nil {
			return p.Where(ctx)
		}
		return nil
	}
	go func() {
		// The environments' connections start now, as the web's picker does: moving or testing need not wait for ssh.
		err := p2runtime.WhenLive(context.Background(), ref)
		if err != nil {
			ctx.Post(func() { status = err.Error(); ctx.RequestRender() })
			return
		}
		if variant, _, _ := ref.Variant(); p2runtime.StatusOf(ref) == host.RefStateLive && variant == "owner" {
			_, _ = ref.Call("warm", delta.JsonObjectOf(), context.Background())
		}
	}()
	owner := func() *host.Reference {
		if variant, _, _ := ref.Variant(); variant == "owner" && p2runtime.StatusOf(ref) == host.RefStateLive {
			return ref
		}
		return nil
	}
	run := func(label string, action func() error) {
		status = label + "…"
		ctx.RequestRender()
		go func() {
			err := action()
			ctx.Post(func() {
				if err != nil {
					status = label + ": " + err.Error()
				} else {
					status = label + ": done"
				}
				ctx.RequestRender()
			})
		}()
	}
	byRow := func(row *components.ListRow) *envSummary {
		if row == nil {
			return nil
		}
		for _, env := range envs() {
			if env.name == row.Key {
				env := env
				return &env
			}
		}
		return nil
	}
	var overlay runtime.TuiOverlayHandle
	closeAll := func() {
		scope.Dispose()
		overlay.Close()
	}
	accept := func(env envSummary) {
		service := owner()
		if service == nil || env.test == nil || env.test.ok || env.test.hostKey == nil || env.typ != "ssh" {
			return
		}
		key := env.test.hostKey
		address := env.host
		if env.user != "" {
			address = env.user + "@" + address
		}
		if env.port != 0 {
			address += ":" + formatNumber(env.port)
		}
		why := "unknown key"
		if key.state == "changed" {
			why = "the key CHANGED since it was trusted"
		}
		lines := []string{fmt.Sprintf("Trust %s's host key?", env.name), render.LineText(address) + " · " + why}
		for _, fingerprint := range key.fingerprints {
			lines = append(lines, "  "+render.LineText(fingerprint))
		}
		lines = append(lines, "Compare the fingerprints with the server's own before you trust it.")
		components.Confirm(runtime.Selectors(ctx), lines, func(yes bool) {
			if yes {
				run("Trusting "+env.name, func() error {
					_, err := service.Call("acceptHostKey", delta.JsonObjectOf("name", env.name, "lines", key.lines), context.Background())
					return err
				})
			}
		})
	}
	selected := local
	if current := here(); current != nil && current.Env != nil && *current.Env != "" {
		selected = *current.Env
	}
	// The registry that has the places is read asynchronously: until it answers, the session's environment is not known and this device is selected; the first
	// rows listed with it move the cursor to the session's environment, unless the visitor moved it already.
	var list *components.ListOverlay
	placed := here() != nil
	list = components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title: func() string { return fmt.Sprintf("Environments (%d)", len(envs())) },
		Rows: func() []components.ListRow {
			current := here()
			if current != nil && !placed {
				placed = true
				key := local
				if current.Env != nil && *current.Env != "" {
					key = *current.Env
				}
				ctx.Post(func() {
					if selection := list.Selection(); selection != nil && *selection == local {
						list.Select(key)
					}
				})
			}
			mark := func(env *string) string {
				if current != nil && ((current.Env == nil && env == nil) || (current.Env != nil && env != nil && *current.Env == *env)) {
					return "● "
				}
				return "  "
			}
			row := components.ListRow{Key: local, Label: mark(nil) + "This device"}
			if current != nil && current.Env == nil {
				row.Detail, row.Color = "this session", "accent"
			}
			rows := []components.ListRow{row}
			for _, env := range envs() {
				name := env.name
				mine := current != nil && current.Env != nil && *current.Env == name
				plural := "s"
				if env.sessions == 1 {
					plural = ""
				}
				prefix := ""
				if mine {
					prefix = "this session · "
				}
				row := components.ListRow{Key: name, Label: mark(&name) + name + "  " + hostText(env), Detail: fmt.Sprintf("%s%s · %s session%s", prefix, detail(env), formatNumber(env.sessions), plural)}
				if env.test != nil && !env.test.ok {
					row.Description, row.Color = env.test.error, "error"
				} else if mine {
					row.Color = "accent"
				}
				rows = append(rows, row)
			}
			return rows
		},
		// The session's environment first selected.
		Selected: &selected,
		OnSelect: func(row components.ListRow) {
			p := place()
			if p == nil || p.Where == nil || p.Where(ctx) == nil {
				ctx.Notify("Open a session to move it", runtime.NoticeWarning)
				return
			}
			var target *string
			if row.Key != local {
				key := row.Key
				target = &key
			}
			closeAll()
			if err := p.MoveTo(ctx, target); err != nil {
				ctx.Notify(err.Error(), runtime.NoticeError)
			}
		},
		Actions: []components.ListAction{
			{Key: "ctrl+k", Label: "review host key", Run: func(row *components.ListRow) {
				env := byRow(row)
				if env == nil {
					return
				}
				if env.test != nil && !env.test.ok && env.test.hostKey != nil {
					accept(*env)
				} else {
					ctx.Notify(env.name+" has no host key to review (Ctrl+T tests it)", runtime.NoticeInfo)
				}
			}},
			{Key: "ctrl+t", Label: "test", Run: func(row *components.ListRow) {
				env, service := byRow(row), owner()
				if env != nil && service != nil {
					run("Testing "+env.name, func() error {
						_, err := service.Call("test", delta.JsonObjectOf("name", env.name), context.Background())
						return err
					})
				}
			}},
			{Key: "ctrl+f", Label: "forget host key", Run: func(row *components.ListRow) {
				env, service := byRow(row), owner()
				if env == nil || service == nil {
					return
				}
				components.Confirm(runtime.Selectors(ctx), []string{fmt.Sprintf("Forget %s's trusted host key?", env.name), "The next connection asks again."}, func(yes bool) {
					if yes {
						run(fmt.Sprintf("Forgetting %s's key", env.name), func() error {
							_, err := service.Call("forgetHostKey", delta.JsonObjectOf("name", env.name), context.Background())
							return err
						})
					}
				})
			}},
		},
		Footer: func() []string {
			t := ctx.Theme().Current()
			var lines []string
			if failure, _ := failureRead(); failure != nil {
				if text, ok := failure.(string); ok {
					lines = append(lines, t.Fg("error", "  "+render.LineText(text)))
				}
			}
			lines = append(lines, t.Fg("dim", "  Enter moves this session there (● marks where it runs); Ctrl+K reviews an unverified host key"))
			if status != "" {
				lines = append(lines, t.Fg("muted", "  "+render.LineText(status)))
			}
			return lines
		},
		OnClose: closeAll,
	})
	overlay = ctx.Overlay("envs", list, pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	return nil
}

var commands = p2runtime.Keyed[runtime.TuiCommand]{
	p2runtime.Entry("p2.envs", runtime.TuiCommand{
		Title: "Environments", Category: "Owner", When: "owner && !overlay", Slash: &runtime.TuiCommandSlash{Name: "envs"},
		Run: func(ctx runtime.TuiContext, _ string) error { return openEnvs(ctx) },
	}),
}

// Facet is the plugin's TUI facet.
var Facet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-envs", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, runtime.TuiCommands, commands...)
		return nil
	})
})
