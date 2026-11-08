package envs

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	envpkg "github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/envs/server.ts: the server facet's `EnvService` (the configuration observer, published, and its
// tests), the manager `EnvManagers` carries, the owner's `Envs` and `SandboxSecrets`, and the restart contribution that stops
// the workers of sessions using an edited environment once they are idle.

func objectOf(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

func stringOf(value any) string { s, _ := value.(string); return s }

// SessionsUsing are the sessions using environment name: working there, or with a subagent whose own location is there
// (archived sessions and subagents included). sessions is the catalog's rows, children the child store's lists by session.
func SessionsUsing(name string, sessions []any, children *delta.JsonObject) []*delta.JsonObject {
	var using []*delta.JsonObject
	for _, each := range sessions {
		session := objectOf(each)
		if session == nil {
			continue
		}
		if session.Value("env") == name {
			using = append(using, session)
			continue
		}
		list, _ := children.Value(stringOf(session.Value("id"))).([]any)
		for _, child := range list {
			if location := objectOf(objectOf(child).Value("location")); location != nil && location.Value("env") == name {
				using = append(using, session)
				break
			}
		}
	}
	return using
}

// configV2 is a configuration as versions 1 and 2 of `Envs` know it: sandboxes without `diskGb`.
func configV2(config any) any {
	object := objectOf(config)
	if object == nil || object.Value("type") != "gondolin" {
		return config
	}
	rest := object.Clone()
	rest.Delete("diskGb")
	return rest
}

func mapRows(drop ...string) func(delta.JsonValue) (delta.JsonValue, error) {
	return func(value delta.JsonValue) (delta.JsonValue, error) {
		rows, ok := value.([]any)
		if !ok {
			return value, nil
		}
		out := make([]any, len(rows))
		for at, each := range rows {
			row := objectOf(each)
			if row == nil {
				out[at] = each
				continue
			}
			mapped := row.Clone()
			for _, key := range drop {
				mapped.Delete(key)
			}
			if config, ok := mapped.Get("config"); ok {
				mapped.Set("config", configV2(config))
			}
			out[at] = mapped
		}
		return out, nil
	}
}

func same(value delta.JsonValue) (delta.JsonValue, error) { return value, nil }

// envsAccepts are pages built with older versions of `Envs`: 2 gets rows without sandbox disk sizes; 1 also without users,
// and its delete is never forced. Their saves are a subset of the current ones.
func envsAccepts() contracts.AcceptsTable {
	rows1, rows2 := mapRows("users"), mapRows()
	methodMapping := func(name string, args, result contracts.Mapping) contracts.Entry[contracts.MethodMapping] {
		return contracts.Entry[contracts.MethodMapping]{Name: name, Value: contracts.MethodMapping{Args: args, Result: result}}
	}
	ownerRows := func(rows contracts.Mapping) []contracts.Entry[[]contracts.Entry[contracts.StateMapping]] {
		return []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{{Name: "owner", Value: []contracts.Entry[contracts.StateMapping]{
			{Name: "envs", Value: contracts.StateMapping{Func: rows}},
		}}}
	}
	return contracts.AcceptsTable{
		{Name: "1", Value: contracts.VersionMappings{VariantState: ownerRows(rows1), Methods: []contracts.Entry[contracts.MethodMapping]{
			methodMapping("list", nil, rows1), methodMapping("save", same, nil), methodMapping("delete", same, nil),
		}}},
		{Name: "2", Value: contracts.VersionMappings{VariantState: ownerRows(rows2), Methods: []contracts.Entry[contracts.MethodMapping]{
			methodMapping("list", nil, rows2), methodMapping("save", same, nil),
		}}},
	}
}

func record(item schema.Schema) schema.Schema { return p2runtime.Record(item) }

// restartsDoc holds the configurations' revisions last seen (null before the first), and the workers to stop once idle.
var restartsDoc = must(documents.DefineState("p2.env-restarts", documents.StateOptions{
	Schema: p2runtime.Contract(schema.Obj(schema.Properties{
		schema.Prop("revisions", schema.Union([]schema.Schema{record(schema.Str()), schema.Nul()})),
		schema.Prop("pending", record(schema.Num())),
	})),
	Version: 1,
	Initial: func() *delta.JsonObject {
		return delta.JsonObjectOf("revisions", nil, "pending", delta.NewJsonObject(0))
	},
}))

// testsDoc holds the latest test of each environment, with the revision of the configuration it tested.
var testsDoc = must(documents.DefineState("p2.env-tests", documents.StateOptions{
	Schema: p2runtime.Contract(schema.Obj(schema.Properties{
		schema.Prop("tests", record(schema.Obj(schema.Properties{schema.Prop("revision", schema.Str()), schema.Prop("test", contract.EnvTestSchema)}))),
	})),
	Version: 1,
	Initial: func() *delta.JsonObject { return delta.JsonObjectOf("tests", delta.NewJsonObject(0)) },
}))

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

var serverFacet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := contract.ServeEnvManagers(facet); err != nil {
		return err
	}
	if err := p2runtime.Contribute(facet, "p2.env-restarts", registerRestarts); err != nil {
		return err
	}
	if err := facet.Serve(contract.EnvService, host.LocalFactory(serveEnvService), host.ServeOptions{}); err != nil {
		return err
	}
	if err := facet.Serve(contract.Envs, host.RemoteFactory(serveEnvs), host.ServeOptions{Accepts: envsAccepts()}); err != nil {
		return err
	}
	return facet.Serve(contract.SandboxSecrets, host.RemoteFactory(serveSandboxSecrets), host.ServeOptions{})
})

// registerRestarts stops the workers of the sessions using an environment whose configuration was edited (also through a
// subagent's own location, and through a host's sandboxes), once they are idle.
func registerRestarts(self host.StoreSelf) error {
	service := self.Get(contract.EnvService, delta.NewJsonObject(0))
	catalog := self.Get(sessionscontract.Catalog, delta.NewJsonObject(0))
	status := self.Get(statuscontract.StatusStore, delta.NewJsonObject(0))
	hosts := self.Get(p2runtime.Hosts, delta.NewJsonObject(0))
	store := self.Get(subagentscontract.ChildStore, delta.NewJsonObject(0))
	restarts := self.State(restartsDoc).(documents.SingletonHandle)
	changes := self.Derive([]any{
		service.Member("configs"), catalog.Member("sessions"), status.Member("statuses"), hosts.Member("workers"), store.Member("children"),
	}, func(values []any) (delta.JsonValue, error) {
		return delta.JsonObjectOf("configs", values[0], "sessions", values[1], "statuses", values[2], "workers", values[3], "children", values[4]), nil
	})
	self.On(changes, func(call chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
		return host.AwaitingCtx(ctx, func() error {
			now := objectOf(call.Value)
			configs, sessions, statuses := objectOf(now.Value("configs")), now.Value("sessions"), objectOf(now.Value("statuses"))
			workers, children := objectOf(now.Value("workers")), objectOf(now.Value("children"))
			rows, _ := sessions.([]any)
			revisions := chordjson.NewObject(configs.Len())
			for name, config := range configs.All() {
				revisions.Set(name, EnvRevision(config.(*chordjson.Object)))
			}
			current, err := restarts.Value()
			if err != nil {
				return err
			}
			previous := objectOf(current.Value("revisions"))
			if _, err := restarts.Change(ctx, func(draft *delta.Object) (any, error) {
				if err := draft.Set("revisions", revisions); err != nil {
					return nil, err
				}
				if previous == nil {
					return nil, nil
				}
				// Workers of the sessions using an edited environment (also through a subagent's own location).
				var edited []string
				for _, name := range append(previous.Keys(), revisions.Keys()...) {
					if previous.Value(name) != revisions.Value(name) && !slices.Contains(edited, name) {
						edited = append(edited, name)
					}
				}
				// A host's edit reaches its sandboxes too: their connections go through it.
				var affected []string
				add := func(name string) {
					if !slices.Contains(affected, name) {
						affected = append(affected, name)
					}
				}
				for _, name := range edited {
					add(name)
					for _, sandbox := range SandboxesOn(configs, name) {
						add(sandbox.Name)
					}
				}
				pending := draft.Object("pending")
				for _, name := range affected {
					for _, session := range SessionsUsing(name, rows, children) {
						id := stringOf(session.Value("id"))
						if worker := objectOf(workers.Value(id)); worker != nil && worker.Value("running") == true {
							if err := pending.Set(id, worker.Value("generation")); err != nil {
								return nil, err
							}
						}
					}
				}
				return nil, nil
			}); err != nil {
				return err
			}
			after, err := restarts.Value()
			if err != nil {
				return err
			}
			waiting := objectOf(after.Value("pending"))
			for _, session := range waiting.Keys() {
				generation := waiting.Value(session)
				worker := objectOf(workers.Value(session))
				running := worker != nil && worker.Value("running") == true
				idle := false
				if tasks, ok := objectOf(statuses.Value(session)).Value("tasks").(float64); ok {
					idle = tasks == 0
				}
				if !running || worker.Value("generation") != generation || idle {
					if _, err := restarts.Change(ctx, func(draft *delta.Object) (any, error) {
						draft.Object("pending").Delete(session)
						return nil, nil
					}); err != nil {
						return err
					}
					if running && worker.Value("generation") == generation {
						if _, err := hosts.Call("stop", delta.JsonObjectOf("session", session), ctx); err != nil {
							return err
						}
					}
				}
			}
			return nil
		})
	})
	return nil
}

func serveEnvService(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
	catalog := self.Get(sessionscontract.Catalog, delta.NewJsonObject(0))
	children := self.Get(subagentscontract.ChildStore, delta.NewJsonObject(0))
	tested := self.State(testsDoc).(documents.SingletonHandle)
	dataDir := func() (string, error) {
		info, err := server.Member("info").Value()
		if err != nil {
			return "", err
		}
		return stringOf(objectOf(info).Value("dataDir")), nil
	}
	opened := self.Open(
		func(ctx context.Context) (any, error) {
			directory, err := dataDir()
			if err != nil {
				return nil, err
			}
			store := NewEnvStore(directory)
			if err := store.Start(ctx); err != nil {
				store.Close()
				return nil, err
			}
			return store, nil
		},
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*EnvStore).Close()
			return nil
		},
	)
	// The sessions using an environment, read where the call runs (a method handler).
	uses := func(_ context.Context, name string) (int, error) {
		rows, err := catalog.Member("sessions").Value()
		if err != nil {
			return 0, err
		}
		kids, err := children.Member("children").Value()
		if err != nil {
			return 0, err
		}
		list, _ := rows.([]any)
		return len(SessionsUsing(name, list, objectOf(kids))), nil
	}
	managerOpened := self.Open(
		func(context.Context) (any, error) {
			directory, err := dataDir()
			if err != nil {
				return nil, err
			}
			return NewManager(directory, uses, nil, nil), nil
		},
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*Manager).Close()
			return nil
		},
	)
	store := func() *EnvStore {
		value, err := opened.Value()
		if err != nil {
			panic(err)
		}
		return value.(*EnvStore)
	}
	manager := func() (*Manager, error) {
		value, err := managerOpened.Value()
		if err != nil {
			return nil, err
		}
		return value.(*Manager), nil
	}
	self.(host.StoreSelf).Contribute(contract.EnvManagers, &contract.EnvManagerParts{Manager: &lazyManager{get: manager}})
	// envs.json as the store last read it validly, and why the last read failed: `reload` resolves after the store told
	// this source.
	snapshot := p2runtime.FollowNative(self,
		func(changed func()) func() { return store().Subscribe(func(EnvSnapshot) { changed() }) },
		func() (delta.JsonValue, error) {
			read := store().Snapshot()
			var failure any
			if read.Error != "" {
				failure = read.Error
			}
			return delta.JsonObjectOf("configs", read.Configs, "error", failure), nil
		},
	)
	member := func(key string) chord.Watchable[delta.JsonValue] {
		return self.Derive([]any{snapshot}, func(values []any) (delta.JsonValue, error) {
			return values[0].(*delta.JsonObject).Value(key), nil
		})
	}
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"configs": member("configs"),
			"tests": self.Derive([]any{tested}, func(values []any) (delta.JsonValue, error) {
				return values[0].(*delta.JsonObject).Value("tests"), nil
			}),
			"failure": member("error"),
		},
		Methods: map[string]contracts.MethodFunction{
			"test": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				chordctx.Suspend(ctx)
				name := stringOf(objectOf(args).Value("name"))
				m, err := manager()
				if err != nil {
					return nil, err
				}
				config, err := m.Config(ctx, name)
				if err != nil {
					return nil, err
				}
				test, err := m.Test(ctx, name)
				if err != nil {
					return nil, err
				}
				_, err = tested.Change(ctx, func(draft *delta.Object) (any, error) {
					return nil, draft.Object("tests").Set(name, delta.JsonObjectOf("revision", EnvRevision(config), "test", test))
				})
				return test, err
			},
			"reload": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				// The store's load needs the loop (its listeners follow the section's state): give it back before waiting.
				chordctx.Suspend(ctx)
				return nil, store().Reload(ctx)
			},
		},
	}}, nil
}

func directoryJSON(listing p2runtime.DirectoryListing) delta.JsonValue {
	var parent any
	if listing.Parent != nil {
		parent = *listing.Parent
	}
	directories := make([]any, len(listing.Directories))
	for at, directory := range listing.Directories {
		directories[at] = directory
	}
	return delta.JsonObjectOf("path", listing.Path, "parent", parent, "directories", directories, "truncated", listing.Truncated)
}

// ownerOnly is the factory's check: environments are the owner's.
func ownerOnly(principal data.Principal, what string) error {
	who, err := p2runtime.FromChord(principal)
	if err != nil || !p2runtime.IsOwner(who) {
		return p2runtime.Forbidden(what)
	}
	return nil
}

// serveEnvs serves `Envs`: the owner's environments and sandboxes.
func serveEnvs(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	if err := ownerOnly(principal, "environments are owner-only"); err != nil {
		return contracts.Implementation{}, err
	}
	service := self.Get(contract.EnvService, delta.NewJsonObject(0))
	managers := contract.EnvManagersOf(self)
	catalog := self.Get(sessionscontract.Catalog, delta.NewJsonObject(0))
	store := self.Get(subagentscontract.ChildStore, delta.NewJsonObject(0))
	manager := func() (*Manager, error) {
		parts, err := managers()
		if err != nil {
			return nil, err
		}
		return parts.Manager.(*lazyManager).get()
	}
	envs := self.Derive([]any{service.Member("configs"), catalog.Member("sessions"), service.Member("tests"), store.Member("children")},
		func(values []any) (delta.JsonValue, error) {
			configs, sessions, tests, children := objectOf(values[0]), values[1], objectOf(values[2]), objectOf(values[3])
			rows, _ := sessions.([]any)
			names := configs.Keys()
			slices.SortStableFunc(names, p2runtime.LocaleCompare)
			out := make([]any, 0, len(names))
			for _, name := range names {
				config := configs.Value(name).(*chordjson.Object)
				using := SessionsUsing(name, rows, children)
				users := make([]any, len(using))
				for at, session := range using {
					users[at] = delta.JsonObjectOf("id", session.Value("id"), "title", session.Value("title"), "main", session.Value("main"), "archived", session.Value("archivedAt") != nil)
				}
				row := delta.JsonObjectOf("name", name, "config", config, "sessions", float64(len(using)), "users", users)
				if last := objectOf(tests.Value(name)); last != nil && last.Value("revision") == EnvRevision(config) {
					row.Set("test", last.Value("test"))
				}
				out = append(out, row)
			}
			return out, nil
		})
	// call runs fn with the manager.
	call := func(fn func(m *Manager, ctx context.Context, args *delta.JsonObject) (delta.JsonValue, error)) contracts.MethodFunction {
		return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			// The manager's work is files, processes and SSH: not the section's.
			chordctx.Suspend(ctx)
			m, err := manager()
			if err != nil {
				return nil, err
			}
			return fn(m, ctx, objectOf(args))
		}
	}
	reload := func(ctx context.Context) error {
		_, err := service.Call("reload", nil, ctx)
		return err
	}
	keyAnswer := func(m *Manager, publicKey, key string) (delta.JsonValue, error) {
		path, err := m.KeyPath(key)
		if err != nil {
			return nil, err
		}
		return delta.JsonObjectOf("publicKey", publicKey, "identityFile", path), nil
	}
	return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"envs": envs, "failure": host.Expose(service.Member("failure"))},
		Methods: map[string]contracts.MethodFunction{
			"list": call(func(m *Manager, ctx context.Context, _ *delta.JsonObject) (delta.JsonValue, error) {
				return m.List(ctx)
			}),
			"warm": call(func(m *Manager, ctx context.Context, _ *delta.JsonObject) (delta.JsonValue, error) {
				return nil, m.Warm(ctx)
			}),
			"save": call(func(m *Manager, ctx context.Context, args *delta.JsonObject) (delta.JsonValue, error) {
				if err := m.Save(ctx, stringOf(args.Value("name")), args.Value("config")); err != nil {
					return nil, err
				}
				return nil, reload(ctx)
			}),
			"delete": call(func(m *Manager, ctx context.Context, args *delta.JsonObject) (delta.JsonValue, error) {
				if err := m.Delete(ctx, stringOf(args.Value("name")), args.Value("force") == true); err != nil {
					return nil, err
				}
				return nil, reload(ctx)
			}),
			"test": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return service.Call("test", args, ctx)
			},
			"acceptHostKey": call(func(m *Manager, ctx context.Context, args *delta.JsonObject) (delta.JsonValue, error) {
				lines, _ := args.Value("lines").([]any)
				texts := make([]string, len(lines))
				for at, line := range lines {
					texts[at] = stringOf(line)
				}
				return nil, m.AcceptHostKey(ctx, stringOf(args.Value("name")), texts)
			}),
			"forgetHostKey": call(func(m *Manager, ctx context.Context, args *delta.JsonObject) (delta.JsonValue, error) {
				return nil, m.ForgetHostKey(ctx, stringOf(args.Value("name")))
			}),
			// Every owner (device, associated device, Radius) manages keys, as in Pi Mobile; Envs' factory excludes guests.
			"generateKey": call(func(m *Manager, ctx context.Context, args *delta.JsonObject) (delta.JsonValue, error) {
				publicKey, err := m.GenerateKey(ctx, stringOf(args.Value("name")))
				if err != nil {
					return nil, err
				}
				if err := reload(ctx); err != nil {
					return nil, err
				}
				return keyAnswer(m, publicKey, stringOf(args.Value("name")))
			}),
			"uploadKey": call(func(m *Manager, ctx context.Context, args *delta.JsonObject) (delta.JsonValue, error) {
				publicKey, err := m.UploadKey(ctx, stringOf(args.Value("name")), stringOf(args.Value("key")))
				if err != nil {
					return nil, err
				}
				if err := reload(ctx); err != nil {
					return nil, err
				}
				return keyAnswer(m, publicKey, stringOf(args.Value("name")))
			}),
			"publicKey": call(func(m *Manager, _ context.Context, args *delta.JsonObject) (delta.JsonValue, error) {
				publicKey, err := m.PublicKey(stringOf(args.Value("name")))
				if err != nil || publicKey == "" {
					return nil, err
				}
				return publicKey, nil
			}),
			"listDirectory": call(func(m *Manager, ctx context.Context, args *delta.JsonObject) (delta.JsonValue, error) {
				var listing p2runtime.DirectoryListing
				var err error
				if env := stringOf(args.Value("env")); env != "" {
					listing, err = m.ListDirectory(ctx, env, stringOf(args.Value("path")))
				} else {
					listing, err = p2runtime.ListLocalDirectory(stringOf(args.Value("path")))
				}
				if err != nil {
					return nil, err
				}
				return directoryJSON(listing), nil
			}),
			"sandboxStatus": call(func(m *Manager, ctx context.Context, args *delta.JsonObject) (delta.JsonValue, error) {
				return m.SandboxStatus(ctx, stringOf(args.Value("name")))
			}),
			"sandboxStart": call(func(m *Manager, ctx context.Context, args *delta.JsonObject) (delta.JsonValue, error) {
				return m.SandboxStart(ctx, stringOf(args.Value("name")))
			}),
			"sandboxStop": call(func(m *Manager, ctx context.Context, args *delta.JsonObject) (delta.JsonValue, error) {
				return m.SandboxStop(ctx, stringOf(args.Value("name")))
			}),
			"sandboxResetDisk": call(func(m *Manager, ctx context.Context, args *delta.JsonObject) (delta.JsonValue, error) {
				return m.SandboxResetDisk(ctx, stringOf(args.Value("name")))
			}),
		},
	}}, nil
}

// serveSandboxSecrets serves `SandboxSecrets` (owner only): values the sandbox's HTTP proxy injects into outbound request
// headers for their hosts; changes apply when the sandbox's VM starts next.
func serveSandboxSecrets(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	if err := ownerOnly(principal, "sandbox secrets are owner-only"); err != nil {
		return contracts.Implementation{}, err
	}
	service := self.Get(contract.EnvService, delta.NewJsonObject(0))
	managers := contract.EnvManagersOf(self)
	manager := func() (*Manager, error) {
		parts, err := managers()
		if err != nil {
			return nil, err
		}
		return parts.Manager.(*lazyManager).get()
	}
	return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
		Methods: map[string]contracts.MethodFunction{
			"list": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				chordctx.Suspend(ctx)
				m, err := manager()
				if err != nil {
					return nil, err
				}
				infos, err := m.Secrets(ctx, stringOf(objectOf(args).Value("name")))
				if err != nil {
					return nil, err
				}
				out := make([]any, len(infos))
				for at, info := range infos {
					out[at] = info.JSON()
				}
				return out, nil
			},
			"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				chordctx.Suspend(ctx)
				m, err := manager()
				if err != nil {
					return nil, err
				}
				in := objectOf(args)
				hosts, _ := in.Value("hosts").([]any)
				change := SecretChange{Preset: stringOf(in.Value("preset")), Hosts: make([]string, len(hosts))}
				for at, host := range hosts {
					change.Hosts[at] = stringOf(host)
				}
				if value, ok := in.Value("value").(string); ok {
					change.Value = &value
				}
				if err := m.SetSecret(ctx, stringOf(in.Value("sandbox")), stringOf(in.Value("name")), change, in.Value("addHosts") == true); err != nil {
					return nil, err
				}
				_, err = service.Call("reload", nil, ctx)
				return nil, err
			},
			"delete": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				chordctx.Suspend(ctx)
				m, err := manager()
				if err != nil {
					return nil, err
				}
				in := objectOf(args)
				return nil, m.DeleteSecret(ctx, stringOf(in.Value("sandbox")), stringOf(in.Value("name")))
			},
		},
	}}, nil
}

// lazyManager is the manager `EnvManagers` carries: it opens with the service (Pi's `get manager()` reads it at each use).
type lazyManager struct{ get func() (*Manager, error) }

// WorkingDirectory is a session's working directory on environment env.
func (l *lazyManager) WorkingDirectory(ctx context.Context, env, cwd string, create bool) (string, error) {
	// Resolving it is SSH: not the section's work.
	chordctx.Suspend(ctx)
	m, err := l.get()
	if err != nil {
		return "", err
	}
	return m.WorkingDirectory(ctx, env, cwd, create)
}

// Connection is a pi-env connection to environment name; a manager that did not open answers its error at the first request.
func (l *lazyManager) Connection(name string, config *chordjson.Object) *envpkg.Connection {
	m, err := l.get()
	if err != nil {
		return envpkg.NewConnection(envpkg.ConnectionOptions{CommandFunc: func(context.Context) ([]string, error) { return nil, err }})
	}
	return m.Connection(name, config)
}
