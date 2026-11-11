package host

import (
	"sort"
	"strconv"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/host/adapted.ts. Adapted members (Q10, §22, slice 7b §7; decision "References are the only
// indirection" (5)): one object instance's shared mapping for the consumers of one older version its provider accepts. Per
// state member one derive of the instance's member through `(current) => old` (a changed member), of its variant's whole
// state through `(state) => old` (a removed member), or the member itself (unchanged), made on first use; per key of a keyed
// member one derive of the key's document through the member's mapping, shared by the references holding it. One map and
// one diff per commit per version, whatever the number of old references. Dropped when no reference of the version is left,
// gone with the instance; references move to a new instance's mappings as to any new instance. A version the provider does
// not accept, or a refused member, gives no member (`contract_mismatch`): its consumers wait unbound.
//
// Forced Go differences. The accepts table is data of `contracts.AcceptsTable` (the union `function | "refuse"` is
// `contracts.StateMapping`/`KeyedMapping` with a Refuse flag, an absent mapping a nil pointer); a thrown error is a returned
// one; the Maps and Sets Pi iterates keep their insertion order (a derive list, and a sequence number the keys sort by when
// the mappings detach).

// adaptedInstance is what the mappings of one instance read: its provider, its variant, its own members, its host's batch and
// reports.
type adaptedInstance struct {
	served *served
	// variant is the instance's variant, nil for a local object (Pi's undefined).
	variant *string
	label   string
	batch   *state.Batch
	// member is the instance's state member `member` (`invalid/member` when its variant has none).
	member func(member string) (state.DeriveSource, error)
	// report reports a mapping derive's failure (`derive_failed`).
	report func(member string, err error)
}

// heldKeys are one keyed member's held keys by the instance's key document; compared by identity (the member's entry).
type heldKeys struct {
	byDocument map[state.DeriveSource]*mappedKey
}

// mappedKey is a key's mapping derive, shared by the references holding the key at this version.
type mappedKey struct {
	derive  *state.Derive
	holders int
	// order is when it was made: the order its derive detaches in.
	order int
}

// refusal is why consumers of the older version cannot bind to served's variant: a version it does not accept, or a member of
// it the provider refuses (`contract_mismatch`); nil when they can.
func refusal(entry *served, version int, variant *string) *data.ChordError {
	contract, provider := entry.contract, entry.provider
	accepted := false
	for _, each := range provider.Accepted {
		if each.Version == version {
			accepted = true
			break
		}
	}
	if !accepted {
		return notServedHere(contract, version)
	}
	mappings := mappingsOf(entry, version)
	variants, err := contracts.VariantsAt(contract, version)
	if err != nil {
		return data.AsChordError(err, contract.Name)
	}
	name := contracts.LocalVariant
	if variant != nil {
		name = *variant
	}
	old, found := variants.Get(name)
	if !found {
		return nil
	}
	for _, member := range old.State.Keys() {
		if mapping := stateMapping(entry, mappings, variant, member); mapping != nil && mapping.Refuse {
			return refused(contract, version, member)
		}
	}
	for _, member := range old.Keyed.Keys() {
		if mapping := keyedMappingOf(mappings, member); mapping != nil && mapping.Refuse {
			return refused(contract, version, member)
		}
	}
	return nil
}

// adaptedMembers are one instance's members as the consumers of one older version read them.
type adaptedMembers struct {
	version  int
	instance adaptedInstance
	// members are the members read so far: a mapping derive, or the instance's member itself (unchanged).
	members map[string]state.DeriveSource
	// derives are the mapping derives it made, in order (an unchanged member is the instance's own: it never detaches that
	// one).
	derives []*state.Derive
	// keys are the keys held so far, by member, then by the instance's key document: two keyed members over one handle map
	// the same document differently (Astra).
	keys  map[string]*heldKeys
	spawn int
}

func newAdaptedMembers(instance adaptedInstance, version int) *adaptedMembers {
	return &adaptedMembers{version: version, instance: instance, members: map[string]state.DeriveSource{}}
}

// member is state member `member` as this version reads it, made by the first use; `contract_mismatch` when the provider
// refuses it or does not accept this version.
func (m *adaptedMembers) member(member string) (state.DeriveSource, error) {
	if made, found := m.members[member]; found {
		return made, nil
	}
	source, err := m.make(member)
	if err != nil {
		return nil, err
	}
	m.members[member] = source
	return source, nil
}

// key is the document `document` at a key of keyed member `member` as this version reads it: the document itself when
// unchanged, else a derive of it through the mapping, shared by its holders; release (once per call) lets the last holder's
// go. `contract_mismatch` when the provider refuses the member.
func (m *adaptedMembers) key(member string, document state.DeriveSource) (pinnedKey, error) {
	mapping, err := m.keyed(member)
	if err != nil {
		return pinnedKey{}, err
	}
	if mapping == nil {
		return pinnedKey{source: document, release: func() {}}, nil
	}
	if m.keys == nil {
		m.keys = map[string]*heldKeys{}
	}
	keys := m.keys
	byMember := keys[member]
	if byMember == nil {
		byMember = &heldKeys{byDocument: map[state.DeriveSource]*mappedKey{}}
		keys[member] = byMember
	}
	byDocument := byMember.byDocument
	held := byDocument[document]
	if held == nil {
		derive := state.NewDerive([]any{document}, func(values []any) (delta.JsonValue, error) { return mapping(values[0]) }, m.instance.batch)
		// A failure of the mapping is the reader's (its watch gets it); nothing to report for the provider. The sources are
		// the document itself.
		_ = derive.Attach(func(source any, _ int) (state.DeriveInput, error) { return source.(state.DeriveInput), nil }, func(error) {})
		m.spawn++
		held = &mappedKey{derive: derive, order: m.spawn}
		byDocument[document] = held
	}
	held.holders++
	released := false
	return pinnedKey{
		source: held.derive,
		release: func() {
			if released {
				return
			}
			released = true
			held.holders--
			if held.holders > 0 || byDocument[document] != held {
				return
			}
			delete(byDocument, document)
			if len(byDocument) == 0 && keys[member] == byMember {
				delete(keys, member)
			}
			held.derive.Detach(adaptedEnded(m.instance.label))
		},
	}, nil
}

// keyed is the provider's mapping of keyed member `member`'s documents to this version's shape (nil: unchanged), counted;
// `contract_mismatch` when the provider refuses it.
func (m *adaptedMembers) keyed(member string) (contracts.Mapping, error) {
	if err := m.assertAccepted(); err != nil {
		return nil, err
	}
	mapping := keyedMappingOf(m.mappings(), member)
	if mapping == nil {
		return nil, nil
	}
	if mapping.Refuse {
		return nil, refused(m.contract(), m.version, member)
	}
	batch := m.instance.batch
	call := mapping.Func
	return func(current delta.JsonValue) (delta.JsonValue, error) {
		batch.Count(state.CounterContractAdapts)
		// The provider's mapping, typed by its contract; its result is the old shape (not validated).
		return call(current)
	}, nil
}

// detach: its instance ended, or the version's last reference left: its derives go (their watches end with err).
func (m *adaptedMembers) detach(err error) {
	for _, derive := range m.derives {
		derive.Detach(err)
	}
	m.derives = nil
	clear(m.members)
	var keys []*mappedKey
	for _, byMember := range m.keys {
		for _, held := range byMember.byDocument {
			keys = append(keys, held)
		}
	}
	sort.Slice(keys, func(a, b int) bool { return keys[a].order < keys[b].order })
	for _, held := range keys {
		held.derive.Detach(err)
	}
	m.keys = nil
}

// make is what `member` is at this version: the instance's member itself (unchanged), or a derive through the mapping, of the
// member when the current variant has it, else of the variant's whole current state (a removed member).
func (m *adaptedMembers) make(member string) (state.DeriveSource, error) {
	if err := m.assertAccepted(); err != nil {
		return nil, err
	}
	instance := m.instance
	variant, batch := instance.variant, instance.batch
	mapping := stateMapping(instance.served, m.mappings(), variant, member)
	if mapping == nil {
		return instance.member(member)
	}
	if mapping.Refuse {
		return nil, refused(m.contract(), m.version, member)
	}
	call := mapping.Func
	mapValue := func(value any) (delta.JsonValue, error) {
		batch.Count(state.CounterContractAdapts)
		// The provider's mapping, typed by its contract; its result is the old member's shape (not validated).
		return call(value)
	}
	variants, err := contracts.CurrentVariants(m.contract())
	if err != nil {
		return nil, data.AsChordError(err, instance.label)
	}
	name := contracts.LocalVariant
	if variant != nil {
		name = *variant
	}
	current := variants.Value(name)
	// The member when the current variant has it, else the variant's whole current state (a removed member).
	kept := current.State.Has(member)
	names := []string{member}
	if !kept {
		names = current.State.Keys()
	}
	sources := make([]any, len(names))
	for index, each := range names {
		source, err := instance.member(each)
		if err != nil {
			return nil, err
		}
		sources[index] = source
	}
	var compute func(values []any) (delta.JsonValue, error)
	if kept {
		compute = func(values []any) (delta.JsonValue, error) { return mapValue(values[0]) }
	} else {
		compute = func(values []any) (delta.JsonValue, error) {
			whole := delta.NewJsonObject(len(names))
			for index, each := range names {
				whole.Set(each, values[index])
			}
			return mapValue(whole)
		}
	}
	derive := state.NewDerive(sources, compute, batch)
	m.derives = append(m.derives, derive)
	// The sources are the instance's members already.
	_ = derive.Attach(
		func(source any, _ int) (state.DeriveInput, error) { return source.(state.DeriveInput), nil },
		func(err error) { instance.report(member, err) },
	)
	return derive, nil
}

// assertAccepted returns `contract_mismatch` unless the provider accepts this version.
func (m *adaptedMembers) assertAccepted() error {
	for _, accepted := range m.instance.served.provider.Accepted {
		if accepted.Version == m.version {
			return nil
		}
	}
	return notServedHere(m.contract(), m.version)
}

func (m *adaptedMembers) contract() *contracts.Contract { return m.instance.served.contract }

// mappings are this version's mappings in the provider's accepts table.
func (m *adaptedMembers) mappings() *contracts.VersionMappings {
	return mappingsOf(m.instance.served, m.version)
}

// mappingsOf are version's mappings in served's accepts table (nil: none).
func mappingsOf(entry *served, version int) *contracts.VersionMappings {
	name := strconv.Itoa(version)
	// `checkAccepts` established the table's shape.
	table := entry.provider.Accepts
	for index := range table {
		if table[index].Name == name {
			return &table[index].Value
		}
	}
	return nil
}

// stateMapping is the mapping of state member `member` of `variant` in mappings; nil when it is unchanged.
func stateMapping(entry *served, mappings *contracts.VersionMappings, variant *string, member string) *contracts.StateMapping {
	if mappings == nil {
		return nil
	}
	var section []contracts.Entry[contracts.StateMapping]
	if entry.kind == servedLocal {
		section = mappings.State
	} else {
		name := ""
		if variant != nil {
			name = *variant
		}
		for index := range mappings.VariantState {
			if mappings.VariantState[index].Name == name {
				section = mappings.VariantState[index].Value
				break
			}
		}
	}
	for index := range section {
		if section[index].Name == member {
			return &section[index].Value
		}
	}
	return nil
}

// keyedMappingOf is the mapping of keyed member `member` in mappings; nil when it is unchanged.
func keyedMappingOf(mappings *contracts.VersionMappings, member string) *contracts.KeyedMapping {
	if mappings == nil {
		return nil
	}
	for index := range mappings.Keyed {
		if mappings.Keyed[index].Name == member {
			return &mappings.Keyed[index].Value
		}
	}
	return nil
}

// notServedHere is the `contract_mismatch` of a version the provider does not serve.
func notServedHere(contract *contracts.Contract, version int) *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{
		Code: data.CodeContractMismatch, Message: contract.Name + " v" + strconv.Itoa(version) + " is not served here",
	}, nil)
}

// refused is the `contract_mismatch` of a member of version the provider refuses.
func refused(contract *contracts.Contract, version int, member string) *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{
		Code:    data.CodeContractMismatch,
		Message: contract.Name + " v" + strconv.Itoa(version) + `: member "` + member + `" is refused by its provider`,
	}, nil)
}

// adaptedEnded is the `released` error its derives end with when its instance or version goes.
func adaptedEnded(label string) *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: label + ": ended"}, nil)
}
