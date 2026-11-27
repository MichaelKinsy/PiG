package contracts

import (
	"math"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/contracts/declare.ts (declarations: `remote(...)` and `local(...)`, the checks every
// declared version passes, and the reading of untyped declaration input). A member is state, a method, a keyed member
// (local only), an event or a queries member (local only).

// RemoteStateEntry is a remote state member under its name: Schema or *Grantable wrapping Schema.
type RemoteStateEntry = Entry[RemoteStateMember]

// RemoteMethodEntry is a remote method under its name: *Method or *Grantable wrapping *Method.
type RemoteMethodEntry = Entry[RemoteMethodMember]

// RemoteEventEntry is a remote event member under its name: Schema or *Grantable wrapping Schema.
type RemoteEventEntry = Entry[RemoteEventMember]

// FlagEntry is a flag under its name with its MessageData label.
type FlagEntry = Entry[MessageData]

// LocalEntry is a local contract's member under its name: a state schema, `method(...)`, `keyed(schema)`,
// `event(schema)` or `queries(item, args)`.
type LocalEntry = Entry[LocalMember]

// StateEntry constructs a remote state entry.
func StateEntry(name string, member RemoteStateMember) RemoteStateEntry {
	return RemoteStateEntry{Name: name, Value: member}
}

// Meth constructs a remote method entry.
func Meth(name string, method RemoteMethodMember) RemoteMethodEntry {
	return RemoteMethodEntry{Name: name, Value: method}
}

// EventEntry constructs a remote event entry.
func EventEntry(name string, member RemoteEventMember) RemoteEventEntry {
	return RemoteEventEntry{Name: name, Value: member}
}

// Flag constructs a flag entry.
func Flag(name string, label MessageData) FlagEntry {
	return FlagEntry{Name: name, Value: label}
}

// Mem is `{ [name]: member }` of a local contract's members.
func Mem(name string, member LocalMember) LocalEntry { return LocalEntry{Name: name, Value: member} }

// RemoteDeclaration is the declaration of a remote contract: one shape for every caller, with optional grantable
// members and flags.
type RemoteDeclaration struct {
	// Version is a positive safe integer.
	Version float64
	Params  Params
	// HostAddress is the address of the host serving an object for these params; never part of the fingerprint.
	HostAddress HostAddress
	// Admission is the schema of the data the type's admission hands its factory: an obj(...) without a "granted"
	// field, JSON only. Never part of the fingerprint, never validated at runtime; nil for a contract without one.
	Admission *Schema
	State     []RemoteStateEntry
	Methods   []RemoteMethodEntry
	Events    []RemoteEventEntry
	// Flags are rights that are no member, each with its MessageData label (never part of the fingerprint; only the
	// flag names are).
	Flags []FlagEntry
}

// LocalDeclaration is the declaration of a local contract.
type LocalDeclaration struct {
	// Version is a positive safe integer.
	Version float64
	Params  Params
	// Members is one flat map of members, where a schema is state, a *Method a method, a *KeyedMember a keyed state
	// member, an *EventDeclaration an event member and a *QueriesMember a queries member.
	Members []LocalEntry
}

// Remote declares a remote contract: one shape of state, methods, events and flags.
func Remote(name string, declaration RemoteDeclaration) (*Contract, error) {
	if err := CheckName(name, "A contract name"); err != nil {
		return nil, err
	}
	if declaration.HostAddress == nil {
		return nil, fail("%s.hostAddress: expected a function of the params", name)
	}
	members, grantable, err := readRemoteMembers(declaration, name)
	if err != nil {
		return nil, err
	}
	flagEntries, err := entriesOfOptional(declaration.Flags, name+".flags")
	if err != nil {
		return nil, err
	}
	flags := NewNamedMap[MessageData](len(flagEntries))
	for _, entry := range flagEntries {
		if !IsMessageData(entry.Value) {
			return nil, fail("%s.flags.%s: expected its label as a message ({ namespace, key, params })", name, entry.Name)
		}
		flags.Set(entry.Name, entry.Value)
	}
	var admitted *AdmissionData
	if declaration.Admission != nil {
		admitted, err = ReadAdmission(*declaration.Admission, name+".admission")
		if err != nil {
			return nil, err
		}
	}
	return declare(KindRemote, name, declaration.Version, declaration.Params, members, grantable, declaration.HostAddress, admitted, flags)
}

func readRemoteMembers(declaration RemoteDeclaration, name string) (*MembersData, map[string]bool, error) {
	grantable := map[string]bool{}
	unmarked := func(member string, value any) any {
		if g, ok := value.(*Grantable); ok && g != nil {
			grantable[member] = true
			return g.Member
		}
		return value
	}

	stateEntries, err := entriesOfOptional(declaration.State, name+".state")
	if err != nil {
		return nil, nil, err
	}
	state := NewNamedMap[*Snapshot](len(stateEntries))
	for _, entry := range stateEntries {
		path := name + ".state." + entry.Name
		raw := unmarked(entry.Name, entry.Value)
		switch v := raw.(type) {
		case Schema:
			snapshot, err := SnapshotSchema(v, path)
			if err != nil {
				return nil, nil, err
			}
			state.Set(entry.Name, snapshot)
		case *Method:
			return nil, nil, fail("%s: a method belongs in methods", path)
		case *KeyedMember:
			return nil, nil, fail("%s: keyed members are only in local contracts", path)
		case *EventDeclaration:
			return nil, nil, fail("%s: an event belongs in events", path)
		case *QueriesMember:
			return nil, nil, fail("%s: queries members are only in local contracts", path)
		default:
			return nil, nil, fail("%s: expected a schema", path)
		}
	}

	methodEntries, err := entriesOfOptional(declaration.Methods, name+".methods")
	if err != nil {
		return nil, nil, err
	}
	methods := NewNamedMap[*MethodData](len(methodEntries))
	for _, entry := range methodEntries {
		path := name + ".methods." + entry.Name
		raw := unmarked(entry.Name, entry.Value)
		m, ok := raw.(*Method)
		if !ok || m == nil {
			return nil, nil, fail("%s: expected method(...)", path)
		}
		md, err := readMethod(m, path)
		if err != nil {
			return nil, nil, err
		}
		methods.Set(entry.Name, md)
	}

	eventEntries, err := entriesOfOptional(declaration.Events, name+".events")
	if err != nil {
		return nil, nil, err
	}
	events := NewNamedMap[*Snapshot](len(eventEntries))
	for _, entry := range eventEntries {
		path := name + ".events." + entry.Name
		raw := unmarked(entry.Name, entry.Value)
		s, ok := raw.(Schema)
		if !ok {
			return nil, nil, fail("%s: expected the schema of the event's values", path)
		}
		snapshot, err := eventSnapshot(s, path)
		if err != nil {
			return nil, nil, err
		}
		events.Set(entry.Name, snapshot)
	}

	return &MembersData{
		State:   state,
		Methods: methods,
		Keyed:   NewNamedMap[*KeyedSnapshot](0),
		Events:  events,
		Queries: NewNamedMap[*QueriesData](0),
	}, grantable, nil
}

// ReadAdmission is an admission schema as Chord-owned data, with its fingerprint: JSON only (no bytes()), an obj(...)
// without a "granted" field. Upstream's `admissionData`.
func ReadAdmission(schema Schema, path string) (*AdmissionData, error) {
	snapshot, err := SnapshotSchema(schema, path)
	if err != nil {
		return nil, err
	}
	if snapshot.Bytes {
		return nil, fail("%s: an admission's value is JSON only (no bytes())", path)
	}
	normal, ok := snapshot.Normal.(*delta.JsonObject)
	if !ok || normal.Value("type") != "object" {
		return nil, fail("%s: an admission's data is an obj(...)", path)
	}
	if props, ok := normal.Value("properties").(*delta.JsonObject); ok && props.Has("granted") {
		return nil, fail(`%s: "granted" is Chord's: the admitted data has no field of that name`, path)
	}
	fingerprint, err := FingerprintOf(snapshot.Normal)
	if err != nil {
		return nil, err
	}
	return &AdmissionData{Schema: snapshot, Fingerprint: fingerprint}, nil
}

// Local declares a local contract: one flat map of members, where a schema is state, `method(...)` a method,
// `keyed(schema)` a keyed state member, `event(schema)` an event member and `queries(item, args)` a queries member.
func Local(name string, declaration LocalDeclaration) (*Contract, error) {
	if err := CheckName(name, "A contract name"); err != nil {
		return nil, err
	}
	entries, err := entriesOf(declaration.Members, name+".members")
	if err != nil {
		return nil, err
	}
	members := &MembersData{
		State:   NewNamedMap[*Snapshot](0),
		Methods: NewNamedMap[*MethodData](0),
		Keyed:   NewNamedMap[*KeyedSnapshot](0),
		Events:  NewNamedMap[*Snapshot](0),
		Queries: NewNamedMap[*QueriesData](0),
	}
	for _, entry := range entries {
		path := name + ".members." + entry.Name
		switch member := entry.Value.(type) {
		case *Method:
			if member == nil {
				return nil, notAMember(path)
			}
			method, err := readMethod(member, path)
			if err != nil {
				return nil, err
			}
			members.Methods.Set(entry.Name, method)
		case *QueriesMember:
			if member == nil {
				return nil, notAMember(path)
			}
			queries, err := readQueries(member, path)
			if err != nil {
				return nil, err
			}
			members.Queries.Set(entry.Name, queries)
		case *KeyedMember:
			if member == nil {
				return nil, notAMember(path)
			}
			snapshot, err := keyedSnapshot(member.Schema, member.Key, path)
			if err != nil {
				return nil, err
			}
			members.Keyed.Set(entry.Name, snapshot)
		case *EventDeclaration:
			if member == nil {
				return nil, notAMember(path)
			}
			snapshot, err := eventSnapshot(member.Schema, path)
			if err != nil {
				return nil, err
			}
			members.Events.Set(entry.Name, snapshot)
		case Schema:
			snapshot, err := SnapshotSchema(member, path)
			if err != nil {
				return nil, err
			}
			members.State.Set(entry.Name, snapshot)
		default:
			return nil, notAMember(path)
		}
	}
	return declare(KindLocal, name, declaration.Version, declaration.Params, members, NoGrants, nil, nil, NewNamedMap[MessageData](0))
}

func notAMember(path string) error {
	return fail("%s: expected a schema, method(...) or keyed(...)", path)
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

// readQueries is a queries member as Chord-owned snapshots: its items' schema, and each named query's arguments, closed.
func readQueries(member *QueriesMember, path string) (*QueriesData, error) {
	entries, err := entriesOf(member.Args, path+".args")
	if err != nil {
		return nil, err
	}
	args := NewNamedMap[*Snapshot](len(entries))
	for _, entry := range entries {
		snapshot, err := QueryArguments(entry.Value, path+".args."+entry.Name)
		if err != nil {
			return nil, err
		}
		args.Set(entry.Name, snapshot)
	}
	item, err := SnapshotSchema(member.Item, path+".item")
	if err != nil {
		return nil, err
	}
	return &QueriesData{Item: item, Args: args}, nil
}

// QueryArguments snapshots a named query's argument schema: closed, and JSON only (no bytes() leaf).
func QueryArguments(schema Schema, path string) (*Snapshot, error) {
	snapshot, err := SnapshotArguments(schema, path)
	if err != nil {
		return nil, err
	}
	if snapshot.Bytes {
		return nil, fail("%s: a query's arguments are JSON only (no bytes())", path)
	}
	return snapshot, nil
}

// keyedSnapshot is a keyed member's snapshot: its schema's and, keyed by a host kind, the kind (declaration policy: a
// declared one, its name in the member's normal form).
func keyedSnapshot(schema Schema, key *HostKind, path string) (*KeyedSnapshot, error) {
	snapshot, err := SnapshotSchema(schema, path)
	if err != nil {
		return nil, err
	}
	if key != nil {
		if _, err := KindParams(key); err != nil {
			return nil, err
		}
	}
	return &KeyedSnapshot{Snapshot: *snapshot, Key: key}, nil
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

func declare(kind Kind, name string, version float64, params Params, members *MembersData, grantable map[string]bool, hostAddress HostAddress, admission *AdmissionData, flags *NamedMap[MessageData]) (*Contract, error) {
	if version != math.Trunc(version) || math.Abs(version) > 1<<53-1 || version < 1 {
		return nil, fail("%s.version: expected a positive integer", name)
	}
	kinds := map[string]MemberKind{}
	err := checkMembers(members, kinds, func(member string, memberKind MemberKind) string {
		return memberPath(name, kind, member, memberKind)
	})
	if err != nil {
		return nil, err
	}
	for flag := range flags.All() {
		if reservedMembers[flag] {
			return nil, fail(`%s.flags.%s: "%s" is reserved: a reference has it`, name, flag, flag)
		}
		if _, exists := kinds[flag]; exists {
			return nil, fail(`%s.flags.%s: "%s" is a member: a name is a member or a flag`, name, flag, flag)
		}
	}
	declared, err := entriesOf(params, name+".params")
	if err != nil {
		return nil, err
	}
	paramsData, err := ReadParams(declared, name+".params")
	if err != nil {
		return nil, err
	}
	//portlint:allow numbers version is checked a positive safe integer above, so int(version) is exact
	first, err := Materialize(kind, paramsData, int(version), members, grantable, flags.Keys(), nil)
	if err != nil {
		return nil, err
	}
	return CreateContract(ContractData{
		Kind:        kind,
		Name:        name,
		Params:      paramsData,
		HostAddress: hostAddress,
		Versions:    []VersionData{first},
		Kinds:       kinds,
		Admission:   admission,
		Flags:       flags,
	}), nil
}

// checkMembers checks a version's members: a name keeps one kind across the kinds of members and the whole history.
func checkMembers(members *MembersData, kinds map[string]MemberKind, path func(member string, kind MemberKind) string) error {
	seen := map[string]bool{}
	record := func(member string, kind MemberKind) error {
		if seen[member] {
			return fail(`%s: "%s" is declared twice`, path(member, kind), member)
		}
		seen[member] = true
		return recordKind(kinds, member, kind, path(member, kind))
	}
	for member := range members.Queries.All() {
		if err := record(member, MemberKindQueries); err != nil {
			return err
		}
	}
	for member := range members.State.All() {
		if err := record(member, MemberKindState); err != nil {
			return err
		}
	}
	for member := range members.Keyed.All() {
		if err := record(member, MemberKindKeyed); err != nil {
			return err
		}
	}
	for member := range members.Events.All() {
		if err := record(member, MemberKindEvent); err != nil {
			return err
		}
	}
	for member := range members.Methods.All() {
		if err := record(member, MemberKindMethod); err != nil {
			return err
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

// reservedMembers are the names a reference has itself (`release`, `granted`, and `state` and `retry` with failures),
// and `then`, which would make an awaited reference thenable.
var reservedMembers = map[string]bool{"then": true, "release": true, "granted": true, "state": true, "retry": true}

// memberPath is the path of a member in errors: `notes.methods.add`, `game.events.paddle`, `info.members.clear`.
func memberPath(base string, kind Kind, member string, memberKind MemberKind) string {
	if kind == KindLocal {
		return base + ".members." + member
	}
	return base + "." + sectionOf(memberKind) + "." + member
}

// sectionOf is the section of a remote contract a member of each kind is declared in.
func sectionOf(kind MemberKind) string {
	switch kind {
	case MemberKindState, MemberKindKeyed, MemberKindQueries:
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
