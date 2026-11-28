package host

// pi: packages/chord/src/host/query-reader.ts
// Ports packages/chord/test/host/query-reader.test.ts @0db9861ec018 (14 cases): `self.liveQuery` and `self.count` read
// another object's named query through its reference, or the owner's own; the first run comes before the reader is live;
// a commit of the store's owner publishes the diff in its batch; arguments may follow a value; a reader whose read cannot
// run parks and heals; refused arguments break it; the mapping gets only the declared columns.
//
// DRAFT: every case reaches the reader through createHost + serve + self.liveQuery / self.count / self.queries (Pi has no
// test of Reader or GivenQuery alone). Names the host lane has not landed are marked ASSUMED (Host STATE 'PART 2b API').
// The package-level names are prefixed `qr`.
//
// Forced differences (Go): a builder that throws is a panic of Build (ASSUMED: the host lane's OwnQuery.Target recovers it,
// as Pi's try/catch around `builder(args)`); `self.query(named.all, ...)` (a named query given as a table) cannot be
// written (Self.Query takes a store.Table: types state it, see the second failures case); its other two arms are written
// with a foreign query for the table and a first of 0.

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

var (
	qrNotes = mustTableDef("notes", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{
			store.Col("id", store.Int()), store.Col("board", store.Str()), store.Col("text", store.Str()),
			store.Col("stars", store.Int(contracts.Default(0))), store.Col("hidden", store.Bool(contracts.Default(false))),
		},
		Key:     []string{"id"},
		Indexes: store.IndexRecords{{Name: "by_board", Columns: []string{"board", "id"}}},
	})
	qrNote      = contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("text", contracts.Str()))
	qrBoardArgs = contracts.Obj(contracts.Prop("board", contracts.Str()))
	qrNoteStore = mustContract(contracts.Local("app.notes", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("notes", contracts.Queries(qrNote, []contracts.Property{
				contracts.Prop("ofBoard", qrBoardArgs), contracts.Prop("one", contracts.Obj(contracts.Prop("id", contracts.Int()))),
			})),
			contracts.Mem("put", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("board", contracts.Str()), contracts.Prop("text", contracts.Str())))),
			contracts.Mem("star", contracts.NewMethod(contracts.Int())),
			contracts.Mem("rename", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("text", contracts.Str())))),
			contracts.Mem("drop", contracts.NewMethod(contracts.Int())),
		},
	}))
	qrPage  = contracts.Obj(contracts.Prop("rows", contracts.Arr(qrNote)), contracts.Prop("more", contracts.Bool()))
	qrBoard = mustContract(contracts.Local("app.board", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("board", contracts.Str())},
		Members: []contracts.LocalEntry{contracts.Mem("notes", qrPage), contracts.Mem("count", contracts.Int())},
	}))
)

// qrProbe is how often the store's mapping ran, and with which columns (Pi's `Probe`).
type qrProbe struct {
	mu     sync.Mutex
	mapped [][]string
}

func (p *qrProbe) push(columns []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mapped = append(p.mapped, columns)
}

func (p *qrProbe) all() [][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]string(nil), p.mapped...)
}

func qrInt(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case float64:
		return int64(typed)
	}
	panic("not an int")
}

func qrNoteJSON(id int64, text any) *delta.JsonObject {
	return jsonObject("id", float64(id), "text", text)
}

// qrServeStore is Pi's `serveStore(h, probe, declare)`.
func qrServeStore(h FacetHost, probe *qrProbe) error {
	return h.Serve(qrNoteStore, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		self := plain.(StoreSelf)
		table := self.Table(qrNotes)
		notes := self.Queries(table, QueriesOptions{
			Map: func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
				columns := make([]string, 0, len(row))
				for name := range row {
					columns = append(columns, name)
				}
				slices.Sort(columns)
				probe.push(columns)
				return qrNoteJSON(qrInt(row["id"]), row["text"]), nil
			},
			Reads: []string{"id", "text"},
		}, map[string]QueryBuilder{
			"ofBoard": {Build: func(args delta.JsonValue) *store.Query {
				board := args.(*delta.JsonObject).Value("board").(string)
				if board == "broken" {
					panic("no such board")
				}
				return store.NewQuery(qrNotes).Where(func(n, _ store.ColumnRefs) store.Condition { return store.Eq(n["board"], board) }).
					OrderBy(func(n, _ store.ColumnRefs) *store.ColumnRef { return n["id"] }, "desc")
			}},
			"one": {Build: func(args delta.JsonValue) *store.Query {
				id := qrInt(args.(*delta.JsonObject).Value("id"))
				return store.NewQuery(qrNotes).Where(func(n, _ store.ColumnRefs) store.Condition { return store.Eq(n["id"], id) })
			}},
		})
		tx := func(ctx context.Context, write func(tx documents.Tx) error) (delta.JsonValue, error) {
			_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, write(tx) }, ctx)
			return nil, err
		}
		return contracts.MembersImplementation{
			Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{"notes": notes},
			Methods: map[string]contracts.MethodFunction{
				"put": exMethod(func(row *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					stored := store.Row{"id": qrInt(row.Value("id")), "board": row.Value("board"), "text": row.Value("text")}
					return tx(ctx, func(tx documents.Tx) error { return tx.Put(table, stored) })
				}),
				"star": func(arg delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return tx(ctx, func(tx documents.Tx) error {
						return tx.Update(table, store.Row{"id": qrInt(arg)}, store.Row{"stars": int64(5)})
					})
				},
				"rename": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return tx(ctx, func(tx documents.Tx) error {
						return tx.Update(table, store.Row{"id": qrInt(args.Value("id"))}, store.Row{"text": args.Value("text")})
					})
				}),
				"drop": func(arg delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return tx(ctx, func(tx documents.Tx) error { return tx.Delete(table, store.Row{"id": qrInt(arg)}) })
				},
			},
		}, nil
	}, ServeOptions{})
}

// qrLive collects the live queries a factory made, weakly (Pi's `instancesOf(LiveQuery, "read")`).
type qrLive struct {
	mu   sync.Mutex
	refs []weak.Pointer[store.LiveQuery]
}

func (l *qrLive) note(member chord.Watchable[delta.JsonValue]) {
	if l == nil {
		return
	}
	if live, ok := unwrapMember(member).(*store.LiveQuery); ok {
		l.mu.Lock()
		l.refs = append(l.refs, weak.Make(live))
		l.mu.Unlock()
	}
}

// last is the last n collected.
func (l *qrLive) last(n int) []weak.Pointer[store.LiveQuery] {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]weak.Pointer[store.LiveQuery](nil), l.refs[len(l.refs)-n:]...)
}

// qrServeBoard is Pi's `serveBoard(h, first)`.
func qrServeBoard(h FacetHost, first int64, live *qrLive) error {
	return h.Serve(qrBoard, func(params *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		args := jsonObject("board", params.Value("board"))
		ofBoard := plain.Get(qrNoteStore, jsonObject()).Queries("notes")["ofBoard"]
		notes, count := plain.LiveQuery(ofBoard, args, first), plain.Count(ofBoard, args)
		live.note(notes)
		live.note(count)
		return contracts.MembersImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"notes": notes, "count": count},
		}, nil
	}, ServeOptions{})
}

// qrHosted is Pi's `hosted(store, probe)`: a host on chordStore with the note store and the boards served by two plugins.
type qrHosted struct {
	Host
	errors *hostErrors
	notes  *Reference
	ctx    context.Context
	meter  context.Context
	counts func() map[string]float64
	live   *qrLive
}

func qrHost(t *testing.T, chordStore *store.ChordStore, probe *qrProbe) *qrHosted {
	t.Helper()
	telemetry, ctx, counts := meteredHost(t)
	errs := &hostErrors{}
	host := newHost(t, HostOptions{Store: chordStore, Telemetry: telemetry, OnError: errs.push})
	live := &qrLive{}
	if probe == nil {
		probe = &qrProbe{}
	}
	_, err := host.Load([]HostFacet{
		{Name: "@test/store", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return qrServeStore(h, probe) })},
		{Name: "@test/boards", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return qrServeBoard(h, 2, live) })},
	}, ctx)
	must(t, err)
	return &qrHosted{Host: host, errors: errs, notes: mustGet(t, host, qrNoteStore, jsonObject()), ctx: ctx, meter: ctx, counts: counts, live: live}
}

func (h *qrHosted) call(t *testing.T, ctx context.Context, method string, args delta.JsonValue) {
	t.Helper()
	if _, err := h.notes.Call(method, args, ctx); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

func (h *qrHosted) put(t *testing.T, ctx context.Context, id int, board, text string) {
	t.Helper()
	h.call(t, ctx, "put", jsonObject("id", float64(id), "board", board, "text", text))
}

func (h *qrHosted) runs() float64 { return h.counts()["chord.query.runs"] }

func qrValue(t *testing.T, ref *Reference, member string) string {
	t.Helper()
	value, err := ref.Member(member).Value()
	must(t, err)
	return nexttest.JSONText(t, value)
}

func qrWant(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %s, want %s", what, got, want)
	}
}

func TestQueryReaderOpen(t *testing.T) {
	t.Run("reads another object's named query before the reader is live, and counts it", func(t *testing.T) {
		chordStore := memoryStore(t)
		first := qrHost(t, chordStore, nil)
		for _, id := range []int{1, 2, 3} {
			first.put(t, background, id, "a", "t"+string(rune('0'+id)))
		}
		first.put(t, background, 9, "b", "other")
		board := mustGet(t, first.Host, qrBoard, jsonObject("board", "a"))
		qrWant(t, "notes", qrValue(t, board, "notes"), `{"rows":[{"id":3,"text":"t3"},{"id":2,"text":"t2"}],"more":true}`)
		qrWant(t, "count", qrValue(t, board, "count"), `3`)
		closeHosts(t, first.Host)
	})

	t.Run("publishes a commit's change in its batch as a diff; a commit that cannot change it runs nothing", func(t *testing.T) {
		chordStore := memoryStore(t)
		h := qrHost(t, chordStore, nil)
		h.put(t, background, 1, "a", "t1")
		board := mustGet(t, h.Host, qrBoard, jsonObject("board", "a"))
		watch, err := board.Member("notes").Watch(background)
		must(t, err)
		calls := nexttest.ChangesOf(watch)
		before := h.runs()
		// Another board's note: the list's probe misses; the count keeps no keys and runs.
		h.put(t, h.ctx, 7, "b", "x")
		deliveredOn(h.Host)
		if calls.Len() != 0 || h.runs()-before != 1 {
			t.Fatalf("other board: %d calls, %v runs; want 0, 1", calls.Len(), h.runs()-before)
		}
		before = h.runs()
		// A column neither read depends on: nothing runs.
		h.call(t, h.ctx, "star", float64(1))
		if h.runs()-before != 0 {
			t.Fatalf("star ran %v reads, want 0", h.runs()-before)
		}
		h.put(t, h.ctx, 2, "a", "t2")
		deliveredOn(h.Host)
		qrWant(t, "notes", qrValue(t, board, "notes"), `{"rows":[{"id":2,"text":"t2"},{"id":1,"text":"t1"}],"more":false}`)
		qrWant(t, "count", qrValue(t, board, "count"), `2`)
		if calls.Len() != 1 {
			t.Fatalf("calls = %d, want 1", calls.Len())
		}
		_, _ = watch.Stop()
		closeHosts(t, h.Host)
	})
}

func TestQueryReaderProjection(t *testing.T) {
	t.Run("hands the mapping only the declared columns; an unlisted column's update changes no item", func(t *testing.T) {
		probe := &qrProbe{}
		h := qrHost(t, memoryStore(t), probe)
		h.put(t, background, 1, "a", "t1")
		board := mustGet(t, h.Host, qrBoard, jsonObject("board", "a"))
		rows, err := board.Member("notes").Value()
		must(t, err)
		item := rows.(*delta.JsonObject).Value("rows").([]any)[0]
		if last := probe.all()[len(probe.all())-1]; strings.Join(last, ",") != "id,text" {
			t.Fatalf("mapped columns = %v, want [id text]", last)
		}
		mapped := len(probe.all())
		h.call(t, background, "star", float64(1))
		if len(probe.all()) != mapped {
			t.Fatalf("mapping ran %d more times after an unlisted column's update", len(probe.all())-mapped)
		}
		again, err := board.Member("notes").Value()
		must(t, err)
		if again.(*delta.JsonObject).Value("rows").([]any)[0] != item {
			t.Fatal("the item is not the same object after an unlisted column's update")
		}
		h.call(t, background, "rename", jsonObject("id", float64(1), "text", "renamed"))
		qrWant(t, "rows", qrValue(t, board, "notes"), `{"rows":[{"id":1,"text":"renamed"}],"more":false}`)
		closeHosts(t, h.Host)
	})
}

var (
	qrFilter   = mustContract(contracts.Local("app.filter", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("board", contracts.Str()), contracts.Mem("show", contracts.NewMethod(contracts.Str()))}}))
	qrFiltered = mustContract(contracts.Local("app.filtered", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("notes", qrPage)}}))
)

// qrServeFiltered is Pi's `serveFiltered(h, factories)`: a filter whose `board` a method sets, and a view whose arguments
// follow it.
func qrServeFiltered(h FacetHost, factories *[]string) error {
	if err := h.Serve(qrFilter, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		board := plain.Writable("a")
		return contracts.MembersImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"board": plain.Derive([]any{board}, func(values []any) (delta.JsonValue, error) { return values[0], nil })},
			Methods: map[string]contracts.MethodFunction{"show": func(arg delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				return nil, board.Set(arg)
			}},
		}, nil
	}, ServeOptions{}); err != nil {
		return err
	}
	return h.Serve(qrFiltered, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		if factories != nil {
			*factories = append(*factories, "filtered")
		}
		filter := plain.Get(qrFilter, jsonObject())
		args := plain.Derive([]any{filter.Member("board")}, func(values []any) (delta.JsonValue, error) { return jsonObject("board", values[0]), nil })
		ofBoard := plain.Get(qrNoteStore, jsonObject()).Queries("notes")["ofBoard"]
		return contracts.MembersImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"notes": plain.LiveQuery(ofBoard, args, 5)},
		}, nil
	}, ServeOptions{})
}

func qrFilteredHost(t *testing.T, factories *[]string) (Host, *store.ChordStore, *Reference) {
	t.Helper()
	chordStore := memoryStore(t)
	host := newHost(t, HostOptions{Store: chordStore, OnError: func(HostError) {}})
	_, err := host.Load(qrFacets(factories), background)
	must(t, err)
	return host, chordStore, mustGet(t, host, qrNoteStore, jsonObject())
}

var qrStoreFacet = HostFacet{Name: "@test/store", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return qrServeStore(h, &qrProbe{}) })}

func qrFacets(factories *[]string) []HostFacet {
	return []HostFacet{qrStoreFacet, {Name: "@test/views", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return qrServeFiltered(h, factories) })}}
}

func qrPut(t *testing.T, notes *Reference, id int, board, text string) {
	t.Helper()
	if _, err := notes.Call("put", jsonObject("id", float64(id), "board", board, "text", text), background); err != nil {
		t.Fatalf("put: %v", err)
	}
}

func qrRows(t *testing.T, view *Reference) string {
	t.Helper()
	value, err := view.Member("notes").Value()
	must(t, err)
	return nexttest.JSONText(t, value.(*delta.JsonObject).Value("rows"))
}

func TestQueryReaderArgumentsThatFollowAValue(t *testing.T) {
	t.Run("shows the old rows until the read for the new arguments ran, then switches in one frame", func(t *testing.T) {
		host, _, notes := qrFilteredHost(t, nil)
		qrPut(t, notes, 1, "a", "a1")
		qrPut(t, notes, 2, "b", "b2")
		view, filter := mustGet(t, host, qrFiltered, jsonObject()), mustGet(t, host, qrFilter, jsonObject())
		qrWant(t, "rows", qrRows(t, view), `[{"id":1,"text":"a1"}]`)
		watch, err := view.Member("notes").Watch(background)
		must(t, err)
		calls := nexttest.ChangesOf(watch)
		if _, err := filter.Call("show", "b", background); err != nil {
			t.Fatal(err)
		}
		// WAITS (reader task): the new read runs on the reader's owned task; the test waits for the view's frame
		// (qrSettled: the host loop's Wait for tasks, `Loop.Wait`), not for a time.
		qrSettled(host)
		flushed(host)
		deliveredOn(host)
		// One frame, from the rows of "a" straight to the rows of "b": never an empty list in between.
		qrWant(t, "rows", qrRows(t, view), `[{"id":2,"text":"b2"}]`)
		if calls.Len() != 1 {
			t.Fatalf("frames = %d, want 1", calls.Len())
		}
		qrPut(t, notes, 3, "a", "a3")
		qrPut(t, notes, 4, "b", "b4")
		qrWant(t, "rows after the old board's note and the new one's", qrRows(t, view), `[{"id":4,"text":"b4"},{"id":2,"text":"b2"}]`)
		_, _ = watch.Stop()
		closeHosts(t, host)
	})
}

func TestQueryReaderArgumentsTheBuilderRefuses(t *testing.T) {
	t.Run("fail the member; a write to the previous arguments' rows neither heals it nor publishes them", func(t *testing.T) {
		host, _, notes := qrFilteredHost(t, nil)
		qrPut(t, notes, 1, "a", "first")
		view, filter := mustGet(t, host, qrFiltered, jsonObject()), mustGet(t, host, qrFilter, jsonObject())
		watch, err := view.Member("notes").Watch(background)
		must(t, err)
		calls := nexttest.ChangesOf(watch)
		if _, err := filter.Call("show", "broken", background); err != nil {
			t.Fatal(err)
		}
		flushed(host)
		if _, err := view.Member("notes").Value(); err == nil || !strings.Contains(err.Error(), "no such board") {
			t.Fatalf("value error = %v, want no such board", err)
		}
		qrPut(t, notes, 1, "a", "edited")
		deliveredOn(host)
		if _, err := view.Member("notes").Value(); err == nil || !strings.Contains(err.Error(), "no such board") {
			t.Fatalf("value error after the write = %v, want no such board", err)
		}
		if items := calls.Items(); len(items) != 1 || items[0].Kind != chord.WatchCallFailed {
			t.Fatalf("calls = %v, want one failed", items)
		}
		// Arguments that build again read again: the member heals with their rows.
		if _, err := filter.Call("show", "a", background); err != nil {
			t.Fatal(err)
		}
		qrSettled(host)
		flushed(host)
		qrWant(t, "rows", qrRows(t, view), `[{"id":1,"text":"edited"}]`)
		_, _ = watch.Stop()
		closeHosts(t, host)
	})
}

func TestQueryReaderFailures(t *testing.T) {
	t.Run("arguments the contract's schema refuses break the reader", func(t *testing.T) {
		chordStore := memoryStore(t)
		errs := &hostErrors{}
		host := newHost(t, HostOptions{Store: chordStore, OnError: errs.push})
		broken := mustContract(contracts.Local("app.broken", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("notes", qrPage)}}))
		_, err := host.Load([]HostFacet{qrStoreFacet, {Name: "@test/views", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(broken, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
				// Plugin code is bundled without type checks: a wrong argument reaches the schema.
				one := plain.Get(qrNoteStore, jsonObject()).Queries("notes")["one"]
				return contracts.MembersImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"notes": plain.LiveQuery(one, jsonObject("id", "x"), 1)},
				}, nil
			}, ServeOptions{})
		})}}, background)
		must(t, err)
		_, getErr := host.Get(background, broken, jsonObject(), GetOptions{})
		refused := rejection(t, getErr)
		if !strings.Contains(nexttest.JSONText(t, refused), `"reason":"args"`) {
			t.Fatalf("refused = %v, want invalid/args", refused)
		}
		if got := failures(errs.All()); len(got) != 1 || !strings.HasSuffix(got[0], ": broken") {
			t.Fatalf("activation failures = %v, want one broken", got)
		}
		closeHosts(t, host)
	})

	t.Run("a builder that throws breaks the reader: its own code, not a failure to wait for", func(t *testing.T) {
		// `isOwnCode`: a throwing builder is a plain error, which is the reader's own code (broken), where a provider that is
		// not live is waited for. The attempt's activation fails with it at once instead of parking.
		chordStore := memoryStore(t)
		errs := &hostErrors{}
		host := newHost(t, HostOptions{Store: chordStore, OnError: errs.push})
		broken := mustContract(contracts.Local("app.thrower", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("notes", qrPage)}}))
		_, err := host.Load([]HostFacet{qrStoreFacet, {Name: "@test/views", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(broken, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
				ofBoard := plain.Get(qrNoteStore, jsonObject()).Queries("notes")["ofBoard"]
				return contracts.MembersImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"notes": plain.LiveQuery(ofBoard, jsonObject("board", "broken"), 1)},
				}, nil
			}, ServeOptions{})
		})}}, background)
		must(t, err)
		_, getErr := host.Get(background, broken, jsonObject(), GetOptions{})
		if getErr == nil || !strings.Contains(getErr.Error(), "no such board") {
			t.Fatalf("get error = %v, want the builder's own error", getErr)
		}
		if got := failures(errs.All()); len(got) != 1 || !strings.HasSuffix(got[0], ": broken") {
			t.Fatalf("activation failures = %v, want one broken", got)
		}
		closeHosts(t, host)
	})

	t.Run("the wrong form of each call throws naming the other; a private query needs its schema", func(t *testing.T) {
		// Pi's three refusals, in Pi's order (query-reader.test.ts:359-390). The first (`self.query(named.all, ...)`, a named
		// query given as a table) cannot be written in Go: `Self.Query` takes a store.Table, which no named query is (a table
		// that is none of the factory's is refused as undeclared), and no other path hands the host a named query as a table
		// (nextnode calls no Self method; it is the plugin API in process): the compiler states the refusal, Pi's run-time
		// check of a JavaScript call (read-declarations.ts) has no Go counterpart. The second (a table given to
		// `self.liveQuery`) is a value that is no named query of this object (`nqForeign`, as in the staging case); the third
		// is the first of 0 (`{}` for Pi's `{ first: n }`). The owner's own private query is then counted, read in its own
		// activation, with no dependency on itself.
		chordStore := memoryStore(t)
		errs := &hostErrors{}
		host := newHost(t, HostOptions{Store: chordStore, OnError: errs.push})
		own := mustContract(contracts.Local("app.own", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
		var thrown []string
		_, err := host.Load([]HostFacet{{Name: "@test/own", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(own, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
				table := plain.(StoreSelf).Table(qrNotes)
				named := plain.(StoreSelf).Queries(table, QueriesOptions{Map: func(row store.Row, _ PartnerRow) (delta.JsonValue, error) { return float64(qrInt(row["id"])), nil }},
					map[string]QueryBuilder{"all": {Args: contracts.Obj(), Build: func(delta.JsonValue) *store.Query { return store.NewQuery(qrNotes) }}})
				attempt := func(work func()) {
					defer func() {
						if r := recover(); r != nil {
							thrown = append(thrown, r.(error).Error())
						}
					}()
					work()
				}
				attempt(func() { plain.LiveQuery(nqForeign{}, jsonObject(), 1) })
				attempt(func() { plain.LiveQuery(named["all"], jsonObject(), 0) })
				return contracts.MembersImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": plain.Count(named["all"], jsonObject())}}, nil
			}, ServeOptions{})
		})}}, background)
		must(t, err)
		want := []string{
			"app.own: self.liveQuery reads a named query; a table of this object is read with self.query",
			"app.own: self.liveQuery: { first: n } is required, a positive integer",
		}
		if !slices.Equal(thrown, want) {
			t.Fatalf("thrown = %q, want %q", thrown, want)
		}
		ownRef := mustGet(t, host, own, jsonObject())
		qrWant(t, "n", qrValue(t, ownRef, "n"), `0`)
		closeHosts(t, host)
	})
}

var (
	qrTagSet = func() *store.ColumnSet {
		set, err := store.DefineColumns("tags.columns", store.ColumnSetOptions{Columns: []store.ColumnProp{store.Col("note", store.Int()), store.Col("tag", store.Str())}, Key: []string{"note", "tag"}})
		if err != nil {
			panic(err)
		}
		return set
	}()
	qrTags        = mustTableDef("tags", store.TableOptions{Version: 1, Columns: append([]store.ColumnProp(nil), qrTagSet.Schemas...), Key: []string{"note", "tag"}, Exposes: qrTagSet})
	qrTagStore    = mustContract(contracts.Local("app.tags", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("tag", contracts.NewMethod(contracts.Obj(contracts.Prop("note", contracts.Int()), contracts.Prop("tag", contracts.Str()))))}}))
	qrTagged      = mustContract(contracts.Local("app.tagged", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("notes", qrPage)}}))
	qrTaggedNotes = mustContract(contracts.Local("app.tagged-notes", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("notes", contracts.Queries(qrNote, []contracts.Property{contracts.Prop("tagged", contracts.Obj(contracts.Prop("tag", contracts.Str())))})),
		contracts.Mem("put", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("board", contracts.Str()), contracts.Prop("text", contracts.Str())))),
	}}))
	qrMine = mustTableDef("mine", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Int()), store.Col("board", store.Str()), store.Col("text", store.Str())}, Key: []string{"id"}})
)

func qrServeTags(h FacetHost) error {
	return h.Serve(qrTagStore, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		table := plain.(StoreSelf).Table(qrTags)
		return contracts.MembersImplementation{Methods: map[string]contracts.MethodFunction{
			"tag": exMethod(func(row *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
				_, err := plain.Tx(func(tx documents.Tx) (any, error) {
					return nil, tx.Put(table, store.Row{"note": qrInt(row.Value("note")), "tag": row.Value("tag")})
				}, ctx)
				return nil, err
			}),
		}}, nil
	}, ServeOptions{})
}

// qrServeTagged is Pi's `serveTagged`: a store whose query tests `exists` in the tags' column set (another plugin's), and
// a view of it.
func qrServeTagged(h FacetHost, extra func(self Self)) error {
	if err := h.Serve(qrTaggedNotes, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		table := plain.(StoreSelf).Table(qrMine)
		notes := plain.(StoreSelf).Queries(table, QueriesOptions{Map: func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
			return qrNoteJSON(qrInt(row["id"]), row["text"]), nil
		}},
			map[string]QueryBuilder{"tagged": {Build: func(args delta.JsonValue) *store.Query {
				tag := args.(*delta.JsonObject).Value("tag").(string)
				return store.NewQuery(qrMine).Where(func(n, _ store.ColumnRefs) store.Condition {
					return store.Exists(qrTagSet, store.Match{{Column: "note", Ref: n["id"]}}, func(t store.ColumnRefs) store.Condition { return store.Eq(t["tag"], tag) })
				})
			}}})
		return contracts.MembersImplementation{
			Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{"notes": notes},
			Methods: map[string]contracts.MethodFunction{"put": exMethod(func(row *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
				_, err := plain.Tx(func(tx documents.Tx) (any, error) {
					return nil, tx.Put(table, store.Row{"id": qrInt(row.Value("id")), "board": row.Value("board"), "text": row.Value("text")})
				}, ctx)
				return nil, err
			})},
		}, nil
	}, ServeOptions{}); err != nil {
		return err
	}
	return h.Serve(qrTagged, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		tagged := plain.Get(qrTaggedNotes, jsonObject()).Queries("notes")["tagged"]
		return contracts.MembersImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"notes": plain.LiveQuery(tagged, jsonObject("tag", "red"), 5)},
		}, nil
	}, ServeOptions{})
}

func qrState(t *testing.T, view *Reference) *RefState {
	t.Helper()
	return view.State().Value()
}

func TestQueryReaderAColumnSetNobodyServes(t *testing.T) {
	t.Run("parks the reader failed { waiting } and heals when the set is served; the other owner's commit then reaches it", func(t *testing.T) {
		chordStore := memoryStore(t)
		errs := &hostErrors{}
		host := newHost(t, HostOptions{Store: chordStore, OnError: errs.push})
		tagged := HostFacet{Name: "@test/tagged", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return qrServeTagged(h, nil) })}
		_, err := host.Load([]HostFacet{tagged}, background)
		must(t, err)
		notes := mustGet(t, host, qrTaggedNotes, jsonObject())
		if _, err := notes.Call("put", jsonObject("id", float64(1), "board", "a", "text", "t1"), background); err != nil {
			t.Fatal(err)
		}
		view, err := host.Hold(background, qrTagged, jsonObject(), GetOptions{})
		must(t, err)
		flushed(host)
		state := qrState(t, view)
		if string(state.Kind) != "failed" || string(state.Cause.Kind) != "waiting" || !strings.Contains(state.Cause.Error.Message, `"tags.columns" is not served`) || string(state.Cause.Error.Code) != "unavailable" {
			t.Fatalf("state = %+v, want failed { waiting } unavailable: tags.columns is not served", state)
		}
		_, err = host.Reload([]HostFacet{tagged, {Name: "@test/tags", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return qrServeTags(h) })}}, background, ReloadOptions{})
		must(t, err)
		// The tags' store goes live when someone holds it: its table then exposes the set, and the reader runs.
		tags := mustGet(t, host, qrTagStore, jsonObject())
		// The store's own re-run of the read over the set, once bound, is no task of the host: the view's state is what the
		// test waits on (Pi's `await flushed()`).
		waitLive(t, view)
		flushed(host)
		if got := qrState(t, view); string(got.Kind) != "live" {
			t.Fatalf("state = %+v, want live", got)
		}
		qrWant(t, "rows", qrRows(t, view), `[]`)
		// The tags' owner commits: the read over its table runs in that owner's step.
		if _, err := tags.Call("tag", jsonObject("note", float64(1), "tag", "red"), background); err != nil {
			t.Fatal(err)
		}
		qrWant(t, "rows after the red tag", qrRows(t, view), `[{"id":1,"text":"t1"}]`)
		if _, err := tags.Call("tag", jsonObject("note", float64(1), "tag", "blue"), background); err != nil {
			t.Fatal(err)
		}
		qrWant(t, "rows after the blue tag", qrRows(t, view), `[{"id":1,"text":"t1"}]`)
		// A closing host reports no read failed: its objects keep their sets bound until every object is torn down (the
		// readers' reads are closed by then), and leave none in the store.
		before := errs.Len()
		closeHosts(t, host)
		if errs.Len() != before {
			t.Fatalf("closing reported %v", errs.All()[before:])
		}
		if _, ok := chordStore.Reads.Get(qrTagSet.Name); ok { // ASSUMED shape of Reads.Get: (value, ok)
			t.Fatal("the store still has a read set for tags.columns")
		}
		view.Release()
	})
}

func TestQueryReaderReadersBeforeNatives(t *testing.T) {
	t.Run("an attempt that waits on its reader holds no native: it opens when the read ran", func(t *testing.T) {
		// Opus S3, Fable S9: a waiting attempt keeps nothing open that a native holds (a socket, a process).
		chordStore := memoryStore(t)
		host := newHost(t, HostOptions{Store: chordStore, OnError: func(HostError) {}})
		var mu sync.Mutex
		var natives []string
		note := func(what string) { mu.Lock(); natives = append(natives, what); mu.Unlock() }
		got := func() string { mu.Lock(); defer mu.Unlock(); return strings.Join(natives, ",") }
		watching := mustContract(contracts.Local("app.watching", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("notes", qrPage)}}))
		tagged := HostFacet{Name: "@test/tagged", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return qrServeTagged(h, nil) })}
		watcher := HostFacet{Name: "@test/watching", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(watching, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
				plain.Open(func(context.Context) (any, error) { note("open"); return nil, nil },
					func(any, NativeCloseReason, context.Context) error { note("close"); return nil })
				tagged := plain.Get(qrTaggedNotes, jsonObject()).Queries("notes")["tagged"]
				return contracts.MembersImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"notes": plain.LiveQuery(tagged, jsonObject("tag", "red"), 5)},
				}, nil
			}, ServeOptions{})
		})}
		_, err := host.Load([]HostFacet{tagged, watcher}, background)
		must(t, err)
		view, err := host.Hold(background, watching, jsonObject(), GetOptions{})
		must(t, err)
		flushed(host)
		if state := qrState(t, view); string(state.Kind) != "failed" || string(state.Cause.Kind) != "waiting" {
			t.Fatalf("state = %+v, want failed { waiting }", state)
		}
		if got() != "" {
			t.Fatalf("natives = %q, want none", got())
		}
		_, err = host.Reload([]HostFacet{tagged, watcher, {Name: "@test/tags", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return qrServeTags(h) })}}, background, ReloadOptions{})
		must(t, err)
		mustGet(t, host, qrTagStore, jsonObject())
		waitLive(t, view)
		flushed(host)
		if state := qrState(t, view); string(state.Kind) != "live" {
			t.Fatalf("state = %+v, want live", state)
		}
		if got() != "open" {
			t.Fatalf("natives = %q, want open", got())
		}
		view.Release()
		closeHosts(t, host)
	})
}

func TestQueryReaderArgumentsThatHaveNoValueWhenTheReaderOpens(t *testing.T) {
	t.Run("break the attempt, naming the rule: a followed source attaches after the readers, so waiting would never end", func(t *testing.T) {
		// Opus S1: the argument's derive threw `not_ready`, the reader waited, and nothing could wake the attempt.
		chordStore := memoryStore(t)
		host := newHost(t, HostOptions{Store: chordStore, OnError: func(HostError) {}})
		following := mustContract(contracts.Local("app.following", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("notes", qrPage)}}))
		_, err := host.Load([]HostFacet{{Name: "@test/following", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			if err := qrServeStore(h, &qrProbe{}); err != nil {
				return err
			}
			return h.Serve(following, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
				board := plain.Follow(func(FollowObserver[any], context.Context) (*FollowAttachment[delta.JsonValue], error) {
					return &FollowAttachment[delta.JsonValue]{Value: jsonObject("board", "a"), Detach: func() {}}, nil
				})
				args := plain.Derive([]any{board}, func(values []any) (delta.JsonValue, error) {
					return jsonObject("board", values[0].(*delta.JsonObject).Value("board")), nil
				})
				ofBoard := plain.Get(qrNoteStore, jsonObject()).Queries("notes")["ofBoard"]
				return contracts.MembersImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"notes": plain.LiveQuery(ofBoard, args, 5)},
				}, nil
			}, ServeOptions{})
		})}}, background)
		must(t, err)
		view, err := host.Hold(background, following, jsonObject(), GetOptions{})
		must(t, err)
		flushed(host)
		state := qrState(t, view)
		rule := "a reader's arguments read references, registries, writables and earlier reads, not a followed source or a native"
		if string(state.Kind) != "failed" || string(state.Cause.Kind) != "broken" || string(state.Cause.Error.Code) != "invalid" || !strings.Contains(state.Cause.Error.Message, rule) {
			t.Fatalf("state = %+v, want failed { broken } invalid: %s", state, rule)
		}
		view.Release()
		closeHosts(t, host)
	})
}

func TestQueryReaderAProvidersRestart(t *testing.T) {
	t.Run("does not restart its readers: they read the successor's query and give one frame, none when equal", func(t *testing.T) {
		var factories []string
		chordStore := memoryStore(t)
		host := newHost(t, HostOptions{Store: chordStore, OnError: func(HostError) {}})
		views := HostFacet{Name: "@test/views", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return qrServeFiltered(h, &factories) })}
		_, err := host.Load([]HostFacet{qrStoreFacet, views}, background)
		must(t, err)
		notes := mustGet(t, host, qrNoteStore, jsonObject())
		for _, id := range []int{1, 2} {
			qrPut(t, notes, id, "a", "t"+string(rune('0'+id)))
		}
		view := mustGet(t, host, qrFiltered, jsonObject())
		watch, err := view.Member("notes").Watch(background)
		must(t, err)
		calls := nexttest.ChangesOf(watch)
		if strings.Join(factories, ",") != "filtered" {
			t.Fatalf("factories = %v", factories)
		}
		// The same code again: the readers bind to the successor, and equal rows send nothing.
		again := HostFacet{Name: "@test/store", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return qrServeStore(h, &qrProbe{}) })}
		_, err = host.Reload([]HostFacet{again, views}, background, ReloadOptions{})
		must(t, err)
		qrSettled(host)
		flushed(host)
		deliveredOn(host)
		if strings.Join(factories, ",") != "filtered" || calls.Len() != 0 {
			t.Fatalf("after the reload: factories %v, %d frames; want [filtered], 0", factories, calls.Len())
		}
		// The successor's commit reaches the reader, once.
		qrPut(t, notes, 3, "a", "t3")
		deliveredOn(host)
		value, err := view.Member("notes").Value()
		must(t, err)
		qrWant(t, "ids", nexttest.JSONText(t, value.(*delta.JsonObject).Value("rows")), `[{"id":3,"text":"t3"},{"id":2,"text":"t2"},{"id":1,"text":"t1"}]`)
		if calls.Len() != 1 {
			t.Fatalf("frames = %d, want 1", calls.Len())
		}
		_, _ = watch.Stop()
		closeHosts(t, host)
	})
}

func TestQueryReaderCount(t *testing.T) {
	t.Run("is exact and uncapped; skipped only by an update of no condition column", func(t *testing.T) {
		h := qrHost(t, memoryStore(t), nil)
		for id := 1; id <= 300; id++ {
			h.put(t, background, id, "a", "t")
		}
		board := mustGet(t, h.Host, qrBoard, jsonObject("board", "a"))
		qrWant(t, "count", qrValue(t, board, "count"), `300`)
		before := h.runs()
		// A rename: the list's mapped column (one run), no condition column of the count.
		h.call(t, h.ctx, "rename", jsonObject("id", float64(300), "text", "renamed"))
		if h.runs()-before != 1 {
			t.Fatalf("rename ran %v reads, want 1", h.runs()-before)
		}
		h.call(t, h.ctx, "drop", float64(300))
		qrWant(t, "count after the drop", qrValue(t, board, "count"), `299`)
		closeHosts(t, h.Host)
	})
}

func TestQueryReaderMemory(t *testing.T) {
	t.Run("collects a reader's live query once its object ended", func(t *testing.T) {
		chordStore := memoryStore(t)
		h := qrHost(t, chordStore, nil)
		board := mustGet(t, h.Host, qrBoard, jsonObject("board", "a"))
		qrWant(t, "count", qrValue(t, board, "count"), `0`)
		mine := h.live.last(2)
		board.Release()
		closeHosts(t, h.Host)
		must(t, chordStore.Close(background))
		expectCollected(t, mine...)
	})
}

// qrSettled waits until the reads the host's readers queued ran (what Pi's `await flushed()` after a move waits for, with no
// sleep): the drain tasks of every reader of the attempts the objects have. Not Loop.Wait: a reference's follow task lives
// as long as the host.
func qrSettled(host Host) {
	var readers []*reader
	hostTurn(host, func() {
		for _, object := range host.(*chordHost).objectList() {
			for _, each := range []*attempt{object.phase.attempt, object.exposed, object.old, object.kept, object.showsReadiness} {
				if each != nil {
					readers = append(readers, each.readers...)
				}
			}
		}
	})
	for _, each := range readers {
		each.settle()
	}
}

func TestQueryReaderTheCommitStepChoosesItsReads(t *testing.T) {
	t.Run("a read opened while a commit waits in the queue is refreshed by it", func(t *testing.T) {
		chordStore, _, holdNextCommit := faultyStore(t)
		h := qrHost(t, chordStore, nil)
		// One commit held in its step, the note's commit queued behind it: staged before the reader exists.
		held := holdNextCommit()
		blocker := startQueued(t, chordStore, func() {
			if _, err := chordStore.Commit([]store.StagedWrite{qrNoopWrite{}}, openFence(), background); err != nil {
				t.Errorf("blocker: %v", err)
			}
		})
		<-held.Entered()
		// Pi's `await flushed()` lets the queued commit reach the queue by microtask order; a goroutine has no such order:
		// startQueued returns once the second commit is queued behind the held one.
		late := startQueued(t, chordStore, func() {
			if _, err := h.notes.Call("put", jsonObject("id", float64(1), "board", "a", "text", "late"), background); err != nil {
				t.Errorf("late: %v", err)
			}
		})
		// The board's get is Pi's un-awaited `host.get`: it ends when the reader is live, which waits for the commits.
		opened := make(chan *Reference, 1)
		go func() { opened <- mustGet(t, h.Host, qrBoard, jsonObject("board", "a")) }()
		flushed(h.Host)
		held.Release()
		<-blocker
		<-late
		board := <-opened
		qrWant(t, "rows", qrValue(t, board, "notes"), `{"rows":[{"id":1,"text":"late"}],"more":false}`)
		qrWant(t, "count", qrValue(t, board, "count"), `1`)
		closeHosts(t, h.Host)
	})
}

// qrNoopWrite is a write that changes nothing: a commit step that has a write to close (what holdNextCommit holds).
type qrNoopWrite struct{}

func (qrNoopWrite) StageOrder() store.WriteOrder { return store.OrderWrite }

func (qrNoopWrite) Apply(*store.Step) ([]store.StoreWrite, error) { return nil, nil }
