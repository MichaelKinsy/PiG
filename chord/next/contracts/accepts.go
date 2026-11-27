package contracts

import (
	"context"
	"strconv"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/contracts/accepts.ts (accepted versions: the older versions a provider still serves,
// with the direct mappings their edits require).

// Mapping maps one JSON value to another: a changed state member (current to old), changed arguments (old to current)
// or a changed result (current to old). A removed state member's mapping gets the whole current state (an object of
// its members). A thrown Error is a returned error.
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

// KeyedMapping is a keyed member's mapping (slice 7b): a function mapping each document of the current version back
// (a changed member), or "refuse" (a changed or a removed member: no document maps from the whole state).
type KeyedMapping struct {
	Func   Mapping
	Refuse bool
}

// EventMapping is an event member's mapping (decision "Events: fire-and-forget members"): a function mapping each current
// value back to the older version's, or "refuse" (the version's binding is refused). A removed or an added event needs
// none (an older consumer receives none of it).
type EventMapping struct {
	Func   Mapping
	Refuse bool
}

// VersionMappings is the mappings of one accepted older version.
type VersionMappings struct {
	// State maps state members by member.
	State []Entry[StateMapping]
	// Methods maps methods by member.
	Methods []Entry[MethodMapping]
	// Keyed maps a local contract's keyed members by member.
	Keyed []Entry[KeyedMapping]
	// Events maps event members by member.
	Events []Entry[EventMapping]
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
		if index < 0 {
			return AcceptsChecked{}, fail("%s: not a version of this contract's history", path)
		}
		if index == len(data.Versions)-1 {
			return AcceptsChecked{}, fail("%s: current version needs no entry in accepts", path)
		}
		if err := checkMappings(entry.Value, needs(data, index), path); err != nil {
			return AcceptsChecked{}, err
		}
		versions = append(versions, AcceptedVersion{
			Version:     data.Versions[index].Version,
			Fingerprint: data.Versions[index].Fingerprint,
		})
	}
	return AcceptsChecked{Versions: versions}, nil
}

type stateNeed string

const (
	stateChanged stateNeed = "changed"
	stateRemoved stateNeed = "removed"
)

type keyedNeed string

const (
	keyedChanged keyedNeed = "changed"
	keyedRemoved keyedNeed = "removed"
	keyedRekeyed keyedNeed = "rekeyed"
)

type methodNeed struct {
	removed bool
	args    bool
	result  bool
}

type versionNeeds struct {
	state   *NamedMap[stateNeed]
	methods *NamedMap[methodNeed]
	keyed   *NamedMap[keyedNeed]
	events  *NamedMap[bool]
}

func needs(data *ContractData, index int) versionNeeds {
	old := data.Versions[index].Members
	now := data.Versions[len(data.Versions)-1].Members
	changes := map[Change]bool{}
	for _, v := range data.Versions[index+1:] {
		for _, c := range v.Changes {
			changes[c] = true
		}
	}
	changed := func(member string, side ChangeSide) bool {
		return changes[Change{Member: member, Side: side}]
	}

	state := NewNamedMap[stateNeed](0)
	methods := NewNamedMap[methodNeed](0)
	keyed := NewNamedMap[keyedNeed](0)
	events := NewNamedMap[bool](0)

	for member := range old.Events.All() {
		if now.Events.Has(member) && changed(member, SideEvent) {
			events.Set(member, true)
		}
	}
	for member, oldKeyed := range old.Keyed.All() {
		current, ok := now.Keyed.Get(member)
		if !ok {
			keyed.Set(member, keyedRemoved)
		} else {
			oldKind := ""
			if oldKeyed.Key != nil {
				oldKind = oldKeyed.Key.Kind
			}
			curKind := ""
			if current.Key != nil {
				curKind = current.Key.Kind
			}
			if curKind != oldKind {
				keyed.Set(member, keyedRekeyed)
			} else if changed(member, SideKeyed) {
				keyed.Set(member, keyedChanged)
			}
		}
	}
	for member := range old.State.All() {
		if !now.State.Has(member) {
			state.Set(member, stateRemoved)
		} else if changed(member, SideState) {
			state.Set(member, stateChanged)
		}
	}
	for member := range old.Methods.All() {
		args := changed(member, SideArgs)
		result := changed(member, SideResult)
		if !now.Methods.Has(member) {
			methods.Set(member, methodNeed{removed: true})
		} else if args || result {
			methods.Set(member, methodNeed{args: args, result: result})
		}
	}
	return versionNeeds{state: state, methods: methods, keyed: keyed, events: events}
}

func checkMappings(mappings VersionMappings, need versionNeeds, path string) error {
	if err := checkSection(mappings.State, need.state, path+".state", checkStateMapping); err != nil {
		return err
	}
	if err := checkSection(mappings.Events, need.events, path+".events", checkEventMapping); err != nil {
		return err
	}
	if err := checkSection(mappings.Methods, need.methods, path+".methods", checkMethodMapping); err != nil {
		return err
	}
	return checkSection(mappings.Keyed, need.keyed, path+".keyed", checkKeyedMapping)
}

func checkSection[M, N any](section []Entry[M], need *NamedMap[N], path string, check func(M, N, string) error) error {
	ordered := orderedEntries(section)
	byName := NewNamedMap[M](len(ordered))
	for _, entry := range ordered {
		byName.Set(entry.Name, entry.Value)
	}
	for key := range byName.All() {
		if !need.Has(key) {
			return fail("%s.%s: unnecessary mapping", path, key)
		}
	}
	for key, n := range need.All() {
		mapping, ok := byName.Get(key)
		if !ok {
			return fail("%s.%s: missing mapping", path, key)
		}
		if err := check(mapping, n, path+"."+key); err != nil {
			return err
		}
	}
	return nil
}

func checkStateMapping(mapping StateMapping, need stateNeed, path string) error {
	if mapping.Func != nil && !mapping.Refuse {
		return nil
	}
	if need == stateRemoved && mapping.Refuse && mapping.Func == nil {
		return nil
	}
	if need == stateRemoved {
		return fail(`%s: expected a function or "refuse"`, path)
	}
	return fail("%s: expected a function", path)
}

func checkEventMapping(mapping EventMapping, _ bool, path string) error {
	if (mapping.Func != nil) != mapping.Refuse {
		return nil
	}
	return fail(`%s: expected a function or "refuse"`, path)
}

func checkKeyedMapping(mapping KeyedMapping, need keyedNeed, path string) error {
	if mapping.Refuse && mapping.Func == nil {
		return nil
	}
	if need == keyedChanged && mapping.Func != nil && !mapping.Refuse {
		return nil
	}
	because := ""
	if need == keyedRekeyed {
		because = " (its key kind changed: a value mapping cannot map keys)"
	}
	if need == keyedChanged {
		return fail(`%s: expected a function or "refuse"%s`, path, because)
	}
	return fail(`%s: expected "refuse"%s`, path, because)
}

func checkMethodMapping(mapping MethodMapping, need methodNeed, path string) error {
	if mapping.Refuse {
		if mapping.Args != nil || mapping.Result != nil || mapping.Call != nil {
			return fail(`%s: expected { call } or "refuse"`, path)
		}
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
