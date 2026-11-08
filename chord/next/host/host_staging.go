package host

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/src/next/host/staging.ts. Staging (slice 7b §2, per plugin since slice 7c): one plugin's setup run and
// checked (`stagePlugin`), staged plugins merged into one table (`addStaged`), and a recording `self` that runs an object's
// new factory with every declaration check an attempt makes and no effect (`recordFactory`).
//
// Forced Go differences. Pi's `Map`s are insertion ordered: `namedMap` keeps that order. A declaration that throws in Pi
// panics in Go (Self's methods return no error), and the factory runs under tryPlugin, as an attempt's does. Pi's handles of
// a recording `self` are one `Proxy` that gives itself back for every property and call (INERT): Go's are typed inert values
// that are safe to use (a document handle of the recording's own sealed documents, whose reads return `not_ready`, as an
// attempt's in its factory do before activation, and whose changes are refused `released`; a `*store.TableHandle` whose reads
// and queries return errInert; a never-bound `*Reference`, constants, an inert writable, native and job), so a factory
// that reads or calls one while staging neither panics nor reaches anything.

// pluginNamePattern is an npm package name, scoped or not.
var pluginNamePattern = regexp.MustCompile(`^(?:@[a-z0-9~-][a-z0-9._~-]*/)?[a-z0-9~-][a-z0-9._~-]*$`)

// IsPluginName reports whether name is a valid plugin name: an npm package name, scoped or not (the plugins component checks
// it too).
func IsPluginName(name string) bool { return pluginNamePattern.MatchString(name) }

// namedMap is a map by name that iterates in insertion order (Pi's `Map<string, V>`).
type namedMap[V any] struct {
	names  []string
	values map[string]V
}

func newNamedMap[V any]() *namedMap[V] { return &namedMap[V]{values: map[string]V{}} }

func (m *namedMap[V]) get(name string) (V, bool) {
	value, ok := m.values[name]
	return value, ok
}

func (m *namedMap[V]) has(name string) bool {
	_, ok := m.values[name]
	return ok
}

// set sets name; a new name goes last, an old one keeps its place.
func (m *namedMap[V]) set(name string, value V) {
	if _, ok := m.values[name]; !ok {
		m.names = append(m.names, name)
	}
	m.values[name] = value
}

// delete removes name.
func (m *namedMap[V]) delete(name string) {
	if _, ok := m.values[name]; !ok {
		return
	}
	delete(m.values, name)
	for at, each := range m.names {
		if each == name {
			m.names = append(m.names[:at:at], m.names[at+1:]...)
			break
		}
	}
}

func (m *namedMap[V]) len() int { return len(m.names) }

// keys are the names in insertion order (not a copy: do not change it).
func (m *namedMap[V]) keys() []string { return m.names }

// stagedRegistry is a registry a staged plugin serves: the defining plugin, the token and its provider.
type stagedRegistry struct {
	plugin   string
	registry registries.Registry[any, any]
	provider registries.RegistryProvider[any, any]
}

// stagedPlugin is one plugin's setup, run and checked: what it serves and contributes, nothing published yet.
type stagedPlugin struct {
	name string
	// types are the served types by name, each with its plugin, factory, provider (its `accepts` checked) and `linger`.
	types *namedMap[*served]
	// registries are the served registries by name.
	registries *namedMap[stagedRegistry]
	// contributions are the facet contributions, in the setup's order.
	contributions []facetContribution
}

// staged is a plugin set's staged plugins, merged: what they serve, by name, and each plugin's own table.
type staged struct {
	types      *namedMap[*served]
	registries *namedMap[stagedRegistry]
	plugins    *namedMap[*stagedPlugin]
}

// emptyStaged is a staged table with nothing in it.
func emptyStaged() *staged {
	return &staged{types: newNamedMap[*served](), registries: newNamedMap[stagedRegistry](), plugins: newNamedMap[*stagedPlugin]()}
}

// stagePlugin runs one plugin's setup and checks what it declared: its name (an npm name), a type or registry it serves
// twice, a remote type on a host without a principal or address, every provider's `accepts` (`checkAccepts`) and every
// registry provider's shape. It returns the first error, naming the plugin.
func stagePlugin(plugin HostFacet, host *chordHost) (*stagedPlugin, error) {
	name := plugin.Name
	if !IsPluginName(name) {
		return nil, fmt.Errorf("%q is not a valid plugin name", name)
	}
	types := newNamedMap[*served]()
	registryTable := newNamedMap[stagedRegistry]()
	var contributions []facetContribution
	_, err := tryPlugin(func() (any, error) {
		if !plugin.Facet.hasSetup() {
			return nil, errors.New("the facet has no setup")
		}
		set, err := setUp(name, plugin.Facet, host.References)
		if err != nil {
			return nil, err
		}
		for _, entry := range set.served {
			if err := stageEntry(name, entry, types, registryTable, host); err != nil {
				return nil, err
			}
		}
		contributions = set.contributions
		return nil, nil
	})
	if err != nil {
		return nil, named(err, fmt.Sprintf("plugin %q", name))
	}
	return &stagedPlugin{name: name, types: types, registries: registryTable, contributions: contributions}, nil
}

// addStaged merges plugin into table; a type or registry another plugin serves there already refuses it (its error
// returned, nothing merged).
func addStaged(table *staged, plugin *stagedPlugin) error {
	for _, name := range plugin.types.keys() {
		if other, ok := table.types.get(name); ok {
			return fmt.Errorf("plugin %q: %s is served twice (plugin %q)", plugin.name, name, other.plugin)
		}
	}
	for _, name := range plugin.registries.keys() {
		if other, ok := table.registries.get(name); ok {
			return fmt.Errorf("plugin %q: registry %s is served twice (plugin %q)", plugin.name, name, other.plugin)
		}
	}
	for _, name := range plugin.types.keys() {
		entry, _ := plugin.types.get(name)
		table.types.set(name, entry)
	}
	for _, name := range plugin.registries.keys() {
		entry, _ := plugin.registries.get(name)
		table.registries.set(name, entry)
	}
	table.plugins.set(plugin.name, plugin)
	return nil
}

// stageEntry stages one served entry of plugin into its tables: a registry's provider, or a type as `served`.
func stageEntry(plugin string, entry servedEntry, types *namedMap[*served], registryTable *namedMap[stagedRegistry], host *chordHost) error {
	if entry.kind == servedRegistry {
		if registryTable.has(entry.registry.Name) {
			return fmt.Errorf("registry %s is served twice", entry.registry.Name)
		}
		if err := registries.CheckProvider(entry.registry.Name, entry.provider); err != nil {
			return err
		}
		registryTable.set(entry.registry.Name, stagedRegistry{plugin: plugin, registry: entry.registry, provider: entry.provider})
		return nil
	}
	contract := entry.contract
	if types.has(contract.Name) {
		return fmt.Errorf("%s is served twice", contract.Name)
	}
	// A client serves only local types (§3): a remote object is owned by a principal of the host serving it.
	if contract.Kind == contracts.KindRemote && host.principal == nil {
		return fmt.Errorf("%s: a host without a principal serves only local types", contract.Name)
	}
	// A remote object lives on the host its `hostAddress` names: only a host with an address can be that host.
	if contract.Kind == contracts.KindRemote && host.address == nil {
		return fmt.Errorf("%s: a host without an address serves only local types", contract.Name)
	}
	provider, err := data.BindProvider(contract, entry.options.Accepts)
	if err != nil {
		return err
	}
	contractData, err := contracts.ContractDataOf(contract)
	if err != nil {
		return err
	}
	var local LocalFactory
	var remote RemoteFactory
	kind := servedLocal
	if contract.Kind == contracts.KindLocal {
		factory, ok := entry.factory.(LocalFactory)
		if !ok {
			return fmt.Errorf("%s: a local type is served with a LocalFactory, not %T", contract.Name, entry.factory)
		}
		local = factory
	} else {
		factory, ok := entry.factory.(RemoteFactory)
		if !ok {
			return fmt.Errorf("%s: a remote type is served with a RemoteFactory, not %T", contract.Name, entry.factory)
		}
		kind, remote = servedRemote, factory
	}
	types.set(contract.Name, &served{
		kind:         kind,
		plugin:       plugin,
		provider:     provider,
		linger:       durationOr(entry.options.Linger, 0),
		contract:     contract,
		local:        local,
		remote:       remote,
		hostLifetime: contract.Kind == contracts.KindLocal && len(contractData.Params.Fields) == 0,
	})
	return nil
}

// Recorded is what a recorded factory declared: what a reload's fate and checks need, nothing activated.
type Recorded struct {
	documents     []documents.AnyDefinition
	tables        []*store.TableDefinition
	jobs          []*JobDefinition
	contributions []registries.Registry[any, any]
	// requests are its requests: the types it gets (`self.get`, with their checked params) and the registries it reads
	// (`self.registry`).
	requests []request
}

// requestKind tells what a recorded request asks for.
type requestKind uint8

const (
	// requestType is a type and its params (`self.get`).
	requestType requestKind = iota
	// requestRegistry is a registry (`self.registry`).
	requestRegistry
)

// request is a recorded request, with the contract version it was made at: a type and its params, or a registry.
type request struct {
	kind     requestKind
	contract *contracts.Contract
	params   *delta.JsonObject
	registry registries.Registry[any, any]
}

// recordFactory runs served's factory for object (its params and principal) against a recording `self` and returns what it
// declared. Every declaration check an attempt makes runs, without effects (declared twice; persisted data in a type
// without a stable identity, on a host without a store, or claimed by another owner; contributions outside host-lifetime
// objects, or twice; `retryOn` sources that do not outlive the attempt; params; a document or job declared at an older
// version than the object's, or at its version with another schema); an error is the factory's. Handles it hands out are
// inert, except registry values (the host's own, which `retryOn` checks by kind).
func recordFactory(object *chordObject, entry *served, ctx context.Context) (Recorded, error) {
	recording := &recordingSelf{object: object, served: entry, ctx: ctx, declaring: true, documents: newNamedMap[documents.AnyDefinition](),
		tables: newNamedMap[*store.TableDefinition](), jobs: newNamedMap[*JobDefinition](), contributions: newNamedMap[registries.Registry[any, any]]()}
	_, err := tryPlugin(func() (any, error) {
		defer func() { recording.declaring = false }()
		if entry.kind == servedLocal {
			return entry.local(object.params, recording)
		}
		// A remote object's identity has its principal.
		return entry.remote(object.params, object.principal, recording)
	})
	if err != nil {
		return Recorded{}, named(err, fmt.Sprintf("plugin %q, %s", entry.plugin, object.text))
	}
	return recording.recorded(), nil
}

// named is a staging error naming where it happened (what): its message prefixed, the error itself its cause.
func named(err error, what string) error {
	return &namedError{message: what + ": " + err.Error(), cause: err}
}

// namedError is an error with a prefixed message that unwraps to its cause.
type namedError struct {
	message string
	cause   error
}

func (e *namedError) Error() string { return e.message }
func (e *namedError) Unwrap() error { return e.cause }

// recordingSelf is a factory's `self` during staging: it records declarations and checks them; it activates nothing.
type recordingSelf struct {
	declaring     bool
	object        *chordObject
	served        *served
	ctx           context.Context
	documents     *namedMap[documents.AnyDefinition]
	tables        *namedMap[*store.TableDefinition]
	jobs          *namedMap[*JobDefinition]
	contributions *namedMap[registries.Registry[any, any]]
	requests      []request
	// inert are the documents of the handles State returns and token their seal token; made by the first.
	inert *documents.Documents
	token *documents.SealToken
}

var _ StoreSelf = (*recordingSelf)(nil)

func (r *recordingSelf) recorded() Recorded {
	out := Recorded{requests: r.requests}
	for _, name := range r.documents.keys() {
		definition, _ := r.documents.get(name)
		out.documents = append(out.documents, definition)
	}
	for _, name := range r.tables.keys() {
		definition, _ := r.tables.get(name)
		out.tables = append(out.tables, definition)
	}
	for _, name := range r.jobs.keys() {
		definition, _ := r.jobs.get(name)
		out.jobs = append(out.jobs, definition)
	}
	for _, name := range r.contributions.keys() {
		registry, _ := r.contributions.get(name)
		out.contributions = append(out.contributions, registry)
	}
	return out
}

// State implements Self.
func (r *recordingSelf) State(definition documents.AnyDefinition) any {
	r.assertDeclaring("state")
	what := `state "` + definition.Name + `"`
	if r.documents.has(definition.Name) {
		panic(declarationError("%s: %s is declared twice", r.object.text, what))
	}
	if definition.Persist {
		r.assertClaimable(what, "chord")
	}
	if err := r.object.documents.Check(definition); err != nil {
		panic(err)
	}
	r.documents.set(definition.Name, definition)
	// Pi's INERT answers `self.state(D).value`, `.change(...)` and every other use of the handle, none of which touches
	// anything. The handle here is a real one of the recording's own documents (not the object's: the object's slots
	// stay as they are): a read is the `not_ready` error an attempt's own read in its factory is, before activation, and
	// a change or retire is refused `released` (the documents are sealed).
	handle, err := r.inertDocuments().Declare(definition, r.token, r)
	if err != nil {
		panic(err)
	}
	return handle
}

// inertDocuments is the documents the recording's handles are of: the object's, as a copy that never activates, whose
// transactions are refused (sealed) and whose failures and store are none.
func (r *recordingSelf) inertDocuments() *documents.Documents {
	if r.inert == nil {
		r.inert = documents.NewDocuments(&recordingDocuments{objectDocuments{objectClock{object: r.object}}})
		r.inert.Seal()
		r.token = documents.NewSealToken()
	}
	return r.inert
}

// At implements documents.KeyDeclarer: `state(D).at(key)` of a recorded factory is a source of the recording's own
// documents (Pi's INERT), pinned for no one.
func (r *recordingSelf) At(slot *documents.KeyedSlot, key string) (*documents.KeySource, error) {
	r.assertDeclaring("state(...).at")
	if _, err := slot.Pin(key, r.ctx); err != nil {
		return nil, err
	}
	return slot.KeySourceOf(key), nil
}

// recordingDocuments is the owner of a recording's documents: the object's label, batch and context, but no store and a
// failure that fails nothing.
type recordingDocuments struct{ objectDocuments }

// Store implements documents.DocumentsOwner: none, so nothing persists.
func (*recordingDocuments) Store() (*store.ChordStore, string, bool) { return nil, "", false }

// Fail implements documents.DocumentsOwner: a recorded factory fails nothing.
func (*recordingDocuments) Fail(*data.ChordError) {}

// inertTableOwner is the owner of a recording's table handles: never live, so every read and query returns errInert.
type inertTableOwner struct{ label string }

func (o inertTableOwner) Label() string         { return o.label }
func (o inertTableOwner) Scope() any            { return nil }
func (o inertTableOwner) AssertLive() error     { return errInert }
func (o inertTableOwner) Fail(*data.ChordError) {}

// Table implements StoreSelf.
func (r *recordingSelf) Table(definition *store.TableDefinition) *store.TableHandle {
	r.assertDeclaring("table")
	chordStore := r.assertPersistable(`table "` + definition.Name + `"`)
	if r.tables.has(definition.Name) {
		panic(declarationError(`%s: table "%s" is declared twice`, r.object.text, definition.Name))
	}
	object := r.object
	sqlName := store.TableSqlName(object.served.contract.Name, definition.Name)
	if err := chordStore.Claims.AssertTable(sqlName, object); err != nil {
		panic(err)
	}
	r.tables.set(definition.Name, definition)
	// A handle whose reads and queries return errInert (its owner is never live): nothing reaches the store.
	return store.NewTableHandle(definition, sqlName, chordStore, inertTableOwner{label: object.text})
}

// Derive implements Self.
func (r *recordingSelf) Derive([]any, func(values []any) (delta.JsonValue, error)) chord.Watchable[delta.JsonValue] {
	r.assertDeclaring("derive")
	return state.ConstantOf[delta.JsonValue](nil)
}

// Get implements Self.
func (r *recordingSelf) Get(typ *contracts.Contract, params *delta.JsonObject) *Reference {
	r.assertDeclaring("get")
	checked, err := checkParams(typ, params, r.ctx)
	if err != nil {
		panic(err)
	}
	r.requests = append(r.requests, request{kind: requestType, contract: typ, params: checked.params})
	// A reference nothing binds and nothing indexes: inert, whatever its members are asked (its `at(key)` too: Pi's INERT takes any call).
	host := r.object.host
	inert, err := host.reference(typ, r.object, host.own(), GetOptions{})
	if err != nil {
		panic(err)
	}
	return inert.reference
}

// Registry implements Self: the host's own value (an attempt declares the same one): `retryOn` checks its kind.
func (r *recordingSelf) Registry(registry registries.Registry[any, any]) *registries.RegistryValueSource {
	r.assertDeclaring("registry")
	r.requests = append(r.requests, request{kind: requestRegistry, registry: registry})
	return r.object.host.registryRecord(registry.Name).Value(registry.Version)
}

// Writable implements Self.
func (r *recordingSelf) Writable(any) state.Writable[any] {
	r.assertDeclaring("writable")
	return inertWritable{}
}

// RetryOn implements Self.
func (r *recordingSelf) RetryOn(sources ...any) {
	r.assertDeclaring("retryOn")
	for _, source := range sources {
		if _, err := retrySource(unwrapMember(source), r.object.text); err != nil {
			panic(err)
		}
	}
}

// Contribute implements StoreSelf.
func (r *recordingSelf) Contribute(registry registries.Registry[any, any], _ any) {
	r.assertDeclaring("contribute")
	entry := r.served
	label := r.object.text
	// R21: plugin code is bundled without type checks.
	if entry.kind != servedLocal || !entry.hostLifetime {
		panic(declarationError("%s: self.contribute is valid only in a local type with empty params", label))
	}
	if r.contributions.has(registry.Name) {
		panic(declarationError("%s: registry %s is contributed to twice", label, registry.Name))
	}
	r.contributions.set(registry.Name, registry)
}

// Open implements Self.
func (r *recordingSelf) Open(
	func(ctx context.Context) (any, error),
	func(value any, reason NativeCloseReason, ctx context.Context) error,
	...NativeOptions,
) Native[any] {
	r.assertDeclaring("open")
	return inertNative{}
}

// Tx implements Self.
func (r *recordingSelf) Tx(func(tx documents.Tx) (any, error), context.Context) (any, error) {
	panic(declarationError("%s: self.tx runs in calls, not in a factory", r.object.text))
}

// Follow implements Self.
func (r *recordingSelf) Follow(FollowAttach[delta.JsonValue]) chord.Watchable[delta.JsonValue] {
	r.assertDeclaring("follow")
	return state.ConstantOf[delta.JsonValue](nil)
}

// On implements Self.
func (r *recordingSelf) On(JSONSource, OnHandler[delta.JsonValue]) { r.assertDeclaring("on") }

// OnNative implements Self.
func (r *recordingSelf) OnNative(NativeSource, OnNativeHandler[any]) { r.assertDeclaring("on") }

// OnLive implements Self.
func (r *recordingSelf) OnLive(OnLiveHandler) { r.assertDeclaring("onLive") }

// Job implements Self.
func (r *recordingSelf) Job(definition *JobDefinition, _ JobImplementation) JobHandle {
	r.assertDeclaring("job")
	if r.jobs.has(definition.Name) {
		panic(declarationError(`%s: job "%s" is declared twice`, r.object.text, definition.Name))
	}
	if definition.Persist != nil {
		r.assertClaimable(`job "`+definition.Name+`"`, "chord.jobs", "chord.job-states")
	}
	if err := r.object.checkJob(definition); err != nil {
		panic(err)
	}
	r.jobs.set(definition.Name, definition)
	return inertJob{batch: r.object.host.batch}
}

// assertPersistable: persisted data needs a stable identity (P3) and the host's store, as an attempt checks; it returns the
// store.
func (r *recordingSelf) assertPersistable(what string) *store.ChordStore {
	object := r.object
	contractData, err := contracts.ContractDataOf(r.served.contract)
	if err != nil {
		panic(err)
	}
	if r.served.kind != servedLocal || len(contractData.Params.Fields) > 0 {
		panic(declarationError("%s: %s: persist needs a stable identity (a local type with empty params)", object.text, what))
	}
	if object.host.store == nil {
		panic(declarationError("%s: %s: persist needs a store", object.text, what))
	}
	return object.host.store
}

// assertClaimable: persisted documents in namespaces: as assertPersistable, and not claimed by another owner.
func (r *recordingSelf) assertClaimable(what string, namespaces ...string) {
	chordStore := r.assertPersistable(what)
	for _, namespace := range namespaces {
		if err := chordStore.Claims.AssertDocuments(r.object.served.contract.Name, namespace, r.object); err != nil {
			panic(err)
		}
	}
}

func (r *recordingSelf) assertDeclaring(declaration string) {
	if !r.declaring {
		panic(declarationError("%s: self.%s is valid only in the factory", r.object.text, declaration))
	}
}

// errInert is what an inert handle's operations return: nothing runs while a factory is recorded.
var errInert = errors.New("a handle of a recorded factory is inert")

// inertWritable is the writable of a recording `self`.
type inertWritable struct{}

func (inertWritable) Value() any { return nil }
func (inertWritable) Watch(context.Context) (chord.NativeWatchHandle[any], error) {
	return nil, errInert
}
func (inertWritable) Set(any) error { return nil }

// inertNative is the native of a recording `self`.
type inertNative struct{}

func (inertNative) Value() (any, error) { return nil, errInert }

// inertJob is the job handle of a recording `self`.
type inertJob struct{ batch *state.Batch }

func (inertJob) Start(any, context.Context, StartOptions) (Run, error) { return nil, errInert }
func (inertJob) Outcome(string, context.Context) (Outcome, error)      { return Outcome{}, errInert }
func (inertJob) Abort(string, context.Context) (AbortResult, error)    { return "", errInert }
func (j inertJob) Running() state.SourceHandle {
	return state.NewDerive(nil, func([]any) (delta.JsonValue, error) { return nil, nil }, j.batch)
}
