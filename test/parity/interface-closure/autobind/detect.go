package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/test/parity/interface-closure/autobind/rules"
)

// Gap reasons. Every ID is either not a gap or a gap with exactly one of these.
const (
	reasonNoSymbol  = "no-go-symbol"
	reasonMember    = "member-missing"
	reasonSignature = "signature-mismatch"
	reasonType      = "type-mismatch"
	reasonExercise  = "not-exercised"
	reasonUndecided = "undecidable"
	reasonChild     = "child-gap"
	reasonCLI       = "cli-row"
	statusNotGap    = "not-a-gap"
	ruleDesignedOut = "designed-out in the ledger"
	ruleCLIReviewed = "reviewed command-line row"
)

// decision is the outcome for one interface ID.
type decision struct {
	ID        string
	Pkg       string
	Disp      string // ledger disposition
	Gap       bool
	Reason    string
	Detail    string
	Candidate string            // Go symbol the rules chose or came closest to
	Evidence  string            // how the row is exercised, for a row that is not a gap
	Sym       *sym              // the Go symbol of a row that is not a gap
	Info      *exerciseInfo     // what exercises it
	Held      string            // the holds.json reason that keeps the row pending, if any
	Layers    map[string]string // the layers a row that is not a gap claims complete, when they are not the default shape, production and behavior
	Targets   []string          // further Go symbols of a row that is not a gap
	// DesignedOut is the rationale of a row that no Go member can stand for because it is a JavaScript runtime mechanic (L1); the sync writes
	// the row as designed-out with it.
	DesignedOut string
}

func gap(reason, detail, candidate string) *decision {
	return &decision{Gap: true, Reason: reason, Detail: detail, Candidate: candidate}
}

// renameTable is the documented-rename input: upstream ID -> Go target (`file#Symbol` or `file#Owner.Member`).
type renameTable map[string]string

type detector struct {
	ix         *index
	l          *ledger
	reps       map[string]string        // documented Go representations, keyed pkg:Name
	narrow     map[string][]narrowEntry // reviewed narrow interfaces for a Pi class whose Go package would import-cycle, keyed pkg:Name (T9n)
	narrowMemo map[string]string
	forms      map[string][]string // reviewed Go types with every member of an upstream type that has its own Go form, keyed pkg:Name
	renames    renameTable
	typeRen    map[string]string // upstream type name -> Go type name, from renames of type rows
	aliases    aliasTable
	srcProps   map[string][]propShape // sourceProps by pkg:name
	// reviewed are the lead-approved type exceptions (reviewed-types.json): a row whose type no rule decides holds when its Go
	// member or type is the recorded one; used records which were applied.
	reviewed     map[string]reviewedType
	reviewedUsed map[string]bool
	top          map[string][]*sym
	shapeTop     map[string][]*sym
	dec          map[string]*decision
	bagCache     map[string]bool
	typeMemo     map[string]*types.TypeName
	typeBusy     map[string]bool
	shapeBusy    map[string]bool
	lib          *library                     // rule P1(b) tables and mutation records (library.go); built on first use when nil
	row          *upstreamEntry               // the property row whose member is being exercised, for rule P1(b)
	srcText      []string                     // the pinned upstream package sources, read on first use by rule CC (rulings.go)
	placements   map[string]reviewedPlacement // rule RP's reviewed placements (placements-reviewed.json), keyed pkg:Name
}

func newDetector(ix *index, l *ledger, renames renameTable, aliases aliasTable) *detector {
	d := &detector{ix: ix, l: l, renames: renames, typeRen: map[string]string{}, aliases: aliases, top: map[string][]*sym{}, shapeTop: map[string][]*sym{}, dec: map[string]*decision{}, bagCache: map[string]bool{}, typeMemo: map[string]*types.TypeName{}, typeBusy: map[string]bool{}, shapeBusy: map[string]bool{}}
	for id, target := range renames {
		e := l.byID[id]
		if e == nil || (e.Kind != "interface" && e.Kind != "class" && e.Kind != "type-alias") || strings.Contains(id, "::") {
			continue
		}
		_, name, _ := strings.Cut(target, "#")
		d.typeRen[e.Name] = name
	}
	return d
}

func (d *detector) topSyms(pkg string) []*sym {
	if s, ok := d.top[pkg]; ok {
		return s
	}
	s := d.ix.topLevel(d.ix.dirsFor(pkg))
	d.top[pkg] = s
	return s
}

// shapeSyms are the top-level declarations a shape-only match (N5) may choose: topSyms without the placement directories that
// also hold another package's port.
func (d *detector) shapeSyms(pkg string) []*sym {
	if s, ok := d.shapeTop[pkg]; ok {
		return s
	}
	allowed := map[string]bool{}
	for _, dir := range d.ix.shapeDirsFor(pkg) {
		allowed[dir] = true
	}
	var s []*sym
	for _, sym := range d.topSyms(pkg) {
		if allowed[sym.Dir] {
			s = append(s, sym)
		}
	}
	d.shapeTop[pkg] = s
	return s
}

// generics collects the type parameters in scope of a row: its own, its calls' and its ancestors'.
func (d *detector) generics(e *upstreamEntry) map[string]bool {
	g := map[string]bool{}
	for cur := e; cur != nil; cur = d.l.byID[cur.ParentID] {
		for _, t := range cur.Shape.TypeParameters {
			g[t.Name] = true
		}
		for _, c := range append(append([]callShape{}, cur.Shape.Calls...), cur.Shape.Constructs...) {
			for _, t := range c.TypeParameters {
				g[t.Name] = true
			}
		}
	}
	return g
}

// typeParamsOf returns the type parameter names of the upstream interface or class name, preferring package pkg's declaration.
func (d *detector) typeParamsOf(pkg, name string) []string {
	var first []string
	found := false
	for _, e := range d.l.entries {
		if e.Name != name || e.Kind != "interface" && e.Kind != "class" {
			continue
		}
		var out []string
		for _, tp := range e.Shape.TypeParameters {
			out = append(out, tp.Name)
		}
		if parseID(e.ID).Pkg == pkg {
			return out
		}
		if !found {
			first, found = out, true
		}
	}
	return first
}

func (d *detector) checker(e *upstreamEntry) *checker {
	return &checker{renames: d.typeRen, generics: d.generics(e), aliases: d.aliases, pkg: parseID(e.ID).Pkg, bags: d.signalBag, reps: d.reps,
		resolve: func(name string) *types.TypeName { return d.typeFor(parseID(e.ID).Pkg, name) }, propNames: d.propNames,
		discriminates: d.writesLiteral, forms: d.forms, narrow: d.narrowAccepts, props: func(name string) []propShape { return d.propShapes(parseID(e.ID).Pkg, name) },
		ctxConsumers: d.ix.ctxConsumers, typeParams: func(name string) []string { return d.typeParamsOf(parseID(e.ID).Pkg, name) }}
}

// signalBag reports whether name is an upstream interface whose only property is an AbortSignal.
func (d *detector) signalBag(name string) bool {
	if v, ok := d.bagCache[name]; ok {
		return v
	}
	res := false
	for _, e := range d.l.entries {
		if e.Name == name && (e.Kind == "interface") && e.Role == "" {
			props := d.l.properties(e.ID)
			res = len(props) > 0
			for _, p := range props {
				if !isSignalType(p.Shape.Type) {
					res = false
				}
			}
			break
		}
	}
	d.bagCache[name] = res
	return res
}

// candidates lists the Go declarations that may stand for a top-level upstream row: the documented rename when there is one,
// otherwise every declaration of the right kind whose name satisfies N1 or N2, in seed order. An upstream row is public API, so
// when an exported declaration satisfies the name rules an unexported one is not a candidate (N5): the fewest-gaps choice must not
// replace a public Go symbol with an internal one that happens to fit better.
func (d *detector) candidates(e *upstreamEntry, pkg string, kinds ...string) []*sym {
	if target, ok := d.renames[e.ID]; ok {
		file, name, _ := strings.Cut(target, "#")
		dir := file[:max(strings.LastIndex(file, "/"), 0)]
		if _, ok := d.ix.pkgs[dir]; ok {
			var out []*sym
			for _, s := range d.ix.topLevel([]string{dir}) {
				if s.Name == name && s.File == file {
					c := *s
					c.Tier = ruleRename
					out = append(out, &c)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
		// A documented rename whose Go target no longer exists does not hide the declaration the name rules find.
	}
	var out []*sym
	for _, s := range d.topSyms(pkg) {
		if !contains(kinds, s.Kind) {
			continue
		}
		if rule := nameRule(e.Name, s.Name); rule != "" {
			c := *s
			c.Tier = rule
			out = append(out, &c)
		}
	}
	if slices.ContainsFunc(out, func(s *sym) bool { return token.IsExported(s.Name) }) {
		out = slices.DeleteFunc(out, func(s *sym) bool { return !token.IsExported(s.Name) })
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Tier == ruleExact) != (out[j].Tier == ruleExact) {
			return out[i].Tier == ruleExact
		}
		return out[i].Rank < out[j].Rank
	})
	return out
}

func contains(list []string, s string) bool {
	return slices.Contains(list, s)
}

func selfExclude(name string) func(string) bool {
	return func(caller string) bool { return caller == name }
}

func typeExclude(name string) func(string) bool {
	return func(caller string) bool { return caller == name || strings.HasPrefix(caller, name+".") }
}

// exerciseInfo is what exercises a symbol: asserting tests (best first) and the reachable function that uses it.
type exerciseInfo struct {
	Tests []string // test:<file>#<Test>, at most three
	Call  string   // a function reachable from cmd/pig that is, or uses, the symbol
}

// exercised applies E1 and E2: a Go test that references the symbol and asserts, or a use reachable from cmd/pig.
func (d *detector) exercised(s *sym, keys []string, exclude func(string) bool) (*exerciseInfo, string) {
	info := &exerciseInfo{}
	unreached := ""
	tests := d.ix.testsFor(keys)
	for _, t := range tests {
		if t.Asserts && len(info.Tests) < 3 {
			info.Tests = append(info.Tests, t.Ref)
		}
	}
	if (s.Kind == "func" || s.Kind == "method") && d.ix.reach[s.File+"#"+s.Qualified()] {
		info.Call = s.File + "#" + s.Qualified()
	} else if reachable, unreachable := d.ix.prodFor(keys, s.Dir, exclude); len(reachable) > 0 {
		info.Call = reachable[0]
	} else if len(unreachable) > 0 {
		unreached = unreachable[0]
	}
	if (s.Kind == "field" || s.Kind == "method") && !d.testSupportMember(s) {
		// P1: a field or method closes a row when production code reachable from cmd/pig reads, sets or calls it (a), or through the
		// library route (b) in library.go; a test alone leaves a caller-free stub looking finished.
		if info.Call != "" {
			return info, ""
		}
		lib, libWhy := d.libraryMember(s, keys, tests) // P1(b): the library route, library.go
		if lib != nil {
			return lib, ""
		}
		if unreached != "" {
			return nil, "P1: production code uses " + s.Target() + " only in " + unreached + ", which no path from cmd/pig reaches (static reach: dynamic dispatch is not followed); P1(b) " + libWhy
		}
		return nil, "P1: no production code reads, sets or calls " + s.Target() + " (a test alone does not close a member); P1(b) " + libWhy
	}
	if len(info.Tests) > 0 || info.Call != "" {
		return info, ""
	}
	if len(tests) > 0 {
		return nil, "tests reference " + s.Target() + " but none asserts, and no caller is reachable from cmd/pig"
	}
	return nil, "no test references " + s.Target() + " and no caller is reachable from cmd/pig"
}

// testSupportDir reports whether dir is a test-support package: the Go form of an upstream `testing` subpath (routingtest, durabletest,
// mcptest, telemetrytest) is a directory whose name ends in "test".
func testSupportDir(dir string) bool { return strings.HasSuffix(path.Base(dir), "test") }

// testSupportMember reports whether P1 is waived for s: the row being decided belongs to an upstream `testing` subpath and s lives in
// a test-support package. Such members exist to be driven by other packages' tests and no production path reaches them, so an
// asserting test that uses the member closes it. A row of any other entry point keeps P1 even when its Go form is a test-support
// type (extensiontest.Fake standing in for ExtensionAPI, for example).
func (d *detector) testSupportMember(s *sym) bool {
	return d.row != nil && parseID(d.row.ID).Entry == "testing" && testSupportDir(s.Dir)
}

// exercise decides the exercise step for a symbol: not a gap with the evidence, or the not-exercised gap.
func (d *detector) exercise(s *sym, keys []string, exclude func(string) bool) *decision {
	info, why := d.exercised(s, keys, exclude)
	return settle(s, info, why)
}

func settle(s *sym, info *exerciseInfo, why string) *decision {
	if why != "" {
		return gap(reasonExercise, why, s.Target())
	}
	ev := "call:" + info.Call
	if len(info.Tests) > 0 {
		ev = info.Tests[0]
	}
	return &decision{Candidate: s.Target(), Evidence: ev, Sym: s, Info: info}
}

// exerciseKeys are the declarations whose references exercise a type (rule E3): the type, its fields, its methods, its New<Name>
// constructor and the typed constants of its package, because a test that builds a value through a constructor or compares one of its constants reaches the
// type without naming it.
func (d *detector) exerciseKeys(tn *types.TypeName, dir string) (keys []string, reachable bool) {
	keys = d.ix.typeKeys(tn)
	for _, m := range d.ix.members(tn, dir, 0) {
		if m.Kind == "method" {
			keys = append(keys, m.Key)
			reachable = reachable || d.ix.reach[m.File+"#"+m.Qualified()]
		}
	}
	if pkg := tn.Pkg(); pkg != nil {
		for _, name := range pkg.Scope().Names() {
			switch o := pkg.Scope().Lookup(name).(type) {
			case *types.Const:
				if types.Identical(o.Type(), tn.Type()) {
					keys = append(keys, d.ix.declKey(o))
				}
			case *types.Func:
				if ctorRule(tn.Name(), o.Name()) != "" {
					keys = append(keys, d.ix.declKey(o))
					reachable = reachable || d.ix.reach[d.ix.file(o)+"#"+o.Name()]
				}
			}
		}
	}
	return keys, reachable
}

// exerciseOfType is the exercise step for a type row (rule E3).
func (d *detector) exerciseOfType(c *sym, tn *types.TypeName) *decision {
	keys, reachable := d.exerciseKeys(tn, c.Dir)
	info, why := d.exercised(c, keys, typeExclude(c.Name))
	if why != "" && reachable {
		info, why = &exerciseInfo{Call: c.File + "#" + c.Qualified()}, ""
	}
	return settle(c, info, why)
}

// decide returns the decision for id, computing the whole declaration it belongs to on first use.
func (d *detector) decide(id string) *decision {
	if r, ok := d.dec[id]; ok {
		return r
	}
	top := topID(id)
	if topRow := d.l.byID[top]; topRow != nil && id != top {
		d.decide(top)
		if r, ok := d.dec[id]; ok {
			return r
		}
	}
	d.evalTop(top)
	if r, ok := d.dec[id]; ok {
		return r
	}
	d.set(id, gap(reasonUndecided, "row is not reachable from its declaration", ""))
	return d.dec[id]
}

// setCLI decides a command-line row. No Go package symbol rule applies to a flag, so the row is a gap unless the ledger holds a reviewed
// hand-written closure: a ported row with Go targets, production reachability and evidence whose Go references still name declarations.
func (d *detector) setCLI(id string) {
	row := d.l.mapping[id]
	if row != nil && row.Disposition == "ported" && len(row.Targets) > 0 && len(row.Production) > 0 && len(row.Evidence) > 0 {
		resolves := true
		for _, ref := range append(slices.Clone(row.Targets), row.Production...) {
			ref = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(ref, "call:"), "tested:"), "public-api:")
			if file, frag, ok := strings.Cut(ref, "#"); ok && strings.HasSuffix(file, ".go") && !d.l.declares(file, frag) {
				resolves = false
			}
		}
		if resolves {
			d.set(id, &decision{Evidence: ruleCLIReviewed})
			return
		}
	}
	d.set(id, gap(reasonCLI, "command-line row: no reviewed closure (parser, help, consumer, behavior) in the ledger", ""))
}

func (d *detector) set(id string, r *decision) {
	if row := d.l.mapping[id]; row != nil && row.Disposition == "designed-out" {
		r = &decision{Evidence: ruleDesignedOut} // a recorded decision that Pig has no counterpart
	}
	r.ID = id
	r.Pkg = parseID(id).Pkg
	if strings.HasPrefix(id, "cli:") {
		r.Pkg = "cli"
	}
	if row := d.l.mapping[id]; row != nil {
		r.Disp = row.Disposition
	}
	d.dec[id] = r
}

// evalTop decides a declaration row and every row below it (calls, properties, constructors).
func (d *detector) evalTop(top string) {
	e := d.l.byID[top]
	parts := parseID(top)
	if e == nil {
		return
	}
	if parts.IsCLI {
		d.setCLI(top)
		return
	}
	if row := d.l.mapping[top]; row != nil && row.Disposition == "designed-out" {
		d.set(top, &decision{Evidence: ruleDesignedOut})
		d.designedOutChildren(top)
		return
	}
	var kinds []string
	switch e.Kind {
	case "function":
		kinds = []string{"func"}
	case "variable":
		kinds = []string{"func", "const", "var"}
	case "interface", "class", "type-alias":
		kinds = []string{"type"}
	default:
		d.setTree(top, gap(reasonUndecided, "external type: not part of the Pi API", ""))
		return
	}
	cands := d.candidates(e, parts.Pkg, kinds...)
	if len(cands) == 0 {
		var ambiguous []string
		if _, renamed := d.renames[e.ID]; !renamed {
			cands, ambiguous = d.shapeCandidates(e, parts.Pkg)
		}
		if len(ambiguous) > 0 {
			d.setTree(top, gap(reasonUndecided, "N5: the shape matches several Go declarations: "+strings.Join(firstN(ambiguous, 4), ", "), ""))
			return
		}
	}
	if len(cands) == 0 && (d.designedOutTop(e) || d.placedTop(e)) {
		return // TG, TR, RP (rulings.go)
	}
	if len(cands) == 0 {
		d.setTree(top, gap(reasonNoSymbol, "no Go "+strings.Join(kinds, "/")+" named "+e.Name+" (N1, N2) and no documented rename", ""))
		return
	}
	var best map[string]*decision
	bestGaps := -1
	for _, c := range cands {
		out := d.evalWith(e, parts, c)
		n := 0
		for _, r := range out {
			if r.Gap {
				n++
			}
		}
		if bestGaps < 0 || n < bestGaps {
			best, bestGaps = out, n
		}
		if n == 0 {
			break
		}
	}
	for id, r := range best {
		d.set(id, r)
	}
	d.fillMissing(top, best[top])
}

// fillMissing marks descendants the evaluation did not reach as gaps below their owner.
func (d *detector) fillMissing(id string, owner *decision) {
	for _, ch := range d.l.children[id] {
		if _, ok := d.dec[ch.ID]; !ok {
			cand := ""
			detail := "owner row was not decided"
			if owner != nil {
				cand, detail = owner.Candidate, "owner is a gap: "+owner.Reason+": "+owner.Detail
			}
			d.set(ch.ID, gap(reasonChild, detail, cand))
		}
		d.fillMissing(ch.ID, d.dec[ch.ID])
	}
}

func (d *detector) designedOutChildren(top string) {
	for _, c := range d.l.children[top] {
		d.set(c.ID, &decision{Evidence: ruleDesignedOut})
		d.designedOutChildren(c.ID)
	}
}

// setTree gives a row and all its descendants the same gap.
func (d *detector) setTree(id string, r *decision) {
	c := *r
	d.set(id, &c)
	for _, ch := range d.l.children[id] {
		d.setTree(ch.ID, gap(reasonChild, "owner is a gap: "+r.Reason, r.Candidate))
	}
}

// evalWith decides the declaration and its descendants against one candidate Go symbol.
func (d *detector) evalWith(e *upstreamEntry, parts idParts, c *sym) map[string]*decision {
	out := map[string]*decision{}
	chk := d.checker(e)
	switch e.Kind {
	case "function", "variable":
		if c.Kind == "func" && e.Kind == "variable" && len(e.Shape.Calls) == 0 {
			d.evalValueFunc(e, c, chk, out)
			return out
		}
		if c.Kind == "func" {
			d.evalFunc(e, c, chk, out)
			return out
		}
		if len(e.Shape.Calls) > 0 {
			out[e.ID] = gap(reasonType, "upstream is callable, Go symbol is a "+c.Kind, c.Target())
			return out
		}
		d.evalValue(e, c, chk, out)
	case "interface", "class":
		d.evalType(e, parts, c, chk, out)
	case "type-alias":
		d.evalAlias(e, c, chk, out)
	}
	return out
}

// refute turns a refuting or undecided verdict into its gap; it returns nil for a verdict that holds.
func refute(v verdict, ifNo, target string) *decision {
	switch v.ok {
	case no:
		return gap(ifNo, v.why, target)
	case unknown:
		return gap(reasonUndecided, v.why, target)
	}
	return nil
}

func okDecision(c *sym, evidence string) *decision {
	return &decision{Candidate: c.Target(), Evidence: evidence}
}

// foldChildren turns an owner that is itself fine into a child-gap when a descendant is a gap.
func (d *detector) foldChildren(e *upstreamEntry, r *decision, out map[string]*decision) {
	if r.Gap {
		return
	}
	for _, ch := range d.l.children[e.ID] {
		if row := d.l.mapping[ch.ID]; row != nil && row.Disposition == "designed-out" {
			continue
		}
		if cr := out[ch.ID]; cr != nil && cr.Gap {
			*r = *gap(reasonChild, "a row below it is a gap, for example "+ch.ID+" ("+cr.Reason+")", r.Candidate)
			return
		}
	}
}

// evalFunc applies existence (N), signature (S) to each call overload, and exercise (E).
func (d *detector) evalFunc(e *upstreamEntry, c *sym, chk *checker, out map[string]*decision) {
	sig := c.Obj.Type().(*types.Signature)
	self := d.exercise(c, []string{c.Key}, selfExclude(c.Name))
	out[e.ID] = self
	for _, row := range d.l.callRows(e.ID) {
		// OV1: an overload renamed to another Go function (replicatedState(source, options) and replicatedState(initial)) is judged against that
		// function and its own exercise.
		if target, ok := d.renames[row.ID]; ok {
			if m := d.renamedMember(target, e.Name); m != nil {
				if fn, isFunc := m.Obj.(*types.Func); isFunc {
					d.row = e
					own := d.exercise(m, []string{m.Key}, selfExclude(m.Qualified()))
					d.row = nil
					out[row.ID] = d.callDecision(withoutLiteralLead(row), m, fn.Type().(*types.Signature), own)
					continue
				}
			}
		}
		out[row.ID] = d.callDecision(row, c, sig, self)
	}
	d.foldChildren(e, self, out)
}

// callDecision judges one call overload against a Go signature; it is exercised exactly when its owner is.
func (d *detector) callDecision(row *upstreamEntry, c *sym, sig *types.Signature, owner *decision) *decision {
	v := d.reviewedVerdict(row.ID, c.Target(), d.callVerdict(row, sig, d.errorUnionPinned(row.Shape.Returns, c)))
	if g := refute(v, reasonSignature, c.Target()); g != nil {
		return g
	}
	if owner.Gap && owner.Reason == reasonExercise {
		return gap(reasonExercise, owner.Detail, c.Target())
	}
	if strings.Contains(row.ID, "#ExtensionAPI::property:on::call:") && !d.handPorted(row.ID) {
		// The eleven extension API layers of an `on` overload are written by hand: no rule derives a ported row for one, so the rule
		// cannot close the row, and the owner it would close stays a child-gap.
		return gap(reasonUndecided, "ExtensionAPI.on overload: its ported row carries the extension API layers, which a rule cannot derive", c.Target())
	}
	r := okDecision(c, owner.Evidence)
	r.Sym, r.Info = owner.Sym, owner.Info
	return r
}

// handPorted reports whether the mapping holds a hand-written ported row for id: one the detector did not derive.
func (d *detector) handPorted(id string) bool {
	row := d.l.mapping[id]
	return row != nil && row.Disposition == "ported" && !strings.HasPrefix(row.Rationale, derivedPrefix)
}

func (d *detector) evalValue(e *upstreamEntry, c *sym, chk *checker, out map[string]*decision) {
	up := e.Shape.Type
	if s := chk.aliases[satisfiesKey+parseID(e.ID).Pkg][e.Name]; s != nil {
		up = *s // V3: `as const satisfies X` narrows the literal at compile time only; the declared contract is X
	}
	v := valueVerdict(chk, up, c.Obj)
	r := refute(v, reasonType, c.Target())
	if r == nil {
		r = d.exercise(c, []string{c.Key}, selfExclude(c.Name))
	}
	out[e.ID] = r
}

// evalValueFunc applies V3: an upstream value that is not callable may be a Go function with no parameters (or only a context)
// whose first result has the value's type. A Go function that takes parameters is a different shape.
func (d *detector) evalValueFunc(e *upstreamEntry, c *sym, chk *checker, out map[string]*decision) {
	sig := c.Obj.Type().(*types.Signature)
	var v verdict
	if keyedCatalog(e.Shape.Type, sig) {
		v = yesV() // V4
	} else if n := sig.Params().Len(); n > 1 || n == 1 && !isContext(sig.Params().At(0).Type()) {
		v = noV("V3: upstream value, Go function %s takes parameters", c.Name)
	} else if sig.Results().Len() == 0 {
		v = noV("V3: upstream value, Go function %s returns nothing", c.Name)
	} else {
		v = chk.agree(e.Shape.Type, sig.Results().At(0).Type())
	}
	r := refute(v, reasonType, c.Target())
	if r == nil {
		r = d.exercise(c, []string{c.Key}, selfExclude(c.Name))
	}
	out[e.ID] = r
}

// valueVerdict applies V1 (a literal upstream value equals the Go constant) and V2 (otherwise the type rules).
func valueVerdict(chk *checker, up string, obj types.Object) verdict {
	up = strings.TrimSpace(up)
	if cst, ok := obj.(*types.Const); ok {
		switch {
		case stringLit.MatchString(up):
			if cst.Val().Kind() == constant.String && constant.StringVal(cst.Val()) == decodeTSString(up[1:len(up)-1]) {
				return yesV()
			}
			return noV("V1: constant %s differs from upstream %s", cst.Val().ExactString(), up)
		case numberLit.MatchString(up):
			if cst.Val().ExactString() == up {
				return yesV()
			}
			return noV("V1: constant %s differs from upstream %s", cst.Val().ExactString(), up)
		}
	}
	return chk.agree(up, obj.Type())
}

// evalType applies member (M) and exercise (E) rules to an interface or class.
func (d *detector) evalType(e *upstreamEntry, parts idParts, c *sym, chk *checker, out map[string]*decision) {
	tn, ok := c.Obj.(*types.TypeName)
	if !ok {
		out[e.ID] = gap(reasonType, "Go symbol is not a type", c.Target())
		return
	}
	self := d.exerciseOfType(c, tn)
	out[e.ID] = self
	members := d.ix.members(tn, c.Dir, c.Rank)
	for _, ch := range d.l.children[e.ID] {
		switch ch.Role {
		case "property":
			d.evalProperty(ch, parts, c, members, out)
		case "construct-overload":
			d.evalConstruct(ch, parts, c, out)
		}
	}
	d.foldChildren(e, self, out)
}

func (d *detector) memberFor(prop *upstreamEntry, members []*sym) *sym {
	name := prop.Shape.Name
	if target, ok := d.renames[prop.ID]; ok {
		if m := d.renamedMember(target, name); m != nil {
			return m
		}
		// A documented rename whose Go owner no longer exists (the type was renamed since) does not hide the member the name rules find.
	}
	if names := symbolMemberNames(name); names != nil {
		// N6: a symbol-keyed member `[Symbol.X]` has no Go spelling; its Go form is the method named for the symbol.
		for _, m := range members {
			if m.Kind == "method" && slices.Contains(names, m.Name) {
				return m
			}
		}
		return nil
	}
	var folded, tagged *sym
	for _, m := range members {
		switch {
		case m.Name == name || m.Name == upperFirst(name):
			return m
		case m.JSON == name && tagged == nil:
			tagged = m
		case folded == nil && nameRule(name, m.Name) == ruleFolded:
			folded = m
		}
	}
	if tagged != nil {
		return tagged
	}
	return folded
}

// renamedMember resolves a documented member rename `file#Owner.Member` to the Go field or method, wherever its owner lives.
func (d *detector) renamedMember(target, prop string) *sym {
	file, qual, _ := strings.Cut(target, "#")
	owner, name, ok := strings.Cut(qual, ".")
	if !ok {
		// The documented target is the owning type alone: the member is found in it by the name rules.
		owner, name = qual, ""
	}
	dir := file[:max(strings.LastIndex(file, "/"), 0)]
	if _, ok := d.ix.pkgs[dir]; !ok {
		return nil
	}
	for _, s := range d.ix.topLevel([]string{dir}) {
		if name == "" && s.Name == owner && s.Kind == "func" {
			// G1: a generic upstream method is a package function of the owner's package whose first parameter is the receiver, because
			// a Go method cannot have type parameters.
			return s
		}
		if s.Kind != "type" || s.Name != owner {
			continue
		}
		for _, m := range d.ix.members(s.Obj.(*types.TypeName), s.Dir, s.Rank) {
			if name != "" && m.Name == name || name == "" && (m.Name == prop || m.Name == upperFirst(prop) || m.JSON == prop || nameRule(prop, m.Name) != "") {
				return m
			}
		}
	}
	return nil
}

func (d *detector) evalProperty(prop *upstreamEntry, parts idParts, owner *sym, members []*sym, out map[string]*decision) {
	if prop.Shape.Name == "handleInput" && !d.hasBehaviorContract(prop.ID) {
		// B1: handleInput is an input state machine; the ledger requires a reviewed state-transition contract that no rule derives.
		g := gap(reasonUndecided, "B1: handleInput needs a state-transition contract in test/parity/behavior-contracts.toml", owner.Target())
		out[prop.ID] = g
		for _, c := range d.l.callRows(prop.ID) {
			out[c.ID] = gap(reasonChild, "owner is a gap: "+reasonUndecided, owner.Target())
		}
		return
	}
	m := d.memberFor(prop, members)
	if isSignalType(prop.Shape.Type) {
		// M4: a property whose type is an AbortSignal (possibly optional) is the context.Context argument of the Go call that
		// takes the options. A function or object type that merely mentions AbortSignal is judged by the member rules.
		self := d.exercise(owner, []string{owner.Key}, typeExclude(owner.Name))
		out[prop.ID] = self
		return
	}
	if m == nil {
		m = d.registeredMember(prop, owner, members)
	}
	if m == nil {
		m = d.placedMember(prop) // RP (rulings.go)
	}
	if why := ""; m == nil {
		if why = d.placedDesignedOut(prop); why != "" {
			out[prop.ID] = &decision{Candidate: owner.Target(), Evidence: ruleDesignedOut, DesignedOut: why}
			for _, c := range d.l.callRows(prop.ID) {
				out[c.ID] = &decision{Candidate: owner.Target(), Evidence: ruleDesignedOut, DesignedOut: why}
			}
			return
		}
	}
	if m == nil && d.discriminator(prop, owner) {
		// M5: a string-literal property is the Go type's JSON discriminator when the type marshals itself and a parameterless method returns that literal.
		out[prop.ID] = d.exercise(owner, []string{owner.Key}, typeExclude(owner.Name))
		return
	}
	if m == nil && prop.Shape.Optional && stringAnyMap(owner) {
		// M7: an upstream interface of optional members that Go declares as map[string]any (a bag sent verbatim) carries every member as a key.
		out[prop.ID] = d.exercise(owner, []string{owner.Key}, typeExclude(owner.Name))
		return
	}
	if m == nil && prop.SourcePath == nodeEventsPath {
		// L1e: a member a class inherits from Node's EventEmitter (events.d.ts), not one the class declares: Go carries no emitter base class.
		dec := &decision{Candidate: owner.Target(), Evidence: ruleDesignedOut, DesignedOut: eventEmitterRationale(prop.Shape.Name, owner.Name)}
		out[prop.ID] = dec
		for _, c := range d.l.callRows(prop.ID) {
			own := *dec // a decision carries its row's ID: rows must not share one
			out[c.ID] = &own
		}
		return
	}
	if m == nil && d.errorRuntimeMember(prop) {
		// L1: name, stack and an unset cause are members every JavaScript Error carries; a Go error is identified by its type.
		out[prop.ID] = &decision{Candidate: owner.Target(), Evidence: ruleDesignedOut, DesignedOut: errorRuntimeRationale(prop.Shape.Name)}
		for _, c := range d.l.callRows(prop.ID) {
			out[c.ID] = &decision{Candidate: owner.Target(), Evidence: ruleDesignedOut, DesignedOut: errorRuntimeRationale(prop.Shape.Name)}
		}
		return
	}
	if m == nil && d.phantomBrand(prop) {
		// T12p: an optional property keyed by a `declare const X: unique symbol` carries a type parameter for the compiler only; nothing sets or
		// reads it at run time, and a Go type parameter carries the same type.
		dec := &decision{Candidate: owner.Target(), Evidence: ruleDesignedOut, DesignedOut: phantomBrandRationale(prop.Shape.Name)}
		out[prop.ID] = dec
		for _, c := range d.l.callRows(prop.ID) {
			own := *dec // a decision carries its row's ID: rows must not share one
			out[c.ID] = &own
		}
		return
	}
	if d.evalRenamedOverloads(prop, owner, out, m) {
		return
	}
	if m == nil && d.accessorPair(prop, owner, members, out) {
		return // AP (rulings.go)
	}
	if m == nil {
		if keys := d.registryKeys(prop, owner); len(keys) == 1 {
			// M6: a property of a key registry is the constant of the Go key type whose value is the property name. The row is a member
			// row, so P1 applies to the constant as to a field: production code reachable from cmd/pig must use it, a test alone does not.
			key := keys[0]
			info, why := d.exercised(key, []string{key.Key}, selfExclude(key.Qualified()))
			if why == "" && info.Call == "" {
				why = "P1: no production code uses the key constant " + key.Target() + " (a test alone does not close a registry key)"
			}
			out[prop.ID] = settle(key, info, why)
			return
		} else if len(keys) > 1 {
			names := make([]string, len(keys))
			for i, k := range keys {
				names[i] = k.Target()
			}
			out[prop.ID] = gap(reasonMember, "M6: the key "+prop.Shape.Name+" has "+strconv.Itoa(len(keys))+" constants ("+strings.Join(names, ", ")+"); one key has one constant", owner.Target())
			return
		}
		out[prop.ID] = gap(reasonMember, "no field or method for property "+prop.Shape.Name+" in "+owner.Target()+" (N1, N2, json tag)", owner.Target())
		for _, c := range d.l.callRows(prop.ID) {
			out[c.ID] = gap(reasonMember, "property has no Go member", owner.Target())
		}
		return
	}
	typeV, sig := d.memberShape(prop, m)
	sigV := yesV()
	if sig != nil && len(d.l.callRows(prop.ID)) == 0 && optionalFuncType(prop.Shape.Type) {
		// M3: the inventory records an optional function property without a call row; its function type is the signature.
		propChk := d.checker(prop)
		if fn, isFn := parseFuncType(unparen(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(prop.Shape.Type), "| undefined")))); isFn {
			propChk.errUnionPinned = d.errorUnionPinned(fn.Returns, m)
		}
		sigV = d.reviewedVerdict(prop.ID, m.Target(), propChk.agree(prop.Shape.Type, sig))
		if call, ok := parseFuncType(unparen(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(prop.Shape.Type), "| undefined")))); sigV.ok != yes && ok {
			if w, folded := d.foldedSignature(prop, call, sig); folded {
				sigV = w // RP folds (rulings.go)
			}
		}
	}
	self := refute(typeV, reasonType, m.Target())
	if self == nil {
		self = refute(sigV, reasonSignature, m.Target())
	}
	if self == nil {
		d.row = prop
		self = d.exercise(m, []string{m.Key}, selfExclude(m.Qualified()))
		d.row = nil
		if why := ""; self.Gap && self.Reason == reasonExercise {
			if why = d.carriedWithoutConsumer(prop, m); why == "" {
				why = d.inheritedErrorStub(prop, m) // ER (rulings.go)
			}
			if why != "" {
				self = &decision{Candidate: m.Target(), Evidence: ruleDesignedOut, DesignedOut: why} // CC (rulings.go)
			}
		}
	}
	out[prop.ID] = self
	for _, cr := range d.l.callRows(prop.ID) {
		if sig == nil {
			out[cr.ID] = gap(reasonType, "upstream property is callable, Go member is not", m.Target())
			continue
		}
		out[cr.ID] = d.callDecision(cr, m, sig, self)
	}
	d.foldChildren(prop, self, out)
}

// accessorVerdict applies M2: data upstream may be a Go accessor with no parameters returning the value.
func accessorVerdict(chk *checker, up string, sig *types.Signature) verdict {
	n := sig.Params().Len()
	if n > 1 || n == 1 && !isContext(sig.Params().At(0).Type()) {
		return noV("M2: upstream data property, Go method takes parameters")
	}
	if sig.Results().Len() == 0 {
		return noV("M2: upstream data property, Go method returns nothing")
	}
	return chk.agree(up, sig.Results().At(0).Type())
}

func (d *detector) evalConstruct(row *upstreamEntry, parts idParts, owner *sym, out map[string]*decision) {
	var found *sym
	if target, ok := d.renames[row.ID]; ok {
		file, name, _ := strings.Cut(target, "#")
		dir := file[:max(strings.LastIndex(file, "/"), 0)]
		if _, ok := d.ix.pkgs[dir]; ok {
			for _, s := range d.ix.topLevel([]string{dir}) {
				if s.File == file && s.Name == name {
					found = s
				}
			}
		}
		if found != nil && found.Kind == "type" {
			// A documented construction by composite literal: the type itself is the constructor.
			r := d.exerciseOfType(found, found.Obj.(*types.TypeName))
			out[row.ID] = r
			return
		}
	} else {
		for _, s := range d.ix.topLevel(append([]string{owner.Dir}, d.ix.dirsFor(parts.Pkg)...)) {
			if s.Kind == "func" && ctorRule(parts.Name, s.Name) != "" {
				found = s
				break
			}
		}
	}
	if found == nil {
		out[row.ID] = gap(reasonNoSymbol, "no New"+parts.Name+" constructor function (N3)", owner.Target())
		return
	}
	chk := d.checker(row)
	v := d.reviewedVerdict(row.ID, found.Target(), chk.signature(callShape{Parameters: row.Shape.Parameters, Returns: row.Shape.Returns}, found.Obj.Type().(*types.Signature)))
	r := refute(v, reasonSignature, found.Target())
	if r == nil {
		r = d.exercise(found, []string{found.Key}, selfExclude(found.Name))
	}
	out[row.ID] = r
}

// evalAlias applies A1-A4 to a type alias.
func (d *detector) evalAlias(e *upstreamEntry, c *sym, chk *checker, out map[string]*decision) {
	tn, ok := c.Obj.(*types.TypeName)
	if !ok {
		out[e.ID] = gap(reasonType, "Go symbol is not a type", c.Target())
		return
	}
	var shapeV verdict
	if rows := d.l.callRows(e.ID); len(rows) > 0 {
		sig := rules.FuncSignature(tn.Type()) // S11: a Go func type, or an interface with the one method, is the callback
		if sig == nil {
			sig = identityListener(types.NewPointer(tn.Type())) // T7w: a listener wrapper that gives the callback the identity a JavaScript function has
		}
		if sig == nil {
			out[e.ID] = gap(reasonType, "A0: upstream alias is a function type, Go type is "+typeLabel(tn.Type()), c.Target())
			return
		}
		self := d.exerciseOfType(c, tn)
		out[e.ID] = self
		for _, r := range rows {
			out[r.ID] = d.callDecision(r, c, sig, self)
		}
		d.foldChildren(e, self, out)
		return
	}
	shapeV = d.reviewedVerdict(e.ID, c.Target(), d.aliasVerdict(e, tn, chk))
	r := refute(shapeV, reasonType, c.Target())
	if r == nil {
		r = d.exerciseOfType(c, tn)
	}
	out[e.ID] = r
}

// reviewedType is one lead-approved type exception: the Go target it applies to and the reason, with the Pi citation.
type reviewedType struct {
	Go     string `json:"go"`
	Reason string `json:"reason"`
}

// reviewedVerdict applies a reviewed exception to a verdict no rule decided, when the row's Go target is the recorded one. A rule's
// yes or no stands: an exception never hides a refutation.
func (d *detector) reviewedVerdict(id, target string, v verdict) verdict {
	if v.ok != unknown || d.reviewed[id].Go != target {
		return v
	}
	if d.reviewedUsed == nil {
		d.reviewedUsed = map[string]bool{}
	}
	d.reviewedUsed[id] = true
	return yesV()
}

// staleReviewed lists the reviewed exceptions no row used (its row is gone, its Go target moved, or a rule decides the row now), sorted.
func (d *detector) staleReviewed() []string { return staleReviewedTypes(d.reviewed, d.reviewedUsed) }

// staleReviewedTypes is staleReviewed over the exceptions and the set of those a decision used.
func staleReviewedTypes(reviewed map[string]reviewedType, used map[string]bool) []string {
	var stale []string
	for _, id := range slices.Sorted(maps.Keys(reviewed)) {
		if strings.TrimSpace(reviewed[id].Reason) != "" && !used[id] {
			stale = append(stale, id)
		}
	}
	return stale
}

// checkReviewed fails on a reviewed exception without a reason or one that no row used, naming every stale one.
func (d *detector) checkReviewed() error { return reviewedErr(d.reviewed, d.reviewedUsed) }

// reviewedErr is checkReviewed over the exceptions and the set of those a decision used.
func reviewedErr(reviewed map[string]reviewedType, used map[string]bool) error {
	var problems []error
	for _, id := range slices.Sorted(maps.Keys(reviewed)) {
		switch {
		case strings.TrimSpace(reviewed[id].Reason) == "":
			return fmt.Errorf("%s: %s has no reason", reviewedTypesFile, id)
		case !used[id]:
			problems = append(problems, fmt.Errorf("%s: %s is stale: no undecided row with Go target %s", reviewedTypesFile, id, reviewed[id].Go))
		}
	}
	return errors.Join(problems...)
}

// aliasVerdict applies A1: a union of string literals needs a Go string type with a constant for every literal; A2: an alias of one
// named type needs a Go type of that name; any other body is undecidable.
func (d *detector) aliasVerdict(e *upstreamEntry, tn *types.TypeName, chk *checker) verdict {
	body := chk.alias(e.Name)
	if body == nil {
		return unknownV("A4: the upstream declaration of %s is missing or declared twice in the pinned sources", e.Name)
	}
	if lits, ok := chk.literalUtility(*body); ok {
		body = &lits // T18: Exclude/Extract over string literals is the union of the remaining literals
	}
	if lits, ok := stringLiteralUnion(*body); !ok && chk.allStringLike(splitTop(*body, "|"), 0) {
		if basicInfo(tn.Type())&types.IsString == 0 {
			return noV("A1: upstream is an open string union, Go type is %s", typeLabel(tn.Type()))
		}
		return yesV() // A5: an open string union (literals plus string) is any Go string type
	} else if ok {
		if basicInfo(tn.Type())&types.IsString == 0 {
			return noV("A1: upstream is a union of string literals, Go type is %s", typeLabel(tn.Type()))
		}
		have := map[string]bool{}
		scope := tn.Pkg().Scope()
		for _, name := range scope.Names() {
			if cst, ok := scope.Lookup(name).(*types.Const); ok && types.Identical(cst.Type(), tn.Type()) && cst.Val().Kind() == constant.String {
				have[constant.StringVal(cst.Val())] = true
			}
		}
		maps.Copy(have, d.ix.enumValues(tn))
		var missing []string
		for _, l := range lits {
			if !have[l] {
				missing = append(missing, l)
			}
		}
		if len(missing) > 0 {
			return noV("A1: no Go constant of %s for %s", tn.Name(), strings.Join(missing, ", "))
		}
		return yesV()
	}
	b := strings.TrimSpace(*body)
	if v, ok := overloadSetAlias(b, tn.Type()); ok {
		return v
	}
	if identRe.MatchString(b) || genericRe.MatchString(b) && len(splitTop(b, "&")) == 1 {
		return chk.agree(b, tn.Type()) // one named type; `A<x> & B<y>` also matches genericRe and is an intersection
	}
	if v, ok := d.discriminatedVerdict(e, b, tn); ok {
		return v
	}
	if v, ok := d.structuralAlias(b, tn, chk); ok {
		return v
	}
	if obj := plainObjectBody(b); obj != "" {
		return chk.agree(obj, tn.Type()) // A6: an alias of an inline object type is a Go struct with a field for each member
	}
	if v := chk.agree(stripTypeNoise(b), tn.Type()); v.ok != unknown {
		return v // A7: any other alias body is judged by the type rules against the Go type
	}
	return unknownV("A3: upstream alias body %q is not a string-literal union or a single type", truncate(b, 60))
}

// stripLineComments removes each `//` line comment of a TypeScript type body up to its line end. A `//` inside a string literal type
// ("https://...") is not a comment, while a comment may itself hold a quote (`maxBytes?: number; // ... Anthropic's 5MB limit`,
// image-resize-core.ts:7), so the scan follows the string literals instead of excluding quotes from the comment.
func stripLineComments(body string) string {
	out := make([]byte, 0, len(body))
	var quote byte
	for i := 0; i < len(body); i++ {
		ch := body[i]
		switch {
		case quote != 0:
			if ch == '\\' && i+1 < len(body) {
				out = append(out, ch)
				i++
				ch = body[i]
			} else if ch == quote {
				quote = 0
			}
		case ch == '"' || ch == '\'' || ch == '`':
			quote = ch
		case ch == '/' && i+1 < len(body) && body[i+1] == '/':
			out = bytes.TrimRight(out, " \t")
			end := strings.IndexByte(body[i:], '\n')
			if end < 0 {
				return string(out)
			}
			i += end - 1
			continue
		}
		out = append(out, ch)
	}
	return string(out)
}

// unionMemberProps are the properties of the interface a union member names, as the alias row's own package declares it: pi-ai's
// TextContent and pi-mcp's TextContent are different interfaces, and a union member is never resolved to another package's declaration when
// its own package has one. A name no package of the alias declares falls back to the first declaration of that name.
func (d *detector) unionMemberProps(aliasID, name string) []rules.UnionProp {
	prefix := aliasID
	if i := strings.Index(prefix, "/"); i >= 0 {
		prefix = prefix[:i+1]
	}
	var found *upstreamEntry
	for _, e := range d.l.entries {
		if e.Name != name || e.Role != "" || e.Kind != "interface" {
			continue
		}
		if strings.HasPrefix(e.ID, prefix) {
			found = e
			break
		}
		if found == nil {
			found = e
		}
	}
	var out []rules.UnionProp
	if found != nil {
		for _, p := range d.l.properties(found.ID) {
			out = append(out, rules.UnionProp{Name: p.Shape.Name, Type: p.Shape.Type, Optional: p.Shape.Optional})
		}
	}
	return out
}

// discriminatedVerdict applies U3-U6 to an alias whose body is a union of object types: a sealed Go interface with one concrete struct per
// member (U4) or a tagged struct (U5). ok is false when the body is not a discriminated union the rules can parse.
func (d *detector) discriminatedVerdict(e *upstreamEntry, body string, tn *types.TypeName) (verdict, bool) {
	members := splitTop(strings.TrimSpace(strings.TrimPrefix(stripTypeNoise(body), "|")), "|")
	if len(members) < 2 || tn.Pkg() == nil {
		return verdict{}, false
	}
	lookup := func(name string) []rules.UnionProp { return d.unionMemberProps(e.ID, name) }
	du, f := rules.ParseDiscriminated(members, lookup)
	if f.Result != rules.Yes {
		return verdict{}, false
	}
	var pkg *pkgInfo
	for _, p := range d.ix.pkgs {
		if p.Path == tn.Pkg().Path() {
			pkg = p
			break
		}
	}
	if pkg == nil {
		return verdict{}, false
	}
	var res rules.Finding
	switch u := tn.Type().Underlying().(type) {
	case *types.Interface:
		res = rules.CheckSealedInterface(du, tn.Type(), rules.Implementers(u, tn.Pkg()), rules.UnionOptions{Resolver: rules.NewASTTags(pkg.Files, pkg.Info)})
	case *types.Struct:
		res = rules.CheckTaggedStruct(du, u, rules.StringUnionOptions{})
	default:
		return verdict{}, false
	}
	switch res.Result {
	case rules.Yes:
		return yesV(), true
	case rules.No:
		return noV("%s: %s", res.Rule, res.Why), true
	}
	return unknownV("%s: %s", res.Rule, res.Why), true
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// all decides every ID of the ledger.
func (d *detector) all() []*decision {
	var out []*decision
	for _, e := range d.l.entries {
		out = append(out, d.decide(e.ID))
	}
	for id := range d.l.mapping {
		if d.l.byID[id] == nil {
			// A mapping row with no package declaration: the command-line interface inventory.
			if derived := d.cliDecision(id); !derived.Gap {
				d.set(id, derived) // C1: the Go source proves every layer
			} else {
				d.setCLI(id) // otherwise a reviewed hand closure in the ledger
			}
			out = append(out, d.dec[id])
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *decision) String() string {
	if r.Gap {
		return fmt.Sprintf("GAP(%s) %s", r.Reason, r.ID)
	}
	return statusNotGap + " " + r.ID
}

// optionalFuncType reports whether an upstream property type is a function type, possibly parenthesised and optional.
func optionalFuncType(up string) bool {
	up = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(up), "| undefined"))
	for strings.HasPrefix(up, "(") && strings.HasSuffix(up, ")") && matchingParen(up) && !isFuncType(up) {
		up = strings.TrimSpace(up[1 : len(up)-1])
	}
	return isFuncType(up)
}

// hasBehaviorContract reports whether the ledger row cites behavior contracts and every one is ported or a divergence.
func (d *detector) hasBehaviorContract(id string) bool {
	row := d.l.mapping[id]
	if row == nil || len(row.BehaviorContracts) == 0 {
		return false
	}
	statuses := d.l.contractStatuses()
	for _, c := range row.BehaviorContracts {
		if s := statuses[c]; s != "ported" && s != "divergence" {
			return false // the validator rejects behavior complete over a contract that is pending or unknown
		}
	}
	return true
}

// keyedCatalog applies V4: an upstream per-provider model catalog constant (ChatModelCatalog, ImageModelCatalog or
// ClassifierModelCatalog instantiated for one provider) is a Go function that takes the provider name and returns the catalog slice.
func keyedCatalog(up string, sig *types.Signature) bool {
	name, _, _ := strings.Cut(strings.TrimSpace(up), "<")
	switch name {
	case "ChatModelCatalog", "ImageModelCatalog", "ClassifierModelCatalog":
	default:
		return false
	}
	if sig.Params().Len() != 1 || sig.Results().Len() == 0 {
		return false
	}
	if b, ok := sig.Params().At(0).Type().Underlying().(*types.Basic); !ok || b.Kind() != types.String {
		return false
	}
	_, ok := sig.Results().At(0).Type().Underlying().(*types.Slice)
	return ok
}

var literalTypeRe = regexp.MustCompile(`^"([^"\\]*)"$`)

// ruleKeyRegistry is the rule a key-registry property's Go constant is found under.
const ruleKeyRegistry = "M6"

// registryKeys applies M6. Pi declares a key registry as an interface read only through keyof or an indexed access, so other packages
// can add keys by declaration merging (pi-tui Keybindings, ai ApiOptionsMap). When its values carry nothing the Go form needs (all
// true, or all one Go type), it has no Go struct: its Go form is the key's string type, and a property is the constant of that type,
// declared in the type's package, whose value is the property name. It returns every such constant (more than one is a duplicate),
// or nil when the rule does not apply.
func (d *detector) registryKeys(prop *upstreamEntry, owner *sym) []*sym {
	tn, ok := owner.Obj.(*types.TypeName)
	if !ok || tn.Pkg() == nil || !d.keyRegistry(prop.ParentID) {
		return nil
	}
	key := types.Unalias(tn.Type())
	if b, ok := key.Underlying().(*types.Basic); !ok || b.Info()&types.IsString == 0 {
		return nil
	}
	var out []*sym
	for _, s := range d.ix.topLevel([]string{owner.Dir}) {
		c, ok := s.Obj.(*types.Const)
		if ok && types.Identical(types.Unalias(c.Type()), key) && c.Val().Kind() == constant.String && constant.StringVal(c.Val()) == prop.Shape.Name {
			found := *s
			found.Tier = ruleKeyRegistry
			out = append(out, &found)
		}
	}
	return out
}

// keyRegistry reports whether the upstream interface id is a key registry: every property has the literal type true (pi-tui
// Keybindings), or every property type names an upstream type and all of them resolve to one Go type (ai ApiOptionsMap, whose every
// options type is ai.StreamOptions), so that the Go form keeps only the keys.
func (d *detector) keyRegistry(id string) bool {
	props, flags := 0, 0
	var value types.Type
	for _, ch := range d.l.children[id] {
		if ch.Role != "property" {
			continue
		}
		props++
		typ := strings.TrimSpace(ch.Shape.Type)
		if typ == "true" {
			flags++
			continue
		}
		if !typeNameRe.MatchString(typ) {
			return false
		}
		tn := d.typeFor(parseID(id).Pkg, typ)
		if tn == nil {
			return false
		}
		// A Go alias of the value type (ai.AzureEndpointOptions = StreamOptions) is the same Go type. A value of any is a stub of the
		// upstream value type, not a type the map shares.
		t := types.Unalias(tn.Type())
		if isEmptyInterface(t) || value != nil && !types.Identical(t, value) {
			return false
		}
		value = t
	}
	return props > 0 && (flags == props || flags == 0)
}

// typeNameRe matches an upstream type written as one bare name.
var typeNameRe = regexp.MustCompile(`^[A-Za-z_]\w*$`)

// discriminator reports M5 for a property whose type is one string literal.
func (d *detector) discriminator(prop *upstreamEntry, owner *sym) bool {
	m := literalTypeRe.FindStringSubmatch(strings.TrimSpace(prop.Shape.Type))
	tn, ok := owner.Obj.(*types.TypeName)
	if m == nil || !ok {
		return false
	}
	return d.marshalsLiteral(tn, m[1])
}

// writesLiteral reports whether a Go type writes the member key with the string literal lit: as its JSON discriminator (M5) or as a
// constant pair in a hand-written encoder method (T12d).
func (d *detector) writesLiteral(tn *types.TypeName, key, lit string) bool {
	if tn == nil || tn.Pkg() == nil {
		return false
	}
	return d.marshalsLiteral(tn, lit) || d.ix.encodedPairs[tn.Pkg().Path()+"."+tn.Name()+"\x00"+key+"\x00"+lit]
}

// marshalsLiteral reports whether a Go type writes the string literal lit as a JSON discriminator: it implements json.Marshaler and a
// parameterless method of it returns lit.
func (d *detector) marshalsLiteral(tn *types.TypeName, lit string) bool {
	if tn == nil || tn.Pkg() == nil {
		return false
	}
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return false
	}
	if sel := types.NewMethodSet(types.NewPointer(named)).Lookup(tn.Pkg(), "MarshalJSON"); sel == nil {
		return false
	}
	prefix := tn.Pkg().Path() + "." + tn.Name() + "."
	for key, l := range d.ix.literalMethods {
		if strings.HasPrefix(key, prefix) && l == lit {
			return true
		}
	}
	return false
}

// decodeTSString returns the value of the body of a TypeScript single- or double-quoted string literal: \n, \t and the other
// single-character escapes, \xNN, \uNNNN, \u{N...}, and a backslash before any other character.
func decodeTSString(body string) string {
	if !strings.Contains(body, `\`) {
		return body
	}
	var b strings.Builder
	rs := []rune(body)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '\\' || i+1 == len(rs) {
			b.WriteRune(rs[i])
			continue
		}
		i++
		switch rs[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'v':
			b.WriteByte('\v')
		case '0':
			b.WriteByte(0)
		case 'x', 'u':
			digits := 2
			if rs[i] == 'u' {
				digits = 4
			}
			start := i + 1
			end := start + digits
			if rs[i] == 'u' && start < len(rs) && rs[start] == '{' {
				start++
				end = start
				for end < len(rs) && rs[end] != '}' {
					end++
				}
			}
			if end > len(rs) {
				b.WriteRune(rs[i])
				continue
			}
			code, err := strconv.ParseUint(string(rs[start:end]), 16, 32)
			if err != nil {
				b.WriteRune(rs[i])
				continue
			}
			b.WriteRune(rune(code))
			i = end - 1
			if rs[start-1] == '{' {
				i = end
			}
		default:
			b.WriteRune(rs[i])
		}
	}
	return b.String()
}

var (
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	readonlyKw   = regexp.MustCompile(`\breadonly\s+`)
)

// plainObjectBody returns an inline object type body without comments and readonly modifiers, or "" when the body is anything else.
// stripTypeNoise removes comments and readonly modifiers, which carry no shape, from an upstream type text.
func stripTypeNoise(body string) string {
	return strings.TrimSpace(readonlyKw.ReplaceAllString(stripLineComments(blockComment.ReplaceAllString(body, "")), ""))
}

func plainObjectBody(body string) string {
	b := stripTypeNoise(body)
	if !strings.HasPrefix(b, "{") || !strings.HasSuffix(b, "}") || len(splitTop(b, "|")) > 1 || len(splitTop(b, "&")) > 1 {
		return ""
	}
	return b
}

// tsLibPrefix is where the inventory records a member that a class inherits from the TypeScript library declarations.
const tsLibPrefix = "node_modules/typescript/lib/"

// errorRuntimeMember is rule L1: a property declared by the TypeScript library on Error (name, stack, cause) that the Go type has no member for.
// name and stack are runtime captures; cause is the same unless a constructor takes ErrorOptions, which sets it and so needs a Go cause.
func (d *detector) errorRuntimeMember(prop *upstreamEntry) bool {
	if !strings.HasPrefix(prop.SourcePath, tsLibPrefix) {
		return false
	}
	owner := d.l.byID[prop.ParentID]
	if owner == nil || !d.extendsError(owner) {
		return false
	}
	switch prop.Shape.Name {
	case "name", "stack":
		return true
	case "cause":
		return !strings.Contains(owner.RawShape, "ErrorOptions")
	}
	return false
}

// extendsError reports whether a class carries the Error members the TypeScript library declares: its message property is inherited from there.
func (d *detector) extendsError(owner *upstreamEntry) bool {
	for _, c := range d.l.children[owner.ID] {
		if c.Shape.Name == "message" && strings.HasPrefix(c.SourcePath, tsLibPrefix) {
			return true
		}
	}
	return false
}

// errorRuntimeRationale is the approved designed-out text for an Error runtime member (owner decision of 2026-10-01 for ai ModelsError.name and .stack).
func errorRuntimeRationale(name string) string {
	what := map[string]string{
		"name":  "Error.name is the class name string the JavaScript constructor assigns",
		"stack": "Error.stack is a JavaScript runtime capture",
		"cause": "Error.cause is unset: no constructor of the class passes ErrorOptions",
	}[name]
	return what + "; a Go error has no such member, and callers match the error by its Go type with errors.As. Designed out as JavaScript runtime mechanics, as the owner approved on 2026-10-01 (lead session, 'Yes go with recommendations') for ai ModelsError.name and ModelsError.stack."
}

// symbolMemberNames are the Go method names that stand for a symbol-keyed member `[Symbol.X]` (rule N6): asyncIterator is the stream a
// caller ranges over (Events), asyncDispose is Dispose, and a package symbol such as LAYOUT_NODE is the method of its PascalCase name
// (LayoutNode). It returns nil for a property that is not symbol-keyed.
func symbolMemberNames(prop string) []string {
	inner, ok := strings.CutPrefix(prop, "[Symbol.")
	if !ok {
		return nil
	}
	inner = strings.TrimSuffix(inner, "]")
	switch inner {
	case "asyncIterator":
		return []string{"Events"}
	case "asyncDispose":
		return []string{"Dispose"}
	}
	var pascal strings.Builder
	for part := range strings.SplitSeq(inner, "_") {
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		if part != strings.ToUpper(part) {
			lower = part // camelCase stays, only its first letter rises
		}
		pascal.WriteString(upperFirst(lower))
	}
	return []string{pascal.String()}
}

// evalRenamedOverloads (OV1): an overloaded upstream property (`on("event", handler)`, `entry(id)` and `entry(conversationId, id)`) whose
// call overloads are renamed to Go methods is decided overload by overload: each call row is judged against its own method, and the
// property row takes the first overload gap or, with none, the evidence of the first overload. An overload without a rename uses the
// property's own Go member fallback, and the rule applies only when at least one overload is renamed and every overload has a method.
// It reports whether it decided the property.
func (d *detector) openOverload(id string, decision *decision) bool {
	return decision.Gap || needsExtensionAPILayers(id) && !d.handPorted(id)
}

func (d *detector) evalRenamedOverloads(prop *upstreamEntry, owner *sym, out map[string]*decision, fallback *sym) bool {
	calls := d.l.callRows(prop.ID)
	renamed := false
	methods := make([]*sym, len(calls))
	for i, c := range calls {
		if target, ok := d.renames[c.ID]; ok {
			renamed = true
			methods[i] = d.renamedMember(target, prop.Shape.Name)
		} else {
			methods[i] = fallback
		}
		if methods[i] == nil {
			return false
		}
		if _, isFunc := methods[i].Obj.(*types.Func); !isFunc {
			return false
		}
	}
	if !renamed {
		return false
	}
	var first *decision
	var gapped *decision
	for i, c := range calls {
		m := methods[i]
		sig := m.Obj.(*types.Func).Type().(*types.Signature)
		if sig.Recv() == nil {
			sig = withoutReceiverParam(sig, prop.ParentID) // G1
		}
		d.row = prop
		self := d.exercise(m, []string{m.Key}, selfExclude(m.Qualified()))
		d.row = nil
		out[c.ID] = d.callDecision(withoutLiteralLead(c), m, sig, self)
		if first == nil {
			first = out[c.ID]
		}
		if d.openOverload(c.ID, out[c.ID]) && gapped == nil {
			gapped = out[c.ID]
		}
	}
	switch {
	case gapped != nil:
		detail := gapped.Detail
		if !gapped.Gap {
			detail = "it needs the extension API layers no rule derives"
		}
		out[prop.ID] = gap(reasonChild, "an overload is a gap: "+detail, owner.Target())
	default:
		out[prop.ID] = &decision{Candidate: first.Candidate, Evidence: first.Evidence, Sym: first.Sym, Info: first.Info}
	}
	return true
}

// withoutLiteralLead is the call overload without its leading string-literal parameter: a documented rename of an overloaded
// property to one Go method puts that literal (`on("session_start", handler)`) in the method's name.
func withoutLiteralLead(c *upstreamEntry) *upstreamEntry {
	ps := c.Shape.Parameters
	if len(ps) == 0 || !strings.HasPrefix(strings.TrimSpace(ps[0].Type), `"`) {
		return c
	}
	cp := *c
	cp.Shape.Parameters = ps[1:]
	return &cp
}

// stringAnyMap reports whether the Go owner type is map[string]any (or an alias of it).
func stringAnyMap(owner *sym) bool {
	tn, ok := owner.Obj.(*types.TypeName)
	if !ok {
		return false
	}
	m, ok := tn.Type().Underlying().(*types.Map)
	if !ok {
		return false
	}
	if b, ok := m.Key().Underlying().(*types.Basic); !ok || b.Kind() != types.String {
		return false
	}
	i, ok := m.Elem().Underlying().(*types.Interface)
	return ok && i.NumMethods() == 0 && i.NumEmbeddeds() == 0
}

var symbolKeyRe = regexp.MustCompile(`^\[Symbol\.(\w+)\]$`)

// phantomBrand reports whether prop is an optional, readonly property keyed by a unique symbol that the upstream source declares with
// `declare const X: unique symbol` (no run-time value), the TypeScript idiom for a phantom type marker.
func (d *detector) phantomBrand(prop *upstreamEntry) bool {
	m := symbolKeyRe.FindStringSubmatch(prop.Shape.Name)
	if m == nil || !prop.Shape.Optional || !prop.Shape.Readonly {
		return false
	}
	return d.checker(prop).aliases[brandKey][m[1]] != nil
}

// phantomBrandRationale is the designed-out text of a phantom type marker.
func phantomBrandRationale(name string) string {
	return "Pi declares " + name + " as an optional readonly property keyed by a `declare const ...: unique symbol`, which has no run-time value: it only lets the compiler carry a type parameter through the type. No code sets or reads it, and the Go type parameter carries the same type, so no Go member exists or is needed (language mechanic, LEAD-RULINGS-1520 principle)."
}

// nodeEventsPath is the declaration file of Node's EventEmitter members.
const nodeEventsPath = "node_modules/@types/node/events.d.ts"

// eventEmitterRationale is the designed-out text of a member inherited from Node's EventEmitter.
func eventEmitterRationale(member, owner string) string {
	return owner + "." + member + " is inherited from Node's EventEmitter (node_modules/@types/node/events.d.ts), not declared by Pi. Go has no emitter base class: " +
		"the Go type returns what Pi emits (the sequences a Process call produces) instead of publishing 'data' and 'paste' events to listeners, so there is no listener registry whose members could be ported. " +
		"Designed out as inherited Node library mechanics, as DIVERGENCES D103 records for the inherited Error members."
}
