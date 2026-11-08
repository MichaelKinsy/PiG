// Package tui is `/devices` in the TUI (p2-tui-port.md P11g), after the web's Devices sheet (owner): a one-time sign-in token for another device (only the
// localhost device owner may mint one), and signing out this device or all devices. Signing out is the launcher's: the TUI's sign-in cookie lives there, never
// in this worker, so it is a C launcher action (`signOut`, section 3.2) whose consent UI shows the scope ("all devices, this one included").
//
// Ports apps/p2/plugins/devices/tui.ts.
package tui

import (
	"context"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/devices/contract"
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

func showToken(ctx runtime.TuiContext, token string) {
	var handle runtime.TuiOverlayHandle
	handle = ctx.Overlay("device-token", p2tui.NewTextOverlay(ctx, "One-time sign-in token", func() []string {
		return []string{
			"This token grants full owner access. Give it only to your own device, not to collaborators.",
			"Open Pi's domain on the other device, paste it into the sign-in page and sign in. It expires.",
			"",
			"  " + render.LineText(token),
		}
	}, func() { handle.Close() }), pitui.OverlaySpec{Width: p2tui.Percent(90), Anchor: "center"})
}

// signOut: the launcher's consent UI (section 3.3) is the confirmation: it states the scope before anything is signed out. On success the server revokes this
// connection; the launcher then forgets its cookie and asks for an owner link. The device owner on loopback has no cookie, so "this device" is refused there
// (`denied`, like declining).
func signOut(ctx runtime.TuiContext, all bool) {
	go func() {
		result := ctx.Grant("signOut", map[string]any{"all": all})
		ctx.Post(func() {
			if !result.OK && result.Error == "denied" {
				ctx.Notify("Not signed out", runtime.NoticeWarning)
				return
			}
			done := "Signed out on this device"
			if all {
				done = "Signed out all devices"
			}
			p2tui.GrantNotice(ctx, done, result)
		})
	}()
}

func openDevices(ctx runtime.TuiContext) error {
	scope := chordtui.NewTuiScope(context.Background(), ctx.Host(), ctx.RequestRender)
	ref, err := scope.Hold(contract.Devices, delta.JsonObjectOf())
	if err != nil {
		scope.Dispose()
		return err
	}
	canMint := chordtui.WatchMember(scope, ref.Member("canMint"))
	status := ""
	var overlay runtime.TuiOverlayHandle
	closeAll := func() {
		scope.Dispose()
		overlay.Close()
	}
	overlay = ctx.Overlay("devices", components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title: func() string { return "Devices" },
		Rows: func() []components.ListRow {
			var rows []components.ListRow
			if value, ok := canMint(); ok && value == true {
				rows = append(rows, components.ListRow{Key: "token", Label: "Generate a one-time sign-in token for another device"})
			} else {
				rows = append(rows, components.ListRow{Key: "token-help", Label: "Tokens come from the host: npm run owner-link (associated devices cannot mint)", Color: "muted"})
			}
			return append(rows,
				components.ListRow{Key: "signout", Label: "Sign out on this device"},
				components.ListRow{Key: "signout-all", Label: "Sign out all devices", Color: "error"})
		},
		OnSelect: func(row components.ListRow) {
			switch row.Key {
			case "token":
				variant, _, _ := ref.Variant()
				if p2runtime.StatusOf(ref) != host.RefStateLive || variant != "owner" {
					return
				}
				status = "Generating token…"
				go func() {
					value, err := ref.Call("token", delta.JsonObjectOf(), context.Background())
					ctx.Post(func() {
						if err != nil {
							status = err.Error()
							if status == "" {
								status = "Could not generate a sign-in token."
							}
						} else {
							status = ""
							if object, ok := value.(interface{ Value(string) any }); ok {
								if token, ok := object.Value("token").(string); ok {
									showToken(ctx, token)
								}
							}
						}
						ctx.RequestRender()
					})
				}()
			case "signout", "signout-all":
				closeAll()
				signOut(ctx, row.Key == "signout-all")
			}
		},
		Footer: func() []string {
			if status == "" {
				return nil
			}
			return []string{ctx.Theme().Current().Fg("muted", "  "+render.LineText(status))}
		},
		OnClose: closeAll,
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	return nil
}

var commands = p2runtime.Keyed[runtime.TuiCommand]{
	p2runtime.Entry("p2.devices", runtime.TuiCommand{
		Title:    "Devices and sign-out",
		Category: "Owner",
		When:     "owner && !overlay",
		Slash:    &runtime.TuiCommandSlash{Name: "devices"},
		Run:      func(ctx runtime.TuiContext, _ string) error { return openDevices(ctx) },
	}),
}

// Facet is the plugin's TUI facet.
var Facet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-devices", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, runtime.TuiCommands, commands...)
		return nil
	})
})
