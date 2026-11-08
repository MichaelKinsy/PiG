package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	envcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	pimcp "github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/mcp/server.ts: the MCP plugin's server facet: the manager (the owner's configuration, the leases of
// session workers with their projects' configurations, the environments and where sessions work, the supervisor of
// instances, the OAuth sign-ins) and its view per principal: a session worker's (its locations and calls, fenced to the
// session's current worker; its leases released when the view ends) and the owner's (every instance with its errors and
// sign-ins, restart, signing in and out); and the sign-in routes browsers open.

// EnvConnector is how the manager reaches environments, given its object (whose `EnvService` it declared).
type EnvConnector func(self host.Self) ConnectEnv

// viewLinger is how long a worker's view outlives its last reference: a worker object restarting does not end its leases.
const viewLinger = 30 * time.Second

// connectionManager is the part of the envs plugin's SSH manager that connects (`EnvManager.Connection`).
type connectionManager interface {
	Connection(name string, config *chordjson.Object) *env.Connection
}

// sshEnvironments is the environments of envs.json through the envs plugin's SSH manager (its keys and known hosts; read at
// each connect, as it reopens with a reload).
func sshEnvironments(self host.Self) ConnectEnv {
	managers := envcontract.EnvManagersOf(self)
	return func(name string, config *chordjson.Object) *env.Connection {
		parts, err := managers()
		if err == nil {
			if manager, ok := parts.Manager.(connectionManager); ok {
				return manager.Connection(name, config)
			}
			err = errors.New("the SSH manager does not connect to environments")
		}
		failure := err
		return env.NewConnection(env.ConnectionOptions{CommandFunc: func(context.Context) ([]string, error) { return nil, failure }})
	}
}

// Server is the plugin's server facet.
var Server = ServerFacet(viewLinger, sshEnvironments)

// ServerFacet is the facet, with views lingering linger after their last reference, reaching environments through connector
// (tests shorten the one and replace the other).
func ServerFacet(linger time.Duration, connector EnvConnector) host.Facet {
	return host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		if err := facet.Serve(contract.McpManager, host.LocalFactory(managerFactory(connector)), host.ServeOptions{}); err != nil {
			return err
		}
		if err := facet.Serve(contract.Mcp, host.RemoteFactory(view), host.ServeOptions{Linger: &linger}); err != nil {
			return err
		}
		return p2runtime.Contribute(facet, "p2.mcp-routes", signInRoutes)
	})
}

// The documents the manager keeps in memory across a reload of the plugin.
var (
	// leasedState holds the leases of session workers, so the new supervisor starts what they use.
	leasedState = mustState("p2.mcp.leases", schema.Obj(schema.Properties{schema.Prop("leases", schema.Arr(schema.Obj(schema.Properties{
		schema.Prop("session", schema.Str()),
		schema.Prop("location", p2runtime.LocationSchema),
		schema.Prop("project", schema.Union([]schema.Schema{
			schema.Obj(schema.Properties{schema.Prop("kind", p2runtime.Literal("none"))}),
			schema.Obj(schema.Properties{schema.Prop("kind", p2runtime.Literal("file")), schema.Prop("text", schema.Str())}),
			schema.Obj(schema.Properties{schema.Prop("kind", p2runtime.Literal("unreadable")), schema.Prop("error", schema.Str())}),
		})),
		schema.Prop("generation", schema.Int()),
		schema.Prop("incarnation", schema.Str()),
	})))}), func() *delta.JsonObject { return delta.JsonObjectOf("leases", []any{}) })
	// mementoState is what a restarted manager's natives start from (in memory, kept across a reload's restart; never
	// shown): the servers last read validly (a new reader may first see a save caught half-way) and the instances as last
	// listed (their tools stay listed until their servers answer again).
	mementoState = mustState("p2.mcp.memento", schema.Obj(schema.Properties{
		schema.Prop("servers", schema.Unknown()),
		schema.Prop("errors", schema.Arr(schema.Str())),
		schema.Prop("instances", schema.Arr(contract.InstanceSchema)),
	}), func() *delta.JsonObject {
		return delta.JsonObjectOf("servers", []any{}, "errors", []any{}, "instances", []any{})
	})
)

func mustState(name string, shape schema.Schema, initial func() *delta.JsonObject) *documents.StateDefinition {
	definition, err := documents.DefineState(name, documents.StateOptions{Schema: p2runtime.Contract(shape), Version: 1, Initial: initial})
	if err != nil {
		panic(err)
	}
	return definition
}

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func objectArgs(args delta.JsonValue) *delta.JsonObject {
	if object := objectOf(args); object != nil {
		return object
	}
	return delta.NewJsonObject(0)
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}

func intOf(value any) int {
	number, _ := value.(float64)
	return int(number)
}

// placer is `place`, computed once per input: the routine, the leases' view and the methods share it. Where sessions work is
// compared by content: the catalog changes with every turn's activity, mostly without moving a session.
type placer struct {
	mu   sync.Mutex
	last *placed
}

type placed struct {
	config, leases, envs any
	at                   string
	placement            Placement
}

// sameValue is whether a and b are the same JSON value (one object or array, unchanged).
func sameValue(a, b any) bool {
	if x, ok := a.([]any); ok {
		y, ok := b.([]any)
		return ok && len(x) == len(y) && (len(x) == 0 || &x[0] == &y[0])
	}
	// Two values of one uncomparable type (an object) are not compared: they are taken as changed.
	if kind := reflect.TypeOf(a); kind != nil && kind == reflect.TypeOf(b) && !kind.Comparable() {
		return false
	}
	return a == b
}

func (p *placer) place(config, leases, envs, work, locks delta.JsonValue) (Placement, error) {
	workText, err := chordjson.Marshal([]any{work, locks})
	if err != nil {
		return Placement{}, err
	}
	at := string(workText)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last != nil && sameValue(p.last.config, config) && sameValue(p.last.leases, leases) && sameValue(p.last.envs, envs) && p.last.at == at {
		return p.last.placement, nil
	}
	state, err := ownerStateOf(config)
	if err != nil {
		return Placement{}, err
	}
	held, err := leasesOf(objectOf(leases).Value("leases"))
	if err != nil {
		return Placement{}, err
	}
	environments := objectOf(envs)
	if environments == nil {
		environments = delta.NewJsonObject(0)
	}
	sessions := WorkEnvironments{}
	for session, each := range objectOf(work).All() {
		list, _ := each.([]any)
		for _, entry := range list {
			sessions[session] = append(sessions[session], stringOf(entry))
		}
		if _, ok := sessions[session]; !ok {
			sessions[session] = []string{}
		}
	}
	lockForms, err := fromJSON[map[string]p2runtime.SessionLock](locks)
	if err != nil {
		return Placement{}, err
	}
	placement := Place(state.OwnerConfig, held, environments, sessions, lockForms)
	p.last = &placed{config: config, leases: leases, envs: envs, at: at, placement: placement}
	return placement, nil
}

// memoOwner is the owner configuration of the memento, which keeps the servers as JSON.
func memoOwner(value delta.JsonValue) (OwnerConfig, error) {
	object := objectOf(value)
	state, err := ownerStateOf(delta.JsonObjectOf("servers", object.Value("servers"), "errors", object.Value("errors"), "failure", nil))
	return state.OwnerConfig, err
}

// managerFactory is the manager: the file, the supervisor and the sign-ins as natives it follows, the leases as its
// document, and the routine that starts and closes instances as they change.
func managerFactory(connector EnvConnector) func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
	return func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
		envs := self.Get(envcontract.EnvService, delta.NewJsonObject(0))
		catalog := self.Get(sessionscontract.Catalog, delta.NewJsonObject(0))
		store := self.Get(subagentscontract.ChildStore, delta.NewJsonObject(0))
		locks := self.Get(sessionscontract.SessionLocks, delta.NewJsonObject(0))
		leased := self.State(leasedState).(*documents.Singleton)
		memento := self.State(mementoState).(*documents.Singleton)
		places := &placer{}
		connect := connector(self)
		work := self.Derive([]any{catalog.Member("sessions"), store.Member("children")}, func(values []any) (delta.JsonValue, error) {
			_, shown := workOf(values[0], values[1])
			return shown, nil
		})
		dataDir := func() (string, error) {
			info, err := server.Member("info").Value()
			return stringOf(objectOf(info).Value("dataDir")), err
		}
		memoValue := func() (*delta.JsonObject, error) { return memento.Value() }

		file := self.Open(
			func(ctx context.Context) (any, error) {
				dir, err := dataDir()
				if err != nil {
					return nil, err
				}
				last, err := memoValue()
				if err != nil {
					return nil, err
				}
				remembered, err := memoOwner(last)
				if err != nil {
					return nil, err
				}
				opened := NewOwnerConfigFile(dir, remembered)
				if err := opened.Start(ctx); err != nil {
					opened.Close()
					return nil, err
				}
				return opened, nil
			},
			func(value any, _ host.NativeCloseReason, _ context.Context) error {
				value.(*OwnerConfigFile).Close()
				return nil
			})
		var instances host.Native[any]
		signIns := self.Open(
			func(context.Context) (any, error) {
				dir, err := dataDir()
				if err != nil {
					return nil, err
				}
				return NewSignIns(NewMcpCredentials(dir), "p2", func(name string) {
					if running, err := instances.Value(); err == nil {
						running.(*Instances).ReconnectServer(name)
					}
				}, SignInTimeout), nil
			},
			func(value any, _ host.NativeCloseReason, _ context.Context) error {
				value.(*SignIns).Close()
				return nil
			})
		instances = self.Open(
			func(context.Context) (any, error) {
				version := pigversion.Version
				last, err := memoValue()
				if err != nil {
					return nil, err
				}
				seed, err := instancesOf(last.Value("instances"))
				if err != nil {
					return nil, err
				}
				opened := NewInstances(ClientInfo{Name: "p2", Version: version}, Reconnect, seed, connect, func(server OAuthServer) (oauth.McpAuthProvider, error) {
					current, err := signIns.Value()
					if err != nil {
						return nil, err
					}
					return current.(*SignIns).AuthProvider(server)
				})
				// What the leases use now starts at once (after a restart with the tools listed before), so the supervisor's
				// first list already shows it; derives compute only after the natives opened, so placed from the sources.
				placement, err := placeNow(places, file, leased, envs, catalog, store, locks)
				if err != nil {
					opened.Close()
					return nil, err
				}
				opened.Reconcile(placement)
				return opened, nil
			},
			func(value any, _ host.NativeCloseReason, _ context.Context) error {
				value.(*Instances).Close()
				return nil
			})
		probe := self.Open(
			func(context.Context) (any, error) { return NewLoopbackProbe(), nil },
			func(value any, _ host.NativeCloseReason, _ context.Context) error {
				return value.(*LoopbackProbe).Close()
			})

		// The file, the supervisor and the sign-ins as they are: every method's change is made before it returns.
		configured := p2runtime.FollowNative(self,
			func(changed func()) func() {
				opened, err := file.Value()
				if err != nil {
					return func() {}
				}
				return opened.(*OwnerConfigFile).Subscribe(changed)
			},
			func() (delta.JsonValue, error) {
				opened, err := file.Value()
				if err != nil {
					return nil, err
				}
				return ownerStateJSON(opened.(*OwnerConfigFile).State()), nil
			})
		listed := p2runtime.FollowNative(self,
			func(changed func()) func() {
				running, err := instances.Value()
				if err != nil {
					return func() {}
				}
				signing, err := signIns.Value()
				if err != nil {
					return func() {}
				}
				stops := []func(){running.(*Instances).Subscribe(changed), signing.(*SignIns).Subscribe(changed)}
				return func() {
					for _, stop := range stops {
						stop()
					}
				}
			},
			func() (delta.JsonValue, error) {
				running, err := instances.Value()
				if err != nil {
					return nil, err
				}
				signing, err := signIns.Value()
				if err != nil {
					return nil, err
				}
				return delta.JsonObjectOf("instances", instancesJSON(running.(*Instances).List()), "signIns", signInsJSON(signing.(*SignIns).List())), nil
			})
		keepMemento(self, memento, configured, listed)

		placement := func() (Placement, error) {
			return currentPlacement(places, configured, leased, envs, work, locks)
		}
		// The current configuration, leases, environments and places of work, then every change of them, start and close
		// instances; methods that change leases reconcile at once too, so a call right after `setLocations` finds its
		// instance.
		inputs := self.Derive([]any{configured, leased, envs.Member("configs"), work, locks.Member("locks")}, func(values []any) (delta.JsonValue, error) {
			return delta.JsonObjectOf("config", values[0], "leases", values[1], "envs", values[2], "work", values[3], "locks", values[4]), nil
		})
		self.On(inputs, func(call chord.WatchCall[delta.JsonValue], _ host.Ctx) *host.Handled {
			value := objectOf(call.Value)
			running, err := instances.Value()
			if err != nil {
				return host.Threw(err)
			}
			placement, err := places.place(value.Value("config"), value.Value("leases"), value.Value("envs"), value.Value("work"), value.Value("locks"))
			if err != nil {
				return host.Threw(err)
			}
			running.(*Instances).Reconcile(placement)
			return nil
		})
		leases := self.Derive([]any{inputs}, func(values []any) (delta.JsonValue, error) {
			value := objectOf(values[0])
			placement, err := places.place(value.Value("config"), value.Value("leases"), value.Value("envs"), value.Value("work"), value.Value("locks"))
			if err != nil {
				return nil, err
			}
			out := make([]any, len(placement.Leases))
			for i, each := range placement.Leases {
				used := []string{}
				for _, use := range each.Servers {
					if !containsString(used, use.ID) {
						used = append(used, use.ID)
					}
				}
				out[i] = delta.JsonObjectOf("session", each.Lease.Session, "location", toJSON(formOf(each.Lease.Location)),
					"instances", stringsJSON(used), "errors", stringsJSON(each.Errors))
			}
			return out, nil
		})
		running := func() (*Instances, error) {
			value, err := instances.Value()
			if err != nil {
				return nil, err
			}
			return value.(*Instances), nil
		}
		// A method that suspended reads and reconciles in a section of the loop again, as Pi's handler resumes in it.
		reconcileIn := func(ctx context.Context) error {
			var err error
			if sectionErr := host.InSection(ctx, func() {
				var value *Instances
				if value, err = running(); err != nil {
					return
				}
				var current Placement
				if current, err = placement(); err != nil {
					return
				}
				value.Reconcile(current)
			}); sectionErr != nil {
				return sectionErr
			}
			return err
		}
		methods := managerMethods(leased, running, placement, reconcileIn)
		for name, method := range signInMethods(configured, func() (*SignIns, error) {
			value, err := signIns.Value()
			if err != nil {
				return nil, err
			}
			return value.(*SignIns), nil
		}, func() (*LoopbackProbe, error) {
			value, err := probe.Value()
			if err != nil {
				return nil, err
			}
			return value.(*LoopbackProbe), nil
		}) {
			methods[name] = method
		}
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"instances": self.Derive([]any{listed}, func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value("instances"), nil }),
				"signIns":   self.Derive([]any{listed}, func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value("signIns"), nil }),
				"leases":    leases,
				"errors": self.Derive([]any{configured}, func(values []any) (delta.JsonValue, error) {
					state := objectOf(values[0])
					out := append([]any{}, asList(state.Value("errors"))...)
					if failure, ok := state.Value("failure").(string); ok {
						out = append(out, failure+"; keeping the last valid servers")
					}
					return out, nil
				}),
			},
			Methods: methods,
		}}, nil
	}
}

func asList(value any) []any {
	list, _ := value.([]any)
	return list
}

// placeNow is the placement of what the sources hold now.
func placeNow(places *placer, file host.Native[any], leased *documents.Singleton, envs, catalog, store, locks *host.Reference) (Placement, error) {
	opened, err := file.Value()
	if err != nil {
		return Placement{}, err
	}
	sessions, err := catalog.Member("sessions").Value()
	if err != nil {
		return Placement{}, err
	}
	children, err := store.Member("children").Value()
	if err != nil {
		return Placement{}, err
	}
	_, shown := workOf(sessions, children)
	return currentPlacementOf(places, ownerStateJSON(opened.(*OwnerConfigFile).State()), leased, envs, shown, locks)
}

func currentPlacement(places *placer, configured chord.Watchable[delta.JsonValue], leased *documents.Singleton, envs *host.Reference, work chord.Watchable[delta.JsonValue], locks *host.Reference) (Placement, error) {
	return currentPlacementOf(places, configured.Value(), leased, envs, work.Value(), locks)
}

func currentPlacementOf(places *placer, config delta.JsonValue, leased *documents.Singleton, envs *host.Reference, work delta.JsonValue, locks *host.Reference) (Placement, error) {
	held, err := leased.Value()
	if err != nil {
		return Placement{}, err
	}
	configs, err := envs.Member("configs").Value()
	if err != nil {
		return Placement{}, err
	}
	locked, err := locks.Member("locks").Value()
	if err != nil {
		return Placement{}, err
	}
	return places.place(config, held, configs, work, locked)
}

// keepMemento keeps the memento a restarted manager's natives start from: the servers of each valid read, and the instances
// once one of them lists other tools (instances closing change no tools, so a manager ending writes nothing).
func keepMemento(self host.Self, memento *documents.Singleton, configured, listed chord.Watchable[delta.JsonValue]) {
	self.On(configured, func(call chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
		return host.AwaitingCtx(ctx, func() error {
			state := objectOf(call.Value)
			if state.Value("failure") != nil {
				return nil
			}
			_, err := memento.Change(ctx, func(draft *delta.Object) (any, error) {
				if err := draft.Set("servers", state.Value("servers")); err != nil {
					return nil, err
				}
				return nil, draft.Set("errors", state.Value("errors"))
			})
			return err
		})
	})
	self.On(listed, func(call chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
		// The comparison reads the memento here, in the handler's section: only the write is blocking work.
		current := asList(objectOf(call.Value).Value("instances"))
		stored, err := memento.Value()
		if err != nil {
			return host.Threw(err)
		}
		remembered := map[string]string{}
		for _, each := range asList(stored.Value("instances")) {
			remembered[stringOf(objectOf(each).Value("id"))] = toolNames(objectOf(each))
		}
		changed := false
		for _, each := range current {
			names, known := remembered[stringOf(objectOf(each).Value("id"))]
			if !known || names != toolNames(objectOf(each)) {
				changed = true
			}
		}
		if !changed {
			return nil
		}
		return host.AwaitingCtx(ctx, func() error {
			_, err := memento.Change(ctx, func(draft *delta.Object) (any, error) { return nil, draft.Set("instances", current) })
			return err
		})
	})
}

// toolNames is the names of the tools instance lists, as one string.
func toolNames(instance *delta.JsonObject) string {
	var names string
	for i, each := range asList(instance.Value("tools")) {
		if i > 0 {
			names += "\n"
		}
		names += stringOf(objectOf(each).Value("name"))
	}
	return names
}

// projectAt is the project configuration the worker found at location: none, the file's text, or why it could not read it.
func projectAt(location p2runtime.Location, projects []any) ProjectSource {
	for _, each := range projects {
		file := objectOf(each)
		if file == nil {
			continue
		}
		found, err := fromJSON[locationForm](file.Value("location"))
		if err != nil || !p2runtime.SameLocation(locationOf(found), location) {
			continue
		}
		if text, ok := file.Value("text").(string); ok {
			return ProjectSource{Kind: ProjectFile, Text: text}
		}
		return ProjectSource{Kind: ProjectUnreadable, Error: stringOf(file.Value("error"))}
	}
	return ProjectSource{Kind: ProjectNone}
}

// sameLease is whether a and b are one lease.
func sameLease(a, b Lease) bool {
	return a.Session == b.Session && p2runtime.SameLocation(a.Location, b.Location) && a.Generation == b.Generation && a.Incarnation == b.Incarnation
}

// managerMethods are the manager's methods: the lease rules applied on the leases' line, calls through the leased instance,
// restarts.
func managerMethods(leased *documents.Singleton, instances func() (*Instances, error), placement func() (Placement, error), reconcile func(ctx context.Context) error) map[string]contracts.MethodFunction {
	return map[string]contracts.MethodFunction{
		"setLocations": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := objectArgs(args)
			caller := LeaseCaller{Session: stringOf(in.Value("session")), Generation: intOf(in.Value("generation")), Incarnation: stringOf(in.Value("incarnation"))}
			locations := asList(in.Value("locations"))
			projects := asList(in.Value("projects"))
			places := make([]LeasePlace, len(locations))
			for i, each := range locations {
				form, err := fromJSON[locationForm](each)
				if err != nil {
					return nil, err
				}
				location := locationOf(form)
				places[i] = LeasePlace{Location: location, Project: projectAt(location, projects)}
			}
			var refused string
			// Decided on the object's line: two generations' concurrent calls see each other's leases.
			if _, err := leased.Change(ctx, func(draft *delta.Object) (any, error) {
				held, err := leasesOf(draft.Snapshot().Value("leases"))
				if err != nil {
					return nil, err
				}
				result := SetLocations(held, caller, places)
				if result.Refused != "" {
					refused = result.Refused
					return nil, nil
				}
				return nil, draft.Set("leases", leasesJSON(result.Leases))
			}); err != nil {
				return nil, err
			}
			if refused != "" {
				return nil, p2runtime.Forbidden(refused)
			}
			return nil, reconcile(ctx)
		},
		"releaseLeases": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := objectArgs(args)
			session, generation := stringOf(in.Value("session")), intOf(in.Value("generation"))
			if _, err := leased.Change(ctx, func(draft *delta.Object) (any, error) {
				held, err := leasesOf(draft.Snapshot().Value("leases"))
				if err != nil {
					return nil, err
				}
				return nil, draft.Set("leases", leasesJSON(ReleaseGeneration(held, session, generation)))
			}); err != nil {
				return nil, err
			}
			return nil, reconcile(ctx)
		},
		"call": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := objectArgs(args)
			caller := LeaseCaller{Session: stringOf(in.Value("session")), Generation: intOf(in.Value("generation")), Incarnation: stringOf(in.Value("incarnation"))}
			form, err := fromJSON[locationForm](in.Value("location"))
			if err != nil {
				return nil, err
			}
			location := locationOf(form)
			stored, err := leased.Value()
			if err != nil {
				return nil, err
			}
			held, err := leasesOf(stored.Value("leases"))
			if err != nil {
				return nil, err
			}
			lease := HeldLease(held, caller, location)
			switch lease.Kind {
			case LeaseStale:
				return nil, p2runtime.Forbidden("Another worker of this session holds its MCP servers")
			case LeaseNone:
				return nil, fmt.Errorf("No MCP servers are leased at %s", p2runtime.LocationText(location))
			}
			current, err := placement()
			if err != nil {
				return nil, err
			}
			name := stringOf(in.Value("server"))
			id := ""
			for _, placed := range current.Leases {
				if sameLease(placed.Lease, lease.Lease) {
					id, _ = placed.InstanceOf(name)
					break
				}
			}
			if id == "" {
				return nil, fmt.Errorf(`MCP server "%s" is not configured at %s`, name, p2runtime.LocationText(location))
			}
			running, err := instances()
			if err != nil {
				return nil, err
			}
			chordctx.Suspend(ctx) // a tool runs as long as its server takes: the loop goes on meanwhile
			result, err := running.Call(ctx, id, stringOf(in.Value("tool")), in.Value("args"))
			if err != nil {
				return nil, err
			}
			return resultJSON(result)
		},
		"restart": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			running, err := instances()
			if err != nil {
				return nil, err
			}
			chordctx.Suspend(ctx) // stopping waits for the process
			return nil, running.Restart(stringOf(objectArgs(args).Value("instance")))
		},
	}
}

// signInMethods are the manager's sign-in methods: flows of the owner's OAuth servers, the callback route's completion, the
// probe.
func signInMethods(configured chord.Watchable[delta.JsonValue], signIns func() (*SignIns, error), probe func() (*LoopbackProbe, error)) map[string]contracts.MethodFunction {
	oauthServer := func(name string) (OAuthServer, error) {
		state, err := ownerStateOf(configured.Value())
		if err != nil {
			return OAuthServer{}, err
		}
		for _, server := range state.Servers {
			if server.Name == name {
				if oauth := OAuthServerOf(server); oauth != nil {
					return *oauth, nil
				}
			}
		}
		return OAuthServer{}, data.NewPluginError("not_found", fmt.Sprintf(`No MCP server "%s" signs in with OAuth`, name))
	}
	return map[string]contracts.MethodFunction{
		// The flow is listed before the answer: a caller following `signIns` for its end sees it under way first.
		"beginSignIn": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := objectArgs(args)
			server, err := oauthServer(stringOf(in.Value("server")))
			if err != nil {
				return nil, err
			}
			redirect := objectOf(in.Value("redirect"))
			signing, err := signIns()
			if err != nil {
				return nil, err
			}
			chordctx.Suspend(ctx) // a loopback listener, discovery over HTTP and the callback's registration
			start, err := signing.Begin(server, SignInRedirect{Route: redirect.Value("kind") == "route", URL: stringOf(redirect.Value("url"))})
			if err != nil {
				return nil, asPluginError(err)
			}
			if start.Authorized {
				return delta.JsonObjectOf("kind", "authorized"), nil
			}
			return delta.JsonObjectOf("kind", "redirect", "url", start.URL), nil
		},
		"completeSignIn": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := objectArgs(args)
			signing, err := signIns()
			if err != nil {
				return nil, err
			}
			response := AuthorizationResponse{State: stringOf(in.Value("state"))}
			failure, hasError := in.Value("error").(string)
			code, hasCode := in.Value("code").(string)
			switch {
			case hasError:
				response.Error = failure
			case !hasCode:
				response.Error = "The authorization response has no code"
			default:
				response.Code, response.Iss = code, stringOf(in.Value("iss"))
			}
			chordctx.Suspend(ctx) // the token exchange
			server, err := signing.Complete(response)
			if err != nil {
				return nil, err
			}
			return delta.JsonObjectOf("server", server), nil
		},
		"pasteSignIn": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := objectArgs(args)
			signing, err := signIns()
			if err != nil {
				return nil, err
			}
			chordctx.Suspend(ctx) // the token exchange
			return nil, signing.Paste(stringOf(in.Value("id")), stringOf(in.Value("redirectUrl")))
		},
		"cancelSignIn": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
			signing, err := signIns()
			if err != nil {
				return nil, err
			}
			signing.Cancel(stringOf(objectArgs(args).Value("id")))
			return nil, nil
		},
		"signOut": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			server, err := oauthServer(stringOf(objectArgs(args).Value("server")))
			if err != nil {
				return nil, err
			}
			signing, err := signIns()
			if err != nil {
				return nil, err
			}
			chordctx.Suspend(ctx) // the credentials file's lock and I/O
			return nil, asPluginError(signing.SignOut(ctx, server))
		},
		"probe": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
			running, err := probe()
			if err != nil {
				return nil, err
			}
			url, err := running.URL(stringOf(objectArgs(args).Value("origin")))
			if err != nil {
				return nil, err
			}
			return delta.JsonObjectOf("url", url), nil
		},
	}
}

// asPluginError is err as the caller's error with its message (a discovery that failed, an unset variable).
func asPluginError(err error) error {
	if err == nil {
		return nil
	}
	var plugin *data.PluginError
	if errors.As(err, &plugin) {
		return err
	}
	return data.NewPluginError("sign_in", err.Error())
}

// resultJSON is a tool's result as the server sent it.
func resultJSON(result *pimcp.CallToolResult) (delta.JsonValue, error) {
	encoded := []byte(result.Raw)
	if encoded == nil {
		var err error
		if encoded, err = json.Marshal(result); err != nil {
			return nil, err
		}
	}
	return chordjson.Decode(encoded)
}
