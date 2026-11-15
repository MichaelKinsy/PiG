package host

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/test/host/list.test.ts (Pi 0db9861ec018, 473 lines; two pages cases added by Pi 36c709e720b6): 18
// cases (invariant x4, pages x8, arguments x1, reload x3, declaration and memory x2). Subtest names are Pi's `it` texts.
//
// Go forms: `await Promise.all([...])` is `parallel(...)`, or, where the calls make commits that Pi's call order enlists in
// order (the invariant's groups), `commitLog.start` per call: each commit enlisted before the next call starts;
// `await delivered()` is `deliveredOn(host)`; `await flushed()` is
// `flushed(host)`; `screen.list.value` is `mustListValue(t, screen)`; a `ChordStore`'s `reads.on(set)?.size` is
// `len(store.Reads.On(set))`; the two `Probe`s are structs of atomics and `instances` (weak references) as in
// host_live_query_test.go.

var (
	lsRows = mustTableDef("rows", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{
			store.Col("id", store.Int()), store.Col("group", store.Str()), store.Col("touched", store.Int()),
			store.Col("title", store.Str(contracts.Default(""))),
		},
		Key:     []string{"id"},
		Indexes: store.IndexRecords{{Name: "by_group", Columns: []string{"group", "touched"}}},
	})
	lsItem = contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("touched", contracts.Int()))
	lsRow  = contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("group", contracts.Str()), contracts.Prop("touched", contracts.Int()))

	lsStoreContract = mustContract(contracts.Local("app.rows", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("rows", contracts.Queries(lsItem, []contracts.Property{
				contracts.Prop("ofGroup", contracts.Obj(contracts.Prop("group", contracts.Str()))),
			})),
			contracts.Mem("write", contracts.NewMethod(contracts.Arr(contracts.Obj(contracts.Prop("put", lsRow))))),
			contracts.Mem("touch", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("at", contracts.Int())))),
			contracts.Mem("drop", contracts.NewMethod(contracts.Int())),
			contracts.Mem("clear", contracts.NewMethod(contracts.Str())),
		},
	}))
	lsScreenContract = mustContract(contracts.Local("app.screen", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("view", contracts.Str())},
		Members: []contracts.LocalEntry{
			contracts.Mem("list", contracts.Obj(contracts.Prop("rows", contracts.Arr(lsItem)), contracts.Prop("moreAfter", contracts.Bool()))),
			contracts.Mem("more", contracts.NewMethod()),
			contracts.Mem("show", contracts.NewMethod(contracts.Str())),
		},
	}))
	// lsBoard is Pi's `Board`, the screen as a client holds it over a link (the variant is `v` as remoteOneVariant's).
	lsBoard = remoteOneVariant("app.board", contracts.Params{}, contracts.VariantDeclaration{
		State:   []contracts.Property{contracts.Prop("list", contracts.Obj(contracts.Prop("rows", contracts.Arr(lsItem)), contracts.Prop("moreAfter", contracts.Bool())))},
		Methods: []contracts.MethodEntry{contracts.Meth("more", contracts.NewMethod())},
	})
)

// lsSubscriber is Pi's test Subscriber: the calls it was advanced with, none folded.
type lsSubscriber struct {
	mu    sync.Mutex
	calls []chord.WatchCall[delta.JsonValue]
}

func (s *lsSubscriber) Advance(call chord.WatchCall[delta.JsonValue], _ context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}
func (s *lsSubscriber) End(chord.WatchEnd) {}
func (s *lsSubscriber) EndAfterDelivery()  {}
func (s *lsSubscriber) all() []chord.WatchCall[delta.JsonValue] {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// lsProbe is what the tests count: how often the screen's factory ran, and the lists it made (Pi's `Probe`, and
// `instancesOf(List, "read")`).
type lsProbe struct {
	factories atomic.Int32
	lists     instances[list]
}

// lsStoreProbe is what a test adds to the store's factory, and takes of it: more declarations (a native), its named queries.
type lsStoreProbe struct {
	declare func(self StoreSelf)
	queries instances[OwnQuery]
}

func lsServeStore(h FacetHost, direction string, probe *lsStoreProbe) error {
	return h.Serve(lsStoreContract, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
		self := plain.(StoreSelf)
		table := self.Table(lsRows)
		if probe != nil && probe.declare != nil {
			probe.declare(self)
		}
		rows := self.Queries(
			table,
			QueriesOptions{
				Map: func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
					return jsonObject("id", float64(row["id"].(int64)), "touched", float64(row["touched"].(int64))), nil
				},
				Reads: []string{"id", "touched"},
			},
			map[string]QueryBuilder{
				// by_group (group, touched): the newest touched first.
				"ofGroup": {Build: func(args delta.JsonValue) *store.Query {
					group := args.(*delta.JsonObject).Value("group").(string)
					return store.NewQuery(lsRows).
						Where(func(r, _ store.ColumnRefs) store.Condition { return store.Eq(r["group"], group) }).
						OrderBy(func(r, _ store.ColumnRefs) *store.ColumnRef { return r["touched"] }, direction)
				}},
			})
		if probe != nil {
			own, _ := ownQueries(rows)
			probe.queries.add(own["ofGroup"])
		}
		tx := func(ctx context.Context, write func(tx documents.Tx) error) (delta.JsonValue, error) {
			_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, write(tx) }, ctx)
			return nil, err
		}
		return contracts.Implementation{Members: contracts.VariantImplementation{
			Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{"rows": rows},
			Methods: map[string]contracts.MethodFunction{
				"write": func(puts delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return tx(ctx, func(tx documents.Tx) error {
						for _, item := range puts.([]any) {
							put := item.(*delta.JsonObject).Value("put").(*delta.JsonObject)
							row := store.Row{"id": int64(put.Value("id").(float64)), "group": put.Value("group"), "touched": int64(put.Value("touched").(float64))}
							if err := tx.Put(table, row); err != nil {
								return err
							}
						}
						return nil
					})
				},
				"touch": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return tx(ctx, func(tx documents.Tx) error {
						return tx.Update(table, store.Row{"id": int64(args.Value("id").(float64))}, store.Row{"touched": int64(args.Value("at").(float64))})
					})
				}),
				"drop": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return tx(ctx, func(tx documents.Tx) error { return tx.Delete(table, store.Row{"id": int64(id.(float64))}) })
				},
				"clear": func(group delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return tx(ctx, func(tx documents.Tx) error {
						return tx.DeleteWhere(table, store.Eq(store.NewColumnRef("group"), group))
					})
				},
			},
		}}, nil
	}, ServeOptions{})
}

func lsServeScreen(h FacetHost, first int, probe *lsProbe) error {
	return h.Serve(lsScreenContract, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
		self := plain
		probe.factories.Add(1)
		shown := self.Writable("a")
		args := self.Derive([]any{shown}, func(values []any) (delta.JsonValue, error) { return jsonObject("group", values[0]), nil })
		source := ListSource{Query: self.Get(lsStoreContract, jsonObject()).Queries("rows")["ofGroup"], Args: args}
		handle := self.List("rows", []ListSource{source}, ListOptions{First: first})
		probe.lists.add(listOf(handle))
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"list": handle.Value()},
			Methods: map[string]contracts.MethodFunction{
				"more": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, handle.More(ctx) },
				"show": func(group delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					return nil, shown.Set(group.(string))
				},
			},
		}}, nil
	}, ServeOptions{})
}

func lsStorePlugin(direction string, probe *lsStoreProbe) HostFacet {
	return HostFacet{Name: "@test/store", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return lsServeStore(h, direction, probe) })}
}

func lsScreenPlugin(first int, probe *lsProbe) HostFacet {
	return HostFacet{Name: "@test/screen", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return lsServeScreen(h, first, probe) })}
}

// lsWorld is `hosted`'s result: a host with the store and a screen of pages of `first` rows; `seed` rows of group "a" touched
// at their id.
type lsWorld struct {
	t       *testing.T
	store   *store.ChordStore
	host    Host
	rows    *Reference
	screen  *Reference
	plugins map[string]HostFacet
}

func lsHosted(t *testing.T, first, seed int, probe *lsProbe, storeProbe *lsStoreProbe) *lsWorld {
	t.Helper()
	if probe == nil {
		probe = &lsProbe{}
	}
	chordStore := memoryStore(t)
	host := newHost(t, HostOptions{Store: chordStore, OnError: (&hostErrors{}).push})
	plugins := map[string]HostFacet{"store": lsStorePlugin("desc", storeProbe), "screen": lsScreenPlugin(first, probe)}
	_, err := host.Load([]HostFacet{plugins["store"], plugins["screen"]}, background)
	must(t, err)
	rows := mustGet(t, host, lsStoreContract, jsonObject())
	if seed > 0 {
		all := []any{}
		for index := range seed {
			all = append(all, jsonObject("put", jsonObject("id", float64(index+1), "group", "a", "touched", float64(index+1))))
		}
		mustCall(t, rows, "write", all)
	}
	screen := mustGet(t, host, lsScreenContract, jsonObject("view", "v"))
	return &lsWorld{t: t, store: chordStore, host: host, rows: rows, screen: screen, plugins: plugins}
}

func (w *lsWorld) close() { closeHosts(w.t, w.host) }

func (w *lsWorld) touch(id, at int) {
	w.t.Helper()
	mustCall(w.t, w.rows, "touch", jsonObject("id", float64(id), "at", float64(at)))
}
func (w *lsWorld) put(id int, group string, touched int) {
	w.t.Helper()
	mustCall(w.t, w.rows, "write", []any{jsonObject("put", jsonObject("id", float64(id), "group", group, "touched", float64(touched)))})
}
func (w *lsWorld) drop(id int) { w.t.Helper(); mustCall(w.t, w.rows, "drop", float64(id)) }
func (w *lsWorld) more()       { w.t.Helper(); mustCall(w.t, w.screen, "more", nil) }
func (w *lsWorld) state() ([]int, bool) {
	w.t.Helper()
	rows, moreAfter := mustListValue(w.t, w.screen)
	return rowIDs(rows), moreAfter
}
func (w *lsWorld) registered() int { return len(w.store.Reads.On("app.rows:rows")) }

func lsExpect(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// Pi: describe("the invariant")
func TestListTheInvariant(t *testing.T) {
	for _, seed := range []uint32{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("after every frame the value is a gapless prefix of the true order (seed %d)", seed), func(t *testing.T) {
			const first = 5
			w := lsHosted(t, first, 12, nil, nil)
			enlisted := countCommits(w.store)
			r := newRandom(seed)
			truth := map[int]int{}
			for id := 1; id <= 12; id++ {
				truth[id] = id
			}
			clock, nextID := 100, 13
			// `r.chance(0.5) ? ++clock : r.int(50)`: r.int is drawn only when the chance fails.
			newAt := func() int {
				if r.chance(0.5) {
					clock++
					return clock
				}
				return r.int(50)
			}
			// The true order: newest touched first, ties by id (the key appended to the order).
			order := func() []int {
				ids := make([]int, 0, len(truth))
				for id := range truth {
					ids = append(ids, id)
				}
				slices.SortFunc(ids, func(a, b int) int {
					if truth[a] != truth[b] {
						return truth[b] - truth[a]
					}
					return b - a
				})
				return ids
			}
			lastStep := ""
			check := func(step string) {
				t.Helper()
				shown, moreAfter := w.state()
				expected := order()
				if fmt.Sprint(shown) != fmt.Sprint(expected[:len(shown)]) {
					t.Log(lastStep, "expected top", expected[:8])
					for _, l := range listsOf(w.host) {
						t.Log(l.describe())
					}
				}
				lsExpect(t, step, shown, expected[:len(shown)])
				if !moreAfter && len(shown) != len(expected) {
					t.Fatalf("%s: the whole order: %d rows, want %d", step, len(shown), len(expected))
				}
			}
			for step := range 300 {
				type call struct {
					run     func()
					commits bool // the call makes a commit
				}
				pending := []call{}
				described := []string{}
				// One commit, or a group of commits queued together.
				commits := 1
				if r.chance(0.2) {
					commits = 3
				}
				for ; commits > 0; commits-- {
					kind := r.int(10)
					live := make([]int, 0, len(truth))
					for id := range truth {
						live = append(live, id)
					}
					slices.Sort(live) // Pi's Map iteration order is insertion order: ids ascend until the writes; sorted here
					pick, have := 0, len(live) > 0
					if have {
						pick = live[r.int(len(live))]
					}
					switch {
					case kind < 4 && have:
						clock++
						truth[pick] = clock
						at := clock
						described = append(described, fmt.Sprintf("touch %d at %d", pick, at))
						pending = append(pending, call{func() { w.touch(pick, at) }, true})
					case kind < 6:
						id := nextID
						nextID++
						at := newAt()
						truth[id] = at
						described = append(described, fmt.Sprintf("put %d at %d", id, at))
						pending = append(pending, call{func() { w.put(id, "a", at) }, true})
					case kind < 8 && have:
						delete(truth, pick)
						described = append(described, fmt.Sprintf("drop %d", pick))
						pending = append(pending, call{func() { w.drop(pick) }, true})
					case kind == 8:
						// A bulk write of many pages' rows at once.
						bulk := []any{}
						for range 4 * first {
							id := nextID
							nextID++
							at := newAt()
							truth[id] = at
							bulk = append(bulk, jsonObject("put", jsonObject("id", float64(id), "group", "a", "touched", float64(at))))
						}
						described = append(described, fmt.Sprintf("bulk %d..", nextID-4*first))
						pending = append(pending, call{func() { mustCall(t, w.rows, "write", bulk) }, true})
					default:
						described = append(described, "more")
						pending = append(pending, call{w.more, false})
					}
				}
				// `await Promise.all(pending)`: Pi's calls of a turn enlist their commits in call order, and a later call of a group may
				// pick (touch, drop) a row an earlier one creates: each commit is enlisted before the next call starts.
				var started sync.WaitGroup
				for _, p := range pending {
					enlisted.start(&started, p.commits, p.run)
				}
				started.Wait()
				flushed(w.host)
				lastStep = fmt.Sprintf("step %d: %v", step, described)
				check(fmt.Sprintf("seed %d, step %d", seed, step))
			}
			// Loading to the end shows the whole order.
			for range 200 {
				if _, moreAfter := w.state(); !moreAfter {
					break
				}
				w.more()
			}
			shown, _ := w.state()
			lsExpect(t, "the whole order", shown, order())
			w.close()
		})
	}
}

// Pi: describe("pages")
func TestListPages(t *testing.T) {
	t.Run("loads a first page, and with more only the next rows: the loaded ones are the same objects", func(t *testing.T) {
		w := lsHosted(t, 3, 8, nil, nil)
		shown, moreAfter := w.state()
		lsExpect(t, "first page", []any{shown, moreAfter}, []any{[]int{8, 7, 6}, true})
		loaded, _ := mustListValue(t, w.screen)
		watch, err := w.screen.Member("list").Watch(background)
		must(t, err)
		calls := nexttest.ChangesOf(watch)
		parallel(w.more, w.more)
		deliveredOn(w.host)
		shown, _ = w.state()
		lsExpect(t, "ids", shown, []int{8, 7, 6, 5, 4, 3, 2, 1})
		now, _ := mustListValue(t, w.screen)
		for index, row := range loaded {
			if now[index] != row { // the same *delta.JsonObject
				t.Fatalf("row %d is not the same object", index)
			}
		}
		// Two calls in turn, each one frame that adds its page's rows.
		lsExpect(t, "calls", callsIDs(calls.Items()), []any{[]int{8, 7, 6, 5, 4, 3}, []int{8, 7, 6, 5, 4, 3, 2, 1}})
		_, _ = watch.Stop()
		w.close()
	})

	t.Run("moves a row touched in a later page to the top: one frame, no gap, no duplicate", func(t *testing.T) {
		w := lsHosted(t, 3, 9, nil, nil)
		w.more()
		w.more()
		watch, err := w.screen.Member("list").Watch(background)
		must(t, err)
		calls := nexttest.ChangesOf(watch)
		w.touch(2, 100)
		deliveredOn(w.host)
		shown, _ := w.state()
		lsExpect(t, "ids", shown, []int{2, 9, 8, 7, 6, 5, 4, 3, 1})
		if n := len(calls.Items()); n != 1 {
			t.Fatalf("calls = %d, want 1", n)
		}
		_, _ = watch.Stop()
		w.close()
	})

	// Pi (36c709e720b6): "a row moving between two pages sets the list once per page and publishes one frame, diffed from the value before
	// the commit". Go forms: `resolveWatchable(screen.list) as Source` is the member's source (subscribed on the loop, where
	// the batch runs); Pi's Subscriber is a mutex-guarded recorder; `applyImmutable` is delta.ApplyImmutable.
	t.Run("a row moving between two pages sets the list once per page and publishes one frame, diffed from the value before the commit", func(t *testing.T) {
		w := lsHosted(t, 3, 9, nil, nil)
		w.more()
		w.more()
		before, err := w.screen.Member("list").Value()
		must(t, err)
		// As a link's provider side follows it: every frame in its batch, none folded (a watch folds them).
		var source state.SourceHandle
		sub := &lsSubscriber{}
		w.screen.binding.loop.Run(func() {
			resolved, err := resolveWatchable(w.screen.Member("list"))
			must(t, err)
			source = resolved.(state.SourceHandle)
			source.Subscribe(sub)
		})
		// Row 2 leaves the third page and enters the first: both pages adopt in one commit.
		w.touch(2, 100)
		lsSettled(w.host)
		calls := sub.all()
		if len(calls) != 1 {
			t.Fatalf("calls = %d, want 1", len(calls))
		}
		if calls[0].Kind != chord.WatchCallValue {
			t.Fatalf("call = %+v, want a value call", calls[0])
		}
		lsExpect(t, "ids", rowIDs(calls[0].Value.(*delta.JsonObject).Value("rows").([]any)), []int{2, 9, 8, 7, 6, 5, 4, 3, 1})
		applied, err := delta.ApplyImmutable(before, calls[0].Ops)
		must(t, err)
		lsExpect(t, "applyImmutable(before, ops)", applied, calls[0].Value)
		w.screen.binding.loop.Run(func() { source.Unsubscribe(sub) })
		w.close()
	})

	// Pi (36c709e720b6): "over a link, the last row of a second page moving to the top is one state message: no protocol error, the
	// link stays open".
	t.Run("over a link, the last row of a second page moving to the top is one state message: no protocol error, the link stays open", func(t *testing.T) {
		chordStore := memoryStore(t)
		serverErrors, clientErrors := &hostErrors{}, &hostErrors{}
		server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), Store: chordStore, OnError: serverErrors.push})
		board := HostFacet{Name: "@test/board", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(lsBoard, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				args := self.Derive([]any{self.Writable("a")}, func(values []any) (delta.JsonValue, error) { return jsonObject("group", values[0]), nil })
				handle := self.List("rows", []ListSource{{Query: self.Get(lsStoreContract, jsonObject()).Queries("rows")["ofGroup"], Args: args}}, ListOptions{First: 3})
				return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"list": handle.Value()},
					Methods: map[string]contracts.MethodFunction{
						"more": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, handle.More(ctx) },
					},
				}}, nil
			}, ServeOptions{})
		})}
		_, err := server.Load([]HostFacet{lsStorePlugin("desc", nil), board}, background)
		must(t, err)
		rows := mustGet(t, server, lsStoreContract, jsonObject())
		puts := []any{}
		for _, id := range []int{1, 2, 3, 4} {
			puts = append(puts, jsonObject("put", jsonObject("id", float64(id), "group", "a", "touched", float64(id))))
		}
		mustCall(t, rows, "write", puts)
		client := newHost(t, HostOptions{OnError: clientErrors.push})
		_, err = client.Load(nil, background)
		must(t, err)
		up, down := link.MemoryLinkPair()
		must(t, server.Accept(up, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		must(t, client.Connect(down, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
		held := mustGet(t, client, lsBoard, jsonObject())
		mustCall(t, held, "more", nil)
		transported(up, down)
		shown, _ := mustListValue(t, held)
		lsExpect(t, "ids", rowIDs(shown), []int{4, 3, 2, 1})
		// The one row of the second page becomes the first row of the first.
		mustCall(t, rows, "touch", jsonObject("id", float64(1), "at", float64(100)))
		transported(up, down)
		if serverErrors.Len() != 0 || clientErrors.Len() != 0 {
			t.Fatalf("errors = server %v, client %v; want none", serverErrors.All(), clientErrors.All())
		}
		if kind := stateOfRef(held).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		shown, _ = mustListValue(t, held)
		lsExpect(t, "ids after", rowIDs(shown), []int{1, 4, 3, 2})
		held.Release()
		closeHosts(t, client, server)
	})

	t.Run("keeps the last page open while no row is behind it: empty, short and exactly full lists take a new row", func(t *testing.T) {
		for _, seed := range []int{0, 2, 3} {
			w := lsHosted(t, 3, seed, nil, nil)
			shown, moreAfter := w.state()
			lsExpect(t, "seeded", []any{len(shown), moreAfter}, []any{seed, false})
			// A row behind everything shown (the order is descending: the oldest touch is last).
			w.put(50, "a", 0)
			shown, moreAfter = w.state()
			if seed < 3 {
				lsExpect(t, "short", []any{shown[len(shown)-1], moreAfter}, []any{50, false})
			} else {
				lsExpect(t, "full", []any{shown, moreAfter}, []any{[]int{3, 2, 1}, true})
			}
			w.close()
		}
	})

	t.Run("a stale moreAfter is corrected by a more that loads nothing; the last page is open again", func(t *testing.T) {
		w := lsHosted(t, 2, 3, nil, nil)
		shown, moreAfter := w.state()
		lsExpect(t, "first", []any{shown, moreAfter}, []any{[]int{3, 2}, true})
		// The row behind the bounded page goes: the list cannot know, and still says more.
		w.drop(1)
		if _, moreAfter = w.state(); !moreAfter {
			t.Fatal("moreAfter = false, want true (stale)")
		}
		w.more()
		shown, moreAfter = w.state()
		lsExpect(t, "corrected", []any{shown, moreAfter}, []any{[]int{3, 2}, false})
		// Open at its end again: a row behind a full page is known at once, behind a short one it appears.
		w.put(9, "a", 0)
		shown, moreAfter = w.state()
		lsExpect(t, "behind a full page", []any{shown, moreAfter}, []any{[]int{3, 2}, true})
		w.drop(9)
		w.drop(2)
		w.more()
		w.put(8, "a", 0)
		shown, moreAfter = w.state()
		lsExpect(t, "behind a short page", []any{shown, moreAfter}, []any{[]int{3, 8}, false})
		w.close()
	})

	t.Run("shows a prefix, never a hole, while a bulk write overflows a page, and loads on by itself", func(t *testing.T) {
		const first = 2
		w := lsHosted(t, first, 4, nil, nil)
		w.more()
		watch, err := w.screen.Member("list").Watch(background)
		must(t, err)
		calls := nexttest.ChangesOf(watch)
		// Twenty rows into the first page's range: more than a page reads (4 pages' rows).
		bulk, expected := []any{}, []int{}
		for index := range 20 {
			bulk = append(bulk, jsonObject("put", jsonObject("id", float64(100+index), "group", "a", "touched", float64(1000+index))))
			expected = append([]int{100 + index}, expected...)
		}
		expected = append(expected, 4, 3, 2, 1)
		mustCall(t, w.rows, "write", bulk)
		// Pi: `for (let wait = 0; wait < 20; wait++) await flushed()`: the loading on is the list's own goroutines, which
		// lsSettled waits for; no sleep.
		lsSettled(w.host)
		frames := calls.Items()
		valueRows := func(call chord.WatchCall[delta.JsonValue]) ([]any, bool) {
			if call.Kind != chord.WatchCallValue {
				return []any{}, false
			}
			object := call.Value.(*delta.JsonObject)
			return object.Value("rows").([]any), object.Value("moreAfter").(bool)
		}
		// The first frame ends at the cut and says more; every frame is a prefix; the last has everything again.
		rows0, more0 := valueRows(frames[0])
		lsExpect(t, "first frame", []any{len(rows0), more0}, []any{4 * first, true})
		for _, frame := range frames {
			rows, _ := valueRows(frame)
			lsExpect(t, "a frame", rowIDs(rows), expected[:len(rows)])
		}
		shown, moreAfter := w.state()
		lsExpect(t, "last", []any{shown, moreAfter}, []any{expected, false})
		_, _ = watch.Stop()
		w.close()
	})

	t.Run("keeps the number of pages bounded over many touches: the rows a touch moves merge back", func(t *testing.T) {
		const first = 4
		w := lsHosted(t, first, 40, nil, nil)
		for range 12 {
			w.more()
		}
		if shown, _ := w.state(); len(shown) != 40 {
			t.Fatalf("rows = %d, want 40", len(shown))
		}
		for touch := range 400 {
			w.touch(1+((touch*7)%40), 1000+touch)
		}
		if shown, _ := w.state(); len(shown) != 40 {
			t.Fatalf("rows = %d, want 40", len(shown))
		}
		// Every page is one read of the store over the table. Neighbours that hold more than one page's rows together
		// stay apart, so a page holds more than half a page on average: at most 2 × 40 / 4 pages, at least 40 / 8.
		if reads := w.registered(); reads < 5 || reads > 20 {
			t.Fatalf("reads = %d, want 5..20", reads)
		}
		w.close()
	})
}

// Pi: describe("arguments that follow a value")
func TestListArgumentsThatFollowAValue(t *testing.T) {
	t.Run("switch every page in one frame, and start again at a first page of the other rows", func(t *testing.T) {
		w := lsHosted(t, 2, 5, nil, nil)
		mustCall(t, w.rows, "write", []any{
			jsonObject("put", jsonObject("id", 6.0, "group", "b", "touched", 6.0)),
			jsonObject("put", jsonObject("id", 7.0, "group", "b", "touched", 7.0)),
			jsonObject("put", jsonObject("id", 8.0, "group", "b", "touched", 8.0)),
		})
		w.more()
		watch, err := w.screen.Member("list").Watch(background)
		must(t, err)
		calls := nexttest.ChangesOf(watch)
		mustCall(t, w.screen, "show", "b")
		lsSettled(w.host)
		// The bookmarks are positions of the same order: the pages reopen under them, for the other group's rows.
		if n := len(calls.Items()); n != 1 {
			t.Fatalf("calls = %d, want 1", n)
		}
		shown, _ := w.state()
		lsExpect(t, "ids", shown, []int{8, 7, 6})
		_, _ = watch.Stop()
		w.close()
	})
}

// Pi: describe("reload")
func TestListReload(t *testing.T) {
	reload := func(w *lsWorld, facets ...HostFacet) error {
		_, err := w.host.Reload(facets, background, ReloadOptions{})
		return err
	}

	t.Run("of the list's plugin keeps the bookmarks: the pages it had, one value, no first-page collapse", func(t *testing.T) {
		probe := &lsProbe{}
		w := lsHosted(t, 3, 10, probe, nil)
		w.more()
		w.more()
		shown, _ := w.state()
		lsExpect(t, "before", shown, []int{10, 9, 8, 7, 6, 5, 4, 3, 2})
		must(t, reload(w, w.plugins["store"], lsScreenPlugin(3, probe)))
		// The first activation, staging's recording run, and the successor: it starts once the old attempt's work ended (the
		// reload returns before): that work is a task of the host's loop, which deliveredOn joins, then the reads it started.
		lsSettled(w.host)
		if n := probe.factories.Load(); n != 3 {
			t.Fatalf("factories = %d, want 3", n)
		}
		shown, moreAfter := w.state()
		lsExpect(t, "after", []any{shown, moreAfter}, []any{[]int{10, 9, 8, 7, 6, 5, 4, 3, 2}, true})
		// A write into a reopened range is one update.
		w.touch(5, 500)
		shown, _ = w.state()
		lsExpect(t, "touched", shown, []int{5, 10, 9, 8, 7, 6, 4, 3, 2})
		w.close()
	})

	t.Run("of the store's plugin does not restart the list: the same rows send nothing, a new order starts again", func(t *testing.T) {
		probe := &lsProbe{}
		w := lsHosted(t, 3, 10, probe, nil)
		w.more()
		watch, err := w.screen.Member("list").Watch(background)
		must(t, err)
		calls := nexttest.ChangesOf(watch)
		must(t, reload(w, lsStorePlugin("desc", nil), w.plugins["screen"]))
		lsSettled(w.host)
		if n := probe.factories.Load(); n != 1 {
			t.Fatalf("factories = %d, want 1", n)
		}
		if n := len(calls.Items()); n != 0 {
			t.Fatalf("calls = %d, want 0", n)
		}
		shown, _ := w.state()
		lsExpect(t, "same rows", shown, []int{10, 9, 8, 7, 6, 5})
		// The new builder orders the other way: the bookmarks are of another order, the list starts again.
		must(t, reload(w, lsStorePlugin("asc", nil), w.plugins["screen"]))
		lsSettled(w.host)
		shown, moreAfter := w.state()
		lsExpect(t, "new order", []any{shown, moreAfter}, []any{[]int{1, 2, 3}, true})
		_, _ = watch.Stop()
		w.close()
	})

	t.Run("a more called while the store's successor opens waits for it: nothing is read through the retired query, and nothing of it stays", func(t *testing.T) {
		// Code review round 2 (Astra B2, Sol B1), through the public reload: the successor waits in its native's open,
		// after the old attempt's reads retired. `more` used the old tiling: its closed query registered a read of the
		// old code's statement, which stayed in the store's index after the screen was released.
		old := &lsStoreProbe{}
		w := lsHosted(t, 2, 4, nil, old)
		shown, _ := w.state()
		lsExpect(t, "before", shown, []int{4, 3})
		opening, opened := make(chan struct{}), make(chan struct{})
		next := lsStorePlugin("desc", &lsStoreProbe{declare: func(self StoreSelf) {
			self.Open(func(ctx context.Context) (any, error) {
				close(opening)
				// Pi awaits `opened.promise`: a park, which the loop's join does not count as running.
				_, _ = chordctx.Receive(ctx, opened)
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error { return nil })
		}})
		reloaded := make(chan error, 1)
		go func() { reloaded <- reload(w, next, w.plugins["screen"]) }()
		<-opening
		if n := w.registered(); n != 0 { // the old provider's reads retired
			t.Fatalf("registered = %d, want 0", n)
		}
		completed := make(chan struct{})
		go func() { w.more(); close(completed) }()
		deliveredOn(w.host)
		select {
		case <-completed:
			t.Fatal("more completed before the successor opened")
		default:
		}
		if n := w.registered(); n != 0 {
			t.Fatalf("registered = %d, want 0", n)
		}
		close(opened)
		must(t, <-reloaded)
		<-completed
		lsSettled(w.host)
		// Loaded behind the successor's pages, with its statement.
		shown, moreAfter := w.state()
		lsExpect(t, "after", []any{shown, moreAfter}, []any{[]int{4, 3, 2, 1}, false})
		if w.registered() == 0 {
			t.Fatal("registered = 0, want > 0")
		}
		// Nothing keeps the old code's query while the screen still shows the list.
		expectCollected(t, old.queries.at(0))
		w.screen.Release()
		untilEnded(t, w.host)
		if n := w.registered(); n != 0 {
			t.Fatalf("registered = %d, want 0", n)
		}
		w.close()
	})
}

// Pi: describe("declaration and memory")
func TestListDeclarationAndMemory(t *testing.T) {
	t.Run("refuses a list without a name, a second list of a name, no page size, and several sources", func(t *testing.T) {
		chordStore := memoryStore(t)
		host := newHost(t, HostOptions{Store: chordStore, OnError: (&hostErrors{}).push})
		var thrown []string
		bad := mustContract(contracts.Local("app.bad", contracts.LocalDeclaration{
			Version: 1, Params: contracts.Params{},
			Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
		}))
		facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return firstError(lsServeStore(h, "desc", nil), h.Serve(bad, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				source := ListSource{Query: self.Get(lsStoreContract, jsonObject()).Queries("rows")["ofGroup"], Args: jsonObject("group", "a")}
				attempt := func(work func()) {
					if message := panicMessage(work); message != "" {
						thrown = append(thrown, message)
					}
				}
				attempt(func() { self.List("", []ListSource{source}, ListOptions{First: 3}) })
				attempt(func() { self.List("a", []ListSource{source}, ListOptions{}) })
				attempt(func() { self.List("a", []ListSource{source, source}, ListOptions{First: 3}) })
				self.List("a", []ListSource{source}, ListOptions{First: 3})
				attempt(func() { self.List("a", []ListSource{source}, ListOptions{First: 3}) })
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": self.Derive([]any{}, func([]any) (delta.JsonValue, error) { return 1.0, nil })},
				}}, nil
			}, ServeOptions{}))
		})
		_, err := host.Load([]HostFacet{{Name: "@test/bad", Facet: facet}}, background)
		must(t, err)
		mustGet(t, host, bad, jsonObject())
		want := []string{
			"app.bad: self.list: a list has a name",
			"app.bad: self.list: { first: n } is required, a positive integer",
			"app.bad: self.list: one source, [[namedQuery, args]]",
			`app.bad: list "a" is declared twice`,
		}
		if len(thrown) != len(want) {
			t.Fatalf("thrown = %q, want %q", thrown, want)
		}
		for index := range want {
			if !strings.Contains(thrown[index], want[index]) { // Go: the panic's text is fmt.Sprint(recovered): contains, as host_live_query_test.go
				t.Fatalf("thrown[%d] = %q, want %q", index, thrown[index], want[index])
			}
		}
		closeHosts(t, host)
	})

	t.Run("keeps nothing of a list once its object ended: its pages leave the store's index", func(t *testing.T) {
		probe := &lsProbe{}
		w := lsHosted(t, 3, 10, probe, nil)
		w.more()
		ended := probe.lists.at(-1)
		if w.registered() == 0 {
			t.Fatal("registered = 0, want > 0")
		}
		w.screen.Release()
		flushed(w.host)
		w.close()
		expectCollected(t, ended)
	})
}

var _ sync.Mutex
