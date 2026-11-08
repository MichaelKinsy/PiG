// Package ports is p2's ports plugin (apps/p2/plugins/ports): HTTP services on this device that the owner opens through
// Radius, and the `ports` tool with which a session's agent opens and closes them.
//
// Each forward gets its own origin, `https://fwd--<label>--<id>.rdtun.com`, next to the brain's own
// `https://<label>.rdtun.com`. Radius authenticates the owner for it; the brain relays its requests to `localhost:<port>`.
// A public forward is also open to anyone, without signing in, at `https://pub--<label>--<id>.rdtun.com`: the brain alone
// decides that.
package ports

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/ports/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/ports/server.ts.

var serverFacet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := contract.ServeForwardsNative(facet); err != nil {
		return err
	}
	if err := facet.Serve(contract.ForwardStore, host.LocalFactory(serveForwardStore), host.ServeOptions{}); err != nil {
		return err
	}
	return facet.Serve(contract.Ports, host.RemoteFactory(servePorts), host.ServeOptions{})
})

func objectOf(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

func stringOf(value any) string { s, _ := value.(string); return s }

func objectArgs(args delta.JsonValue) *delta.JsonObject {
	if object := objectOf(args); object != nil {
		return object
	}
	return delta.NewJsonObject(0)
}

// serveForwardStore serves `ForwardStore`: the forwards' list and Radius origin; the store itself is the `ForwardsNative`.
func serveForwardStore(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
	catalog := self.Get(sessionscontract.Catalog, delta.NewJsonObject(0))
	opened := self.Open(
		func(ctx context.Context) (any, error) {
			info, err := server.Member("info").Value()
			if err != nil {
				return nil, err
			}
			store, err := contract.OpenForwards(stringOf(objectArgs(info).Value("dataDir")))
			if err != nil {
				return nil, err
			}
			// Forwards of archived sessions close first, so the list never shows them.
			sessions, err := catalog.Member("sessions").Value()
			if err != nil {
				return nil, err
			}
			rows, _ := sessions.([]any)
			for _, each := range rows {
				if row := objectOf(each); row != nil && row.Value("archivedAt") != nil {
					if err := store.CloseSession(stringOf(row.Value("id"))); err != nil {
						return nil, err
					}
				}
			}
			return store, nil
		},
		func(any, host.NativeCloseReason, context.Context) error { return nil },
	)
	forwards := func() *contract.Forwards {
		value, err := opened.Value()
		if err != nil {
			panic(err)
		}
		return value.(*contract.Forwards)
	}
	// The store's list and Radius origin: a method's write resolves after the store told this source.
	listed := p2runtime.FollowNative(self,
		func(changed func()) func() { return forwards().Subscribe(changed) },
		func() (delta.JsonValue, error) {
			store := forwards()
			list := store.List()
			rows := make([]any, len(list))
			for at, forward := range list {
				rows[at] = contract.ForwardView(forward, nil, "")
			}
			var origin any
			if current := store.RadiusOrigin(); current != nil {
				origin = *current
			}
			return delta.JsonObjectOf("list", rows, "radiusOrigin", origin), nil
		},
	)
	storeSelf := self.(host.StoreSelf)
	storeSelf.Contribute(contract.ForwardsNative, &contract.ForwardsParts{Forwards: forwards})
	p2runtime.ContributeKeyed(storeSelf, sessionscontract.SessionArchived, p2runtime.Entry("ports", func(ctx context.Context, session string) error {
		// The write waits for the disk, not for the host.
		chordctx.Suspend(ctx)
		return forwards().CloseSession(session)
	}))
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"list": self.Derive([]any{listed}, func(values []any) (delta.JsonValue, error) {
				return objectOf(values[0]).Value("list"), nil
			}),
			"radiusOrigin": self.Derive([]any{listed}, func(values []any) (delta.JsonValue, error) {
				return objectOf(values[0]).Value("radiusOrigin"), nil
			}),
		},
	}}, nil
}

// titleOf is the title of the session with id among the catalog's rows.
func titleOf(sessions any, id string) string {
	rows, _ := sessions.([]any)
	for _, each := range rows {
		if row := objectOf(each); row != nil && row.Value("id") == id {
			return stringOf(row.Value("title"))
		}
	}
	return ""
}

func originOf(value any) *string {
	if text, ok := value.(string); ok {
		return &text
	}
	return nil
}

// viewOf is a forward as the owner and the agent see it: with who opened it and its addresses.
func viewOf(forward contract.Forward, origin *string, sessions any) *delta.JsonObject {
	title := ""
	if forward.Session != "" {
		title = titleOf(sessions, forward.Session)
	}
	return contract.ForwardView(forward, origin, title)
}

// servePorts serves `Ports`: the owner manages the forwards, a worker's tool calls open and close them; the worker's
// identity fixes its session.
func servePorts(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	who, err := p2runtime.FromChord(principal)
	if err != nil || !p2runtime.IsOwner(who) && !p2runtime.IsWorker(who) {
		return contracts.Implementation{}, p2runtime.Forbidden("ports are owner-only")
	}
	store := self.Get(contract.ForwardStore, delta.NewJsonObject(0))
	natives := contract.ForwardsOf(self)
	catalog := self.Get(sessionscontract.Catalog, delta.NewJsonObject(0))
	server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
	serverPort := func() (int, error) {
		info, err := server.Member("info").Value()
		if err != nil {
			return 0, err
		}
		port, _ := objectArgs(info).Value("port").(float64)
		return int(port), nil
	}
	forwards := func() (*contract.Forwards, error) {
		parts, err := natives()
		if err != nil {
			return nil, err
		}
		return parts.Forwards(), nil
	}
	if p2runtime.IsOwner(who) {
		view := self.Derive([]any{store.Member("list"), store.Member("radiusOrigin"), catalog.Member("sessions")}, func(values []any) (delta.JsonValue, error) {
			list, _ := values[0].([]any)
			origin := originOf(values[1])
			rows := make([]any, len(list))
			for at, each := range list {
				rows[at] = viewOf(forwardOf(objectOf(each)), origin, values[2])
			}
			return rows, nil
		})
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"forwards": view,
				"radiusOrigin": self.Derive([]any{store.Member("radiusOrigin")}, func(values []any) (delta.JsonValue, error) {
					return values[0], nil
				}),
			},
			Methods: map[string]contracts.MethodFunction{
				"add": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					chordctx.Suspend(ctx)
					in := objectArgs(args)
					port, _ := in.Value("port").(float64)
					var name *string
					if text, ok := in.Value("name").(string); ok {
						name = &text
					}
					own, err := serverPort()
					if err != nil {
						return nil, err
					}
					current, err := forwards()
					if err != nil {
						return nil, err
					}
					_, err = current.Add(int(port), name, []int{own}, in.Value("public") == true, "")
					return nil, err
				},
				"remove": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					chordctx.Suspend(ctx)
					current, err := forwards()
					if err != nil {
						return nil, err
					}
					return nil, current.Remove(stringOf(objectArgs(args).Value("id")))
				},
				"setPublic": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					chordctx.Suspend(ctx)
					in := objectArgs(args)
					current, err := forwards()
					if err != nil {
						return nil, err
					}
					return nil, current.SetPublic(stringOf(in.Value("id")), in.Value("public") == true)
				},
			},
		}}, nil
	}
	worker := who.(p2runtime.WorkerPrincipal)
	hosts := self.Get(p2runtime.Hosts, delta.NewJsonObject(0))
	return contracts.Implementation{Variant: "worker", Members: contracts.VariantImplementation{
		Methods: map[string]contracts.MethodFunction{
			"query": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				chordctx.Suspend(ctx)
				workers, err := hosts.Member("workers").Value()
				if err != nil {
					return nil, err
				}
				current := objectOf(objectArgs(workers).Value(worker.Session))
				if current == nil || current.Value("running") != true || current.Value("generation") != float64(worker.Generation) {
					return nil, p2runtime.Forbidden("stale worker")
				}
				row, err := catalog.Call("get", delta.JsonObjectOf("id", worker.Session), ctx)
				if err != nil {
					return nil, err
				}
				session := objectArgs(row)
				if session.Value("archivedAt") != nil {
					return nil, p2runtime.Forbidden("Session is archived")
				}
				query := objectArgs(objectArgs(args).Value("query"))
				action := stringOf(query.Value("action"))
				if env := stringOf(session.Value("env")); env != "" && action == "open" {
					return nil, fmt.Errorf("This session runs on %q: ports of remote environments cannot be forwarded yet, only ports of this device", env)
				}
				forwardsNow, err := forwards()
				if err != nil {
					return nil, err
				}
				own, err := serverPort()
				if err != nil {
					return nil, err
				}
				port, _ := query.Value("port").(float64)
				var opened *contract.Forward
				var closed *bool
				switch action {
				case "open":
					options := contract.OpenOptions{}
					if text, ok := query.Value("name").(string); ok {
						options.Name = &text
					}
					if flag, ok := query.Value("public").(bool); ok {
						options.Public = &flag
					}
					forward, err := forwardsNow.OpenForSession(worker.Session, int(port), options, []int{own})
					if err != nil {
						return nil, err
					}
					opened = &forward
				case "close":
					was, err := forwardsNow.ClosePort(int(port))
					if err != nil {
						return nil, err
					}
					closed = &was
				}
				sessions, err := catalog.Member("sessions").Value()
				if err != nil {
					return nil, err
				}
				origin := forwardsNow.RadiusOrigin()
				list := forwardsNow.List()
				rows := make([]any, len(list))
				for at, forward := range list {
					rows[at] = viewOf(forward, origin, sessions)
				}
				reply := delta.JsonObjectOf("forwards", rows, "addressesKnown", origin != nil)
				if opened != nil {
					reply.Set("forward", viewOf(*opened, origin, sessions))
				}
				if closed != nil {
					reply.Set("closed", *closed)
				}
				return reply, nil
			},
		},
	}}, nil
}

// forwardOf is a stored forward from its row of the list the store publishes.
func forwardOf(row *delta.JsonObject) contract.Forward {
	port, _ := row.Value("port").(float64)
	public, _ := row.Value("public").(bool)
	return contract.Forward{ID: stringOf(row.Value("id")), Port: int(port), Name: stringOf(row.Value("name")), Public: public, Session: stringOf(row.Value("session"))}
}
