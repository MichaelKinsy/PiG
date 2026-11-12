package contracts

import (
	"math"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/contracts/declare.ts (declarations: `remote(...)` and `local(...)`, the checks every
// declared version passes, and the reading of untyped declaration input). A member is state, a method, a keyed member
// (local only) or an event.
//
// Forced Go differences. A declaration is a typed struct: upstream's checks that name an unknown key, a non-object, a
// method under `state`, a schema under `methods` or a keyed member in a remote contract have no Go input (checkKeys is
// designed out; a nil *Method is the closest remaining case and reports `expected method(...)`). The generic
// RemoteVariants, LocalVariants and SameMembers types are type-level only. Ordered objects are slices of Entry values:
// a nil slice is absent (`expected an object`), an empty non-nil one is `{}`.

// VariantDeclaration is one remote variant's declaration: its state schemas, methods and events (the schemas of the
// values its object emits), each possibly absent.
type VariantDeclaration struct {
	State   []Property
	Methods []MethodEntry
	Events  []Property
}

// MethodEntry is a method under its name.
type MethodEntry = Entry[*Method]

// VariantEntry is a variant declaration under its name.
type VariantEntry = Entry[VariantDeclaration]

// LocalEntry is a local contract's member under its name: a state schema, `method(...)`, `keyed(schema)` or
// `event(schema)`.
type LocalEntry = Entry[LocalMember]

// Meth is `{ [name]: method }` of a variant's methods.
func Meth(name string, method *Method) MethodEntry { return MethodEntry{Name: name, Value: method} }

// Var is `{ [name]: declaration }` of a remote contract's variants.
func Var(name string, declaration VariantDeclaration) VariantEntry {
	return VariantEntry{Name: name, Value: declaration}
}

// Mem is `{ [name]: member }` of a local contract's members.
func Mem(name string, member LocalMember) LocalEntry { return LocalEntry{Name: name, Value: member} }

// RemoteDeclaration is the declaration of a remote contract.
type RemoteDeclaration struct {
	// Version is a positive safe integer.
	Version float64
	Params  Params
	// HostAddress is the address of the host serving an object for these params; never part of the fingerprint.
	HostAddress HostAddress
	Variants    []VariantEntry
}

// LocalDeclaration is the declaration of a local contract.
type LocalDeclaration struct {
	// Version is a positive safe integer.
	Version float64
	Params  Params
	// Members is one flat map of members, where a schema is state, a *Method a method, a *KeyedMember a keyed state
	// member and an *EventDeclaration an event member.
	Members []LocalEntry
}

// Remote declares a remote contract: per-principal variants of state, methods and events.
func Remote(name string, declaration RemoteDeclaration) (*Contract, error) {
	if err := CheckName(name, "A contract name"); err != nil {
		return nil, err
	}
	variants, err := entriesOf(declaration.Variants, name+".variants")
	if err != nil {
		return nil, err
	}
	if len(variants) == 0 {
		return nil, fail("%s.variants: at least one variant is required", name)
	}
	if declaration.HostAddress == nil {
		return nil, fail("%s.hostAddress: expected a function of the params", name)
	}
	read := NewNamedMap[*VariantData](len(variants))
	for _, entry := range variants {
		data, err := readVariant(entry.Value, name+"."+entry.Name)
		if err != nil {
			return nil, err
		}
		read.Set(entry.Name, data)
	}
	return declare(KindRemote, name, declaration.Version, declaration.Params, read, declaration.HostAddress)
}

// Local declares a local contract: one flat map of members, where a schema is state, `method(...)` a method,
// `keyed(schema)` a keyed state member and `event(schema)` an event member.
func Local(name string, declaration LocalDeclaration) (*Contract, error) {
	if err := CheckName(name, "A contract name"); err != nil {
		return nil, err
	}
	members, err := entriesOf(declaration.Members, name+"."+LocalVariant)
	if err != nil {
		return nil, err
	}
	variant := &VariantData{State: NewNamedMap[*Snapshot](0), Methods: NewNamedMap[*MethodData](0), Keyed: NewNamedMap[*Snapshot](0), Events: NewNamedMap[*Snapshot](0)}
	for _, entry := range members {
		path := name + "." + LocalVariant + "." + entry.Name
		switch member := entry.Value.(type) {
		case *Method:
			if member == nil {
				return nil, notAMember(path)
			}
			method, err := readMethod(member, path)
			if err != nil {
				return nil, err
			}
			variant.Methods.Set(entry.Name, method)
		case *KeyedMember:
			if member == nil {
				return nil, notAMember(path)
			}
			snapshot, err := SnapshotSchema(member.Schema, path)
			if err != nil {
				return nil, err
			}
			variant.Keyed.Set(entry.Name, snapshot)
		case *EventDeclaration:
			if member == nil {
				return nil, notAMember(path)
			}
			snapshot, err := eventSnapshot(member.Schema, path)
			if err != nil {
				return nil, err
			}
			variant.Events.Set(entry.Name, snapshot)
		case Schema:
			snapshot, err := SnapshotSchema(member, path)
			if err != nil {
				return nil, err
			}
			variant.State.Set(entry.Name, snapshot)
		default:
			return nil, notAMember(path)
		}
	}
	variants := NewNamedMap[*VariantData](1)
	variants.Set(LocalVariant, variant)
	return declare(KindLocal, name, declaration.Version, declaration.Params, variants, nil)
}

func notAMember(path string) error {
	return fail("%s: expected a schema, method(...) or keyed(...)", path)
}

func readVariant(source VariantDeclaration, path string) (*VariantData, error) {
	stateEntries, err := entriesOfOptional(source.State, path+".state")
	if err != nil {
		return nil, err
	}
	state := NewNamedMap[*Snapshot](len(stateEntries))
	for _, entry := range stateEntries {
		snapshot, err := SnapshotSchema(entry.Value, path+".state."+entry.Name)
		if err != nil {
			return nil, err
		}
		state.Set(entry.Name, snapshot)
	}
	methodEntries, err := entriesOfOptional(source.Methods, path+".methods")
	if err != nil {
		return nil, err
	}
	methods := NewNamedMap[*MethodData](len(methodEntries))
	for _, entry := range methodEntries {
		if entry.Value == nil {
			return nil, fail("%s.methods.%s: expected method(...)", path, entry.Name)
		}
		method, err := readMethod(entry.Value, path+".methods."+entry.Name)
		if err != nil {
			return nil, err
		}
		methods.Set(entry.Name, method)
	}
	eventEntries, err := entriesOfOptional(source.Events, path+".events")
	if err != nil {
		return nil, err
	}
	events := NewNamedMap[*Snapshot](len(eventEntries))
	for _, entry := range eventEntries {
		snapshot, err := eventSnapshot(entry.Value, path+".events."+entry.Name)
		if err != nil {
			return nil, err
		}
		events.Set(entry.Name, snapshot)
	}
	return &VariantData{State: state, Methods: methods, Keyed: NewNamedMap[*Snapshot](0), Events: events}, nil
}

// eventSnapshot is an event member's snapshot: JSON only (declaration policy: no `bytes()` leaf, events travel as JSON
// parts).
func eventSnapshot(schema Schema, path string) (*Snapshot, error) {
	snapshot, err := SnapshotSchema(schema, path)
	if err != nil {
		return nil, err
	}
	if snapshot.Bytes {
		return nil, fail("%s: an event's values are JSON only (no bytes())", path)
	}
	return snapshot, nil
}

// readMethod is one method declaration as Chord-owned snapshots; its arguments must be closed.
func readMethod(method *Method, path string) (*MethodData, error) {
	args, err := SnapshotArguments(method.Args, path+".args")
	if err != nil {
		return nil, err
	}
	result, err := SnapshotSchema(method.Result, path+".result")
	if err != nil {
		return nil, err
	}
	return &MethodData{Args: args, Result: result}, nil
}

func declare(kind Kind, name string, version float64, params Params, variants *NamedMap[*VariantData], hostAddress HostAddress) (*Contract, error) {
	if version != math.Trunc(version) || math.Abs(version) > 1<<53-1 || version < 1 {
		return nil, fail("%s.version: expected a positive integer", name)
	}
	kinds := map[string]MemberKind{}
	err := checkMembers(variants, kinds, func(variant, member string, memberKind MemberKind) string {
		return memberPath(name, kind, variant, member, memberKind)
	})
	if err != nil {
		return nil, err
	}
	declared, err := entriesOf(params, name+".params")
	if err != nil {
		return nil, err
	}
	paramsData, err := ReadParams(declared, name+".params")
	if err != nil {
		return nil, err
	}
	//portlint:allow numbers version is checked a positive safe integer at declare.go:186, as Number.isSafeInteger(version) at declare.ts:169, so int(version) is exact
	first, err := Materialize(kind, paramsData, int(version), variants, nil)
	if err != nil {
		return nil, err
	}
	return CreateContract(ContractData{Kind: kind, Name: name, Params: paramsData, HostAddress: hostAddress, Versions: []VersionData{first}, Kinds: kinds}), nil
}

// checkMembers checks the members of every variant: a name keeps one kind across variants and the whole history
// (kinds, updated here), a method one signature across variants.
func checkMembers(variants *NamedMap[*VariantData], kinds map[string]MemberKind, path func(variant, member string, kind MemberKind) string) error {
	signatures := map[string]string{}
	for variant, data := range variants.All() {
		for member := range data.State.All() {
			if err := recordKind(kinds, member, MemberKindState, path(variant, member, MemberKindState)); err != nil {
				return err
			}
		}
		for member := range data.Keyed.All() {
			if err := recordKind(kinds, member, MemberKindKeyed, path(variant, member, MemberKindKeyed)); err != nil {
				return err
			}
		}
		for member := range data.Events.All() {
			if err := recordKind(kinds, member, MemberKindEvent, path(variant, member, MemberKindEvent)); err != nil {
				return err
			}
		}
		for member, method := range data.Methods.All() {
			if err := recordKind(kinds, member, MemberKindMethod, path(variant, member, MemberKindMethod)); err != nil {
				return err
			}
			signature, err := jsstring.MarshalJSON([]any{method.Args.Normal, method.Result.Normal})
			if err != nil {
				return fail("%s: %v", path(variant, member, MemberKindMethod), err)
			}
			known, seen := signatures[member]
			if seen && known != string(signature) {
				return fail("%s: a method has the same signature in every variant", path(variant, member, MemberKindMethod))
			}
			signatures[member] = string(signature)
		}
	}
	return nil
}

func recordKind(kinds map[string]MemberKind, member string, kind MemberKind, path string) error {
	if reservedMembers[member] {
		return fail(`%s: "%s" is reserved: a reference has it`, path, member)
	}
	if known, ok := kinds[member]; ok && known != kind {
		article := "a"
		if known == MemberKindEvent {
			article = "an"
		}
		return fail(`%s: "%s" is %s %s member; its kind never changes`, path, member, article, known)
	}
	kinds[member] = kind
	return nil
}

// reservedMembers are the names a reference has itself (`release`, `variant`, and `state` and `retry` with failures),
// and `then`, which would make an awaited reference thenable.
var reservedMembers = map[string]bool{"then": true, "release": true, "variant": true, "state": true, "retry": true}

// memberPath is the path of a member in errors: `notes.editor.methods.add`, `game.player.events.paddle`,
// `info.members.clear`.
func memberPath(base string, kind Kind, variant, member string, memberKind MemberKind) string {
	if kind == KindLocal {
		return base + "." + LocalVariant + "." + member
	}
	return base + "." + variant + "." + sectionOf(memberKind) + "." + member
}

// sectionOf is the section of a remote variant a member of each kind is declared in (keyed members: only local
// contracts).
func sectionOf(kind MemberKind) string {
	switch kind {
	case MemberKindState, MemberKindKeyed:
		return "state"
	case MemberKindEvent:
		return "events"
	}
	return "methods"
}

// entriesOf is the entries of an object in JavaScript's own-key order; a nil slice is not an object and is refused.
func entriesOf[T any](source []Entry[T], path string) ([]Entry[T], error) {
	if source == nil {
		return nil, fail("%s: expected an object", path)
	}
	return orderedEntries(source), nil
}

// entriesOfOptional is entriesOf of an object that defaults to `{}`.
func entriesOfOptional[T any](source []Entry[T], path string) ([]Entry[T], error) {
	if source == nil {
		return nil, nil
	}
	return entriesOf(source, path)
}
