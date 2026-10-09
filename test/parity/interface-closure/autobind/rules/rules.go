// Package rules is the registry of deterministic mapping rules from Pi's TypeScript shapes to PiG's Go shapes.
//
// A rule belongs to one family (naming, optionality, unions, functions, placement, data shapes). It lives in the family's file,
// registers itself in init, and is consulted by the detector at one fixed extension point. A rule decides only when the Go shape is
// provably equivalent under the rule (Yes) or provably not (No); otherwise it reports ok=false and the detector keeps its verdict.
// There are no scores. The same input gives the same answer: rules take no clock, network, map-order or random input.
//
// Extension points, in the order the detector consults them for one upstream row:
//
//	PlacementRule  which repository directories hold the Go counterpart of an upstream package
//	NameRule       whether a Go name stands for an upstream name
//	MemberRule     which Go field or method stands for an upstream property when the name rules find none
//	TypeRule       whether an upstream type text agrees with a Go type
//	SignatureRule  whether an upstream call shape agrees with a Go signature
//
// The built-in rules run first. A registered rule is consulted only when the built-in verdict is not Yes, in registration order
// (family order, then rule name). The first rule that decides wins, so a rule cannot weaken a Yes and two rules never combine.
package rules

import (
	"fmt"
	"go/types"
	"sort"
)

// Tri is a three-valued verdict.
type Tri int

// A rule answers Yes, No, or Unknown. The detector reports Unknown as a gap with reason undecidable.
const (
	Unknown Tri = iota
	Yes
	No
)

// Verdict is a rule's answer and the reason it gives.
type Verdict struct {
	OK  Tri
	Why string
}

// Accept is the Yes verdict; why names the rule, for example "T16: ...".
func Accept(format string, args ...any) Verdict { return Verdict{Yes, fmt.Sprintf(format, args...)} }

// Refute is the No verdict.
func Refute(format string, args ...any) Verdict { return Verdict{No, fmt.Sprintf(format, args...)} }

// Undecided is the Unknown verdict.
func Undecided(format string, args ...any) Verdict {
	return Verdict{Unknown, fmt.Sprintf(format, args...)}
}

// CallShape is one upstream call or construct signature.
type CallShape struct {
	Parameters []Param
	Returns    string
}

// Env is what the detector offers to a rule. Every method is deterministic for one run.
type Env interface {
	// Package is the upstream package of the row being judged (ai, agent, tui, mcp, codemode, coding-agent).
	Package() string
	// Agree applies the whole type rule set, built-in and registered, to an upstream type text and a Go type.
	Agree(up string, t types.Type) Verdict
	// ResolveType returns the Go type that stands for an upstream interface, class or alias name, or nil.
	ResolveType(name string) *types.TypeName
	// AliasBody returns the body of an upstream type alias.
	AliasBody(name string) (string, bool)
	// PropertyNames lists the properties of a named upstream interface or class.
	PropertyNames(name string) []string
	// Representation returns the documented Go spelling of an upstream name that has no named Go type of its own.
	Representation(name string) (string, bool)
	// Generic reports whether name is a type parameter in scope for the row.
	Generic(name string) bool
}

// PlacementRule extends the repository directories searched for the Go counterpart of an upstream package.
type PlacementRule interface {
	Name() string
	// Dirs returns directories (a trailing /... means the whole tree), best match first.
	Dirs(upstreamPackage string) []string
}

// ShapePlacementRule is a PlacementRule whose directories are not all homes of the package. ShapeDirs returns the ones that are:
// the engine accepts a unique shape match there without a name match (N5), so a directory that also holds another package's port
// is searched by name only.
type ShapePlacementRule interface {
	PlacementRule
	ShapeDirs(upstreamPackage string) []string
}

// NameRule decides that a Go name stands for an upstream name.
type NameRule interface {
	Name() string
	// Match returns the rule label (for example "N6") when goName stands for upstream, or "".
	Match(upstream, goName string) string
}

// Property is an upstream property row.
type Property struct {
	Name     string
	Type     string
	Optional bool
	// Calls are the property's call signatures when it is a function.
	Calls []CallShape
}

// MemberRule finds the Go field or method that stands for an upstream property when the name rules find none.
type MemberRule interface {
	Name() string
	// Member returns the Go member of owner, or nil with ok=false when the rule does not apply.
	Member(env Env, prop Property, owner *types.TypeName) (member types.Object, ok bool)
}

// TypeRule decides whether an upstream type text agrees with a Go type.
type TypeRule interface {
	Name() string
	Type(env Env, up string, t types.Type) (Verdict, bool)
}

// SignatureRule decides whether an upstream call shape agrees with a Go signature.
type SignatureRule interface {
	Name() string
	Signature(env Env, up CallShape, sig *types.Signature) (Verdict, bool)
}

// Family numbers follow the rules sprint plan.
type Family int

// The rule families.
const (
	Naming Family = iota + 1
	Optionality
	Unions
	Functions
	Placement
	DataShapes
)

var familyNames = map[Family]string{
	Naming: "naming", Optionality: "optionality", Unions: "unions", Functions: "functions", Placement: "placement", DataShapes: "data-shapes",
}

func (f Family) String() string { return familyNames[f] }

type entry[R interface{ Name() string }] struct {
	family Family
	rule   R
}

var (
	placements []entry[PlacementRule]
	names      []entry[NameRule]
	members    []entry[MemberRule]
	typeRules  []entry[TypeRule]
	signatures []entry[SignatureRule]
)

func add[R interface{ Name() string }](list *[]entry[R], f Family, r R) {
	if f < Naming || f > DataShapes {
		panic(fmt.Sprintf("rules: %q registered under an unknown family %d", r.Name(), f))
	}
	for _, e := range *list {
		if e.rule.Name() == r.Name() {
			panic(fmt.Sprintf("rules: duplicate rule name %q", r.Name()))
		}
	}
	*list = append(*list, entry[R]{f, r})
	sort.SliceStable(*list, func(i, j int) bool {
		a, b := (*list)[i], (*list)[j]
		return a.family < b.family || a.family == b.family && a.rule.Name() < b.rule.Name()
	})
}

// RegisterPlacement registers a placement rule for a family.
func RegisterPlacement(f Family, r PlacementRule) { add(&placements, f, r) }

// RegisterName registers a name rule for a family.
func RegisterName(f Family, r NameRule) { add(&names, f, r) }

// RegisterMember registers a member rule for a family.
func RegisterMember(f Family, r MemberRule) { add(&members, f, r) }

// RegisterType registers a type rule for a family.
func RegisterType(f Family, r TypeRule) { add(&typeRules, f, r) }

// RegisterSignature registers a signature rule for a family.
func RegisterSignature(f Family, r SignatureRule) { add(&signatures, f, r) }

// Dirs returns every registered placement rule's directories for an upstream package, in rule order, without repeats.
func Dirs(upstreamPackage string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range placements {
		for _, d := range e.rule.Dirs(upstreamPackage) {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}

// ShapeDirs returns the registered placement directories where a shape-only match may stand for an upstream symbol: ShapeDirs of a
// ShapePlacementRule, Dirs of any other rule.
func ShapeDirs(upstreamPackage string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range placements {
		dirs := e.rule.Dirs(upstreamPackage)
		if r, ok := e.rule.(ShapePlacementRule); ok {
			dirs = r.ShapeDirs(upstreamPackage)
		}
		for _, d := range dirs {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}

// MatchName returns the first registered name rule's label for the pair, or "".
func MatchName(upstream, goName string) string {
	for _, e := range names {
		if l := e.rule.Match(upstream, goName); l != "" {
			return l
		}
	}
	return ""
}

// FindMember returns the first Go member a registered member rule finds.
func FindMember(env Env, prop Property, owner *types.TypeName) (types.Object, string) {
	for _, e := range members {
		if m, ok := e.rule.Member(env, prop, owner); ok && m != nil {
			return m, e.rule.Name()
		}
	}
	return nil, ""
}

// CheckType returns the first deciding registered type rule's verdict.
func CheckType(env Env, up string, t types.Type) (Verdict, bool) {
	for _, e := range typeRules {
		if v, ok := e.rule.Type(env, up, t); ok && v.OK != Unknown {
			return v, true
		}
	}
	return Verdict{}, false
}

// CheckSignature returns the first deciding registered signature rule's verdict.
func CheckSignature(env Env, up CallShape, sig *types.Signature) (Verdict, bool) {
	for _, e := range signatures {
		if v, ok := e.rule.Signature(env, up, sig); ok && v.OK != Unknown {
			return v, true
		}
	}
	return Verdict{}, false
}

// Registered lists every registered rule as "family/name", sorted, for the conformance test and the report.
func Registered() []string {
	var out []string
	add := func(f Family, n string) { out = append(out, f.String()+"/"+n) }
	for _, e := range placements {
		add(e.family, e.rule.Name())
	}
	for _, e := range names {
		add(e.family, e.rule.Name())
	}
	for _, e := range members {
		add(e.family, e.rule.Name())
	}
	for _, e := range typeRules {
		add(e.family, e.rule.Name())
	}
	for _, e := range signatures {
		add(e.family, e.rule.Name())
	}
	sort.Strings(out)
	return out
}

// Isolate empties the registry and returns the function that restores it. Tests of the detector use it to register a rule
// without leaking it into other tests.
func Isolate() (restore func()) {
	p, n, m, t, s := placements, names, members, typeRules, signatures
	placements, names, members, typeRules, signatures = nil, nil, nil, nil, nil
	return func() { placements, names, members, typeRules, signatures = p, n, m, t, s }
}

// All combines verdicts: a refutation wins over an undecided rule, which wins over success.
func All(vs ...Verdict) Verdict {
	out := Verdict{OK: Yes}
	for _, v := range vs {
		switch {
		case v.OK == No:
			return v
		case v.OK == Unknown && out.OK == Yes:
			out = v
		}
	}
	return out
}
