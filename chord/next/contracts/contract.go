package contracts

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/contracts/contract.ts (contract handles: the branded, immutable value an app declares
// and the Chord-owned data behind it).
//
// Forced Go differences. The WeakMap brand is the Contract itself: a *Contract holds its ContractData and a pointer to
// itself, so a copy of the struct (upstream: a spread copy) or a hand-built value is refused by ContractDataOf; the
// data is collected with the contract. The type-level ContractShape, ShapeOf, CurrentVariants<C>, ParamsOf<C>,
// Implementation<C> and VariantImplementation<V> (NamedQueriesOf included) are designed out: a variant's members are Go values keyed by name
// (VariantImplementation) and params are a *delta.JsonObject. `LOCAL` is LocalVariant, because Local declares a local
// contract.

// Kind is the kind of a contract: "remote" or "local".
type Kind string

// The kinds of a contract.
const (
	KindRemote Kind = "remote"
	KindLocal  Kind = "local"
)

// LocalVariant is the variant name of a local contract's members (upstream's `LOCAL`).
const LocalVariant = "members"

// Contract is a declared contract: a branded handle, immutable by convention. Copies and hand-built values are refused
// (ContractDataOf). Always held by pointer; the pointer is its identity.
type Contract struct {
	Kind        Kind
	Name        string
	Version     int
	Fingerprint string

	data *ContractData
	self *Contract
}

// ContractData is the Chord-owned data behind a contract handle.
type ContractData struct {
	Kind   Kind
	Name   string
	Params *ParamsData
	// HostAddress is a remote contract's host address: the address of the host serving an object for its params; nil for
	// local.
	HostAddress HostAddress
	// Versions is every version of the history, oldest first; the last is current.
	Versions []VersionData
	// Kinds is the kind of every member name ever declared in the history.
	Kinds map[string]MemberKind
	// Admission is a remote contract's admission, as its current version declares it; nil without one, and for local.
	Admission *AdmissionData
}

// AdmissionData is a contract's admission (slice 14 §2.1): the schema of the value its factory is handed, and that
// schema's fingerprint. Never part of the contract's fingerprint; a reload's staging compares it (the value is never
// checked).
type AdmissionData struct {
	Schema      *Snapshot
	Fingerprint string
}

// MemberKind is the kind of a member name.
type MemberKind string

// The kinds of a member.
const (
	MemberKindState  MemberKind = "state"
	MemberKindMethod MemberKind = "method"
	MemberKindKeyed  MemberKind = "keyed"
	MemberKindEvent  MemberKind = "event"
	// MemberKindQueries is a local contract's queries member (slice 14 §3.1).
	MemberKindQueries MemberKind = "queries"
)

// HostAddress is a `hostAddress` as Chord calls it: with checked params (a *delta.JsonObject of the param fields).
// Plugin code: what it returns is checked where routed. A thrown Error is a returned error.
type HostAddress func(params *delta.JsonObject) (Address, error)

// VersionData is one version of a history.
type VersionData struct {
	Version     int
	Fingerprint string
	Variants    *NamedMap[*VariantData]
	// Changes are the changes that produced this version; empty for a base.
	Changes []Change
}

// VariantData is one variant's members at one version.
type VariantData struct {
	State   *NamedMap[*Snapshot]
	Methods *NamedMap[*MethodData]
	// Keyed holds keyed state members (slice 6a §12): only a local contract has any.
	Keyed *NamedMap[*KeyedSnapshot]
	// Events holds event members: their values' schemas (JSON only).
	Events *NamedMap[*Snapshot]
	// Queries holds queries members (slice 14 §3.1): only a local contract has any. Nil and empty are one: contracts
	// without a queries member add nothing to their fingerprint.
	Queries *NamedMap[*QueriesData]
}

// QueriesData is a queries member's snapshots: its items' schema, and each named query's arguments, in declaration order.
type QueriesData struct {
	Item *Snapshot
	Args *NamedMap[*Snapshot]
}

// KeyedSnapshot is a keyed member's schema snapshot and, for a host-keyed one, the host kind its keys are addresses of
// (nil: string keys).
type KeyedSnapshot struct {
	Snapshot
	Key *HostKind
}

// MethodData is a method's argument and result snapshots.
type MethodData struct {
	Args   *Snapshot
	Result *Snapshot
}

// ContractDataOf is the data behind a contract handle; it refuses copies and hand-built values. Every call on one
// contract returns the same *ContractData.
func ContractDataOf(contract *Contract) (*ContractData, error) {
	if contract == nil || contract.self != contract || contract.data == nil {
		name := "nil"
		if contract != nil {
			name = contract.Name
		}
		return nil, fail("%s: not a declared contract (spread and hand-built copies are refused)", name)
	}
	return contract.data, nil
}

// TakesNoArgs reports whether a method takes no arguments: its argument schema is `null`, so its functions omit the
// argument.
func TakesNoArgs(method *MethodData) bool {
	// A schema's normal form is an object of keywords.
	normal, ok := method.Args.Normal.(*delta.JsonObject)
	return ok && normal.Value("type") == "null" && normal.Len() == 1
}

// CurrentVariants is the variants of a contract's current version; a local contract has the one variant LocalVariant.
func CurrentVariants(contract *Contract) (*NamedMap[*VariantData], error) {
	data, err := ContractDataOf(contract)
	if err != nil {
		return nil, err
	}
	return data.Versions[len(data.Versions)-1].Variants, nil
}

// VariantsAt is the variants of version version of a contract's history (an older consumer's binding, Q10). Callers
// name a version the provider binds: its history has it; an unknown version is an error.
func VariantsAt(contract *Contract, version int) (*NamedMap[*VariantData], error) {
	data, err := ContractDataOf(contract)
	if err != nil {
		return nil, err
	}
	for _, versionData := range data.Versions {
		if versionData.Version == version {
			return versionData.Variants, nil
		}
	}
	return nil, fail("%s: no version %d", data.Name, version)
}

// VariantNames is a variant's names as links index them (2b): its state members, its methods and its event members,
// each sorted.
type VariantNames struct {
	State   []string
	Methods []string
	Events  []string
}

// VariantOrder is the current version's variant names in the order links index them: sorted.
func VariantOrder(contract *Contract) ([]string, error) {
	variants, err := CurrentVariants(contract)
	if err != nil {
		return nil, err
	}
	return sortStrings(variants.Keys()), nil
}

// VariantNamesOf is a variant's state members, methods and event members in the order links index them: each sorted; a
// variant without event members shares one empty list (contracts without events pay nothing for them per binding)
// (upstream's `variantNames`).
func VariantNamesOf(variant *VariantData) VariantNames {
	names := VariantNames{State: sortStrings(variant.State.Keys()), Methods: sortStrings(variant.Methods.Keys()), Events: noNames}
	if variant.Events.Len() > 0 {
		names.Events = sortStrings(variant.Events.Keys())
	}
	return names
}

var noNames = []string{}

// MemberNameLists is the member names of one variant of a contract, by kind, in declared order: the result of MemberNamesOf (Pi's
// contract.ts `MemberNames` type; Go's MemberNames is the function of history.ts's type of the same name, so this type is named
// for what it holds).
type MemberNameLists struct {
	State   []string
	Methods []string
	Events  []string
}

// MemberNamesOf is the names of the state members, methods and event members of variant of a contract's current version, in the
// order declared (a local contract has the one variant LocalVariant), for code that fills an implementation from another object's
// members; a local contract's keyed and queries members are not among them (Pi's `memberNames(contract, variant = LOCAL)`: Go
// has no default argument, and Go's MemberNames is taken by history.ts's type, as VariantNamesOf is the name of its kind). It
// refuses a variant the contract does not have and a copy of a contract. The lists are the caller's own: none shares an array with
// the contract, and a kind without members is an empty list, not nil.
func MemberNamesOf(contract *Contract, variant string) (MemberNameLists, error) {
	variants, err := CurrentVariants(contract)
	if err != nil {
		return MemberNameLists{}, err
	}
	found, ok := variants.Get(variant)
	if !ok {
		return MemberNameLists{}, fail("%s: no variant \"%s\"", contract.Name, variant)
	}
	return MemberNameLists{State: ownNames(found.State.Keys()), Methods: ownNames(found.Methods.Keys()), Events: ownNames(found.Events.Keys())}, nil
}

// ownNames is a copy of names that is never nil.
func ownNames(names []string) []string { return append([]string{}, names...) }

// CreateContract is a new contract handle for data.
func CreateContract(data ContractData) *Contract {
	data.Versions = slices.Clone(data.Versions)
	current := data.Versions[len(data.Versions)-1]
	contract := &Contract{Kind: data.Kind, Name: data.Name, Version: current.Version, Fingerprint: current.Fingerprint, data: &data}
	contract.self = contract
	return contract
}

// MethodFunction is a method as a function. Upstream omits the argument when a method's argument schema is `null`
// (TakesNoArgs); the Go function always takes it, nil then. A thrown Error is a returned error.
type MethodFunction func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error)

// VariantImplementation is one variant's members: a Watchable per state member, a function per method, an EventMember
// per event member (an object's own EventSource, or a reference's member it re-exposes) and, in a local contract, a
// KeyedWatchable per keyed member and, per queries member, the named queries it lists (upstream's `NamedQueriesOf`: its
// NamedQuery per listed name), by member name and then query name.
type VariantImplementation struct {
	State   map[string]chord.Watchable[delta.JsonValue]
	Methods map[string]MethodFunction
	Keyed   map[string]KeyedWatchable[delta.JsonValue]
	Events  map[string]EventMember[delta.JsonValue]
	Queries map[string]map[string]NamedQuery[delta.JsonValue, delta.JsonValue]
}

// QueryPage is a page of a named query's items and, when more follow, the cursor to continue after it (Next; nil: none).
type QueryPage[T any] struct {
	Items []T
	Next  *string
}

// QueryPageOptions are the options of NamedQuery.Page.
type QueryPageOptions struct {
	// After is the cursor to continue after (a previous page's Next); nil: from the start.
	After *string
}

// NamedQuery is a named query (slice 14 §3.1): a query a store offers under a name, with arguments of A and items of T.
// Read live with `self.liveQuery`, counted with `self.count`, shown as a list with `self.list`, or read a page at a time.
// (Upstream's type-only `~args` member is designed out with the other type-level machinery.)
type NamedQuery[T, A any] interface {
	// Page is up to limit items for args (after options.After), and the cursor of the next page when there is one.
	Page(args A, limit int64, ctx context.Context, options QueryPageOptions) (QueryPage[T], error)
}

// EventSubscription is a subscription to an event member (EventMember.On): it lives until Stop, its context's abort or
// its reference's release.
type EventSubscription interface {
	// Stop ends the subscription; its listener is not called again, a call already due included. Idempotent.
	Stop()
}

// EventDelivery is what an event listener gets: the emitted value and At, the emitting host's `Date.now()` at the emit
// (unix milliseconds). Upstream's anonymous `{ readonly value: T; readonly at: number }`.
type EventDelivery[T any] struct {
	Value T
	At    int64
}

// EventMember is an event member as its readers have it (decision "Events: fire-and-forget members"): a reference's,
// which receives the values its object emits while it is bound to an instance whose variant has the member, never one
// emitted before it bound, never replayed. Not a Watchable: no value, no derive or routine source.
type EventMember[T any] interface {
	// On subscribes listener at once, bound or not: it gets every event delivered to this member from now on, At the
	// emitting host's `Date.now()` at the emit. It returns the `released` error for a released reference and the abort
	// cause of an aborted ctx.
	On(listener func(EventDelivery[T]), ctx context.Context) (EventSubscription, error)
}

// EventSource is an event member as its object owns it (`self.events<T>()`): it emits.
type EventSource[T any] interface {
	EventMember[T]
	// Emit emits value now, outside any transaction: delivered after every state change published before the event
	// flush (a microtask), to the references bound before this emit. It returns the `released` error after its attempt's
	// seal (upstream throws).
	Emit(value T) error
}

// Implementation is what a factory returns: for a local contract Members alone (Variant is empty), for a remote one
// one variant with exactly that variant's members.
type Implementation struct {
	Variant string
	Members VariantImplementation
}

// KeyedWatchable is a keyed state member (slice 6a §12): documents by key, each watchable on its own; a keyed document
// handle provides it. At is valid only in a factory (it pins the key while that attempt lives). A key is a string, or an
// Address of the member's host kind when the member is host-keyed (Pi's `KeyedWatchable<T, Key>`, whose Key parameter is
// designed out).
type KeyedWatchable[T any] interface {
	// Get is the document at key; found is false when absent.
	Get(ctx context.Context, key any) (value T, found bool, err error)
	// Watch is a watch of the document at key that follows its address; the handle is nil when absent.
	Watch(ctx context.Context, key any) (chord.WatchHandle[T], error)
	// At is, in a factory only, the document at key as a watchable, initial() while absent.
	At(key any) chord.Watchable[T]
}
