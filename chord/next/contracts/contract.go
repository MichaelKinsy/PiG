package contracts

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/next/contracts/contract.ts (contract handles: the branded, immutable value an app declares
// and the Chord-owned data behind it).
//
// Forced Go differences. The WeakMap brand is the Contract itself: a *Contract holds its ContractData and a pointer to
// itself, so a copy of the struct (upstream: a spread copy) or a hand-built value is refused by ContractDataOf; the
// data is collected with the contract. The type-level ContractShape, ShapeOf, CurrentVariants<C>, ParamsOf<C>,
// Implementation<C> and VariantImplementation<V> are designed out: a variant's members are Go values keyed by name
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
}

// MemberKind is the kind of a member name.
type MemberKind string

// The kinds of a member.
const (
	MemberKindState  MemberKind = "state"
	MemberKindMethod MemberKind = "method"
	MemberKindKeyed  MemberKind = "keyed"
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
	Keyed *NamedMap[*Snapshot]
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

// VariantNames is a variant's names as links index them (2b): its state members and its methods, each sorted.
type VariantNames struct {
	State   []string
	Methods []string
}

// VariantOrder is the current version's variant names in the order links index them: sorted.
func VariantOrder(contract *Contract) ([]string, error) {
	variants, err := CurrentVariants(contract)
	if err != nil {
		return nil, err
	}
	return sortStrings(variants.Keys()), nil
}

// VariantNamesOf is a variant's state members and methods in the order links index them: each sorted (upstream's
// `variantNames`).
func VariantNamesOf(variant *VariantData) VariantNames {
	return VariantNames{State: sortStrings(variant.State.Keys()), Methods: sortStrings(variant.Methods.Keys())}
}

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

// VariantImplementation is one variant's members: a Watchable per state member, a function per method and, in a local
// contract, a KeyedWatchable per keyed member.
type VariantImplementation struct {
	State   map[string]chord.Watchable[delta.JsonValue]
	Methods map[string]MethodFunction
	Keyed   map[string]KeyedWatchable[delta.JsonValue]
}

// Implementation is what a factory returns: for a local contract Members alone (Variant is empty), for a remote one
// one variant with exactly that variant's members.
type Implementation struct {
	Variant string
	Members VariantImplementation
}

// KeyedWatchable is a keyed state member (slice 6a §12): documents by key, each watchable on its own; a keyed document
// handle provides it. At is valid only in a factory (it pins the key while that attempt lives).
type KeyedWatchable[T any] interface {
	// Get is the document at key; found is false when absent.
	Get(ctx context.Context, key string) (value T, found bool, err error)
	// Watch is a watch of the document at key that follows its address; the handle is nil when absent.
	Watch(ctx context.Context, key string) (chord.WatchHandle[T], error)
	// At is, in a factory only, the document at key as a watchable, initial() while absent.
	At(key string) chord.Watchable[T]
}
