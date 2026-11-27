package contracts

import (
	"maps"
	"slices"
	"strconv"
)

// Ports packages/chord/src/contracts/evolve.ts (evolution: `evolve(previous, (e) => e...)` appends the next
// version of a history with structured edits).

// Editor is the editor of one kind of contract.
type Editor interface {
	*RemoteEditor | *LocalEditor
}

// Evolve appends the next version (`previous.Version + 1`) with the edits edit makes; edit must return the editor it
// was given. Params and flags never change: a change to either is a new history base. The previous contract is left
// untouched; snapshots of members no edit touched are shared with it.
func Evolve[E Editor](previous *Contract, edit func(E) E) (*Contract, error) {
	data, err := ContractDataOf(previous)
	if err != nil {
		return nil, err
	}
	d := newDraft(data)
	var editor E
	switch any(editor).(type) {
	case *RemoteEditor:
		if data.Kind != KindRemote {
			return nil, fail("%s: evolve with a remote editor needs a remote contract", data.Name)
		}
		editor = any(&RemoteEditor{d}).(E)
	case *LocalEditor:
		if data.Kind != KindLocal {
			return nil, fail("%s: evolve with a local editor needs a local contract", data.Name)
		}
		editor = any(&LocalEditor{d}).(E)
	}
	editErr := EditWith(editor, edit, d.path+": evolve")
	if d.err != nil {
		return nil, d.err
	}
	if editErr != nil {
		return nil, editErr
	}
	next, err := d.next()
	if err != nil {
		return nil, err
	}
	return CreateContract(next), nil
}

// EditWith runs edit on editor (contracts' and tables' rule): an edit returning anything but the editor it was given
// is an error (what names the call).
func EditWith[E comparable](editor E, edit func(E) E, what string) error {
	if edit(editor) != editor {
		return fail("%s must return the editor it was given", what)
	}
	return nil
}

// RemoteEditor edits a remote contract.
type RemoteEditor struct{ d *draft }

// Admission replaces the schema of the admission's value (slice 14 §2.5), or gives the contract one.
func (e *RemoteEditor) Admission(schema Schema) *RemoteEditor {
	e.d.run(func() error { return e.d.setAdmission(schema) })
	return e
}

// State changes a state member.
func (e *RemoteEditor) State(member string, schema Schema) *RemoteEditor {
	e.d.run(func() error { return e.d.state(member, schema) })
	return e
}

// AddState adds a state member (Schema or *Grantable wrapping Schema).
func (e *RemoteEditor) AddState(member string, schema RemoteStateMember) *RemoteEditor {
	e.d.run(func() error { return e.d.addState(member, schema) })
	return e
}

// RemoveState removes a state member.
func (e *RemoteEditor) RemoveState(member string) *RemoteEditor {
	e.d.run(func() error { return e.d.removeState(member) })
	return e
}

// AddMethod adds a method (args, result) — plain (non-grantable).
func (e *RemoteEditor) AddMethod(member string, args, result Schema) *RemoteEditor {
	e.d.run(func() error { return e.d.addMethod(member, NewMethod(args, result)) })
	return e
}

// AddGrantableMethod adds a grantable method (`NewGrantable(NewMethod(...))`).
func (e *RemoteEditor) AddGrantableMethod(member string, method *Grantable) *RemoteEditor {
	e.d.run(func() error { return e.d.addMethod(member, method) })
	return e
}

// Args changes the arguments of a method.
func (e *RemoteEditor) Args(member string, args Schema) *RemoteEditor {
	e.d.run(func() error { return e.d.changeMethod(member, &args, nil) })
	return e
}

// Result changes the result of a method.
func (e *RemoteEditor) Result(member string, result Schema) *RemoteEditor {
	e.d.run(func() error { return e.d.changeMethod(member, nil, &result) })
	return e
}

// Method changes both sides of a method.
func (e *RemoteEditor) Method(member string, args, result Schema) *RemoteEditor {
	e.d.run(func() error { return e.d.changeMethod(member, &args, &result) })
	return e
}

// RemoveMethod removes a method.
func (e *RemoteEditor) RemoveMethod(member string) *RemoteEditor {
	e.d.run(func() error { return e.d.removeMethod(member) })
	return e
}

// Event changes an event member.
func (e *RemoteEditor) Event(member string, schema Schema) *RemoteEditor {
	e.d.run(func() error { return e.d.event(member, schema) })
	return e
}

// AddEvent adds an event member (Schema or *Grantable wrapping Schema).
func (e *RemoteEditor) AddEvent(member string, schema RemoteEventMember) *RemoteEditor {
	e.d.run(func() error { return e.d.addEvent(member, schema) })
	return e
}

// RemoveEvent removes an event member.
func (e *RemoteEditor) RemoveEvent(member string) *RemoteEditor {
	e.d.run(func() error { return e.d.removeEvent(member) })
	return e
}

// LocalEditor edits a local contract.
type LocalEditor struct{ d *draft }

// State changes a state member.
func (e *LocalEditor) State(member string, schema Schema) *LocalEditor {
	e.d.run(func() error { return e.d.state(member, schema) })
	return e
}

// AddState adds a state member.
func (e *LocalEditor) AddState(member string, schema Schema) *LocalEditor {
	e.d.run(func() error { return e.d.addState(member, schema) })
	return e
}

// RemoveState removes a state member.
func (e *LocalEditor) RemoveState(member string) *LocalEditor {
	e.d.run(func() error { return e.d.removeState(member) })
	return e
}

// AddMethod adds a method.
func (e *LocalEditor) AddMethod(member string, args, result Schema) *LocalEditor {
	e.d.run(func() error { return e.d.addMethod(member, NewMethod(args, result)) })
	return e
}

// Args changes the arguments of a method.
func (e *LocalEditor) Args(member string, args Schema) *LocalEditor {
	e.d.run(func() error { return e.d.changeMethod(member, &args, nil) })
	return e
}

// Result changes the result of a method.
func (e *LocalEditor) Result(member string, result Schema) *LocalEditor {
	e.d.run(func() error { return e.d.changeMethod(member, nil, &result) })
	return e
}

// Method changes both sides of a method.
func (e *LocalEditor) Method(member string, args, result Schema) *LocalEditor {
	e.d.run(func() error { return e.d.changeMethod(member, &args, &result) })
	return e
}

// RemoveMethod removes a method.
func (e *LocalEditor) RemoveMethod(member string) *LocalEditor {
	e.d.run(func() error { return e.d.removeMethod(member) })
	return e
}

// Keyed changes a keyed member's schema (and optionally its host kind; nil keeps string keys).
func (e *LocalEditor) Keyed(member string, schema Schema, key ...*HostKind) *LocalEditor {
	var k *HostKind
	if len(key) > 0 {
		k = key[0]
	}
	e.d.run(func() error { return e.d.keyed(member, schema, k) })
	return e
}

// AddKeyed adds a keyed member.
func (e *LocalEditor) AddKeyed(member string, schema Schema, key ...*HostKind) *LocalEditor {
	var k *HostKind
	if len(key) > 0 {
		k = key[0]
	}
	e.d.run(func() error { return e.d.addKeyed(member, schema, k) })
	return e
}

// RemoveKeyed removes a keyed member.
func (e *LocalEditor) RemoveKeyed(member string) *LocalEditor {
	e.d.run(func() error { return e.d.removeKeyed(member) })
	return e
}

// Event changes an event member.
func (e *LocalEditor) Event(member string, schema Schema) *LocalEditor {
	e.d.run(func() error { return e.d.event(member, schema) })
	return e
}

// AddEvent adds an event member.
func (e *LocalEditor) AddEvent(member string, schema Schema) *LocalEditor {
	e.d.run(func() error { return e.d.addEvent(member, schema) })
	return e
}

// RemoveEvent removes an event member.
func (e *LocalEditor) RemoveEvent(member string) *LocalEditor {
	e.d.run(func() error { return e.d.removeEvent(member) })
	return e
}

type draft struct {
	kind      Kind
	data      *ContractData
	version   int
	path      string
	members   *MembersData
	grantable map[string]bool
	kinds     map[string]MemberKind
	changes   []Change
	admission *AdmissionData
	err       error
}

func newDraft(data *ContractData) *draft {
	current := data.Versions[len(data.Versions)-1]
	version := current.Version + 1
	return &draft{
		kind:    data.Kind,
		data:    data,
		version: version,
		path:    data.Name + " v" + strconv.Itoa(version),
		members: &MembersData{
			State:   current.Members.State.Clone(),
			Methods: current.Members.Methods.Clone(),
			Keyed:   current.Members.Keyed.Clone(),
			Events:  current.Members.Events.Clone(),
			Queries: current.Members.Queries,
		},
		grantable: maps.Clone(current.Grantable),
		kinds:     maps.Clone(data.Kinds),
		admission: data.Admission,
	}
}

func (d *draft) run(fn func() error) {
	if d.err != nil {
		return
	}
	d.err = fn()
}

func (d *draft) setAdmission(schema Schema) error {
	if d.kind == KindLocal {
		return fail("%s.admission: only a remote contract has an admission", d.path)
	}
	admitted, err := ReadAdmission(schema, d.path+".admission")
	if err != nil {
		return err
	}
	d.admission = admitted
	return nil
}

func (d *draft) state(member string, schema Schema) error {
	if !d.members.State.Has(member) {
		return fail("%s: no such state member", d.memberPath(member, MemberKindState))
	}
	return d.putState(member, schema)
}

func (d *draft) addState(member string, raw any) error {
	if err := d.refuseDeclared(member, MemberKindState); err != nil {
		return err
	}
	unmarked, err := d.unmarked(member, raw, MemberKindState)
	if err != nil {
		return err
	}
	schema, ok := unmarked.(Schema)
	if !ok {
		return fail("%s: expected a schema", d.memberPath(member, MemberKindState))
	}
	return d.putState(member, schema)
}

func (d *draft) removeState(member string) error {
	if !d.members.State.Delete(member) {
		return fail("%s: no such state member", d.memberPath(member, MemberKindState))
	}
	delete(d.grantable, member)
	return nil
}

func (d *draft) addMethod(member string, raw any) error {
	if err := d.refuseDeclared(member, MemberKindMethod); err != nil {
		return err
	}
	unmarked, err := d.unmarked(member, raw, MemberKindMethod)
	if err != nil {
		return err
	}
	m, ok := unmarked.(*Method)
	if !ok || m == nil {
		return fail("%s: expected grantable(method(...))", d.memberPath(member, MemberKindMethod))
	}
	md, err := readMethod(m, d.memberPath(member, MemberKindMethod))
	if err != nil {
		return err
	}
	d.members.Methods.Set(member, md)
	d.changes = append(d.changes, Change{Member: member, Side: SideArgs}, Change{Member: member, Side: SideResult})
	return d.checked()
}

func (d *draft) removeMethod(member string) error {
	if !d.members.Methods.Delete(member) {
		return fail(`%s: no method "%s"`, d.path, member)
	}
	delete(d.grantable, member)
	return nil
}

func (d *draft) keyed(member string, schema Schema, key *HostKind) error {
	if !d.members.Keyed.Has(member) {
		return fail("%s: no such keyed member", d.memberPath(member, MemberKindKeyed))
	}
	return d.putKeyed(member, schema, key)
}

func (d *draft) addKeyed(member string, schema Schema, key *HostKind) error {
	if err := d.refuseDeclared(member, MemberKindKeyed); err != nil {
		return err
	}
	return d.putKeyed(member, schema, key)
}

func (d *draft) removeKeyed(member string) error {
	if !d.members.Keyed.Delete(member) {
		return fail("%s: no such keyed member", d.memberPath(member, MemberKindKeyed))
	}
	return nil
}

func (d *draft) event(member string, schema Schema) error {
	if !d.members.Events.Has(member) {
		return fail("%s: no such event member", d.memberPath(member, MemberKindEvent))
	}
	return d.putEvent(member, schema)
}

func (d *draft) addEvent(member string, raw any) error {
	if err := d.refuseDeclared(member, MemberKindEvent); err != nil {
		return err
	}
	unmarked, err := d.unmarked(member, raw, MemberKindEvent)
	if err != nil {
		return err
	}
	schema, ok := unmarked.(Schema)
	if !ok {
		return fail("%s: expected the schema of the event's values", d.memberPath(member, MemberKindEvent))
	}
	return d.putEvent(member, schema)
}

func (d *draft) removeEvent(member string) error {
	if !d.members.Events.Delete(member) {
		return fail("%s: no such event member", d.memberPath(member, MemberKindEvent))
	}
	delete(d.grantable, member)
	return nil
}

func (d *draft) next() (ContractData, error) {
	version, err := Materialize(d.kind, d.data.Params, d.version, d.members, d.grantable, d.data.Flags.Keys(), d.changes)
	if err != nil {
		return ContractData{}, err
	}
	versions := append(slices.Clone(d.data.Versions), version)
	return ContractData{
		Kind:        d.kind,
		Name:        d.data.Name,
		Params:      d.data.Params,
		HostAddress: d.data.HostAddress,
		Versions:    versions,
		Kinds:       d.kinds,
		Admission:   d.admission,
		Flags:       d.data.Flags,
	}, nil
}

func (d *draft) unmarked(member string, declared any, kind MemberKind) (any, error) {
	g, marked := declared.(*Grantable)
	if marked && g == nil {
		marked = false
	}
	known := d.data.Kinds[member] == kind
	if known {
		wasGrantable := false
		for _, v := range d.data.Versions {
			if v.Grantable[member] {
				wasGrantable = true
				break
			}
		}
		if marked != wasGrantable {
			return nil, fail("%s: whether it is grantable never changes across a history", d.memberPath(member, kind))
		}
	}
	if !marked {
		return declared, nil
	}
	if d.kind == KindLocal {
		return nil, fail("%s: only a remote contract has grantable members", d.memberPath(member, kind))
	}
	if d.grantable == nil {
		d.grantable = map[string]bool{}
	}
	d.grantable[member] = true
	return g.Member, nil
}

func (d *draft) putState(member string, schema Schema) error {
	snapshot, err := SnapshotSchema(schema, d.memberPath(member, MemberKindState))
	if err != nil {
		return err
	}
	d.members.State.Set(member, snapshot)
	d.changes = append(d.changes, Change{Member: member, Side: SideState})
	return d.checked()
}

func (d *draft) putEvent(member string, schema Schema) error {
	snapshot, err := eventSnapshot(schema, d.memberPath(member, MemberKindEvent))
	if err != nil {
		return err
	}
	d.members.Events.Set(member, snapshot)
	d.changes = append(d.changes, Change{Member: member, Side: SideEvent})
	return d.checked()
}

func (d *draft) putKeyed(member string, schema Schema, key *HostKind) error {
	if d.kind == KindRemote {
		return fail("%s: keyed members are only in local contracts", d.memberPath(member, MemberKindKeyed))
	}
	snapshot, err := keyedSnapshot(schema, key, d.memberPath(member, MemberKindKeyed))
	if err != nil {
		return err
	}
	d.members.Keyed.Set(member, snapshot)
	d.changes = append(d.changes, Change{Member: member, Side: SideKeyed})
	return d.checked()
}

func (d *draft) changeMethod(member string, args, result *Schema) error {
	old, ok := d.members.Methods.Get(member)
	if !ok {
		return fail(`%s: no method "%s"`, d.path, member)
	}
	path := d.memberPath(member, MemberKindMethod)
	newArgs := old.Args
	if args != nil {
		var err error
		if newArgs, err = SnapshotArguments(*args, path+".args"); err != nil {
			return err
		}
		d.changes = append(d.changes, Change{Member: member, Side: SideArgs})
	}
	newResult := old.Result
	if result != nil {
		var err error
		if newResult, err = SnapshotSchema(*result, path+".result"); err != nil {
			return err
		}
		d.changes = append(d.changes, Change{Member: member, Side: SideResult})
	}
	d.members.Methods.Set(member, &MethodData{Args: newArgs, Result: newResult})
	return d.checked()
}

func (d *draft) refuseDeclared(member string, kind MemberKind) error {
	if d.members.State.Has(member) || d.members.Methods.Has(member) || d.members.Keyed.Has(member) ||
		d.members.Events.Has(member) || d.members.Queries.Has(member) || d.data.Flags.Has(member) {
		return fail("%s: already declared", d.memberPath(member, kind))
	}
	return nil
}

func (d *draft) memberPath(member string, kind MemberKind) string {
	return memberPath(d.path, d.kind, member, kind)
}

func (d *draft) checked() error {
	return checkMembers(d.members, d.kinds, d.memberPath)
}
