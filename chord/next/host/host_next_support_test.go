package host

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
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
	"github.com/MichaelKinsy/PiG/chord/nextnode"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/test/next/support.ts: hosts, links, raw peers, notes types, relays and stores for the slice
// tests of host, jobs and remote. Names are Pi's, camelCase.
//
// Go forms (the contract the other D5a test files follow):
//   - `counted`, `metered`, `delivered`, `framesOf`, `changesOf`, `valuesOf`, `expectCollected` are
//     `chord/next/internal/nexttest` (Counted, Metered, Delivered, FramesOf, ChangesOf, ValuesOf, ExpectCollected); not
//     redeclared. `Equal<A,B>` is type-level only (Go's compiler checks it). `settledHeap` is `runtime.GC` rounds in
//     the one benchmark that uses it (jobs bench). `instancesOf(ChordObject|Hold|RemoteObject, ...)` is designed out:
//     a Go test reaches the object through `host.objects` / the attempt's `object` (see failLive) and checks
//     collection with `weak.Pointer` through nexttest.ExpectCollected.
//   - Pi's `Principal` generics are gone: a principal is `data.Principal` (*delta.JsonObject), made by ownerPrincipal,
//     guestPrincipal(session), serverPrincipal, workerPrincipal(session) (Pi's `TestPrincipal` variants).
//   - A blocking Promise is a returned `(value, error)`; every helper that awaits takes the test's `t` and fails it.
//   - Milliseconds are `time.Duration`; an omitted option is nil (`*time.Duration`).
//   - Assumed host API (HostCore): `CreateHost(HostOptions) Host`; `Host.Load([]HostFacet, ctx) error`,
//     `Get(Type, params, ctx, GetOptions) (Reference, error)`, `Hold`, `Accept(link, LinkOptions, ctx)`,
//     `Connect(link, LinkOptions, ctx)`, `Route(kind, RouteOptions, ctx)`, `Close(ctx) error`; `HostFacet{Name, Facet}`;
//     `HostOptions{Address *contracts.Address, Principal, Telemetry, OnError, CloseTimeout/ReadyTimeout
//     *time.Duration, RevokedBy, Compression link.Compression, Store store.Store}`.
//   - Assumed factory shape (AttemptObject): a served local type's factory is
//     `func(params *delta.JsonObject, self Self) (contracts.Implementation, error)`; a remote one's
//     `func(params *delta.JsonObject, principal data.Principal, self Self) (contracts.Implementation, error)`.

var background = chordctx.Background()

// newHost is `createHost(options)`: it fails the test when the host cannot be made (its store is attached to another
// open host).
func newHost(t testing.TB, options HostOptions) Host {
	t.Helper()
	host, err := CreateHost(options)
	must(t, err)
	return host
}

// telemetryOf is a HostOptions.Telemetry from an optional adapter: a nil adapter is the unset interface, never a typed nil.
func telemetryOf(adapter *telemetry.InMemoryTelemetryContext) telemetry.TelemetryContext {
	if adapter == nil {
		return nil
	}
	return adapter
}

// ownerPrincipal is Pi's `{ kind: "owner" }`.
func ownerPrincipal() data.Principal { return delta.JsonObjectOf("kind", "owner") }

// guestPrincipal is Pi's `{ kind: "guest", session }`.
func guestPrincipal(session string) data.Principal {
	return delta.JsonObjectOf("kind", "guest", "session", session)
}

// serverPrincipal is Pi's `{ kind: "server" }`.
func serverPrincipal() data.Principal { return delta.JsonObjectOf("kind", "server") }

// workerPrincipal is Pi's `{ kind: "worker", session }`.
func workerPrincipal(session string) data.Principal {
	return delta.JsonObjectOf("kind", "worker", "session", session)
}

// principalKind is `principal.kind`.
func principalKind(principal data.Principal) string {
	kind, _ := principal.Value("kind").(string)
	return kind
}

// must fails the test when err is not nil.
func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%v", err)
	}
}

// jsonObject is the object of the key/value pairs, as an object literal.
func jsonObject(pairs ...any) *delta.JsonObject { return delta.JsonObjectOf(pairs...) }

// --- Restartable types -------------------------------------------------------------------------------------------

func mustContract(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

// Restartable is a local type whose objects' attempts a test ends by hand; it ends with its last reference.
var Restartable = mustContract(contracts.Local("app.restartable", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
	Members: []contracts.LocalEntry{contracts.Mem("touch", contracts.NewMethod())},
}))

// StoreRestartable is a local type with empty params whose attempts a test ends by hand: it may persist (P3).
var StoreRestartable = mustContract(contracts.Local("app.store", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("touch", contracts.NewMethod())},
}))

// failLive fails the live attempt `self` as a runtime failure of its owner does (slice 6a section 1.4). Pi searches
// the test file's `instancesOf(ChordObject, "attempt")` for the object that owns the attempt; the Go attempt knows
// its object.
func failLive(self Self, err *data.ChordError) {
	current := self.(*attempt)
	// Pi's `failLive` is a synchronous section of the host: enter it.
	current.object.host.enter(func() {
		current.object.failLive(current, liveFailure{kind: "broken", err: err})
	})
}

// attemptList is `attempts: Self[]` of Pi's restartable, written by factories (which run in the host's sections).
type attemptList struct {
	mu   sync.Mutex
	list []Self
}

func (a *attemptList) add(self Self) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.list = append(a.list, self)
}

func (a *attemptList) at(index int) Self {
	a.mu.Lock()
	defer a.mu.Unlock()
	if index < 0 {
		index += len(a.list)
	}
	return a.list[index]
}

func (a *attemptList) len() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.list)
}

// loadedOptions is the options of `loaded`: Pi's `{ telemetry, context, closeTimeout, readyTimeout, store }`.
type loadedOptions struct {
	Telemetry    *telemetry.InMemoryTelemetryContext
	Context      context.Context
	CloseTimeout *time.Duration
	ReadyTimeout *time.Duration
	Store        store.Store
	// Serve is `restartable`'s `serve`: serves other types on the same host.
	Serve func(host FacetHost)
	// Loop drives the host (Pi's fake timers); nil is the real loop.
	Loop sectionLoop
}

// restartable serves Restartable with factory per attempt (its number from 1) and gets object 1 live. fail() fails the
// live attempt as a runtime failure of its owner does; retry() asks for the next attempt and returns once it is live;
// restart() does both.
func restartable(t testing.TB, factory func(attempt int, self Self), options loadedOptions) *restartableRunner {
	t.Helper()
	return restartableOf(t, Restartable, jsonObject("id", float64(1)), func(attempt int, self Self) {
		factory(attempt, self)
	}, options)
}

// storeRestartable is restartable for StoreRestartable: its factories may declare tables and persisted documents.
func storeRestartable(t testing.TB, factory func(attempt int, self StoreSelf), options loadedOptions) *restartableRunner {
	t.Helper()
	return restartableOf(t, StoreRestartable, jsonObject(), func(attempt int, self Self) {
		factory(attempt, self.(StoreSelf))
	}, options)
}

// restartableRunner is the value of Pi's `restartable` (`{ host, errors, reference, attempts, fail, retry, restart }`).
type restartableRunner struct {
	t         testing.TB
	Host      Host
	Errors    *hostErrors
	Reference *Reference
	Attempts  *attemptList
	get       func() *Reference
}

// Fail fails the live attempt as a runtime failure of its owner does.
func (r *restartableRunner) Fail() {
	failLive(r.Attempts.at(-1), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
}

// Retry asks for the next attempt and returns once it is live.
func (r *restartableRunner) Retry() {
	r.Reference.Retry()
	r.get().Release()
}

// Restart is Fail and Retry.
func (r *restartableRunner) Restart() {
	r.Fail()
	r.Retry()
}

func restartableOf(
	t testing.TB, typ *contracts.Contract, params *delta.JsonObject, factory func(attempt int, self Self), options loadedOptions,
) *restartableRunner {
	t.Helper()
	attempts := &attemptList{}
	started := 0
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if options.Serve != nil {
			options.Serve(h)
		}
		return h.Serve(typ, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			started++
			factory(started, self)
			attempts.add(self)
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"touch": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
			}}}, nil
		}, ServeOptions{})
	}, options)
	get := func() *Reference {
		reference, err := host.Get(typ, params, background, GetOptions{})
		must(t, err)
		return reference
	}
	return &restartableRunner{t: t, Host: host, Errors: errs, Reference: get(), Attempts: attempts, get: get}
}

// --- hosts ---------------------------------------------------------------------------------------------------------

// hostErrors is Pi's `errors: HostError[]`: what `onError` got, safe to read from the test goroutine.
type hostErrors struct {
	mu   sync.Mutex
	list []HostError
}

func (e *hostErrors) push(err HostError) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, err)
}

// All is a copy of the errors collected so far.
func (e *hostErrors) All() []HostError {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]HostError(nil), e.list...)
}

// Len is `errors.length`.
func (e *hostErrors) Len() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.list)
}

// loaded is a host with background errors collected, loaded with one facet (`@test/app`) whose setup is setup.
func loaded(t testing.TB, setup func(host FacetHost, self FacetSelf) error, options loadedOptions) (Host, *hostErrors) {
	t.Helper()
	errs := &hostErrors{}
	host := newHost(t, HostOptions{
		Telemetry:    telemetryOf(options.Telemetry),
		CloseTimeout: options.CloseTimeout,
		ReadyTimeout: options.ReadyTimeout,
		Store:        options.Store,
		OnError:      errs.push,
		loop:         options.Loop,
	})
	ctx := options.Context
	if ctx == nil {
		ctx = background
	}
	must(t, host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(setup)}}, ctx))
	return host, errs
}

// loadedPlugin is one plugin of loadedPlugins: its name and setup.
type loadedPlugin struct {
	Name  string
	Setup func(host FacetHost, self FacetSelf) error
}

// loadedPlugins is a host with background errors collected, loaded with one facet per plugin, in the order given.
func loadedPlugins(t testing.TB, setups []loadedPlugin, options loadedOptions) (Host, *hostErrors) {
	t.Helper()
	errs := &hostErrors{}
	host := newHost(t, HostOptions{Telemetry: telemetryOf(options.Telemetry), ReadyTimeout: options.ReadyTimeout, OnError: errs.push, loop: options.Loop})
	facets := make([]HostFacet, len(setups))
	for i, plugin := range setups {
		facets[i] = HostFacet{Name: plugin.Name, Facet: DefineFacet(plugin.Setup)}
	}
	must(t, host.Load(facets, background))
	return host, errs
}

// failures is the kinds and objects of the reported `activation_failed` errors, with their causes' kinds.
func failures(errs []HostError) []string {
	var out []string
	for _, err := range errs {
		if err.Kind == ErrActivationFailed {
			out = append(out, err.Object+": "+string(err.Failure.Kind))
		}
	}
	return out
}

// --- stores --------------------------------------------------------------------------------------------------------

// openMemoryStore opens a store on a fresh `:memory:` database through wrap (which wraps it for the fault helpers).
func openMemoryStore(t testing.TB, wrap func(sqlite.SqliteDatabase) sqlite.SqliteDatabase, outbox []bool) *store.ChordStore {
	t.Helper()
	database, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatalf("open :memory:: %v", err)
	}
	var db sqlite.SqliteDatabase = database
	if wrap != nil {
		db = wrap(db)
	}
	options := store.StoreOptions{}
	if len(outbox) > 0 {
		options.Outbox = &outbox[0]
	}
	opened, err := store.OpenStore(store.SqliteBackend(db), background, options)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	opened2 := opened.(*store.ChordStore)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = opened2.Close(ctx)
	})
	return opened2
}

// memoryStore is a store on a fresh SQLite `:memory:` database, as Chord's layers see it.
func memoryStore(t testing.TB, outbox ...bool) *store.ChordStore {
	t.Helper()
	return openMemoryStore(t, nil, outbox)
}

// The statements of the Node facade's transactions a test can make fail (Pi's TransactionStatement).
const (
	failBegin    = "BEGIN IMMEDIATE"
	failCommit   = "COMMIT"
	failRollback = "ROLLBACK"
)

// transactionGate holds a transaction inside its backend transaction, at its first statement.
type transactionGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	freed   sync.Once
}

func newTransactionGate() *transactionGate {
	return &transactionGate{entered: make(chan struct{}), release: make(chan struct{})}
}

func (gate *transactionGate) hit() {
	gate.once.Do(func() { close(gate.entered) })
	<-gate.release
}

// Entered is closed when the held transaction reached its first statement.
func (gate *transactionGate) Entered() <-chan struct{} { return gate.entered }

// Release lets the held transaction go on.
func (gate *transactionGate) Release() { gate.freed.Do(func() { close(gate.release) }) }

type gatedExecutor struct {
	sqlite.SqliteExecutor
	gate *transactionGate
}

func (e *gatedExecutor) Exec(sql string) error {
	e.gate.hit()
	return e.SqliteExecutor.Exec(sql)
}

func (e *gatedExecutor) Run(sql string, params ...sqlite.SqliteValue) error {
	e.gate.hit()
	return e.SqliteExecutor.Run(sql, params...)
}

func (e *gatedExecutor) RunCounted(sql string, params ...sqlite.SqliteValue) (int64, error) {
	e.gate.hit()
	return e.SqliteExecutor.RunCounted(sql, params...)
}

func (e *gatedExecutor) Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	e.gate.hit()
	return e.SqliteExecutor.Get(sql, params...)
}

func (e *gatedExecutor) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	e.gate.hit()
	return e.SqliteExecutor.All(sql, params...)
}

// faultyDatabase is a Node SQLite facade on `:memory:` whose next statement of a transaction throws, once per fail
// (the D4 matrix: a failed `BEGIN`, `COMMIT` or `ROLLBACK`); it can also hold the next transaction (holdNextCommit).
// Go forced difference (as store/support_test.go): Go's facade runs its transaction statements on an unexported
// connection, so the wrapper injects the failures around Transaction.
type faultyDatabase struct {
	sqlite.SqliteDatabase
	mu      sync.Mutex
	failing map[string]bool
	gate    *transactionGate
}

func (db *faultyDatabase) take(statement string) bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	failed := db.failing[statement]
	delete(db.failing, statement)
	return failed
}

func (db *faultyDatabase) fail(statement string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.failing[statement] = true
}

func (db *faultyDatabase) takeGate() *transactionGate {
	db.mu.Lock()
	defer db.mu.Unlock()
	gate := db.gate
	db.gate = nil
	return gate
}

// Transaction is the facade's, with the injected failures and the hold.
func (db *faultyDatabase) Transaction(callback func(transaction sqlite.SqliteExecutor) error) error {
	if db.take(failBegin) {
		return errors.New("injected: BEGIN IMMEDIATE failed")
	}
	gate := db.takeGate()
	err := db.SqliteDatabase.Transaction(func(transaction sqlite.SqliteExecutor) error {
		if gate != nil {
			transaction = &gatedExecutor{SqliteExecutor: transaction, gate: gate}
		}
		if err := callback(transaction); err != nil {
			return err
		}
		if db.take(failCommit) {
			return errors.New("injected: COMMIT failed")
		}
		return nil
	})
	if err != nil && db.take(failRollback) {
		return errors.New("injected: ROLLBACK failed")
	}
	return err
}

// faultyStore is a store over a faultyDatabase, its fail and its holdNextCommit: Pi's `faultyStore` plus
// `holdNextCommit(store)`. holdNextCommit holds the store's next commit step inside its backend transaction after its
// admission: Entered is closed there, Release lets it finish; later commits run as usual (Go forced difference: Pi
// replaces `store.commit` for one call; Go holds the next backend transaction at its first statement).
func faultyStore(t testing.TB, outbox ...bool) (st *store.ChordStore, fail func(statement string), holdNextCommit func() *transactionGate) {
	t.Helper()
	var db *faultyDatabase
	st = openMemoryStore(t, func(inner sqlite.SqliteDatabase) sqlite.SqliteDatabase {
		db = &faultyDatabase{SqliteDatabase: inner, failing: map[string]bool{}}
		return db
	}, outbox)
	return st, db.fail, func() *transactionGate {
		gate := newTransactionGate()
		db.mu.Lock()
		db.gate = gate
		db.mu.Unlock()
		return gate
	}
}

// testOwner is the owner of a test's table handles: its failures collected (Pi's `tableOwner()`).
type testOwner struct {
	failures []*data.ChordError
	scope    *struct{}
}

func (owner *testOwner) Label() string             { return "app.store" }
func (owner *testOwner) Scope() any                { return owner.scope }
func (owner *testOwner) AssertLive() error         { return nil }
func (owner *testOwner) Fail(err *data.ChordError) { owner.failures = append(owner.failures, err) }
func tableOwner() *testOwner                       { return &testOwner{scope: new(struct{})} }
func openFence() store.CommitFence {
	return store.CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { return nil }}
}

// tableIn creates definition (and its indexes) in st as `app.store:<name>` and returns a handle to it.
func tableIn(t testing.TB, st *store.ChordStore, definition *store.TableDefinition, owner ...*testOwner) *store.TableHandle {
	t.Helper()
	handleOwner := tableOwner()
	if len(owner) > 0 {
		handleOwner = owner[0]
	}
	table := "app.store:" + definition.Name
	err := store.PrepareTables([]store.DeclaredTable{{Definition: definition, Table: table}}, st, openFence(), "app.store", background)
	must(t, err)
	return store.NewTableHandle(definition, table, st, handleOwner)
}

// rejection is the error err is, as ChordError JSON when it is one; the test fails when err is nil.
func rejection(t testing.TB, err error) any {
	t.Helper()
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if chordErr, ok := data.IsChordError(err); ok {
		return chordErr.ToJSON()
	}
	return err
}

// transported waits until messages and closes in flight on memory links (one task per hop) have arrived.
func transported(links ...*link.MemoryLink) {
	for hop := 0; hop < 6; hop++ {
		for _, end := range links {
			end.Flushed()
		}
	}
}

// --- remote tests --------------------------------------------------------------------------------------------------

// Server is the host kind of the test servers, where every test type but PlacedNotes lives.
var Server = mustKind(contracts.NewHostKind("server"))

// Session is the kind of the relay tests' workers: one per session.
var Session = mustKind(contracts.NewHostKind("session", contracts.Prop("id", contracts.Str())))

func mustKind(kind *contracts.HostKind, err error) *contracts.HostKind {
	if err != nil {
		panic(err)
	}
	return kind
}

// serverAddress is `Server()`.
func serverAddress() contracts.Address {
	return Server.Address(nil)
}

// SERVER_ADDRESS is `Server()` as a hold names it on the wire: the address of every test type served on the server.
var serverWireAddress = link.WireAddress{Kind: "server", Params: jsonObject()}

// RAW_PEER is the incarnation a raw peer greets with.
const rawPeerIncarnation = "00000000-0000-7000-8000-000000000000"

// currentCodec is the codec of the protocol test running now (forEachCodec).
var currentCodec link.Codec = link.BinaryCodec

// testCodec is the codec of the protocol test running now; links in tests use it at both ends.
func testCodec() link.Codec { return currentCodec }

// forEachCodec runs suite once per codec, as `binary codec` and `text codec`.
func forEachCodec(t *testing.T, suite func(t *testing.T)) {
	t.Helper()
	for _, codec := range []link.Codec{link.BinaryCodec, link.TextCodec} {
		t.Run(codec.Name()+" codec", func(t *testing.T) {
			previous := currentCodec
			currentCodec = codec
			defer func() { currentCodec = previous }()
			suite(t)
		})
	}
}

// linkedOptions is the options of `linked`.
type linkedOptions struct {
	Child        data.Principal
	Context      context.Context
	Telemetry    *telemetry.InMemoryTelemetryContext
	ReadyTimeout *time.Duration
	Loop         sectionLoop
}

// linkedHosts is what `linked` returns: Pi's `{ server, client, errors, links }`.
type linkedHosts struct {
	Server, Client Host
	ServerErrors   *hostErrors
	ClientErrors   *hostErrors
	Links          [2]*link.MemoryLink
}

// linked is a server (principal `server`, loaded with one facet whose setup is setup) and a client connected to it, the
// client's link stamped child; background errors collected per host.
func linked(t testing.TB, setup func(host FacetHost) error, options linkedOptions) *linkedHosts {
	t.Helper()
	ctx := options.Context
	if ctx == nil {
		ctx = background
	}
	result := &linkedHosts{ServerErrors: &hostErrors{}, ClientErrors: &hostErrors{}}
	result.Server = newHost(t, HostOptions{
		Address:      new(Server.Address(nil)),
		Principal:    serverPrincipal(),
		Telemetry:    telemetryOf(options.Telemetry),
		ReadyTimeout: options.ReadyTimeout,
		OnError:      result.ServerErrors.push,
		loop:         options.Loop,
	})
	must(t, result.Server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return setup(host)
	})}}, ctx))
	result.Client = newHost(t, HostOptions{
		Telemetry: telemetryOf(options.Telemetry), ReadyTimeout: options.ReadyTimeout, OnError: result.ClientErrors.push, loop: options.Loop,
	})
	must(t, result.Client.Load(nil, ctx))
	a, b := link.MemoryLinkPair()
	result.Links = [2]*link.MemoryLink{a, b}
	child := options.Child
	if child == nil {
		child = ownerPrincipal()
	}
	must(t, result.Server.Accept(a, LinkOptions{Principal: child, Codec: testCodec()}, ctx))
	must(t, result.Client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, ctx))
	return result
}

// rawPeerOptions is the options of a RawPeer: Pi's `{ hello, calls, ack, codec }`; nil is Pi's undefined.
type rawPeerOptions struct {
	// NoHello is `hello: false`.
	NoHello bool
	Calls   int
	// NoAck is `ack: false`.
	NoAck bool
	Codec link.Codec
}

// RawPeer is a peer speaking the wire by hand, in the current test codec, at the other end of a link: it sends `hello`
// (unless NoHello, announcing Calls, default 256), records what arrives, acks every `bound` and `state` with its origin
// (unless NoAck: then ack() sends one), and sends what a test forges.
type RawPeer struct {
	t     testing.TB
	link  link.Link
	Codec link.Codec
	mu    sync.Mutex
	cond  *sync.Cond
	// received are the messages received and not taken, in order.
	received []link.Message
	closed   bool
}

// newRawPeer is Pi's `new RawPeer(link, options)`.
func newRawPeer(t testing.TB, end link.Link, options rawPeerOptions) *RawPeer {
	t.Helper()
	peer := &RawPeer{t: t, link: end, Codec: options.Codec}
	peer.cond = sync.NewCond(&peer.mu)
	if peer.Codec == nil {
		peer.Codec = testCodec()
	}
	end.OnMessage(func(bytes []byte) {
		decoded, err := peer.Codec.Decode(bytes)
		if err != nil {
			t.Errorf("RawPeer: decode: %v", err)
			return
		}
		message, err := link.CheckMessage(decoded, background)
		if err != nil {
			t.Errorf("RawPeer: check: %v", err)
			return
		}
		peer.mu.Lock()
		peer.received = append(peer.received, message)
		peer.mu.Unlock()
		if kind := message.Kind(); !options.NoAck && (kind == "bound" || kind == "state") {
			peer.ack(link.OriginOf(message))
		}
		peer.cond.Broadcast()
	})
	end.OnClose(func() {
		peer.mu.Lock()
		peer.closed = true
		peer.mu.Unlock()
		peer.cond.Broadcast()
	})
	if !options.NoHello {
		calls := options.Calls
		if calls == 0 {
			calls = 256
		}
		peer.send(&link.HelloMessage{Protocol: data.ProtocolVersion, Incarnation: rawPeerIncarnation, Calls: calls})
	}
	return peer
}

func (p *RawPeer) send(message link.Message) {
	p.t.Helper()
	encoded, err := p.Codec.Encode(message)
	if err != nil {
		p.t.Fatalf("RawPeer: encode %s: %v", message.Kind(), err)
	}
	p.link.Send(encoded)
}

// Send sends a message.
func (p *RawPeer) Send(message link.Message) { p.t.Helper(); p.send(message) }

// Ack acknowledges one `bound` or `state` message of origin (absent: the peer's own).
func (p *RawPeer) Ack(origin link.Optional[int]) { p.t.Helper(); p.ack(origin) }

func (p *RawPeer) ack(origin link.Optional[int]) { p.send(&link.AckMessage{Origin: origin}) }

// Close closes the link.
func (p *RawPeer) Close() { p.link.Close() }

// SendBytes sends a message as raw bytes.
func (p *RawPeer) SendBytes(message []byte) { p.link.Send(message) }

// Closed is `peer.closed`.
func (p *RawPeer) Closed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// Next is the first received message of kind not taken yet, waiting for it; the test fails if the link closes first.
func (p *RawPeer) Next(kind string) link.Message {
	p.t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		for index, message := range p.received {
			if message.Kind() == kind {
				p.received = append(p.received[:index:index], p.received[index+1:]...)
				return message
			}
		}
		if p.closed {
			p.t.Fatalf("link closed before %s", kind)
		}
		p.cond.Wait()
	}
}

// Kinds are the kinds received and not taken, in order.
func (p *RawPeer) Kinds() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	kinds := make([]string, len(p.received))
	for i, message := range p.received {
		kinds[i] = message.Kind()
	}
	return kinds
}

// Received is a copy of the messages received and not taken.
func (p *RawPeer) Received() []link.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]link.Message(nil), p.received...)
}

// partValue is a part's value as its owner reads it: opened (inflated when deflated) without limits, and parsed.
func partValue(t testing.TB, part link.Part) any {
	t.Helper()
	unlimited := link.Limits{Part: math.Inf(1), Decoded: math.Inf(1)}
	opened, err := link.OpenPart(part, unlimited, nextnode.NodeCompression, background)
	must(t, err)
	value, err := link.ParsePart(opened)
	must(t, err)
	return value
}

// wireIndexes is Pi's `wireNames(Type, variant)`: the wire's indexes of a remote contract's variant (2b).
type wireIndexes struct {
	Variant int
	State   map[string]int
	Methods map[string]int
	names   contracts.VariantNames
}

// wireNames is the variant's indexes, and its state members' and methods' by name.
func wireNames(t testing.TB, typ *contracts.Contract, variant string) wireIndexes {
	t.Helper()
	variants, err := contracts.CurrentVariants(typ)
	must(t, err)
	order, err := contracts.VariantOrder(typ)
	must(t, err)
	names := contracts.VariantNamesOf(variants.Value(variant))
	indexes := func(list []string) map[string]int {
		out := make(map[string]int, len(list))
		for index, name := range list {
			out[name] = index
		}
		return out
	}
	return wireIndexes{Variant: slices.Index(order, variant), State: indexes(names.State), Methods: indexes(names.Methods), names: names}
}

// Named is a message's members by index as an object by name, their parts parsed (`ops` or `value` per member).
func (w wireIndexes) Named(t testing.TB, members []link.Part, indexes []int) map[string]any {
	t.Helper()
	out := map[string]any{}
	for i, member := range indexes {
		out[w.names.State[member]] = partValue(t, members[i])
	}
	return out
}

// NamedBound is Named of a `bound` entry's members.
func (w wireIndexes) NamedBound(t testing.TB, members []link.BoundMember) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, member := range members {
		out[w.names.State[member.Member]] = partValue(t, member.Value)
	}
	return out
}

// NamedState is Named of a `state` entry's members.
func (w wireIndexes) NamedState(t testing.TB, members []link.StateMember) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, member := range members {
		out[w.names.State[member.Member]] = partValue(t, member.Ops)
	}
	return out
}

// rawLinkedOptions is the options of `rawLinked`.
type rawLinkedOptions struct {
	Child        data.Principal
	NoHello      bool
	ReadyTimeout *time.Duration
	Limits       link.PartLimits
	Compression  link.Compression
	// Parent is `role: "parent"`: the raw peer is the host's parent (`connect`), not its child (`accept`, the default).
	Parent bool
	Loop   sectionLoop
}

// rawLinkedHost is what `rawLinked` returns: Pi's `{ server, peer, errors }`.
type rawLinkedHost struct {
	Server Host
	Peer   *RawPeer
	Errors *hostErrors
}

// rawLinked is a server host as in `linked` and a raw peer on a link it accepted as child, with limits.
func rawLinked(t testing.TB, setup func(host FacetHost) error, options rawLinkedOptions) *rawLinkedHost {
	t.Helper()
	errs := &hostErrors{}
	server := newHost(t, HostOptions{
		Address:      new(Server.Address(nil)),
		Principal:    serverPrincipal(),
		ReadyTimeout: options.ReadyTimeout,
		Compression:  options.Compression,
		OnError:      errs.push,
		loop:         options.Loop,
	})
	must(t, server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return setup(host)
	})}}, background))
	a, b := link.MemoryLinkPair()
	child := options.Child
	if child == nil {
		child = ownerPrincipal()
	}
	linkOptions := LinkOptions{Principal: child, Codec: testCodec(), Limits: options.Limits}
	if options.Parent {
		must(t, server.Connect(a, linkOptions, background))
	} else {
		must(t, server.Accept(a, linkOptions, background))
	}
	return &rawLinkedHost{Server: server, Peer: newRawPeer(t, b, rawPeerOptions{NoHello: options.NoHello}), Errors: errs}
}

// notesVariants are the variants of the notes types: an editor variant for owners, a reader one for guests.
func notesVariants() []contracts.VariantEntry {
	text := contracts.Obj(contracts.Prop("text", contracts.Str()))
	state := func() []contracts.Property {
		return []contracts.Property{contracts.Prop("notes", contracts.Arr(text)), contracts.Prop("count", contracts.Int())}
	}
	return []contracts.VariantEntry{
		contracts.Var("editor", contracts.VariantDeclaration{
			State: state(),
			Methods: []contracts.MethodEntry{
				contracts.Meth("add", contracts.NewMethod(text, contracts.Int())),
				contracts.Meth("fail", contracts.NewMethod(contracts.Obj(contracts.Prop("how", contracts.Str())))),
				contracts.Meth("wait", contracts.NewMethod()),
				contracts.Meth("echo", contracts.NewMethod(
					contracts.Obj(contracts.Prop("data", contracts.Bytes())), contracts.Bytes())),
			},
		}),
		contracts.Var("reader", contracts.VariantDeclaration{
			State:   state(),
			Methods: []contracts.MethodEntry{contracts.Meth("wait", contracts.NewMethod())},
		}),
	}
}

// Notes is the remote type of the link tests: notes per session, an editor variant for owners, a reader one for guests.
var Notes = mustContract(contracts.Remote("app.notes", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Variants:    notesVariants(),
}))

// PlacedNotes is Notes placed on the session's worker: the relay tests' type.
var PlacedNotes = mustContract(contracts.Remote("app.placed-notes", contracts.RemoteDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
		session, _ := params.Value("session").(string)
		return Session.Address(jsonObject("id", session)), nil
	},
	Variants: notesVariants(),
}))

var notesDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("notes", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("list", contracts.Arr(contracts.Obj(contracts.Prop("text", contracts.Str()))))),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("list", []any{}) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// NoteStore is the shared store behind Notes (section 6): the notes of a session, once, whoever views them.
var NoteStore = mustContract(contracts.Local("app.note-store", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("session", contracts.Str())},
	Members: []contracts.LocalEntry{
		contracts.Mem("list", contracts.Arr(contracts.Obj(contracts.Prop("text", contracts.Str())))),
		contracts.Mem("add", contracts.NewMethod(contracts.Obj(contracts.Prop("text", contracts.Str())), contracts.Int())),
	},
}))

// notesProbe is what `serveNotes` records: the handlers' contexts of `wait` calls, and every factory run of Notes.
type notesProbe struct {
	mu        sync.Mutex
	waits     []context.Context
	factories []string
	// changed is closed (and replaced) whenever a `wait` call appends its context.
	changed chan struct{}
}

// AwaitWaits blocks until n `wait` calls started (their handlers run off the loop) and returns their contexts; it fails
// the test if the test ends first.
func (p *notesProbe) AwaitWaits(t testing.TB, n int) []context.Context {
	t.Helper()
	for {
		p.mu.Lock()
		if len(p.waits) >= n {
			waits := append([]context.Context(nil), p.waits...)
			p.mu.Unlock()
			return waits
		}
		if p.changed == nil {
			p.changed = make(chan struct{})
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-changed:
		case <-t.Context().Done():
			t.Fatalf("AwaitWaits: %d wait calls wanted, the test ended first", n)
		}
	}
}

// Waits is a copy of the contexts of the `wait` calls so far.
func (p *notesProbe) Waits() []context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]context.Context(nil), p.waits...)
}

// Factories is a copy of the factory runs so far, as `<session> <principal kind>`.
func (p *notesProbe) Factories() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.factories...)
}

// serveNotes serves NoteStore and Notes over it: `notes` and `count` are derives over the store's list; guests get the
// reader variant. `wait` returns when its context is aborted; `fail` returns a ChordError (`chord`), a PluginError
// (`plugin`) or a plain error. Pass PlacedNotes as typ for the relay tests (the two types differ in name and host
// address only).
func serveNotes(host FacetHost, typ *contracts.Contract) (*notesProbe, error) {
	if typ == nil {
		typ = Notes
	}
	probe := &notesProbe{}
	err := host.Serve(NoteStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		doc := stateOf(self, notesDoc)
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"list": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			},
			Methods: map[string]contracts.MethodFunction{
				"add": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					text := args.(*delta.JsonObject).Value("text")
					if err := doc.Change(func(draft *delta.Object) { appendNote(draft, text) }, ctx); err != nil {
						return nil, err
					}
					return float64(len(doc.Value().Value("list").([]any))), nil
				},
			},
		}}, nil
	}, ServeOptions{})
	if err != nil {
		return nil, err
	}
	err = host.Serve(typ, func(params *delta.JsonObject, principal data.Principal, self Self) (contracts.Implementation, error) {
		session, _ := params.Value("session").(string)
		probe.mu.Lock()
		probe.factories = append(probe.factories, session+" "+principalKind(principal))
		probe.mu.Unlock()
		store := self.Get(NoteStore, jsonObject("session", session))
		notes := self.Derive([]any{store.Member("list")}, func(values []any) (delta.JsonValue, error) { return values[0], nil })
		count := self.Derive([]any{store.Member("list")}, func(values []any) (delta.JsonValue, error) {
			return float64(len(values[0].([]any))), nil
		})
		wait := func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			probe.mu.Lock()
			probe.waits = append(probe.waits, ctx)
			if probe.changed != nil {
				close(probe.changed)
				probe.changed = nil
			}
			probe.mu.Unlock()
			<-ctx.Done()
			return nil, nil
		}
		members := map[string]chord.Watchable[delta.JsonValue]{"notes": notes, "count": count}
		if principalKind(principal) == "guest" {
			return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{
				State: members, Methods: map[string]contracts.MethodFunction{"wait": wait},
			}}, nil
		}
		return contracts.Implementation{Variant: "editor", Members: contracts.VariantImplementation{
			State: members,
			Methods: map[string]contracts.MethodFunction{
				"wait": wait,
				"add": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return store.Call("add", args, ctx)
				},
				"fail": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					switch args.(*delta.JsonObject).Value("how") {
					case "chord":
						return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Message: "not yours"}, nil)
					case "plugin":
						return nil, data.NewPluginError("quota", "too many", jsonObject("limit", float64(1)))
					}
					return nil, errors.New("plain")
				},
				"echo": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					return args.(*delta.JsonObject).Value("data"), nil
				},
			},
		}}, nil
	}, ServeOptions{})
	return probe, err
}

// appendNote is `draft.list.push({ text })`.
func appendNote(draft *delta.Object, text any) {
	_, _ = draft.Array("list").Push(jsonObject("text", text))
}

// singletonDoc is a singleton document handle (`self.State(def)` of a singleton definition) with Pi's call shapes:
// `doc.change((draft) => ...)`, `doc.value`, `doc.retire()`. Its `s` is what a derive takes as a source (and what
// Expose turns into a state member).
type singletonDoc struct{ s *documents.Singleton }

// stateOf is `self.state(def)` for a singleton definition.
func stateOf(self Self, definition *documents.StateDefinition) *singletonDoc {
	return &singletonDoc{s: self.State(definition).(*documents.Singleton)}
}

// Change is `await doc.change((draft) => ..., ctx)`.
func (d *singletonDoc) Change(fn func(draft *delta.Object), ctx context.Context) error {
	_, err := d.s.Change(func(draft *delta.Object) (any, error) { fn(draft); return nil, nil }, ctx)
	return err
}

// Retire is `await doc.retire(ctx)`.
func (d *singletonDoc) Retire(ctx context.Context) error { return d.s.Retire(ctx) }

// Value is `doc.value`.
func (d *singletonDoc) Value() *delta.JsonObject {
	value, err := d.s.Value()
	if err != nil {
		panic(err)
	}
	return value
}

// Member is the document as a state member (Expose).
func (d *singletonDoc) Member() chord.Watchable[delta.JsonValue] { return Expose(d.s) }

// addNote adds a note to the session's store from the server itself; the reference it takes keeps the store.
func addNote(t testing.TB, server Host, session, text string) {
	t.Helper()
	store, err := server.Get(NoteStore, jsonObject("session", session), background, GetOptions{})
	must(t, err)
	_, err = store.Call("add", jsonObject("text", text), background)
	must(t, err)
}

// rawProviderHost is what `rawProvider` returns: Pi's `{ client, peer, errors }`.
type rawProviderHost struct {
	Client Host
	Peer   *RawPeer
	Errors *hostErrors
}

// rawProvider is a client connected to a raw peer playing the provider (the link's limits limits), after the handshake.
func rawProvider(t testing.TB, options rawLinkedOptions) *rawProviderHost {
	t.Helper()
	errs := &hostErrors{}
	client := newHost(t, HostOptions{ReadyTimeout: options.ReadyTimeout, Compression: options.Compression, OnError: errs.push, loop: options.Loop})
	must(t, client.Load(nil, background))
	return &rawProviderHost{Client: client, Peer: rawParent(t, client, options.Limits), Errors: errs}
}

// rawParent connects host to a new raw peer playing its parent (a first link, or the next after a drop), the link's
// limits limits, after the handshake.
func rawParent(t testing.TB, host Host, limits link.PartLimits) *RawPeer {
	t.Helper()
	a, b := link.MemoryLinkPair()
	must(t, host.Connect(a, LinkOptions{Principal: serverPrincipal(), Codec: testCodec(), Limits: limits}, background))
	peer := newRawPeer(t, b, rawPeerOptions{})
	peer.Next("hello")
	return peer
}

// statesCollector collects the states `statesOf` saw.
type statesCollector struct {
	mu     sync.Mutex
	states []string
}

// All is a copy of the states so far.
func (c *statesCollector) All() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.states...)
}

// statesOf starts a `ref.state` watch and collects the states it delivers, by kind (failed ones with their error's
// message).
func statesOf(t testing.TB, watch chord.NativeWatchHandle[*RefState]) *statesCollector {
	t.Helper()
	collected := &statesCollector{}
	watch.Start(func(_ context.Context, call chord.NativeWatchCall[*RefState]) error {
		if call.Kind != chord.WatchCallValue {
			return nil
		}
		text := string(call.Value.Kind)
		if call.Value.Kind == RefStateFailed {
			text = "failed: " + call.Value.Cause.Error.Message
		}
		collected.mu.Lock()
		collected.states = append(collected.states, text)
		collected.mu.Unlock()
		return nil
	})
	return collected
}

// editorWire is Pi's `EDITOR`: the wire's indexes of Notes's editor variant.
func editorWire(t testing.TB) wireIndexes { return wireNames(t, Notes, "editor") }

// bindNext answers the next hold with `bound`: the editor variant with `notes` and `count`; it returns the hold's id.
func bindNext(t testing.TB, peer *RawPeer, notes ...*delta.JsonObject) int {
	t.Helper()
	hold := peer.Next("hold").(*link.HoldMessage).Hold
	bindHold(t, peer, hold, notes...)
	return hold
}

// bindHold sends `bound` for hold: the editor variant with `notes` and `count`.
func bindHold(t testing.TB, peer *RawPeer, hold int, notes ...*delta.JsonObject) {
	t.Helper()
	editor := editorWire(t)
	list := make([]any, len(notes))
	for i, note := range notes {
		list[i] = note
	}
	notesPart, err := link.JsonPart(list)
	must(t, err)
	countPart, err := link.JsonPart(float64(len(notes)))
	must(t, err)
	peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
		Hold:    hold,
		Variant: editor.Variant,
		Members: []link.BoundMember{{Member: editor.State["notes"], Value: notesPart}, {Member: editor.State["count"], Value: countPart}},
		Failed:  []link.FailedMember{},
	}}})
}

// resolved is Pi's `Resolved`: what a route's `resolve` returns in the relay tests.
type resolved struct {
	Link      link.Link
	Principal data.Principal
	Trusted   bool
	Codec     link.Codec
	Limits    link.PartLimits
}

// link is the ResolvedLink of r: Limits nil when none are set.
func (r resolved) link() ResolvedLink {
	resolvedLink := ResolvedLink{Link: r.Link, Principal: r.Principal, Trusted: r.Trusted, Codec: r.Codec}
	if r.Limits.Part != nil || r.Limits.Decoded != nil {
		limits := r.Limits
		resolvedLink.Limits = &limits
	}
	return resolvedLink
}

// relayErrors are the errors per host of `relayed`.
type relayErrors struct{ Tab, Server, Worker *hostErrors }

// relayed is what `relayed` builds: the hosts, the links the server holds, the resolves it ran and the errors per host.
type relayed struct {
	Tab, Server Host
	// Workers is the worker of each resolved session address, the latest one per key.
	Workers sync.Map
	mu      sync.Mutex
	// routeLinks are the server's end of each resolved route link, in order.
	routeLinks []*link.MemoryLink
	// TabLink is the server's end of the tab's link.
	TabLink *link.MemoryLink
	// resolves are the address keys `resolve` ran for, in order.
	resolves []string
	Errors   relayErrors
	// peers are the other ends of the links the relay made itself (the tab's, the workers' own).
	peers []*link.MemoryLink
}

// Transported is Pi's `transported()` for every link the relay owns: messages and closes in flight on the tab link and
// the route links (later ones included) have arrived.
func (r *relayed) Transported() {
	r.mu.Lock()
	ends := append([]*link.MemoryLink{r.TabLink}, r.routeLinks...)
	ends = append(ends, r.peers...)
	r.mu.Unlock()
	transported(ends...)
}

// RouteLinks is a copy of the route links so far.
func (r *relayed) RouteLinks() []*link.MemoryLink {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*link.MemoryLink(nil), r.routeLinks...)
}

// Resolves is a copy of the address keys `resolve` ran for.
func (r *relayed) Resolves() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.resolves...)
}

// Worker is the worker host of an address key (the latest).
func (r *relayed) Worker(key string) Host {
	worker, _ := r.Workers.Load(key)
	host, _ := worker.(Host)
	return host
}

// relayedOptions is the options of `relayed`.
type relayedOptions struct {
	// Tab is who the tab acts as (owner by default).
	Tab data.Principal
	// Authorize admits everyone by default.
	Authorize func(principal data.Principal, address contracts.Address) bool
	// AuthorizeErr is used instead of Authorize when set: an authorize that throws.
	AuthorizeErr func(principal data.Principal, address contracts.Address) (bool, error)
	Trusted      bool
	// TabCodec and RouteCodec are `codecs.tab` and `codecs.route`.
	TabCodec, RouteCodec link.Codec
	Server               func(host FacetHost) error
	Worker               func(host FacetHost) error
	// Resolve wraps the default one (`worker()` makes the worker).
	Resolve func(address contracts.Address, worker func() resolved, ctx context.Context) (resolved, error)
	Context context.Context
	// RevokedBy is the server's and the workers' `revokedBy`; ServerRevokedBy is the server's when it differs.
	RevokedBy       RevokedBy[delta.JsonValue]
	ServerRevokedBy RevokedBy[delta.JsonValue]
	// TabCompression, ServerCompression and WorkerCompression are `compression.{tab,server,worker}`.
	TabCompression, ServerCompression, WorkerCompression link.Compression
	// Limits are the limits of the tab's link at the server.
	Limits link.PartLimits
	// Loop drives all three kinds of host (Pi's fake timers); nil is the real loop.
	Loop sectionLoop
}

// relay builds Pi's `relayed(options)`: three hosts, a tab connected to a server, and a server routing `Session` to an
// in-process worker per address (made by each `resolve`, connected before it loads, W6) that serves PlacedNotes and
// Notes's store.
func relay(t testing.TB, options relayedOptions) *relayed {
	t.Helper()
	ctx := options.Context
	if ctx == nil {
		ctx = background
	}
	result := &relayed{Errors: relayErrors{Tab: &hostErrors{}, Server: &hostErrors{}, Worker: &hostErrors{}}}
	serverRevoked := options.ServerRevokedBy
	if serverRevoked == nil {
		serverRevoked = options.RevokedBy
	}
	server := newHost(t, HostOptions{
		Address:     new(Server.Address(nil)),
		Principal:   serverPrincipal(),
		RevokedBy:   serverRevoked,
		Compression: options.ServerCompression,
		OnError:     result.Errors.Server.push,
		loop:        options.Loop,
	})
	result.Server = server
	codec := options.RouteCodec
	if codec == nil {
		codec = testCodec()
	}
	worker := func(address contracts.Address) resolved {
		result.mu.Lock()
		result.resolves = append(result.resolves, address.Key)
		result.mu.Unlock()
		a, b := link.MemoryLinkPair()
		session, _ := address.Params.Value("id").(string)
		principal := workerPrincipal(session)
		host := newHost(t, HostOptions{
			Address:     &address,
			Principal:   principal,
			RevokedBy:   options.RevokedBy,
			Compression: options.WorkerCompression,
			OnError:     result.Errors.Worker.push,
			loop:        options.Loop,
		})
		must(t, host.Connect(b, LinkOptions{Principal: serverPrincipal(), Trusted: options.Trusted, Codec: codec}, ctx))
		facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
			if _, err := serveNotes(h, PlacedNotes); err != nil {
				return err
			}
			if options.Worker != nil {
				return options.Worker(h)
			}
			return nil
		})
		// `void host.load(...)`: an unawaited promise.
		go func() { _ = host.Load([]HostFacet{{Name: "@test/worker", Facet: facet}}, ctx) }()
		result.Workers.Store(address.Key, host)
		result.mu.Lock()
		result.routeLinks = append(result.routeLinks, a)
		result.peers = append(result.peers, b)
		result.mu.Unlock()
		return resolved{Link: a, Principal: principal, Trusted: options.Trusted, Codec: codec}
	}
	authorize := options.AuthorizeErr
	if authorize == nil {
		plain := options.Authorize
		if plain == nil {
			plain = func(data.Principal, contracts.Address) bool { return true }
		}
		authorize = func(principal data.Principal, address contracts.Address) (bool, error) {
			return plain(principal, address), nil
		}
	}
	resolve := options.Resolve
	if resolve == nil {
		resolve = func(_ contracts.Address, made func() resolved, _ context.Context) (resolved, error) {
			return made(), nil
		}
	}
	must(t, server.Route(Session, RouteOptions{
		Authorize: authorize,
		Resolve: func(address contracts.Address, routing context.Context) (ResolvedLink, error) {
			r, err := resolve(address, func() resolved { return worker(address) }, routing)
			return r.link(), err
		},
	}, ctx))
	must(t, server.Load([]HostFacet{{Name: "@test/server", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if options.Server != nil {
			return options.Server(h)
		}
		return nil
	})}}, ctx))
	tab := newHost(t, HostOptions{Compression: options.TabCompression, OnError: result.Errors.Tab.push, loop: options.Loop})
	must(t, tab.Load(nil, ctx))
	result.Tab = tab
	a, b := link.MemoryLinkPair()
	result.TabLink = a
	result.peers = append(result.peers, b)
	tabCodec := options.TabCodec
	if tabCodec == nil {
		tabCodec = testCodec()
	}
	principal := options.Tab
	if principal == nil {
		principal = ownerPrincipal()
	}
	must(t, server.Accept(a, LinkOptions{Principal: principal, Codec: tabCodec, Limits: options.Limits}, ctx))
	must(t, tab.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: tabCodec}, ctx))
	return result
}

// rawWorkerOptions is the options of `rawWorkers`.
type rawWorkerOptions struct {
	Calls   int
	NoAck   bool
	Trusted bool
	Limits  link.PartLimits
}

// rawWorkers is a `resolve` for `relay` whose workers are raw peers playing the provider, collected in peers in order
// (read it with its Peers method); their links trusted when Trusted.
type rawWorkers struct {
	t       testing.TB
	options rawWorkerOptions
	mu      sync.Mutex
	peers   []*RawPeer
	// made is closed (and replaced) whenever Resolve makes a peer.
	made chan struct{}
}

// At blocks until the index-th raw worker exists (route resolves run on an owned task after the hold arrived) and
// returns it; it fails the test if the test ends first.
func (w *rawWorkers) At(t testing.TB, index int) *RawPeer {
	t.Helper()
	for {
		w.mu.Lock()
		if index < len(w.peers) {
			peer := w.peers[index]
			w.mu.Unlock()
			return peer
		}
		if w.made == nil {
			w.made = make(chan struct{})
		}
		made := w.made
		w.mu.Unlock()
		select {
		case <-made:
		case <-t.Context().Done():
			t.Fatalf("rawWorkers.At: worker %d never resolved", index)
		}
	}
}

// newRawWorkers is Pi's `rawWorkers(peers, options)`.
func newRawWorkers(t testing.TB, options rawWorkerOptions) *rawWorkers {
	return &rawWorkers{t: t, options: options}
}

// Peers is a copy of the raw peers made so far.
func (w *rawWorkers) Peers() []*RawPeer {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]*RawPeer(nil), w.peers...)
}

// Resolve is the `resolve` of relayedOptions: it makes a raw peer for the address.
func (w *rawWorkers) Resolve(address contracts.Address, _ func() resolved, _ context.Context) (resolved, error) {
	a, b := link.MemoryLinkPair()
	peer := newRawPeer(w.t, b, rawPeerOptions{Calls: w.options.Calls, NoAck: w.options.NoAck})
	w.mu.Lock()
	w.peers = append(w.peers, peer)
	if w.made != nil {
		close(w.made)
		w.made = nil
	}
	w.mu.Unlock()
	session, _ := address.Params.Value("id").(string)
	return resolved{
		Link: a, Principal: workerPrincipal(session), Codec: testCodec(), Trusted: w.options.Trusted, Limits: w.options.Limits,
	}, nil
}

// incarnationOf is a host's incarnation, which its handshakes send.
func incarnationOf(host Host) string { return host.(*chordHost).incarnation }

// countingCompression is Pi's `countInflated()`: a compression whose Inflate output bytes are counted. Go forced
// difference: Pi stubs the global `DecompressionStream` that a host without compression uses for async inflates; Go's
// link.InflateAsync has no hook, so the count covers the synchronous Compression a host has, and the async path is
// observed through `chord.link.inflates` and the part's outcome.
type countingCompression struct {
	nextnode.Compression
	mu    sync.Mutex
	bytes int
}

// newCountingCompression wraps the node compression.
func newCountingCompression() *countingCompression {
	return &countingCompression{Compression: nextnode.NodeCompression}
}

// Inflate counts the bytes it puts out.
func (c *countingCompression) Inflate(input []byte, limit float64) nextnode.Inflated {
	out := c.Compression.Inflate(input, limit)
	c.mu.Lock()
	c.bytes += len(out.Bytes)
	c.mu.Unlock()
	return out
}

// Bytes is the count so far.
func (c *countingCompression) Bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// archiveTableSet is Pi's `ArchiveTables`: the tables of `archiveStore`: edits (v1 with an index; v2 adds `done`) and
// notes, whose v2 migration crashed.
type archiveTableSet struct {
	EditsV1, EditsV2, NotesV1, NotesV2 *store.TableDefinition
}

var archiveTables = func() archiveTableSet {
	define := func(name string, options store.TableOptions) *store.TableDefinition {
		definition, err := store.DefineTable(name, options)
		if err != nil {
			panic(err)
		}
		return definition
	}
	evolve := func(previous *store.TableDefinition, edit func(t *store.TableEditor) *store.TableEditor) *store.TableDefinition {
		definition, err := store.EvolveTable(previous, edit)
		if err != nil {
			panic(err)
		}
		return definition
	}
	editsV1 := define("edits", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{
			store.Col("id", store.Str()),
			store.Col("path", store.Str()),
			store.Col("at", store.Int()),
			store.Col("note", store.Opt(store.Str())),
			store.Col("meta", store.Opt(store.Json(contracts.Obj(contracts.Prop("a", contracts.Int())), store.JsonColumnOptions{Version: 1}))),
		},
		Key:     []string{"id"},
		Indexes: store.IndexRecords{{Name: "by_path", Columns: []string{"path", "at"}}},
	})
	editsV2 := evolve(editsV1, func(t *store.TableEditor) *store.TableEditor {
		return t.Add("done", store.Bool(contracts.Default(false)))
	})
	notesV1 := define("notes", store.TableOptions{
		Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Int()), store.Col("text", store.Str())}, Key: []string{"id"},
	})
	notesV2 := evolve(notesV1, func(t *store.TableEditor) *store.TableEditor {
		return t.Rows(func(row store.Row) (any, error) {
			if row["id"] == int64(120) {
				return nil, errors.New("crash")
			}
			return store.Row{"text": row["text"].(string) + "!"}, nil
		})
	})
	return archiveTableSet{editsV1, editsV2, notesV1, notesV2}
}()

// archiveStore is a store holding a bit of everything an archive carries: `app.store:edits` at version 2 with 150 rows
// (two export pages; some written at version 1, `NULL` and `json(...)` cells, an index), `app.store:notes` left
// `migrating` at version 2 after one committed chunk, and documents: a singleton, keyed ones with deltas after their
// base, and a retired one.
func archiveStore(t *testing.T, outbox ...bool) *store.ChordStore {
	t.Helper()
	tables := archiveTables
	st := memoryStore(t, outbox...)
	edits := archiveTableAt(t, st, "app.store:edits", tables.EditsV1)
	notes := archiveTableAt(t, st, "app.store:notes", tables.NotesV1)
	var staged []store.StagedWrite
	editChanges := store.NewTableChanges(edits, &staged)
	for i := range 150 {
		row := store.Row{"id": fmt.Sprintf("e%03d", i), "path": fmt.Sprintf("p/%d", i%4), "at": int64(i)}
		if i%2 == 0 {
			row["note"] = fmt.Sprintf("n%d", i)
		}
		if i%3 == 0 {
			row["meta"] = delta.JsonObjectOf("a", float64(i))
		}
		must(t, editChanges.Put(row))
	}
	noteChanges := store.NewTableChanges(notes, &staged)
	for i := range 150 {
		must(t, noteChanges.Put(store.Row{"id": int64(i), "text": fmt.Sprintf("t%d", i)}))
	}
	if _, err := st.Commit(staged, openFence(), background); err != nil {
		t.Fatalf("archiveStore: commit: %v", err)
	}
	prepare := func(definition *store.TableDefinition, table string) error {
		return store.PrepareTables([]store.DeclaredTable{{Definition: definition, Table: table}}, st, openFence(), "app", background)
	}
	must(t, prepare(tables.EditsV2, "app.store:edits"))
	// The notes migration crashes after its first committed chunk: the table stays `migrating`.
	_ = prepare(tables.NotesV2, "app.store:notes")
	address := store.DocumentAddress{Namespace: "chord", Owner: "app.store", Kind: "memo"}
	var ids []int64
	created := store.CommitFence{
		Admit: func() bool { return true },
		Adopt: func(_ int64, created []int64) error {
			ids = created
			return nil
		},
	}
	var creations []store.StagedWrite
	for _, key := range []string{"", "a", "b", "gone"} {
		keyed := address
		keyed.Key = new(key)
		value := delta.JsonObjectOf("title", key)
		base := store.DocumentBase{Version: 1, Fingerprint: "f1", Value: value}
		creations = append(creations, store.CreateDocument(keyed, base, nexttest.JSONText(t, value), nil))
	}
	if _, err := st.Commit(creations, created, background); err != nil {
		t.Fatalf("archiveStore: create documents: %v", err)
	}
	a, b, gone := ids[1], ids[2], ids[3]
	change := func(id, revision int64, title string) store.StagedWrite {
		ops := []delta.Op{{"s", []any{"title"}, title}}
		content := store.DocumentContent{Kind: "delta", Version: 1, Ops: ops}
		return store.ChangeDocument(id, revision, content, nexttest.JSONText(t, ops))
	}
	commit := func(writes ...store.StagedWrite) {
		if _, err := st.Commit(writes, openFence(), background); err != nil {
			t.Fatalf("archiveStore: commit: %v", err)
		}
	}
	commit(change(a, 1, "a2"), store.RetireDocument(gone, 1))
	commit(change(a, 2, "a3"))
	commit(change(b, 1, "b2"))
	return st
}

// archiveTableAt is definition prepared as table in st, and a handle to it.
func archiveTableAt(t testing.TB, st *store.ChordStore, table string, definition *store.TableDefinition) *store.TableHandle {
	t.Helper()
	err := store.PrepareTables([]store.DeclaredTable{{Definition: definition, Table: table}}, st, openFence(), "app", background)
	must(t, err)
	return store.NewTableHandle(definition, table, st, tableOwner())
}

// exported is every part of st's export.
func exported(t testing.TB, st store.Store, ctx context.Context) []store.ArchivePart {
	t.Helper()
	export, err := st.Export(ctx)
	must(t, err)
	var parts []store.ArchivePart
	for {
		part, ok, err := export.Next(ctx)
		must(t, err)
		if !ok {
			return parts
		}
		parts = append(parts, part)
	}
}
