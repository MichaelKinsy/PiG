package mcp

import (
	"context"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// view is the view of the manager: the owner's, a session worker's, nobody else's.
func view(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	who, err := p2runtime.FromChord(principal)
	if err != nil || (!p2runtime.IsOwner(who) && !p2runtime.IsWorker(who)) {
		return contracts.Implementation{}, p2runtime.Forbidden("MCP servers are for the owner and session workers")
	}
	manager := self.Get(contract.McpManager, delta.NewJsonObject(0))
	if p2runtime.IsOwner(who) {
		forward := func(method string) contracts.MethodFunction {
			return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return manager.Call(method, args, ctx)
			}
		}
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"instances": passThrough(self, manager.Member("instances")),
				"errors": self.Derive([]any{manager.Member("errors"), manager.Member("leases")}, func(values []any) (delta.JsonValue, error) {
					out := append([]any{}, asList(values[0])...)
					for _, each := range asList(values[1]) {
						lease := objectOf(each)
						for _, problem := range asList(lease.Value("errors")) {
							out = append(out, "session "+stringOf(lease.Value("session"))+": "+stringOf(problem))
						}
					}
					return out, nil
				}),
				"signIns": passThrough(self, manager.Member("signIns")),
			},
			Methods: map[string]contracts.MethodFunction{
				"restart": forward("restart"),
				"signIn": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return manager.Call("beginSignIn", delta.JsonObjectOf("server", objectArgs(args).Value("server"), "redirect", delta.JsonObjectOf("kind", "loopback")), ctx)
				},
				"paste":        forward("pasteSignIn"),
				"cancelSignIn": forward("cancelSignIn"),
				"signOut":      forward("signOut"),
			},
		}}, nil
	}
	return workerView(who.(p2runtime.WorkerPrincipal), manager, self), nil
}

// workerView is a session worker's view: its session's servers (without the owner's errors), its locations and calls.
func workerView(principal p2runtime.WorkerPrincipal, manager *host.Reference, self host.Self) contracts.Implementation {
	session, generation := principal.Session, principal.Generation
	current := currentWorker(self.Get(p2runtime.Hosts, delta.NewJsonObject(0)), principal)
	// When the view ends (its worker's link went, and the linger passed), its leases go. A reload keeps them: the next
	// attempt is the same worker.
	self.Open(
		func(context.Context) (any, error) { return nil, nil },
		func(_ any, reason host.NativeCloseReason, ctx context.Context) error {
			if reason.Kind != host.NativeCloseEnd {
				return nil
			}
			_, err := manager.Call("releaseLeases", delta.JsonObjectOf("session", session, "generation", float64(generation)), ctx)
			return err
		})
	forward := func(method string, keys ...string) contracts.MethodFunction {
		return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			if err := current(ctx); err != nil {
				return nil, err
			}
			// Pi builds the call from the worker's identity and the method's own arguments: args never name another lease.
			in := objectArgs(args)
			call := delta.NewJsonObject(0)
			call.Set("session", session)
			call.Set("generation", float64(generation))
			call.Set("incarnation", p2runtime.CtxOf(ctx).Incarnation())
			for _, key := range keys {
				if value := in.Value(key); value != nil {
					call.Set(key, value)
				}
			}
			return manager.Call(method, call, ctx)
		}
	}
	return contracts.Implementation{Variant: "worker", Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"servers": self.Derive([]any{manager.Member("instances"), manager.Member("leases")}, func(values []any) (delta.JsonValue, error) {
				used := map[string]bool{}
				for _, each := range asList(values[1]) {
					lease := objectOf(each)
					if lease.Value("session") == session {
						for _, id := range asList(lease.Value("instances")) {
							used[stringOf(id)] = true
						}
					}
				}
				out := []any{}
				for _, each := range asList(values[0]) {
					instance := objectOf(each)
					if !used[stringOf(instance.Value("id"))] {
						continue
					}
					shown := delta.NewJsonObject(instance.Len())
					for key, value := range instance.All() {
						if key != "error" {
							shown.Set(key, value)
						}
					}
					out = append(out, shown)
				}
				return out, nil
			}),
			"errors": self.Derive([]any{manager.Member("leases")}, func(values []any) (delta.JsonValue, error) {
				out := []any{}
				for _, each := range asList(values[0]) {
					lease := objectOf(each)
					if lease.Value("session") == session {
						out = append(out, asList(lease.Value("errors"))...)
					}
				}
				return out, nil
			}),
		},
		Methods: map[string]contracts.MethodFunction{"setLocations": forward("setLocations", "locations", "projects"), "call": forward("call", "location", "server", "tool", "args")},
	}}
}

// currentWorker refuses all but the session's running current generation, as the location tools do. A worker may ask before
// the server published its start: the check waits for that (the wait ends with the caller's call).
func currentWorker(hosts *host.Reference, principal p2runtime.WorkerPrincipal) func(ctx context.Context) error {
	session, generation := principal.Session, float64(principal.Generation)
	known := func(state *delta.JsonObject) bool {
		if state == nil {
			return false
		}
		published, _ := state.Value("generation").(float64)
		running, _ := state.Value("running").(bool)
		return published > generation || (published == generation && running)
	}
	return func(ctx context.Context) error {
		workers, err := hosts.Member("workers").Value()
		if err != nil {
			return err
		}
		if !known(objectOf(objectOf(workers).Value(session))) {
			if workers, err = p2runtime.Until(ctx, hosts.Member("workers"), func(value delta.JsonValue) bool {
				return known(objectOf(objectOf(value).Value(session)))
			}); err != nil {
				return err
			}
		}
		state := objectOf(objectOf(workers).Value(session))
		if state == nil || state.Value("running") != true || state.Value("generation") != generation {
			return p2runtime.Forbidden("stale worker")
		}
		return nil
	}
}

// passThrough is the member of another object as a state member of this one.
func passThrough(self host.Self, member *host.ReferenceMember) chord.Watchable[delta.JsonValue] {
	return self.Derive([]any{member}, func(values []any) (delta.JsonValue, error) { return values[0], nil })
}
