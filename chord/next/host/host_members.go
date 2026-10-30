package host

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/host/members.ts
//
// The member check (Q14): an implementation is one variant with exactly that variant's members, methods being
// functions and state members JSON watchables; and the resolution of what Chord follows behind every derive's declared
// sources (JSON and native) and every contribution to a registry (slice 8).
//
// The rule for watches across attempts (slice 6a §1.2, §1.4): references and other objects' derives follow a local
// object's state member through its member source (`member-source.ts`), and a key of a keyed member they hold (`at`,
// `watch(key)`) through a member source per key (`keyed-members.ts`); both live as long as the object. While the
// object is failed or activating again they keep their last values and get no frames; when a new attempt goes live
// they re-bind by name to that attempt's member (a pinned key loaded on it first), whether a document or a derive: one
// frame from the last value, and every derive downstream recovers, one that failed on the old member's error included
// (decision "self healing"). A re-exposed member is the re-exposing object's: it freezes with that object, its
// provider's source goes on. At a live failure, members following another object's sources freeze at once; the
// object's own documents and derives freeze once its admitted commits adopted, so until then an own derive over
// another object's member may still change. Holds over a link follow the live attempt's members and take a snapshot at
// each binding.

// members is a checked implementation, as Chord calls and follows it.
type members struct {
	// methods is the methods, each called with its arguments (nil for none) and a context.
	methods *contracts.NamedMap[contracts.MethodFunction]
	// state is the state members, resolved to what Chord follows.
	state *contracts.NamedMap[state.DeriveSource]
	// keyed is the keyed members (slice 6a §12): the object's own slot, or the provider's member it re-exposes.
	keyed *contracts.NamedMap[*keyedBinding]
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
// member re-exposed through a reference (kind reexposed, binding and member).
type keyedBinding struct {
	kind    keyedBindingKind
	slot    *documents.KeyedSlot
	binding *binding
	member  string
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

// factoryResult is what a factory returned: the members of a local contract's variant (variant nil), or a remote
// contract's `{ variant, members }` (variant nil when it named none: Pi's undefined).
type factoryResult struct {
	variant *string
	members implementation
}

// checkImplementation checks what a factory returned (plugin code): a local contract's members, or a remote contract's
// `{ variant, members }` naming one of its current variants. Errors name the variant or member (the failure cause adds
// the object). The returned variant is nil for a local contract.
func checkImplementation(
	contract *contracts.Contract,
	result factoryResult,
) (variant *string, checked *members, err error) {
	variants, err := contracts.CurrentVariants(contract)
	if err != nil {
		return nil, nil, err
	}
	if contract.Kind == contracts.KindLocal {
		// The member check establishes the shape.
		checked, err = checkMembers(variants.Value(contracts.LocalVariant), result.members)
		return nil, checked, err
	}
	var named *contracts.VariantData
	found := false
	if result.variant != nil {
		named, found = variants.Get(*result.variant)
	}
	if !found {
		name := "undefined"
		if result.variant != nil {
			name = jsStringify(*result.variant)
		}
		return nil, nil, fmt.Errorf("variant %s: not a variant of %s", name, contract.Name)
	}
	checked, err = checkMembers(named, result.members)
	return result.variant, checked, err
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
// watchables and keyed members keyed document handles); a method without arguments is called with the context alone.
func checkMembers(
	variant *contracts.VariantData,
	given implementation,
) (*members, error) {
	for _, each := range given {
		if !variant.State.Has(each.name) && !variant.Methods.Has(each.name) && !variant.Keyed.Has(each.name) {
			return nil, errors.New(each.name + ": not a member")
		}
	}
	checked := &members{
		methods: contracts.NewNamedMap[contracts.MethodFunction](variant.Methods.Len()),
		state:   contracts.NewNamedMap[state.DeriveSource](variant.State.Len()),
		keyed:   contracts.NewNamedMap[*keyedBinding](variant.Keyed.Len()),
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
	for _, member := range variant.Keyed.Keys() {
		resolved, err := resolveKeyed(given.own(member))
		if err != nil {
			return nil, err
		}
		if resolved == nil {
			return nil, errors.New(member + ": a keyed member is a keyed document handle")
		}
		checked.keyed.Set(member, resolved)
	}
	return checked, nil
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
// value, whatever methods it has (registries carry natives). Refused (`invalid/cycle`) when it is a registry's value
// that closes a cycle of registries, through the tables and the contributor's `pending` edges.
func contributionOf(
	record *registries.RegistryRecord,
	contributor registries.Contributor,
	version int,
	value any,
	pending []registries.RegistryEdge,
) (*registries.RegistryContribution, error) {
	if source, ok := value.(*registries.RegistryValueSource); ok {
		if err := record.AssertNoCycle(source, pending); err != nil {
			return nil, err
		}
	}
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
