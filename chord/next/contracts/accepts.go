package contracts

import (
	"context"
	"strconv"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/next/contracts/accepts.ts (accepted versions: the older versions a provider still serves,
// with the direct mappings their edits require).
//
// Forced Go differences. AcceptsTable<C> and the mapping types built from Static<> are type-level only (designed
// out); a Go table is data whose functions take and return JSON values. Upstream's union `function | "refuse"` is a
// struct with a Refuse flag; a table is an ordered slice of Entry values keyed by the version number as text; a
// remote table maps state per variant (VersionMappings.VariantState) and a local one per member
// (VersionMappings.State), the two shapes of upstream's `state` section. checkKeys, and inputs a typed table cannot
// express (a mapping that is no function, an unknown key), are designed out.

// Mapping maps one JSON value to another: a changed state member (current to old), changed arguments (old to current)
// or a changed result (current to old). A removed state member's mapping gets the variant's whole current state (an
// object of its members). A thrown Error is a returned error.
type Mapping func(value delta.JsonValue) (delta.JsonValue, error)

// CallFunc is a removed or changed method served by the provider's own function, with the old arguments.
type CallFunc func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error)

// StateMapping is a state mapping: a function, or "refuse" (only for a removed member).
type StateMapping struct {
	Func   Mapping
	Refuse bool
}

// MethodMapping is a method mapping: "refuse", `{ call }`, or the mappings of the changed sides.
type MethodMapping struct {
	Refuse bool
	Args   Mapping
	Result Mapping
	Call   CallFunc
}

// VersionMappings is the mappings of one accepted older version.
type VersionMappings struct {
	// State maps a local contract's state members by member.
	State []Entry[StateMapping]
	// VariantState maps a remote contract's state members by variant, then member.
	VariantState []Entry[[]Entry[StateMapping]]
	// Methods maps methods by member.
	Methods []Entry[MethodMapping]
}

// AcceptsTable holds, per accepted older version (by its number as text), exactly the mappings its later edits require
// (direct: old to current).
type AcceptsTable []Entry[VersionMappings]

// AcceptedVersion is an accepted older version and its fingerprint.
type AcceptedVersion struct {
	Version     int
	Fingerprint string
}

// AcceptsChecked is the accepted versions of a checked table.
type AcceptsChecked struct {
	Versions []AcceptedVersion
}

// CheckAccepts validates an accepts table without calling any mapping: known older versions only, exactly the required
// mappings with the right shapes. Fails with an error naming the path (`notes accepts[1].methods.add`).
func CheckAccepts(contract *Contract, accepts AcceptsTable) (AcceptsChecked, error) {
	data, err := ContractDataOf(contract)
	if err != nil {
		return AcceptsChecked{}, err
	}
	entries := orderedEntries(accepts)
	versions := make([]AcceptedVersion, 0, len(entries))
	for _, entry := range entries {
		path := data.Name + " accepts[" + entry.Name + "]"
		index := -1
		for i, version := range data.Versions {
			if strconv.Itoa(version.Version) == entry.Name {
				index = i
				break
			}
		}
		if index == -1 || index == len(data.Versions)-1 {
			return AcceptsChecked{}, fail("%s: not an older version", path)
		}
		if err := checkMappings(entry.Value, needsOf(data, index), data.Kind, path); err != nil {
			return AcceptsChecked{}, err
		}
		version := data.Versions[index]
		versions = append(versions, AcceptedVersion{Version: version.Version, Fingerprint: version.Fingerprint})
	}
	return AcceptsChecked{Versions: versions}, nil
}

type stateNeed int

const (
	stateChanged stateNeed = iota + 1
	stateRemoved
)

type methodNeed struct {
	removed, args, result bool
}

// needs is what an accepted version needs, by edit and never by schema comparison.
type needs struct {
	// state is per variant, per state member.
	state   *NamedMap[*NamedMap[stateNeed]]
	methods *NamedMap[methodNeed]
}

func needsOf(data *ContractData, index int) needs {
	old := data.Versions[index]
	current := data.Versions[len(data.Versions)-1]
	changes := map[Change]bool{}
	for _, version := range data.Versions[index+1:] {
		for _, change := range version.Changes {
			changes[change] = true
		}
	}
	state := NewNamedMap[*NamedMap[stateNeed]](0)
	collected := NewNamedMap[methodNeed](0)
	for variant, oldVariant := range old.Variants.All() {
		now := current.Variants.Value(variant)
		variantNeeds := NewNamedMap[stateNeed](0)
		for member := range oldVariant.State.All() {
			if !now.State.Has(member) {
				variantNeeds.Set(member, stateRemoved)
			} else if changes[Change{variant, member, SideState}] {
				variantNeeds.Set(member, stateChanged)
			}
		}
		if variantNeeds.Len() > 0 {
			state.Set(variant, variantNeeds)
		}
		for member := range oldVariant.Methods.All() {
			need, _ := collected.Get(member)
			need.removed = need.removed || !now.Methods.Has(member)
			need.args = need.args || changes[Change{variant, member, SideArgs}]
			need.result = need.result || changes[Change{variant, member, SideResult}]
			collected.Set(member, need)
		}
	}
	methods := NewNamedMap[methodNeed](collected.Len())
	for member, need := range collected.All() {
		if need.removed || need.args || need.result {
			methods.Set(member, need)
		}
	}
	return needs{state: state, methods: methods}
}

func checkMappings(mappings VersionMappings, needs needs, kind Kind, path string) error {
	checkVariant := func(variantMappings []Entry[StateMapping], variantNeeds *NamedMap[stateNeed], at string) error {
		return checkSection(variantMappings, variantNeeds, at, checkStateMapping)
	}
	if kind == KindLocal {
		if mappings.VariantState != nil {
			return fail("%s.variantState: a local table has no variants (State)", path)
		}
		// A local table is flat: its state section is the one variant's.
		if err := checkVariant(mappings.State, needs.state.Value(LocalVariant), path+".state"); err != nil {
			return err
		}
	} else {
		if mappings.State != nil {
			return fail("%s.state: a remote table maps state per variant (VariantState)", path)
		}
		if err := checkSection(mappings.VariantState, needs.state, path+".state", checkVariant); err != nil {
			return err
		}
	}
	return checkSection(mappings.Methods, needs.methods, path+".methods", checkMethodMapping)
}

// checkSection checks exactly the needed keys: no missing and no unnecessary mapping; the section may be absent only
// when nothing is needed.
func checkSection[M, N any](section []Entry[M], needs *NamedMap[N], path string, check func(mapping M, need N, path string) error) error {
	mappings := NewNamedMap[M](len(section))
	for _, entry := range orderedEntries(section) {
		mappings.Set(entry.Name, entry.Value)
	}
	for key := range mappings.All() {
		if !needs.Has(key) {
			return fail("%s.%s: unnecessary mapping", path, key)
		}
	}
	for key, need := range needs.All() {
		mapping, ok := mappings.Get(key)
		if !ok {
			return fail("%s.%s: missing mapping", path, key)
		}
		if err := check(mapping, need, path+"."+key); err != nil {
			return err
		}
	}
	return nil
}

func checkStateMapping(mapping StateMapping, need stateNeed, path string) error {
	if mapping.Func != nil || need == stateRemoved && mapping.Refuse {
		return nil
	}
	if need == stateRemoved {
		return fail(`%s: expected a function or "refuse"`, path)
	}
	return fail("%s: expected a function", path)
}

func checkMethodMapping(mapping MethodMapping, need methodNeed, path string) error {
	if mapping.Refuse {
		return nil
	}
	if mapping.Call != nil || need.removed {
		if mapping.Call == nil || mapping.Args != nil || mapping.Result != nil {
			return fail(`%s: expected { call } or "refuse"`, path)
		}
		return nil
	}
	if err := checkSide(mapping.Args, need.args, path+".args"); err != nil {
		return err
	}
	return checkSide(mapping.Result, need.result, path+".result")
}

func checkSide(mapping Mapping, needed bool, path string) error {
	if !needed && mapping != nil {
		return fail("%s: unnecessary mapping", path)
	}
	if needed && mapping == nil {
		return fail("%s: missing mapping", path)
	}
	return nil
}
