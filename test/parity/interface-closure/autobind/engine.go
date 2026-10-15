package main

import (
	"go/types"

	"github.com/MichaelKinsy/PiG/test/parity/interface-closure/autobind/rules"
)

// The built-in rules in rules.go, shape.go and detect.go run first; the registered family rules in rules/ run only when the
// built-in verdict is not yes. See the package comment of rules for the contract.

// checkerEnv adapts a checker to rules.Env.
type checkerEnv struct{ c *checker }

func (e checkerEnv) Package() string { return e.c.pkg }

func (e checkerEnv) Agree(up string, t types.Type) rules.Verdict { return toRules(e.c.agree(up, t)) }

func (e checkerEnv) ResolveType(name string) *types.TypeName {
	if e.c.resolve == nil {
		return nil
	}
	return e.c.resolve(name)
}

func (e checkerEnv) AliasBody(name string) (string, bool) {
	if b := e.c.alias(name); b != nil {
		return *b, true
	}
	return "", false
}

func (e checkerEnv) PropertyNames(name string) []string {
	if _, open := e.c.aliases[augmentedKey][name]; open {
		return nil // a module-augmented interface has no closed property set
	}
	if e.c.propNames != nil {
		if names := e.c.propNames(name); len(names) > 0 {
			return names
		}
	}
	if e.c.props == nil {
		return nil
	}
	var names []string
	for _, p := range e.c.props(name) {
		names = append(names, p.Name)
	}
	return names
}

func (e checkerEnv) Representation(name string) (string, bool) {
	r, ok := e.c.reps[e.c.pkg+":"+name]
	return r, ok
}

func (e checkerEnv) Generic(name string) bool { return e.c.generics[name] }

func toRules(v verdict) rules.Verdict {
	switch v.ok {
	case yes:
		return rules.Verdict{OK: rules.Yes, Why: v.why}
	case no:
		return rules.Verdict{OK: rules.No, Why: v.why}
	}
	return rules.Verdict{OK: rules.Unknown, Why: v.why}
}

func fromRules(v rules.Verdict) verdict {
	switch v.OK {
	case rules.Yes:
		return verdict{yes, v.Why}
	case rules.No:
		return verdict{no, v.Why}
	}
	return verdict{unknown, v.Why}
}

// registeredType consults the registered type rules for a built-in verdict that is not yes.
func (c *checker) registeredType(up string, t types.Type, builtin verdict) verdict {
	if builtin.ok == yes {
		return builtin
	}
	if v, ok := rules.CheckType(checkerEnv{c}, up, t); ok {
		return fromRules(v)
	}
	return builtin
}

// registeredSignature consults the registered signature rules for a built-in verdict that is not yes.
func (c *checker) registeredSignature(up callShape, sig *types.Signature, builtin verdict) verdict {
	if builtin.ok == yes {
		return builtin
	}
	shape := rules.CallShape{Returns: up.Returns}
	for _, p := range up.Parameters {
		shape.Parameters = append(shape.Parameters, rules.Param{Name: p.Name, Type: p.Type, Optional: p.Optional, Rest: p.Rest})
	}
	if v, ok := rules.CheckSignature(checkerEnv{c}, shape, sig); ok {
		return fromRules(v)
	}
	return builtin
}

// registeredMember consults the registered member rules for a property the name rules did not place.
func (d *detector) registeredMember(prop *upstreamEntry, owner *sym, members []*sym) *sym {
	tn, ok := owner.Obj.(*types.TypeName)
	if !ok {
		return nil
	}
	p := rules.Property{Name: prop.Shape.Name, Type: prop.Shape.Type, Optional: prop.Shape.Optional}
	for _, cs := range prop.Shape.Calls {
		call := rules.CallShape{Returns: cs.Returns}
		for _, q := range cs.Parameters {
			call.Parameters = append(call.Parameters, rules.Param{Name: q.Name, Type: q.Type, Optional: q.Optional, Rest: q.Rest})
		}
		p.Calls = append(p.Calls, call)
	}
	obj, _ := rules.FindMember(checkerEnv{d.checker(prop)}, p, tn)
	if obj == nil {
		return nil
	}
	for _, m := range members {
		if m.Obj == obj {
			return m
		}
	}
	return nil
}
