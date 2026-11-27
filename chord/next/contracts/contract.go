package contracts

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/contracts/contract.ts (contract handles: the branded, immutable value an app declares
// and the Chord-owned data behind it).

// Kind is the kind of a contract: "remote" or "local".
type Kind string

// The kinds of a contract.
const (
	KindRemote Kind = "remote"
	KindLocal  Kind = "local"
)

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
	// Flags is a remote contract's flags (rights that are no member, slice 15 §1): name -> MessageData label.
	// Never part of the fingerprint (only the flag names are).
	Flags *NamedMap[MessageData]
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
	Members     *MembersData
	// Grantable is the set of grantable member names in this version (empty for local contracts).
	Grantable map[string]bool
	// Grants is the sorted list of grantable member names + flag names (indexed by wire `bound.granted`).
	Grants []string
	// Changes are the changes that produced this version; empty for a base.
	Changes []Change

	cache *versionCache
}

type versionCache struct {
	mu           sync.Mutex
	grantedCache map[string]*MembersData
}

// MembersData is a contract's members at one version.
type MembersData struct {
	State   *NamedMap[*Snapshot]
	Methods *NamedMap[*MethodData]
	// Keyed holds keyed state members (slice 6a §12): only a local contract has any.
	Keyed *NamedMap[*KeyedSnapshot]
	// Events holds event members: their values' schemas (JSON only).
	Events *NamedMap[*Snapshot]
	// Queries holds queries members (slice 14 §3.1): only a local contract has any. Nil and empty are one: contracts
	// without a queries member add nothing to their fingerprint.
	Queries *NamedMap[*QueriesData]

	orderOnce   sync.Once
	cachedOrder MemberNames
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

// CurrentVersion is the current version of a contract.
func CurrentVersion(contract *Contract) (*VersionData, error) {
	data, err := ContractDataOf(contract)
	if err != nil {
		return nil, err
	}
	return &data.Versions[len(data.Versions)-1], nil
}

// CurrentMembers is the members of a contract's current version.
func CurrentMembers(contract *Contract) (*MembersData, error) {
	v, err := CurrentVersion(contract)
	if err != nil {
		return nil, err
	}
	return v.Members, nil
}

// VersionAt is version `version` of a contract's history (an older consumer's binding, Q10).
func VersionAt(contract *Contract, version int) (*VersionData, error) {
	data, err := ContractDataOf(contract)
	if err != nil {
		return nil, err
	}
	for i := range data.Versions {
		if data.Versions[i].Version == version {
			return &data.Versions[i], nil
		}
	}
	return nil, fail("%s: no version %d", data.Name, version)
}

// NoGrants is the shared empty granted set (do not mutate).
var NoGrants = map[string]bool{}

// GrantedMembers returns the members of version a binding granted `granted` has: its plain members and the granted ones.
func GrantedMembers(version *VersionData, granted map[string]bool) *MembersData {
	if len(version.Grantable) == 0 {
		return version.Members
	}
	var kept []string
	for member := range version.Grantable {
		if granted[member] {
			kept = append(kept, member)
		}
	}
	slices.SortFunc(kept, compare16)
	key := JoinKey(kept)

	if version.cache != nil {
		version.cache.mu.Lock()
		if cached, ok := version.cache.grantedCache[key]; ok {
			version.cache.mu.Unlock()
			return cached
		}
		version.cache.mu.Unlock()
	}

	filterSnapshots := func(src *NamedMap[*Snapshot]) *NamedMap[*Snapshot] {
		dst := NewNamedMap[*Snapshot](src.Len())
		for name, val := range src.All() {
			if !version.Grantable[name] || granted[name] {
				dst.Set(name, val)
			}
		}
		return dst
	}
	filterMethods := func(src *NamedMap[*MethodData]) *NamedMap[*MethodData] {
		dst := NewNamedMap[*MethodData](src.Len())
		for name, val := range src.All() {
			if !version.Grantable[name] || granted[name] {
				dst.Set(name, val)
			}
		}
		return dst
	}
	members := &MembersData{
		State:   filterSnapshots(version.Members.State),
		Methods: filterMethods(version.Members.Methods),
		Keyed:   version.Members.Keyed,
		Events:  filterSnapshots(version.Members.Events),
		Queries: version.Members.Queries,
	}

	if version.cache != nil {
		version.cache.mu.Lock()
		if cached, ok := version.cache.grantedCache[key]; ok {
			version.cache.mu.Unlock()
			return cached
		}
		version.cache.grantedCache[key] = members
		version.cache.mu.Unlock()
	}
	return members
}

// GrantIndexes returns the names of `granted` as the wire carries them: indexes into version.Grants (names it lacks
// are left out), in a fresh slice.
func GrantIndexes(version *VersionData, granted map[string]bool) []int {
	if len(granted) == 0 || len(version.Grants) == 0 {
		return []int{}
	}
	out := make([]int, 0, len(granted))
	for i, name := range version.Grants {
		if granted[name] {
			out = append(out, i)
		}
	}
	return out
}

// MemberNames is a contract's or binding's member names by kind: state, methods and events.
type MemberNames struct {
	State   []string
	Methods []string
	Events  []string
}

// MemberOrder returns members' names in the order links index them: each kind sorted (UTF-16 code unit order).
func MemberOrder(members *MembersData) MemberNames {
	members.orderOnce.Do(func() {
		order := MemberNames{
			State:   sortStrings(members.State.Keys()),
			Methods: sortStrings(members.Methods.Keys()),
			Events:  noNames,
		}
		if members.Events.Len() > 0 {
			order.Events = sortStrings(members.Events.Keys())
		}
		members.cachedOrder = order
	})
	return members.cachedOrder
}

var noNames = []string{}

// MemberNamesOf returns the names of the state members, methods and event members of a contract's current version, in
// the order declared. A local contract's keyed and queries members are not among them.
func MemberNamesOf(contract *Contract) (MemberNames, error) {
	members, err := CurrentMembers(contract)
	if err != nil {
		return MemberNames{}, err
	}
	return MemberNames{
		State:   ownNames(members.State.Keys()),
		Methods: ownNames(members.Methods.Keys()),
		Events:  ownNames(members.Events.Keys()),
	}, nil
}

// ownNames is a copy of names that is never nil.
func ownNames(names []string) []string { return append([]string{}, names...) }

// CreateContract is a new contract handle for data.
func CreateContract(data ContractData) *Contract {
	data.Versions = slices.Clone(data.Versions)
	for i := range data.Versions {
		if data.Versions[i].Grantable != nil {
			data.Versions[i].Grantable = maps.Clone(data.Versions[i].Grantable)
		}
		data.Versions[i].Grants = slices.Clone(data.Versions[i].Grants)
	}
	current := data.Versions[len(data.Versions)-1]
	contract := &Contract{Kind: data.Kind, Name: data.Name, Version: current.Version, Fingerprint: current.Fingerprint, data: &data}
	contract.self = contract
	return contract
}

// MethodFunction is a method as a function. Upstream omits the argument when a method's argument schema is `null`
// (TakesNoArgs); the Go function always takes it, nil then. A thrown Error is a returned error.
type MethodFunction func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error)

// StateBuilder is a lazy builder for a grantable state member (called only if granted).
type StateBuilder func() chord.Watchable[delta.JsonValue]

// EventBuilder is a lazy builder for a grantable event member (called only if granted).
type EventBuilder func() EventMember[delta.JsonValue]

// MembersImplementation is a contract's members: a Watchable (or StateBuilder for grantable) per state member, a
// function per method, an EventMember (or EventBuilder for grantable) per event member and, in a local contract, a
// KeyedWatchable per keyed member and, per queries member, the named queries it lists.
type MembersImplementation struct {
	State       map[string]chord.Watchable[delta.JsonValue]
	StateBuild  map[string]StateBuilder
	Methods     map[string]MethodFunction
	Keyed       map[string]KeyedWatchable[delta.JsonValue]
	Events      map[string]EventMember[delta.JsonValue]
	EventsBuild map[string]EventBuilder
	Queries     map[string]map[string]NamedQuery[delta.JsonValue, delta.JsonValue]
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
// (unix milliseconds).
type EventDelivery[T any] struct {
	Value T
	At    int64
}

// EventMember is an event member as its readers have it (decision "Events: fire-and-forget members"): a reference's,
// which receives the values its object emits while it is bound, never one emitted before it bound, never replayed.
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

// KeyedWatchable is a keyed state member (slice 6a §12): documents by key, each watchable on its own; a keyed document
// handle provides it. At is valid only in a factory (it pins the key while that attempt lives).
type KeyedWatchable[T any] interface {
	// Get is the document at key; found is false when absent.
	Get(ctx context.Context, key any) (value T, found bool, err error)
	// Watch is a watch of the document at key that follows its address; the handle is nil when absent.
	Watch(ctx context.Context, key any) (chord.WatchHandle[T], error)
	// At is, in a factory only, the document at key as a watchable, initial() while absent.
	At(key any) chord.Watchable[T]
}
