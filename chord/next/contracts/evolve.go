package contracts

import (
	"maps"
	"slices"
	"strconv"
)

// Ports packages/chord/src/next/contracts/evolve.ts (evolution: `evolve(previous, (e) => e...)` appends the next
// version of a history with structured edits).
//
// Forced Go differences. The type-level editor interfaces (MethodEditor, RemoteEditor, LocalEditor, Put, Drop, Evolved)
// are designed out: a RemoteEditor and a LocalEditor each wrap one draft. Every edit returns its editor so edits chain,
// and the first failing edit is kept: later edits of the chain are skipped and Evolve returns that error (upstream: the
// first failing edit throws out of `edit`). Evolve takes the editor type of the contract's kind, and refuses a
// contract of the other kind (upstream's EditorOf<C> picks it from the type).

// Editor is the editor of one kind of contract.
type Editor interface {
	*RemoteEditor | *LocalEditor
}

// Evolve appends the next version (`previous.Version + 1`) with the edits edit makes; edit must return the editor it
// was given. Params and the variant set never change: a change to either is a new history base. The previous contract
// is left untouched; snapshots of members no edit touched are shared with it.
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

// State changes a state member of a variant.
func (e *RemoteEditor) State(variant, member string, schema TSchema) *RemoteEditor {
	e.d.run(func() error { return e.d.state(variant, member, schema) })
	return e
}

// AddState adds a state member to a variant.
func (e *RemoteEditor) AddState(variant, member string, schema TSchema) *RemoteEditor {
	e.d.run(func() error { return e.d.addState(variant, member, schema) })
	return e
}

// RemoveState removes a state member of a variant.
func (e *RemoteEditor) RemoveState(variant, member string) *RemoteEditor {
	e.d.run(func() error { return e.d.removeState(variant, member) })
	return e
}

// AddMethod adds a method to a variant.
func (e *RemoteEditor) AddMethod(variant, member string, args, result TSchema) *RemoteEditor {
	e.d.run(func() error { return e.d.addMethod(variant, member, args, result) })
	return e
}

// Args changes the arguments of a method in every variant that has it.
func (e *RemoteEditor) Args(member string, args TSchema) *RemoteEditor {
	e.d.run(func() error { return e.d.changeMethod(member, &args, nil) })
	return e
}

// Result changes the result of a method in every variant that has it.
func (e *RemoteEditor) Result(member string, result TSchema) *RemoteEditor {
	e.d.run(func() error { return e.d.changeMethod(member, nil, &result) })
	return e
}

// Method changes both sides of a method in every variant that has it.
func (e *RemoteEditor) Method(member string, args, result TSchema) *RemoteEditor {
	e.d.run(func() error { return e.d.changeMethod(member, &args, &result) })
	return e
}

// RemoveMethod removes a method from every variant that has it.
func (e *RemoteEditor) RemoveMethod(member string) *RemoteEditor {
	e.d.run(func() error { return e.d.removeMethod(member) })
	return e
}

// LocalEditor edits a local contract: like a remote editor, without a variant.
type LocalEditor struct{ d *draft }

// State changes a state member.
func (e *LocalEditor) State(member string, schema TSchema) *LocalEditor {
	e.d.run(func() error { return e.d.state(LocalVariant, member, schema) })
	return e
}

// AddState adds a state member.
func (e *LocalEditor) AddState(member string, schema TSchema) *LocalEditor {
	e.d.run(func() error { return e.d.addState(LocalVariant, member, schema) })
	return e
}

// RemoveState removes a state member.
func (e *LocalEditor) RemoveState(member string) *LocalEditor {
	e.d.run(func() error { return e.d.removeState(LocalVariant, member) })
	return e
}

// AddMethod adds a method.
func (e *LocalEditor) AddMethod(member string, args, result TSchema) *LocalEditor {
	e.d.run(func() error { return e.d.addMethod(LocalVariant, member, args, result) })
	return e
}

// Args changes the arguments of a method.
func (e *LocalEditor) Args(member string, args TSchema) *LocalEditor {
	e.d.run(func() error { return e.d.changeMethod(member, &args, nil) })
	return e
}

// Result changes the result of a method.
func (e *LocalEditor) Result(member string, result TSchema) *LocalEditor {
	e.d.run(func() error { return e.d.changeMethod(member, nil, &result) })
	return e
}

// Method changes both sides of a method.
func (e *LocalEditor) Method(member string, args, result TSchema) *LocalEditor {
	e.d.run(func() error { return e.d.changeMethod(member, &args, &result) })
	return e
}

// RemoveMethod removes a method.
func (e *LocalEditor) RemoveMethod(member string) *LocalEditor {
	e.d.run(func() error { return e.d.removeMethod(member) })
	return e
}

// draft is the next version under construction. Every edit is checked as it is made; keyed members are carried over
// unchanged (their versions are slice 7's).
type draft struct {
	kind     Kind
	path     string
	err      error
	data     *ContractData
	version  int
	variants *NamedMap[*VariantData]
	kinds    map[string]MemberKind
	changes  []Change
}

func newDraft(data *ContractData) *draft {
	current := data.Versions[len(data.Versions)-1]
	variants := NewNamedMap[*VariantData](current.Variants.Len())
	for name, variant := range current.Variants.All() {
		variants.Set(name, &VariantData{State: variant.State.Clone(), Methods: variant.Methods.Clone(), Keyed: variant.Keyed})
	}
	version := current.Version + 1
	return &draft{
		kind:     data.Kind,
		path:     data.Name + " v" + strconv.Itoa(version),
		data:     data,
		version:  version,
		variants: variants,
		kinds:    maps.Clone(data.Kinds),
	}
}

// run performs an edit unless an earlier edit failed, and keeps the first failure.
func (d *draft) run(edit func() error) {
	if d.err == nil {
		d.err = edit()
	}
}

// next is the contract data with this version appended.
func (d *draft) next() (ContractData, error) {
	version, err := Materialize(d.kind, d.data.Params, d.version, d.variants, d.changes)
	if err != nil {
		return ContractData{}, err
	}
	next := *d.data
	next.Versions = append(slices.Clone(d.data.Versions), version)
	next.Kinds = d.kinds
	return next, nil
}

func (d *draft) state(variant, member string, schema TSchema) error {
	found, err := d.variant(variant)
	if err != nil {
		return err
	}
	if !found.State.Has(member) {
		return fail("%s: no such state member", d.memberPath(variant, member, MemberKindState))
	}
	return d.putState(variant, member, schema)
}

func (d *draft) addState(variant, member string, schema TSchema) error {
	if err := d.refuseDeclared(variant, member, MemberKindState); err != nil {
		return err
	}
	return d.putState(variant, member, schema)
}

func (d *draft) removeState(variant, member string) error {
	found, err := d.variant(variant)
	if err != nil {
		return err
	}
	if !found.State.Delete(member) {
		return fail("%s: no such state member", d.memberPath(variant, member, MemberKindState))
	}
	return nil
}

func (d *draft) addMethod(variant, member string, args, result TSchema) error {
	if err := d.refuseDeclared(variant, member, MemberKindMethod); err != nil {
		return err
	}
	found, _ := d.variant(variant)
	method, err := readMethod(&Method{Args: args, Result: result}, d.memberPath(variant, member, MemberKindMethod))
	if err != nil {
		return err
	}
	found.Methods.Set(member, method)
	d.changes = append(d.changes, Change{variant, member, SideArgs}, Change{variant, member, SideResult})
	return d.checked()
}

func (d *draft) removeMethod(member string) error {
	variants, err := d.variantsWith(member)
	if err != nil {
		return err
	}
	for _, variant := range variants {
		found, _ := d.variant(variant)
		found.Methods.Delete(member)
	}
	return nil
}

func (d *draft) putState(variant, member string, schema TSchema) error {
	snapshot, err := SnapshotSchema(schema, d.memberPath(variant, member, MemberKindState))
	if err != nil {
		return err
	}
	found, _ := d.variant(variant)
	found.State.Set(member, snapshot)
	d.changes = append(d.changes, Change{variant, member, SideState})
	return d.checked()
}

// changeMethod changes the given sides of a method in every variant that has it.
func (d *draft) changeMethod(member string, args, result *TSchema) error {
	variants, err := d.variantsWith(member)
	if err != nil {
		return err
	}
	path := d.memberPath(variants[0], member, MemberKindMethod)
	var newArgs, newResult *Snapshot
	if args != nil {
		if newArgs, err = SnapshotArguments(*args, path+".args"); err != nil {
			return err
		}
	}
	if result != nil {
		if newResult, err = SnapshotSchema(*result, path+".result"); err != nil {
			return err
		}
	}
	for _, variant := range variants {
		found, _ := d.variant(variant)
		old := found.Methods.Value(member)
		changed := &MethodData{Args: old.Args, Result: old.Result}
		if newArgs != nil {
			changed.Args = newArgs
			d.changes = append(d.changes, Change{variant, member, SideArgs})
		}
		if newResult != nil {
			changed.Result = newResult
			d.changes = append(d.changes, Change{variant, member, SideResult})
		}
		found.Methods.Set(member, changed)
	}
	return d.checked()
}

func (d *draft) variant(variant string) (*VariantData, error) {
	found, ok := d.variants.Get(variant)
	if !ok {
		return nil, fail("%s.%s: unknown variant (the variant set never changes)", d.path, variant)
	}
	return found, nil
}

func (d *draft) variantsWith(member string) ([]string, error) {
	variants := VariantsWith(d.variants, member)
	if len(variants) == 0 {
		return nil, fail("%s: no method %s", d.path, quote(member))
	}
	return variants, nil
}

func (d *draft) refuseDeclared(variant, member string, kind MemberKind) error {
	found, err := d.variant(variant)
	if err != nil {
		return err
	}
	if found.State.Has(member) || found.Methods.Has(member) {
		return fail("%s: already declared", d.memberPath(variant, member, kind))
	}
	return nil
}

func (d *draft) memberPath(variant, member string, kind MemberKind) string {
	return memberPath(d.path, d.kind, variant, member, kind)
}

// checked checks that a member's kind never changes, also not after a removal, and that a method keeps one signature
// across variants.
func (d *draft) checked() error {
	return checkMembers(d.variants, d.kinds, d.memberPath)
}
