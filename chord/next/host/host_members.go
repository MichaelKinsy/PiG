package host

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/host/members.ts
//
// The member check (Q14): an implementation is one variant with exactly that variant's members, methods being
// functions, state members JSON watchables and event members the attempt's own sources or references' event members
// (decision "Events: fire-and-forget members"); and the resolution of what Chord follows behind every derive's declared
// sources (JSON and native) and every contribution to a registry (slice 8).
//
// The rule for watches across attempts (decision "References are the only indirection"): consumers' watches and
// derives over another object's member follow it through their reference's member (`reference-source.ts`) and keys
// (`reference-keys.ts`), passed straight through to the instance's own document or derive. While the object points at
// nothing they keep their last values (one `failed` call for a failure); when a new instance goes live they move by
// name to its member (a key pinned there again), whether a document or a derive: one frame from the last value, and
// every derive downstream recovers (decision "self healing"). A re-exposed member is the re-exposing object's
// reference member: it holds with that object, its provider's source goes on. Holds over a link follow the live
// instance's members and take a snapshot at each binding.

// members is a checked implementation, as Chord calls and follows it.
type members struct {
	// methods is the methods, each called with its arguments (nil for none) and a context.
	methods *contracts.NamedMap[contracts.MethodFunction]
	// state is the state members, resolved to what Chord follows.
	state *contracts.NamedMap[state.DeriveSource]
	// keyed is the keyed members (slice 6a §12): the object's own slot, or the provider's member it re-exposes.
	keyed *contracts.NamedMap[*keyedBinding]
	// events is the event members: the attempt's own sources, or nodes fed by the references they re-expose
	// (reexposedEvents).
	events *contracts.NamedMap[*memberEvent]
	// queries is the `queries` members (slice 14 §3.1): the object's own named queries, by member and query name.
	queries map[string]map[string]*OwnQuery
}

// memberEvent is an event member's node: the attempt's own source (own), or a node fed by the reference it re-exposes
// (reexposed).
type memberEvent struct {
	node      *eventNode
	reexposed *reexposedEvents
}

// noEvents is the event members of a variant without any: shared, never written.
var noEvents = contracts.NewNamedMap[*memberEvent](0)

// noQueries is the queries members of a variant without any: shared, never written.
var noQueries = map[string]map[string]*OwnQuery{}

// checkQueries checks the `queries` members: each is what `self.queries` returned (an object's own named queries: a
// reference's are not re-exposed), with a builder for every query the contract lists, which takes the contract's argument
// schema.
func checkQueries(queries *contracts.NamedMap[*contracts.QueriesData], own func(member string) any) (map[string]map[string]*OwnQuery, error) {
	checked := make(map[string]map[string]*OwnQuery, queries.Len())
	for member, listed := range queries.All() {
		declared, ok := ownQueries(own(member))
		if !ok {
			return nil, errors.New(member + ": a queries member is what self.queries returned")
		}
		for name, schema := range listed.Args.All() {
			query, found := declared[name]
			if !found {
				return nil, errors.New(member + `: no builder for the listed query "` + name + `"`)
			}
			if err := query.Listed(schema); err != nil {
				return nil, err
			}
		}
		for _, name := range ownQueryNames(declared) {
			if !declared[name].Checked() {
				return nil, errors.New(member + `: query "` + name + `" is not listed and brings no argument schema`)
			}
		}
		checked[member] = declared
	}
	return checked, nil
}

// keyedBindingKind tells what a keyed member is.
type keyedBindingKind string

const (
	// keyedOwn is a keyed document handle's slot.
	keyedOwn keyedBindingKind = "own"
	// keyedReexposed is another object's keyed member re-exposed through a reference, which reads end at the ultimate
	// provider's slot (rule 22) and pinned keys follow.
	keyedReexposed keyedBindingKind = "reexposed"
)

// keyedBinding is what a keyed member is: a keyed document handle's slot (kind own, slot), or another object's keyed
// member re-exposed through a reference (kind reexposed, binding, member and key).
type keyedBinding struct {
	kind    keyedBindingKind
	slot    *documents.KeyedSlot
	binding *binding
	member  string
	// key is the host kind the re-exposed member is keyed by; empty for string keys.
	key string
}

// implementationMember is one member of what a factory returned.
type implementationMember struct {
	name  string
	value any
}

// implementation is Pi's `{ readonly [member: string]: unknown }`: the own properties of what a factory returned, in the
// order `Object.keys` lists them (a name appears once). Plugin code: nothing about a value is assumed.
type implementation []implementationMember

// own is the own property member, nil when it has none: an inherited `toString` is no implementation of a member named
// so.
func (i implementation) own(member string) any {
	for _, each := range i {
		if each.name == member {
			return each.value
		}
	}
	return nil
}

// grantedImplementation is what a remote factory's return exposes to a caller granted `granted` (slice 15 §1.3):
// its plain members as returned, its granted grantable methods, and its granted grantable state and event members built
// (their builders called now, while the factory's declarations are open); nothing of a member not granted. Plugin code:
// a grantable member that is no function fails the attempt.
func grantedImplementation(
	contract *contracts.Contract,
	impl implementation,
	granted map[string]bool,
) (implementation, error) {
	version, err := contracts.CurrentVersion(contract)
	if err != nil {
		return nil, err
	}
	if len(version.Grantable) == 0 {
		return impl, nil
	}
	exposed := make(implementation, 0, len(impl))
	for _, each := range impl {
		if !version.Grantable[each.name] {
			exposed = append(exposed, each)
			continue
		}
		switch fn := each.value.(type) {
		case contracts.MethodFunction:
			if fn == nil {
				return nil, errors.New(each.name + ": a grantable member is a function (a builder of its state or events)")
			}
			if granted[each.name] {
				exposed = append(exposed, each)
			}
		case contracts.StateBuilder:
			if fn == nil {
				return nil, errors.New(each.name + ": a grantable member is a function (a builder of its state or events)")
			}
			if granted[each.name] {
				if version.Members.Methods.Has(each.name) {
					exposed = append(exposed, each)
				} else {
					built, err := tryPlugin(func() (any, error) { return fn(), nil })
					if err != nil {
						return nil, err
					}
					exposed = append(exposed, implementationMember{name: each.name, value: unwrapMember(built)})
				}
			}
		case contracts.EventBuilder:
			if fn == nil {
				return nil, errors.New(each.name + ": a grantable member is a function (a builder of its state or events)")
			}
			if granted[each.name] {
				if version.Members.Methods.Has(each.name) {
					exposed = append(exposed, each)
				} else {
					built, err := tryPlugin(func() (any, error) { return fn(), nil })
					if err != nil {
						return nil, err
					}
					exposed = append(exposed, implementationMember{name: each.name, value: unwrapMember(built)})
				}
			}
		default:
			return nil, errors.New(each.name + ": a grantable member is a function (a builder of its state or events)")
		}
	}
	return exposed, nil
}

// checkImplementation checks what a factory returned (plugin code; a remote one's as grantedImplementation exposed it):
// exactly the contract's members, a remote one's plain members and the granted ones. sources owns the attempt's own
// event sources (none declared: nil). Errors name the member (the failure cause adds the object).
func checkImplementation(
	contract *contracts.Contract,
	impl implementation,
	sources *eventSourceOwner,
	granted map[string]bool,
) (*members, error) {
	version, err := contracts.CurrentVersion(contract)
	if err != nil {
		return nil, err
	}
	return checkMembers(contracts.GrantedMembers(version, granted), impl, sources)
}

// lacking is the error of a use of member (what: "method" or "state member") that the object label lacks: `forbidden`
// for a member grantable at version (the caller's; nil means the current version) it was not granted (slice 15 §1.3:
// the member exists), else `invalid/member`.
func lacking(label string, contract *contracts.Contract, member string, what string, version *contracts.VersionData) *data.ChordError {
	if version == nil {
		var err error
		version, err = contracts.CurrentVersion(contract)
		if err != nil {
			return data.Invalid(data.ReasonMember, fmt.Sprintf("%s has no %s %q", label, what, member))
		}
	}
	if version.Grantable[member] {
		return data.Forbidden(fmt.Sprintf("%s has not granted %q", label, member))
	}
	return data.Invalid(data.ReasonMember, fmt.Sprintf("%s has no %s %q", label, what, member))
}

// jsStringify is JSON.stringify of a string.
func jsStringify(text string) string {
	out, err := jsstring.MarshalJSON(text)
	if err != nil {
		return text
	}
	return string(out)
}

// checkMembers checks members against a variant's (exactly its members, methods being functions, state members JSON
// watchables, keyed members keyed document handles and event members the attempt's own sources or references' event
// members); a method without arguments is called with the context alone.
func checkMembers(
	variant *contracts.MembersData,
	given implementation,
	sources *eventSourceOwner,
) (*members, error) {
	for _, each := range given {
		if !variant.State.Has(each.name) && !variant.Methods.Has(each.name) && !variant.Keyed.Has(each.name) && !variant.Events.Has(each.name) && !variant.Queries.Has(each.name) {
			return nil, errors.New(each.name + ": not a member")
		}
	}
	checked := &members{
		methods: contracts.NewNamedMap[contracts.MethodFunction](variant.Methods.Len()),
		state:   contracts.NewNamedMap[state.DeriveSource](variant.State.Len()),
		keyed:   contracts.NewNamedMap[*keyedBinding](variant.Keyed.Len()),
		events:  noEvents,
		queries: noQueries,
	}
	for member := range variant.Methods.All() {
		method, ok := asMethod(given.own(member))
		if !ok {
			return nil, errors.New(member + ": a method is a function")
		}
		// A Go method always takes its arguments (nil for none), so a method without arguments needs no wrapper.
		checked.methods.Set(member, method)
	}
	for _, member := range variant.State.Keys() {
		source, err := resolveWatchable(given.own(member))
		if err != nil {
			return nil, err
		}
		if source == nil {
			return nil, errors.New(member + ": a state member is a JSON watchable")
		}
		checked.state.Set(member, source)
	}
	for member, declared := range variant.Keyed.All() {
		resolved, err := resolveKeyed(given.own(member))
		if err != nil {
			return nil, err
		}
		if resolved == nil {
			return nil, errors.New(member + ": a keyed member is a keyed document handle")
		}
		// Declaration policy: the handle's keys are the member's (strings, or addresses of its host kind).
		keys := resolved.key
		if resolved.kind == keyedOwn {
			keys = ""
			if host := resolved.slot.Definition().Current().Host; host != nil {
				keys = host.Kind
			}
		}
		declaredKey := ""
		if declared.Key != nil {
			declaredKey = declared.Key.Kind
		}
		if keys != declaredKey {
			what := func(kind string) string {
				if kind == "" {
					return "string keys"
				}
				return "addresses of " + kind
			}
			return nil, fmt.Errorf("%s: declared with %s, implemented with %s", member, what(declaredKey), what(keys))
		}
		checked.keyed.Set(member, resolved)
	}
	if variant.Events.Len() > 0 {
		events, err := checkEvents(variant.Events, given.own, sources)
		if err != nil {
			return nil, err
		}
		checked.events = events
	}
	if variant.Queries.Len() > 0 {
		queries, err := checkQueries(variant.Queries, given.own)
		if err != nil {
			return nil, err
		}
		checked.queries = queries
	}
	return checked, nil
}

// checkEvents checks event members: each is the attempt's own source (owned by sources) or a reference's event member,
// re-exposed (the declaration boundary, Q14: only Chord-made event members).
func checkEvents(events *contracts.NamedMap[*contracts.Snapshot], own func(member string) any, sources *eventSourceOwner) (*contracts.NamedMap[*memberEvent], error) {
	nodes := contracts.NewNamedMap[*memberEvent](events.Len())
	for _, member := range events.Keys() {
		switch value := own(member).(type) {
		case *ownEventSource:
			if sources != nil && value.ownedBy(sources) {
				nodes.Set(member, &memberEvent{node: &value.eventNode})
				continue
			}
		case *ReferenceEvents:
			reexposed := newReexposedEvents(value)
			nodes.Set(member, &memberEvent{node: &reexposed.eventNode, reexposed: reexposed})
			continue
		}
		return nil, errors.New(member + ": an event member is this object's own self.events() or a reference's event member")
	}
	return nodes, nil
}

// asMethod is value as a method: a function (a nil one is none).
func asMethod(value any) (contracts.MethodFunction, bool) {
	switch method := value.(type) {
	case contracts.MethodFunction:
		return method, method != nil
	case func(delta.JsonValue, context.Context) (delta.JsonValue, error):
		return method, method != nil
	}
	return nil, false
}

// attachDerives resolves every derive's sources; errors name a derive by the member exposing it, else by its position.
// report makes each derive's failure report, with the member exposing it when there is one (`derive_failed`).
func attachDerives(
	derives []*state.Derive,
	exposedState *contracts.NamedMap[state.DeriveSource],
	report func(member string, exposed bool) func(error),
) error {
	// A source exposed by several members is named by the last (Pi's Map.set overwrites).
	exposed := make(map[state.DeriveSource]string, exposedState.Len())
	for member, source := range exposedState.All() {
		exposed[source] = member
	}
	for position, derive := range derives {
		member, isExposed := exposed[derive]
		name := fmt.Sprintf("derive #%d", position+1)
		if isExposed {
			name = member
		}
		resolve := func(source any, index int) (state.DeriveInput, error) {
			resolved, err := resolveWatchable(source)
			if err != nil {
				return nil, err
			}
			if resolved != nil {
				return resolved, nil
			}
			if native, ok := source.(state.NativeSourceHandle); ok {
				return native, nil
			}
			return nil, fmt.Errorf("%s: source %d is not a Chord watchable", name, index)
		}
		if err := derive.Attach(resolve, report(member, isExposed)); err != nil {
			return err
		}
	}
	return nil
}

// contributionOf is a contribution of contributor at version to record, as Chord follows it: a Chord watchable (a
// source, a constant, a document handle, a reference member, a native source) is followed, anything else is a plain
// value, whatever methods it has (registries carry natives).
func contributionOf(
	record *registries.RegistryRecord,
	contributor registries.Contributor,
	version int,
	value any,
) (*registries.RegistryContribution, error) {
	content, err := contentOf(value)
	if err != nil {
		return nil, err
	}
	return registries.NewRegistryContribution(record, contributor, version, content), nil
}

// contentOf is what Chord follows behind a contribution: its JSON or native source when it is a Chord watchable.
func contentOf(value any) (registries.ContributionContent, error) {
	if native, ok := value.(state.NativeSourceHandle); ok {
		return registries.ContributionContent{Kind: registries.ContributionNative, Native: native}, nil
	}
	source, err := resolveWatchable(value)
	if err != nil {
		return registries.ContributionContent{}, err
	}
	if source == nil {
		return registries.ContributionContent{Kind: registries.ContributionPlain, Value: value}, nil
	}
	return registries.ContributionContent{Kind: registries.ContributionJSON, Source: source}, nil
}
