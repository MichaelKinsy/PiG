package host

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Twin of packages/chord/test/examples/69-admission.ts (Pi 7cc21c24f6b5): admission (slice 14 §2). A remote type's `admit` runs
// first in every attempt, declares what it reads and returns a watchable of `allow(value)` or `deny(reason)`; the factory is
// handed the value, which selects the variant; an equal value changes nothing, another one rebuilds the object, a denial seals it
// (`failed { denied }`, running calls rejected `forbidden/revoked`, no value kept). A route asks the same way before it starts
// anything: no worker starts for a person who will be denied.
//
// The script prints these lines and asserts nothing; here each printed value is a check, with the value the script prints on Pi's
// host (node 26, run from a git archive in the tern-ci container; the lines are those of Pi main adea2f27f57a, which equal those
// of 7cc21c24f6b5 but for the token named below):
//
//	1. no share: denied (no access)
//	   (factory runs for s1 as read)
//	2. shared to read: live viewer
//	   (factory runs for s1 as write)
//	3. changed to write: live participant
//	   another person's share: no factory run above this line
//	4. scratch of s2: denied (no access) | scratch of s1: live pad | workers started: [ 'session|s1' ]
//	5. reads open: each answer's session and share (Max in s1 and s2)
//	   a touch of another session: 0 runs, 0 probes
//	   a touch of its own session: 1 runs, 0 probes
//	6. unshared, the running call: forbidden/revoked: example.transcript(s1): no access
//	   transcript: denied (no access) | scratch: denied (no access)
//	   a read of its title: throws forbidden
//	   (factory runs for s1 as read)
//	7. shared again: live viewer | live pad | workers started: [ 'session|s1' ]
//
// Pi's own output differs from the lines above in ONE token by its version: at 7cc21c24f6b5 the 6th step prints `scratch: live
// pad` (and still does 300 ms later: its route-admission.ts `#moved` ran `decision.now()` at the unshare and `#apply` never
// ran after it, a lost wake-up against the script's own comment "the route releases Max's hold at the worker" and the route test
// "a denial unbinds the principal's holds"); at Pi main adea2f27f57a (the same script, run five times) it prints `scratch:
// denied (no access)`, as above, and this twin asserts that: no divergence. The change that made the difference is not
// route-admission.ts, which is the same in both (route.ts's `opened` hook is unrelated).
//
// Pi's `settled()` (10 ms, which lets every microtask and link frame run) is `ex69World.settled`: the links' frames arrive and the hosts'
// deliveries and joined tasks run to their end (deliveredOn), as Pi's `transported()` does (six rounds, test/support.ts:604-606).
// Pi's `sayGate` promise is a channel the handler waits on through chordctx.Receive; the call in flight is started in one section
// (startCallWith) and its outcome read after, as Pi awaits the promise `said`.

var (
	ex69SessionRow = contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("title", contracts.Str()), contracts.Prop("lastTouch", contracts.Int()))
	ex69ShareRow   = contracts.Obj(contracts.Prop("person", contracts.Str()), contracts.Prop("session", contracts.Str()), contracts.Prop("mode", contracts.Str()))

	// The server's store: the sessions, and who may do what in each (a share row per person and session).
	ex69Sessions = exLocal("example.sessions", contracts.Params{},
		contracts.Mem("sessions", contracts.Queries(ex69SessionRow, []contracts.Property{contracts.Prop("one", contracts.Obj(contracts.Prop("id", contracts.Str())))})),
		contracts.Mem("shares", contracts.Queries(ex69ShareRow, []contracts.Property{contracts.Prop("one", contracts.Obj(contracts.Prop("person", contracts.Str()), contracts.Prop("session", contracts.Str())))})),
		contracts.Mem("create", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("title", contracts.Str())))),
		contracts.Mem("touch", contracts.NewMethod(contracts.Str())),
		contracts.Mem("share", contracts.NewMethod(contracts.Obj(contracts.Prop("person", contracts.Str()), contracts.Prop("session", contracts.Str()), contracts.Prop("mode", contracts.Str())))),
		contracts.Mem("unshare", contracts.NewMethod(contracts.Obj(contracts.Prop("person", contracts.Str()), contracts.Prop("session", contracts.Str())))),
	)
	ex69SessionRows = mustTableDef("example.session-rows", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("title", store.Str()), store.Col("lastTouch", store.Int(contracts.Default(0)))},
		Key:     []string{"id"},
	})
	ex69ShareRows = mustTableDef("example.share-rows", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{store.Col("person", store.Str()), store.Col("session", store.Str()), store.Col("mode", store.Str())},
		Key:     []string{"person", "session"},
	})

	// One answer per person and session, computed once on the server: every admission of a session asks this object.
	ex69Answer = exLocal("example.answer", contracts.Params{contracts.Prop("session", contracts.Str()), contracts.Prop("person", contracts.Str())},
		contracts.Mem("mode", contracts.OneOf("none", "read", "write")),
	)

	// The admitted type: its contract declares the value its factory is handed about a caller.
	ex69Transcript = mustContract(contracts.Remote("example.transcript", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str())},
		HostAddress: exServerAddress,
		Admission: func() *contracts.Schema {
			s := contracts.Obj(contracts.Prop("mode", contracts.OneOf("read", "write")))
			return &s
		}(),
		State:   []contracts.RemoteStateEntry{contracts.StateEntry("title", contracts.Str())},
		Methods: []contracts.RemoteMethodEntry{contracts.Meth("say", contracts.NewGrantable(contracts.NewMethod(contracts.Str(), contracts.Str())))},
	}))

	// A type on the session's worker: the route decides who reaches a worker at all.
	ex69Scratch = exRemote("example.scratch", contracts.Params{contracts.Prop("session", contracts.Str())}, exSessionAddress,
		contracts.RemoteDeclaration{State: []contracts.RemoteStateEntry{contracts.StateEntry("text", contracts.Str())}},
	)
)

// ex69World is the example's server, its recorded output and its gate.
type ex69World struct {
	mu      sync.Mutex
	lines   []string
	started []string
	hosts   []Host
	links   []*link.MemoryLink
}

// ex69Gate is the example's `sayGate`: the promise the handler of `say` awaits.
type ex69Gate struct {
	mu sync.Mutex
	ch chan struct{}
}

func (g *ex69Gate) set(ch chan struct{}) { g.mu.Lock(); g.ch = ch; g.mu.Unlock() }
func (g *ex69Gate) get() chan struct{}   { g.mu.Lock(); defer g.mu.Unlock(); return g.ch }

func (w *ex69World) print(line string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = append(w.lines, line)
}

// settled is Pi's `settled()`.
func (w *ex69World) settled() {
	for range 6 {
		w.mu.Lock()
		hosts, links := append([]Host(nil), w.hosts...), append([]*link.MemoryLink(nil), w.links...)
		w.mu.Unlock()
		transported(links...)
		deliveredOn(hosts...)
	}
	w.mu.Lock()
	links := append([]*link.MemoryLink(nil), w.links...)
	w.mu.Unlock()
	transported(links...)
}

func (w *ex69World) track(host Host, ends ...*link.MemoryLink) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if host != nil {
		w.hosts = append(w.hosts, host)
	}
	w.links = append(w.links, ends...)
}

func TestExample69Admission(t *testing.T) {
	meter := nexttest.Metered(t)
	ctx := meter.Context
	world := &ex69World{}
	// `let sayGate = Promise.resolve()`: a gate that is open until step 6 sets one.
	open := make(chan struct{})
	close(open)
	sayGate := &ex69Gate{ch: open}
	errs := &hostErrors{}

	// The server's plugin: the store, the answers, the admitted transcript.
	sessionsFacet := DefineFacet(func(host FacetHost, _ FacetSelf) error {
		if err := host.Serve(ex69Sessions, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
			self := plain.(StoreSelf)
			sessions := self.Table(ex69SessionRows)
			shares := self.Table(ex69ShareRows)
			tx := func(ctx context.Context, write func(tx documents.Tx) error) (delta.JsonValue, error) {
				_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, write(tx) }, ctx)
				return nil, err
			}
			sessionItem := func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
				return jsonObject("id", row["id"], "title", row["title"], "lastTouch", float64(row["lastTouch"].(int64))), nil
			}
			shareItem := func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
				return jsonObject("person", row["person"], "session", row["session"], "mode", row["mode"]), nil
			}
			return contracts.MembersImplementation{
				Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{
					// Each query runs on its table's key.
					"sessions": self.Queries(sessions, QueriesOptions{Map: sessionItem, Reads: []string{"id", "title", "lastTouch"}}, map[string]QueryBuilder{
						"one": {Build: func(args delta.JsonValue) *store.Query {
							id := args.(*delta.JsonObject).Value("id").(string)
							return store.NewQuery(ex69SessionRows).Where(func(r, _ store.ColumnRefs) store.Condition { return store.Eq(r["id"], id) })
						}},
					}),
					"shares": self.Queries(shares, QueriesOptions{Map: shareItem, Reads: []string{"person", "session", "mode"}}, map[string]QueryBuilder{
						"one": {Build: func(args delta.JsonValue) *store.Query {
							object := args.(*delta.JsonObject)
							person, session := object.Value("person").(string), object.Value("session").(string)
							return store.NewQuery(ex69ShareRows).Where(func(r, _ store.ColumnRefs) store.Condition {
								return store.And(store.Eq(r["person"], person), store.Eq(r["session"], session))
							})
						}},
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					"create": exMethod(func(row *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						return tx(ctx, func(tx documents.Tx) error {
							return tx.Put(sessions, store.Row{"id": row.Value("id"), "title": row.Value("title"), "lastTouch": int64(0)})
						})
					}),
					// A touch is an `update` of the column it changes: readers of other columns are not run.
					"touch": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return tx(ctx, func(tx documents.Tx) error {
							return tx.Update(sessions, store.Row{"id": id}, store.Row{"lastTouch": time.Now().UnixMilli()})
						})
					},
					"share": exMethod(func(row *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						return tx(ctx, func(tx documents.Tx) error {
							return tx.Put(shares, store.Row{"person": row.Value("person"), "session": row.Value("session"), "mode": row.Value("mode")})
						})
					}),
					"unshare": exMethod(func(key *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						return tx(ctx, func(tx documents.Tx) error {
							return tx.Delete(shares, store.Row{"person": key.Value("person"), "session": key.Value("session")})
						})
					}),
				},
			}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := host.Serve(ex69Answer, func(params *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
			session, person := params.Value("session").(string), params.Value("person").(string)
			sessions := self.Get(ex69Sessions, jsonObject())
			found := self.LiveQuery(sessions.Queries("sessions")["one"], exConstant(jsonObject("id", session)), 1)
			share := self.LiveQuery(sessions.Queries("shares")["one"], exConstant(jsonObject("person", person, "session", session)), 1)
			mode := self.Derive([]any{found, share}, func(values []any) (delta.JsonValue, error) {
				sessionRows := values[0].(*delta.JsonObject).Value("rows").([]any)
				shareRows := values[1].(*delta.JsonObject).Value("rows").([]any)
				granted := ""
				if len(shareRows) > 0 {
					granted, _ = shareRows[0].(*delta.JsonObject).Value("mode").(string)
				}
				if len(sessionRows) == 1 && (granted == "read" || granted == "write") {
					return granted, nil
				}
				return "none", nil
			})
			return contracts.MembersImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"mode": mode}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		// `admit` runs before the factory. It declares only what it reads; the factory gets the allowed value.
		return host.Serve(ex69Transcript, func(params *delta.JsonObject, _ data.Principal, admitted AdmittedValue, _ Self) (contracts.MembersImplementation, error) {
			session, mode := params.Value("session").(string), admitted.Data.Value("mode").(string)
			world.print(fmt.Sprintf("   (factory runs for %s as %s)", session, mode))
			title := exConstant("transcript of " + session)
			say := func(text delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				if _, err := chordctx.Receive(ctx, sayGate.get()); err != nil {
					return nil, err
				}
				return "said: " + text.(string), nil
			}
			return exVariant("participant", map[string]chord.Watchable[delta.JsonValue]{"title": title}, map[string]contracts.MethodFunction{"say": say}), nil
		}, ServeOptions{Admit: func(params *delta.JsonObject, principal data.Principal, self AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
			if principalKind(principal) != "person" {
				return exConstant(Deny("people only")), nil
			}
			answer := self.Get(ex69Answer, jsonObject("session", params.Value("session"), "person", principal.Value("id")))
			return self.Derive([]any{answer.Member("mode")}, func(values []any) (delta.JsonValue, error) {
				if values[0] == "none" {
					return Deny("no access"), nil
				}
				if values[0] == "write" {
					return Allow(jsonObject("mode", values[0], "granted", []delta.JsonValue{"say"})), nil
				}
				return Allow(jsonObject("mode", values[0])), nil
			}), nil
		}})
	})

	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), Store: memoryStore(t), Telemetry: meter.Telemetry, OnError: errs.push})
	world.track(server)
	// The route asks before anything starts: one decision per person and session with holds, shared by them. It reads a contract the
	// app's API owns (`Answer`): a route is base code and never reloads. Its `resolve` returns the link to the worker itself (the
	// final provider), which the fence after a denial relies on.
	_, err := server.Route(Session, RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Admit: func(principal data.Principal, address contracts.Address, self AdmitSelf) (any, error) {
			if principalKind(principal) != "person" {
				return Allow(nil), nil
			}
			answer := self.Get(ex69Answer, jsonObject("session", address.Params.Value("id"), "person", principal.Value("id")))
			return self.Derive([]any{answer.Member("mode")}, func(values []any) (delta.JsonValue, error) {
				if values[0] == "none" {
					return Deny("no access"), nil
				}
				return Allow(nil), nil
			}), nil
		},
		Resolve: func(address contracts.Address, _ context.Context) (ResolvedLink, error) {
			world.mu.Lock()
			world.started = append(world.started, address.Key)
			world.mu.Unlock()
			up, down := link.MemoryLinkPair()
			session, _ := address.Params.Value("id").(string)
			principal := workerPrincipal(session)
			worker := newHost(t, HostOptions{Address: &address, Principal: principal, OnError: errs.push})
			must(t, worker.Connect(up, LinkOptions{Principal: serverPrincipal()}, ctx))
			facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
				return h.Serve(ex69Scratch, func(*delta.JsonObject, data.Principal, Self) (contracts.MembersImplementation, error) {
					return exVariant("pad", map[string]chord.Watchable[delta.JsonValue]{"text": exConstant("a scratch pad")}, nil), nil
				}, ServeOptions{})
			})
			world.track(worker, up, down)
			if _, err := worker.Load([]HostFacet{{Name: "@example/scratch", Facet: facet}}, ctx); err != nil {
				t.Errorf("worker load: %v", err)
			}
			return ResolvedLink{Link: down, Principal: principal}, nil
		},
	}, ctx)
	must(t, err)
	mustLoad(t)(server.Load([]HostFacet{{Name: "@example/sessions", Facet: sessionsFacet}}, ctx))

	// Max's page: a client whose link the server stamps with his identity.
	page := newHost(t, HostOptions{OnError: errs.push})
	mustLoad(t)(page.Load(nil, ctx))
	a, b := link.MemoryLinkPair()
	must(t, server.Accept(a, LinkOptions{Principal: delta.JsonObjectOf("kind", "person", "id", "max")}, ctx))
	must(t, page.Connect(b, LinkOptions{Principal: serverPrincipal()}, ctx))
	world.track(page, a, b)

	sessions := mustGetWith(t, server, ex69Sessions, ctx)
	call := func(ref *Reference, method string, args delta.JsonValue) {
		t.Helper()
		if _, err := ref.Call(method, args, ctx); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	call(sessions, "create", jsonObject("id", "s1", "title", "Holiday"))
	call(sessions, "create", jsonObject("id", "s2", "title", "Budget"))
	call(sessions, "create", jsonObject("id", "s3", "title", "Garden"))
	shown := func(ref *Reference) string {
		current := stateOfRef(ref)
		if current.Kind == RefStateFailed {
			return fmt.Sprintf("%s (%s)", current.Cause.Kind, current.Cause.Error.Message)
		}
		role := "viewer"
		if exHasGrant(ref, "say") {
			role = "participant"
		}
		if ref.binding.name == "example.scratch" {
			role = "pad"
		}
		return fmt.Sprintf("%s %s", current.Kind, role)
	}
	startedList := func() string {
		world.mu.Lock()
		defer world.mu.Unlock()
		return "[ '" + strings.Join(world.started, "', '") + "' ]"
	}

	// 1. Max holds the transcript of a session he has no share of: denied, and nothing was built for him.
	transcript := mustHold(t, page, ex69Transcript, jsonObject("session", "s1"))
	world.settled()
	world.print("1. no share: " + shown(transcript))

	// 2. A share row is written. The answer changes, the decision with it, and the factory runs with the value.
	call(sessions, "share", jsonObject("person", "max", "session", "s1", "mode", "read"))
	world.settled()
	world.print("2. shared to read: " + shown(transcript))

	// 3. Another value rebuilds the object as the other variant; an equal one changes nothing.
	call(sessions, "share", jsonObject("person", "max", "session", "s1", "mode", "write"))
	world.settled()
	world.print("3. changed to write: " + shown(transcript))
	call(sessions, "share", jsonObject("person", "tanja", "session", "s1", "mode", "write"))
	world.settled()
	world.print("   another person's share: no factory run above this line")

	// 4. The route: no worker starts for a person without access; with a share it starts.
	forbidden := mustHold(t, page, ex69Scratch, jsonObject("session", "s2"))
	scratch := mustHold(t, page, ex69Scratch, jsonObject("session", "s1"))
	world.settled()
	world.print("4. scratch of s2: " + shown(forbidden) + " | scratch of s1: " + shown(scratch) + " | workers started: " + startedList())

	// 5. What following a session costs (each answer reads `sessions.one` of its session live): a touch of another session runs and
	//    probes nothing, a touch of its own runs its one read.
	cost := func(what string, write func()) {
		names := []string{"chord.query.runs", "chord.query.probes"}
		before := meter.Counts()
		write()
		world.settled()
		after := meter.Counts()
		world.print(fmt.Sprintf("   %s: %v runs, %v probes", what, after[names[0]]-before[names[0]], after[names[1]]-before[names[1]]))
	}
	world.print("5. reads open: each answer's session and share (Max in s1 and s2)")
	cost("a touch of another session", func() { call(sessions, "touch", "s3") })
	cost("a touch of its own session", func() { call(sessions, "touch", "s1") })

	// 6. Access is withdrawn under a running call: the call is rejected `forbidden/revoked`, the object shows the denial and keeps
	//    no value, and the route releases Max's hold at the worker.
	gate := make(chan struct{})
	sayGate.set(gate)
	var said *parkedCallResult
	if exHasGrant(transcript, "say") {
		said = startCallWith(t, transcript, "say", "hello")
	}
	world.settled()
	call(sessions, "unshare", jsonObject("person", "max", "session", "s1"))
	outcome := ""
	if said != nil {
		value, err := said.wait(background)
		if err != nil {
			chordErr, _ := data.IsChordError(err)
			outcome = fmt.Sprintf("%s/%s: %s", chordErr.Code, chordErr.Reason, chordErr.Message)
		} else {
			outcome, _ = value.(string)
		}
	}
	world.print("6. unshared, the running call: " + outcome)
	world.settled()
	world.print("   transcript: " + shown(transcript) + " | scratch: " + shown(scratch))
	read := ""
	if value, err := transcript.Member("title").Value(); err != nil {
		chordErr, _ := data.IsChordError(err)
		read = "throws " + string(chordErr.Code)
	} else {
		read, _ = value.(string)
	}
	world.print("   a read of its title: " + read)
	close(gate)

	// 7. Shared again: both come back, the scratch on the worker that is still running.
	call(sessions, "share", jsonObject("person", "max", "session", "s1", "mode", "read"))
	world.settled()
	world.print("7. shared again: " + shown(transcript) + " | " + shown(scratch) + " | workers started: " + startedList())

	want := []string{
		"1. no share: denied (no access)",
		"   (factory runs for s1 as read)",
		"2. shared to read: live viewer",
		"   (factory runs for s1 as write)",
		"3. changed to write: live participant",
		"   another person's share: no factory run above this line",
		"4. scratch of s2: denied (no access) | scratch of s1: live pad | workers started: [ 'session|s1' ]",
		"5. reads open: each answer's session and share (Max in s1 and s2)",
		"   a touch of another session: 0 runs, 0 probes",
		"   a touch of its own session: 1 runs, 0 probes",
		"6. unshared, the running call: forbidden/revoked: example.transcript(s1): no access",
		"   transcript: denied (no access) | scratch: denied (no access)",
		"   a read of its title: throws forbidden",
		"   (factory runs for s1 as read)",
		"7. shared again: live viewer | live pad | workers started: [ 'session|s1' ]",
	}
	world.mu.Lock()
	got := append([]string(nil), world.lines...)
	world.mu.Unlock()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("printed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	must(t, page.Close(ctx))
	must(t, server.Close(ctx))
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}
