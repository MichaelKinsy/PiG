// Package locations is p2's locations plugin (apps/p2/plugins/locations): the location tools (`location_envs`,
// `location_dirs`, `location_move`) with which an agent lists the environments it may work on and their directories and
// moves itself, and the main agent's deferred move.
package locations

import (
	"context"
	"fmt"
	"slices"

	envspkg "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	envscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/locations/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/locations/server.ts.

var serverFacet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return facet.Serve(contract.AgentLocations, host.RemoteFactory(serveAgentLocations), host.ServeOptions{})
})

func objectOf(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

func objectArgs(args delta.JsonValue) *delta.JsonObject {
	if object := objectOf(args); object != nil {
		return object
	}
	return delta.NewJsonObject(0)
}

func stringOf(value any) string { s, _ := value.(string); return s }

// envInfos are the configured environments as an agent sees them, by name: a configuration's last test counts only while
// it tests the configuration as it is now.
func envInfos(configs, tests *delta.JsonObject) []any {
	names := configs.Keys()
	slices.SortFunc(names, p2runtime.LocaleCompare)
	infos := make([]any, 0, len(names))
	for _, name := range names {
		config := objectOf(configs.Value(name))
		kind := stringOf(config.Value("type"))
		info := delta.JsonObjectOf("name", name, "type", kind, "host", config.Value("host"))
		if kind == "ssh" {
			if user, ok := config.Get("user"); ok {
				info.Set("user", user)
			}
			if port, ok := config.Get("port"); ok {
				info.Set("port", port)
			}
		}
		if last := objectOf(objectOf(tests.Value(name)).Value("test")); last != nil &&
			objectOf(tests.Value(name)).Value("revision") == envspkg.EnvRevision(config) {
			if last.Value("ok") == true {
				info.Set("tested", delta.JsonObjectOf("ok", true, "platform", last.Value("platform"), "arch", last.Value("arch")))
			} else {
				info.Set("tested", delta.JsonObjectOf("ok", false, "error", last.Value("error")))
			}
		}
		infos = append(infos, info)
	}
	return infos
}

// serveAgentLocations serves `AgentLocations`: only a current worker generation, for its own session; the worker checked
// that the owner started the turn.
func serveAgentLocations(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	who, err := p2runtime.FromChord(principal)
	worker, isWorker := who.(p2runtime.WorkerPrincipal)
	if err != nil || !isWorker {
		return contracts.Implementation{}, p2runtime.Forbidden("location tools are for session workers")
	}
	hosts := self.Get(p2runtime.Hosts, delta.NewJsonObject(0))
	envs := self.Get(envscontract.EnvService, delta.NewJsonObject(0))
	managers := envscontract.EnvManagersOf(self)
	moves := self.Get(sessionscontract.SessionMoves, delta.NewJsonObject(0))
	catalog := self.Get(sessionscontract.Catalog, delta.NewJsonObject(0))
	current := func() error {
		workers, err := hosts.Member("workers").Value()
		if err != nil {
			return err
		}
		state := objectOf(objectArgs(workers).Value(worker.Session))
		if state == nil || state.Value("running") != true || state.Value("generation") != float64(worker.Generation) {
			return p2runtime.Forbidden("stale worker")
		}
		return nil
	}
	configsOf := func() (*delta.JsonObject, error) {
		value, err := envs.Member("configs").Value()
		return objectArgs(value), err
	}
	return contracts.Implementation{Variant: "worker", Members: contracts.VariantImplementation{
		Methods: map[string]contracts.MethodFunction{
			"envs": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				if err := current(); err != nil {
					return nil, err
				}
				configs, err := configsOf()
				if err != nil {
					return nil, err
				}
				tests, err := envs.Member("tests").Value()
				if err != nil {
					return nil, err
				}
				return delta.JsonObjectOf("envs", envInfos(configs, objectArgs(tests))), nil
			},
			"directories": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				if err := current(); err != nil {
					return nil, err
				}
				in := objectArgs(args)
				path := stringOf(in.Value("path"))
				if name := stringOf(in.Value("env")); name != "" {
					parts, err := managers()
					if err != nil {
						return nil, err
					}
					listing, err := parts.Manager.ListDirectory(ctx, name, path)
					if err != nil {
						return nil, err
					}
					return envspkg.DirectoryJSON(listing), nil
				}
				// Reading this device's folders is the disk's work, not the section's.
				chordctx.Suspend(ctx)
				listing, err := p2runtime.ListLocalDirectory(path)
				if err != nil {
					return nil, err
				}
				return envspkg.DirectoryJSON(listing), nil
			},
			"move": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				if err := current(); err != nil {
					return nil, err
				}
				in := objectArgs(args)
				// The location tools refuse this already; the server keeps a sandboxed session in its sandbox too.
				sessions, err := catalog.Member("sessions").Value()
				if err != nil {
					return nil, err
				}
				var from string
				rows, _ := sessions.([]any)
				for _, each := range rows {
					if row := objectOf(each); row != nil && row.Value("id") == worker.Session {
						from = stringOf(row.Value("env"))
					}
				}
				to := stringOf(in.Value("env"))
				if from != "" && from != to {
					configs, err := configsOf()
					if err != nil {
						return nil, err
					}
					if envspkg.IsSandbox((*chordjson.Object)(configs), from) {
						return nil, p2runtime.Forbidden(fmt.Sprintf("this session works in sandbox %s and cannot leave it", from))
					}
				}
				return moves.Call("relocate", delta.JsonObjectOf("id", worker.Session, "env", in.Value("env"), "cwd", in.Value("cwd")), ctx)
			},
		},
	}}, nil
}
