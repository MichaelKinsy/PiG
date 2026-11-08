package tui

// The sessions commands, the place `/envs` moves the viewed session through, and the facet.
//
// Ports the second half of apps/p2/plugins/sessions/tui.ts.

import (
	"context"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

func surfaceOf(ctx runtime.TuiContext) *Surface {
	surface, _ := ctx.Surface(Slot).(*Surface)
	return surface
}

func openSessions(ctx runtime.TuiContext, _ string) error {
	ctx.Navigate("sessions", "", nil)
	return nil
}

func listDirectory(envs *host.Reference, env *string, path string) (string, error) {
	args := delta.JsonObjectOf("path", path)
	if env != nil && *env != "" {
		args.Set("env", *env)
	}
	result, err := envs.Call("listDirectory", args, context.Background())
	if err != nil {
		return "", err
	}
	return p2tui.JStr(p2tui.JField(result, "path")), nil
}

// relocate moves the viewed session (the web's Relocate, `Sessions.relocate`): the location is resolved and checked as the web's DirectoryPicker does
// (`Envs.listDirectory`, `~` expanded on the session's environment), then the session row shows the listed path at once while the move runs; a refused move (a
// busy session) rolls back with the server's text. Another environment is confirmed first with the web's note. It runs on the owner loop.
func relocate(ctx runtime.TuiContext, session contract.Session, location p2runtime.LocationInput, confirmed bool) {
	moving := !envEqual(location.Env, envOf(session))
	proceed := func() {
		go func() {
			envs, err := OwnerEnvs(ctx)
			if err == nil {
				if variant, _, _ := envs.Variant(); variant != "owner" {
					err = errOnlyOwnerDirs
				}
			}
			var path string
			if err == nil {
				path, err = listDirectory(envs, location.Env, location.Path)
			}
			ctx.Post(func() {
				if err != nil {
					ctx.Fail(err)
					return
				}
				// A move is long work (a job): the row says "moving…" until it is done ("Session moved"), or failed.
				surface := surfaceOf(ctx)
				if surface == nil || surface.Catalog() == nil {
					return
				}
				catalog := surface.Catalog()
				ctx.Notify("Moving to "+render.LineText(LocationLabel(p2runtime.LocationInput{Env: location.Env, Path: path}))+"…", runtime.NoticeInfo)
				args := delta.JsonObjectOf("id", session.ID, "cwd", path)
				if location.Env != nil {
					args.Set("env", *location.Env)
				} else {
					args.Set("env", nil)
				}
				go func() {
					if _, err := catalog.Call("relocate", args, context.Background()); err != nil {
						ctx.Post(func() { ctx.Fail(err) })
					}
				}()
			})
		}()
	}
	// The prompt shows the note above its input, as the web's "Move session to" button: Enter is the confirmation.
	if moving && !confirmed {
		on := "on this device"
		if location.Env != nil {
			on = "on " + render.LineText(*location.Env)
		}
		components.Confirm(runtime.Selectors(ctx), []string{
			"Move this session to " + LocationLabel(location) + "?",
			"Tools will run " + on + ". The history stays; files are not copied.",
		}, func(yes bool) {
			if yes {
				proceed()
			}
		})
		return
	}
	proceed()
}

type onlyOwnerDirs struct{}

func (onlyOwnerDirs) Error() string { return "Only the owner changes directories" }

var errOnlyOwnerDirs error = onlyOwnerDirs{}

func bottomSheet() pitui.OverlaySpec {
	return pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"}
}

// moveTo is `/envs`, Enter on an environment: the viewed session moves there. The directory prompt opens on that environment, prefilled with the session's
// directory when it exists there, else that machine's home; its note confirms the move (as in `/cd`), then `Sessions.relocate`.
func moveTo(ctx runtime.TuiContext, env *string) error {
	surface := surfaceOf(ctx)
	var session *contract.Session
	if surface != nil {
		session = surface.Session(ctx.Identity().Session)
	}
	if session == nil {
		ctx.Notify("Open a session to move it", runtime.NoticeWarning)
		return nil
	}
	session2 := *session
	go func() {
		envs, err := OwnerEnvs(ctx)
		if err != nil {
			return
		}
		if variant, _, _ := envs.Variant(); variant != "owner" {
			return
		}
		path, problem := "~", ""
		if listed, err := listDirectory(envs, env, session2.Cwd); err == nil {
			path = listed
		} else if listed, err := listDirectory(envs, env, "~"); err == nil {
			path = listed
		} else {
			problem = err.Error()
		}
		ctx.Post(func() {
			var overlay runtime.TuiOverlayHandle
			overlay = ctx.Overlay("cd", NewLocationInput(ctx, LocationInputOptions{
				Title: "Working directory", Action: "moves the session", Env: env, Path: path, Moving: true,
				MovingFrom: session2.Env, HasMovingFrom: true, Error: problem,
				OnSubmit: func(location p2runtime.LocationInput) {
					overlay.Close()
					relocate(ctx, session2, location, true)
				},
				OnCancel: func() { overlay.Close() },
			}), bottomSheet())
		})
	}()
	return nil
}

func envOfSession(session contract.Session) *string { return envOf(session) }

var place = runtime.TuiSessionPlace{
	Where: func(ctx runtime.TuiContext) *runtime.SessionWhere {
		surface := surfaceOf(ctx)
		if surface == nil {
			return nil
		}
		session := surface.Session(ctx.Identity().Session)
		if session == nil {
			return nil
		}
		return &runtime.SessionWhere{Env: envOfSession(*session), Cwd: session.Cwd}
	},
	MoveTo: moveTo,
}

func changeDirectory(ctx runtime.TuiContext, argument string) error {
	surface := surfaceOf(ctx)
	if surface == nil {
		return nil
	}
	session := surface.Session(ctx.Identity().Session)
	if session == nil {
		return nil
	}
	current := *session
	fallback := p2runtime.LocationFallback{Env: envOf(current), Cwd: current.Cwd}
	if argument != "" {
		go func() {
			names := EnvNames(ctx)
			ctx.Post(func() {
				location, err := p2runtime.ParseLocation(argument, names, fallback)
				if err != nil {
					ctx.Notify(err.Error(), runtime.NoticeError)
					return
				}
				relocate(ctx, current, location, false)
			})
		}()
		return nil
	}
	var overlay runtime.TuiOverlayHandle
	overlay = ctx.Overlay("cd", NewLocationInput(ctx, LocationInputOptions{
		Title: "Working directory", Action: "moves the session", Env: fallback.Env, Path: current.Cwd, Moving: true,
		OnSubmit: func(location p2runtime.LocationInput) {
			overlay.Close()
			relocate(ctx, current, location, true)
		},
		OnCancel: func() { overlay.Close() },
	}), bottomSheet())
	return nil
}

// selectorScope is the selector's own keys: more specific than app.exit/app.clear so Ctrl+D archives here, as pi's Ctrl+D deletes.
const selectorScope = "sessions && selector && !overlay && !creating"

func selectorCommand(title string, run func(*Surface), keys ...string) runtime.TuiCommand {
	return runtime.TuiCommand{
		Title: title, Category: "Sessions", When: selectorScope, HiddenFromPalette: true, Keys: keys,
		Run: func(ctx runtime.TuiContext, _ string) error {
			if surface := surfaceOf(ctx); surface != nil {
				run(surface)
			}
			return nil
		},
	}
}

// Commands are the session commands.
var Commands = p2runtime.Keyed[runtime.TuiCommand]{
	// pi's app.session.* are unbound by default; p2.sessions keeps Ctrl+R in the chat context (section 1.11).
	p2runtime.Entry("p2.sessions", runtime.TuiCommand{
		Title: "Sessions", Category: "Session", Keys: []string{"ctrl+r"}, When: "chat",
		Slash: &runtime.TuiCommandSlash{Name: "sessions"}, Run: openSessions,
	}),
	p2runtime.Entry("app.session.resume", runtime.TuiCommand{
		Title: "Resume a session", Category: "Session", When: "!overlay",
		Slash: &runtime.TuiCommandSlash{Name: "resume"}, Run: openSessions,
	}),
	p2runtime.Entry("app.session.new", runtime.TuiCommand{
		Title: "New session", Category: "Session", When: "owner && !overlay",
		Slash: &runtime.TuiCommandSlash{
			Name: "new", ArgumentHint: "[[env:]path]", TabOnly: true,
			Complete: func(prefix string, ctx runtime.TuiContext) ([]pitui.AutocompleteItem, error) {
				return CompleteLocation(ctx, prefix, p2runtime.LocationFallback{}), nil
			},
		},
		Run: func(ctx runtime.TuiContext, args string) error {
			ctx.Navigate("sessions", "", nil)
			surface := surfaceOf(ctx)
			if surface == nil {
				return nil
			}
			argument := strings.TrimSpace(args)
			// pi's `/new` starts over in the same directory; without a session to follow, ask for one.
			if argument != "" || ctx.Identity().Session != "" {
				surface.CreateIn(argument)
			} else {
				surface.Command("new")
			}
			return nil
		},
	}),
	p2runtime.Entry("p2.session.cd", runtime.TuiCommand{
		Title: "Change the working directory", Category: "Session", When: "chat && owner && !overlay",
		Slash: &runtime.TuiCommandSlash{
			Name: "cd", ArgumentHint: "[[env:]path]", TabOnly: true,
			Complete: func(prefix string, ctx runtime.TuiContext) ([]pitui.AutocompleteItem, error) {
				surface := surfaceOf(ctx)
				if surface == nil {
					return nil, nil
				}
				session := surface.Session(ctx.Identity().Session)
				if session == nil {
					return nil, nil
				}
				return CompleteLocation(ctx, prefix, p2runtime.LocationFallback{Env: envOf(*session), Cwd: session.Cwd}), nil
			},
		},
		Run: func(ctx runtime.TuiContext, args string) error { return changeDirectory(ctx, strings.TrimSpace(args)) },
	}),
	p2runtime.Entry("p2.session.move", runtime.TuiCommand{
		Title: "Move the session to its project on another machine", Category: "Session", When: "chat && owner && !overlay",
		Slash: &runtime.TuiCommandSlash{
			Name: "move", ArgumentHint: "[env]", TabOnly: true,
			Complete: func(prefix string, ctx runtime.TuiContext) ([]pitui.AutocompleteItem, error) {
				surface := surfaceOf(ctx)
				if surface == nil {
					return nil, nil
				}
				return CompleteMove(ctx, surface, prefix), nil
			},
		},
		Run: func(ctx runtime.TuiContext, args string) error {
			if surface := surfaceOf(ctx); surface != nil {
				RunMove(ctx, surface, strings.TrimSpace(args), func(session contract.Session, location p2runtime.LocationInput) {
					relocate(ctx, session, location, false)
				})
			}
			return nil
		},
	}),
	p2runtime.Entry("p2.session.name", runtime.TuiCommand{
		Title: "Name the session", Category: "Session", When: "chat && owner && !overlay",
		Slash: &runtime.TuiCommandSlash{Name: "name", ArgumentHint: "<title>"},
		Run: func(ctx runtime.TuiContext, args string) error {
			session := ctx.Identity().Session
			title := strings.TrimSpace(args)
			if session == "" {
				return nil
			}
			if title != "" {
				if surface := surfaceOf(ctx); surface != nil {
					surface.RenameSession(session, title)
				}
			} else {
				ctx.Notify("Usage: /name <title>", runtime.NoticeInfo)
			}
			return nil
		},
	}),
	p2runtime.Entry("p2.session.dismissFailed", runtime.TuiCommand{
		Title: "Dismiss failed session operations", Category: "Session", When: "owner && !overlay",
		Slash: &runtime.TuiCommandSlash{Name: "dismiss-failed"},
		Run: func(ctx runtime.TuiContext, _ string) error {
			if surface := surfaceOf(ctx); surface != nil {
				surface.DismissFailed()
			}
			return nil
		},
	}),
	p2runtime.Entry("app.session.toggleSort", selectorCommand("Toggle session sort", (*Surface).ToggleSort)),
	p2runtime.Entry("app.session.toggleNamedFilter", selectorCommand("Toggle pinned filter", (*Surface).TogglePinned)),
	p2runtime.Entry("app.session.toggleArchived", selectorCommand("Toggle archived sessions", (*Surface).ToggleArchived, "ctrl+a")),
	p2runtime.Entry("app.session.togglePath", selectorCommand("Toggle session IDs", (*Surface).ToggleIDs)),
	p2runtime.Entry("app.session.delete", selectorCommand("Archive or restore session", (*Surface).Archive)),
	p2runtime.Entry("app.session.rename", selectorCommand("Rename session", (*Surface).Rename)),
	p2runtime.Entry("p2.session.pin", selectorCommand("Pin or unpin session", (*Surface).Pin, "alt+p")),
}

// Facet is the plugin's TUI facet: the screen, its commands and the place.
var Facet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-sessions", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, runtime.TuiSlots, p2runtime.Entry(Slot, &runtime.TuiSlot{Factory: func(rt *runtime.TuiRuntime) runtime.TuiSurface { return New(rt) }}))
		p2runtime.ContributeKeyed(self, runtime.TuiCommands, Commands...)
		p2runtime.ContributeKeyed(self, runtime.TuiSessionPlaces, p2runtime.Entry("sessions", place))
		return nil
	})
})
