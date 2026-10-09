package main

import (
	"fmt"
	"go/types"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Rule families for the lead rulings (questions/LEAD-RULINGS-1520.md): each closes a whole class of rows from one condition the
// detector checks, never a list of row IDs.
//
//	CC carried without consumer: a member Pi declares and only copies (`name: x.name`), which nothing in Pi's source reads, is designed
//	   out when a reviewed declaration lists it and the Go owner carries a member of the same type that no production code reads
//	   (LEAD-RULINGS-1520 lg-imla-2 census 2: StreamOptions.telemetryContext, simple-options.ts:48)
//	TG type guard on a sealed union: an upstream function `(p: U) => p is T` whose U is a Go sealed interface (an unexported method)
//	   and whose T is a Go type implementing it is designed out: a Go caller narrows with a type switch (LEAD-RULINGS-1520 unblock-types 2)
//	TR test-runner adapter: an upstream interface made only of test-runner globals (describe, it, test, expect and the hooks) whose
//	   documented Go representation is testing.T is designed out when the Go form of the upstream function that takes it takes a
//	   *testing.T: Go registers the cases on *testing.T (LEAD-RULINGS-1520 unblock-types 4)
//	RP reviewed placement: placements-reviewed.json names the Go declarations that hold an upstream type's members (see reviewedPlacement)
//	   (LEAD-RULINGS-1520 unblock-types 1(a) and 3)
//	TL type-level alias: a generic alias whose body computes a type from its parameters (keyof, infer, conditional, mapped) and declares
//	   no function is designed out, naming the Go type that carries the shape (LEAD-RULINGS-1520 principle: language mechanics)
//	ER inherited Error stand-in: an unset cause or a stack inherited from Error whose Go member is a constant stand-in no production code
//	   reads is designed out as rule L1 designs out a missing one (LEAD-RULINGS-1520 lg-imla-2 census 4, D103-style)

// rulingsDecl is the designed-out text the rules share: the ruling and the approved precedent.
const rulingsDecl = "Designed out as language mechanics with no Go meaning, as LEAD-RULINGS-1520 (2026-10-07) rules for this class, in the style the owner approved on 2026-10-01 for Error.stack."

// carriedDecl is a reviewed declaration for rule CC: an upstream member and the Pi source file that declares it.
type carriedDecl struct {
	pkg, file, owner, member string
	why                      string // the ruling and the copy site
}

// carriedDecls are the declarations rule CC may design out. The rule re-checks Pi's source on every run: a read anywhere refutes it.
var carriedDecls = []carriedDecl{{
	pkg: "ai", file: "src/types.ts", owner: "ProviderRequestOptions", member: "telemetryContext",
	why: "Pi 1.0.4 declares ProviderRequestOptions.telemetryContext (ai/src/types.ts:137), which StreamOptions (:189) and the option types built on it inherit, and only copies it (ai/src/api/simple-options.ts:48 buildBaseOptions); no Pi code reads it. Go carries the value with the same type and fabricates no read. Designed out as carried with no consumer in Pi 1.0.4 (LEAD-RULINGS-1520, lg-imla-2 census 2).",
}}

// carriedWithoutConsumer applies rule CC to a property whose Go member m agrees in type but no production code reads. It returns the
// designed-out rationale, or "".
func (d *detector) carriedWithoutConsumer(prop *upstreamEntry, m *sym) string {
	if m.Kind != "field" && m.Kind != "method" || len(d.l.callRows(prop.ID)) > 0 {
		return ""
	}
	for _, c := range carriedDecls {
		if prop.Shape.Name != c.member || upstreamSourceFile(prop.SourcePath) != c.pkg+"/"+c.file {
			continue
		}
		if !d.upstreamDeclaresOnlyIn(c) || !d.upstreamOnlyCopies(c.member) {
			return ""
		}
		return c.why
	}
	return ""
}

// upstreamDeclaresOnlyIn reports whether the reviewed owner is the one declaration of the member in its Pi source file: `interface owner`
// (or `owner<...>`) declares it, and no other declaration in the file has a member of that name. The inventory row names the file, not the
// declaring interface, so this keeps the rule to the reviewed declaration.
func (d *detector) upstreamDeclaresOnlyIn(c carriedDecl) bool {
	key := "declares:" + c.pkg + "/" + c.file + "#" + c.owner + "." + c.member
	if v, ok := d.bagCache[key]; ok {
		return v
	}
	data, err := os.ReadFile(filepath.Join(d.ix.root, ".upstream/current/packages", c.pkg, c.file))
	ok := err == nil
	if ok {
		member := regexp.MustCompile(`(?m)^\s*(?:readonly\s+)?` + regexp.QuoteMeta(c.member) + `\??\s*:`)
		decl := regexp.MustCompile(`(?m)^export\s+(?:interface|type)\s+` + regexp.QuoteMeta(c.owner) + `\b[^\n]*\n((?:[^\n]*\n)*?)\}`)
		body := decl.FindSubmatch(data)
		ok = body != nil && len(member.FindAll(body[1], -1)) == 1 && len(member.FindAll(data, -1)) == 1
	}
	d.bagCache[key] = ok
	return ok
}

// upstreamSourceFile maps an inventory source path (a declaration file of the package's build output, reached directly or through
// node_modules) to the package source file it was built from: node_modules/@earendil-works/pi-ai/dist/types.d.ts -> ai/src/types.ts.
func upstreamSourceFile(p string) string {
	rest, ok := strings.CutPrefix(p, "node_modules/@earendil-works/pi-")
	if !ok {
		return ""
	}
	pkg, file, ok := strings.Cut(rest, "/dist/")
	if !ok || !strings.HasSuffix(file, ".d.ts") {
		return ""
	}
	return pkg + "/src/" + strings.TrimSuffix(file, ".d.ts") + ".ts"
}

var (
	// memberAccess finds a property access `.name` or `?.name`; copyOf is a copy into the same-named property `name: x.name`.
	memberAccess = func(name string) *regexp.Regexp { return regexp.MustCompile(`\.` + regexp.QuoteMeta(name) + `\b`) }
	copyOf       = func(name string) *regexp.Regexp {
		q := regexp.QuoteMeta(name)
		return regexp.MustCompile(`\b` + q + `\s*:\s*[\w$]+(?:\??\.[\w$]+)*\??\.` + q + `\b`)
	}
	// destructured finds `name` taken out of an object pattern: `{ a, name } =` or `{ name: local } =`.
	destructured = func(name string) *regexp.Regexp {
		return regexp.MustCompile(`\{[^{}]*\b` + regexp.QuoteMeta(name) + `\b[^{}]*\}\s*=[^=>]`)
	}
)

// upstreamOnlyCopies reports whether every access to member in Pi's package sources (tests and declaration files excluded) is a copy into
// the same-named property: nothing reads the value.
func (d *detector) upstreamOnlyCopies(member string) bool {
	key := "copies:" + member
	if v, ok := d.bagCache[key]; ok {
		return v
	}
	access, cp, destr := memberAccess(member), copyOf(member), destructured(member)
	only, seen := true, false
	for _, src := range d.upstreamSources() {
		for line := range strings.SplitSeq(src, "\n") {
			if destr.MatchString(line) {
				only = false
			}
			n := len(access.FindAllStringIndex(line, -1))
			if n == 0 {
				continue
			}
			seen = true
			if len(cp.FindAllStringIndex(line, -1)) != n {
				only = false
			}
		}
	}
	d.bagCache[key] = only && seen
	return only && seen
}

// upstreamSources returns the text of every package source file of the pinned upstream: .ts files under packages/*/src that are not
// tests or declaration files, in path order.
func (d *detector) upstreamSources() []string {
	if d.srcText != nil {
		return d.srcText
	}
	d.srcText = []string{}
	base := filepath.Join(d.ix.root, ".upstream/current/packages")
	pkgs, _ := os.ReadDir(base)
	for _, p := range pkgs {
		var files []string
		_ = filepath.WalkDir(filepath.Join(base, p.Name(), "src"), func(f string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // a package without src has no sources to scan
			}
			if e.IsDir() && e.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if !e.IsDir() && strings.HasSuffix(f, ".ts") && !strings.HasSuffix(f, ".d.ts") && !strings.Contains(f, ".test.") {
				files = append(files, f)
			}
			return nil
		})
		sort.Strings(files)
		for _, f := range files {
			if data, err := os.ReadFile(f); err == nil {
				d.srcText = append(d.srcText, string(data))
			}
		}
	}
	return d.srcText
}

// typeGuard matches a type predicate function type `(p: U) => p is T` with simple names.
var typeGuard = regexp.MustCompile(`^\(\s*(\w+)\s*:\s*(\w+)\s*\)\s*=>\s*(\w+)\s+is\s+(\w+)\s*$`)

// sealedTypeGuard applies rule TG to a function row with no Go symbol. It returns the designed-out rationale and the Go type that the
// type switch narrows to, or "".
func (d *detector) sealedTypeGuard(e *upstreamEntry) (string, *sym) {
	if e.Kind != "function" {
		return "", nil
	}
	g := typeGuard.FindStringSubmatch(strings.TrimSpace(e.Shape.Type))
	if g == nil || g[1] != g[3] {
		return "", nil
	}
	pkg := parseID(e.ID).Pkg
	union, narrowed := d.goTypeNamed(pkg, g[2]), d.goTypeNamed(pkg, g[4])
	if union == nil || narrowed == nil {
		return "", nil
	}
	iface, ok := union.Type().Underlying().(*types.Interface)
	if !ok || !sealed(iface) {
		return "", nil
	}
	if !types.Implements(narrowed.Type(), iface) && !types.Implements(types.NewPointer(narrowed.Type()), iface) {
		return "", nil
	}
	target := d.symOf(pkg, union)
	if target == nil {
		return "", nil
	}
	return "TG: " + e.Name + " is a TypeScript type predicate `" + g[1] + " is " + g[4] + "` that narrows the union " + g[2] + " at compile time; Go's " +
		types.TypeString(union.Type(), qualifyByName) + " is a sealed interface (an unexported method), so a Go caller narrows it with a type switch on its variants (" +
		types.TypeString(narrowed.Type(), qualifyByName) + " among them) and reads the variant's own fields. " + rulingsDecl + " (unblock-types 2)", target
}

// goTypeNamed returns the Go type that stands for an upstream type name: the one the rules map an inventory row to, or, for a name the
// package does not export (a union member such as BashToolResultEvent), the Go type of exactly that name in the package's directories.
func (d *detector) goTypeNamed(pkg, name string) *types.TypeName {
	if tn := d.typeFor(pkg, name); tn != nil {
		return tn
	}
	var found *types.TypeName
	for _, s := range d.topSyms(pkg) {
		if s.Kind == "type" && s.Name == name {
			if found != nil {
				return nil // two packages declare it: no unique Go form
			}
			found, _ = s.Obj.(*types.TypeName)
		}
	}
	return found
}

// sealed reports whether an interface has an unexported method, so only its own package can implement it.
func sealed(iface *types.Interface) bool {
	for m := range iface.Methods() {
		if !m.Exported() {
			return true
		}
	}
	return false
}

func qualifyByName(p *types.Package) string { return p.Name() }

// symOf returns the top-level Go symbol of a type name, searching the package's directories and then the type's own directory.
func (d *detector) symOf(pkg string, tn *types.TypeName) *sym {
	for _, s := range d.topSyms(pkg) {
		if s.Obj == tn {
			return s
		}
	}
	dirs := slices.Sorted(maps.Keys(d.ix.pkgs))
	for _, dir := range dirs {
		if d.ix.pkgs[dir].Types != tn.Pkg() {
			continue
		}
		for _, s := range d.ix.topLevel([]string{dir}) {
			if s.Obj == tn {
				return s
			}
		}
	}
	return nil
}

// testRunnerGlobals are the Vitest/Jest globals a runner adapter forwards.
var testRunnerGlobals = []string{"describe", "it", "test", "expect", "beforeEach", "afterEach", "beforeAll", "afterAll"}

// testRunnerAdapter applies rule TR to an interface row with no Go symbol. It returns the designed-out rationale, or "".
func (d *detector) testRunnerAdapter(e *upstreamEntry) (string, *sym) {
	if e.Kind != "interface" || d.reps[parseID(e.ID).Pkg+":"+e.Name] != "testing.T" {
		return "", nil
	}
	props := d.l.properties(e.ID)
	if len(props) == 0 {
		return "", nil
	}
	var names []string
	for _, p := range props {
		if !slices.Contains(testRunnerGlobals, p.Shape.Name) {
			return "", nil
		}
		names = append(names, p.Shape.Name)
	}
	target := d.testingTCounterpart(e)
	if target == nil {
		return "", nil
	}
	return "TR: " + e.Name + " is the Vitest/Jest runner adapter (" + strings.Join(names, ", ") + "): the upstream function hands it the cases to register. Go registers the same cases on *testing.T, the documented representation of " +
		e.Name + " (representations.json): " + target.Target() + " takes the *testing.T, runs each case as a t.Run subtest and asserts with the testing package. " + rulingsDecl + " (unblock-types 4: Vitest runner vs *testing.T)", target
}

// testingTCounterpart returns the Go function of an upstream function that takes the runner, when that Go function takes a *testing.T.
func (d *detector) testingTCounterpart(runner *upstreamEntry) *sym {
	pkg := parseID(runner.ID).Pkg
	takes := regexp.MustCompile(`:\s*` + regexp.QuoteMeta(runner.Name) + `\b`)
	for _, fn := range d.l.entries {
		if fn.Kind != "function" || fn.Role != "" || parseID(fn.ID).Pkg != pkg || !takes.MatchString(fn.Shape.Type) {
			continue
		}
		for _, c := range d.candidates(fn, pkg, "func") {
			sig, ok := c.Obj.Type().(*types.Signature)
			if !ok {
				continue
			}
			for p := range sig.Params().Variables() {
				if types.TypeString(p.Type(), nil) == "*testing.T" {
					return c
				}
			}
		}
	}
	return nil
}

// designOutTree records a rule's designed-out decision for a row and every row below it.
func (d *detector) designOutTree(id, why, target string) {
	d.set(id, &decision{Candidate: target, Evidence: ruleDesignedOut, DesignedOut: why})
	for _, ch := range d.l.children[id] {
		d.designOutTree(ch.ID, why, target)
	}
}

// designedOutTop applies the top-level designed-out rules (TG, TR) to a declaration with no Go symbol. It reports whether one decided it.
func (d *detector) designedOutTop(e *upstreamEntry) bool {
	if why, target := d.sealedTypeGuard(e); why != "" {
		d.designOutTree(e.ID, why, target.Target())
		return true
	}
	if why := d.typeLevelAlias(e); why != "" {
		d.designOutTree(e.ID, why, d.typeLevelCarrier(e, *d.checker(e).alias(e.Name), 0).Target())
		return true
	}
	if why, target := d.testRunnerAdapter(e); why != "" {
		d.designOutTree(e.ID, why, target.Target())
		return true
	}
	return false
}

// Rule RP, reviewed placement (LEAD-RULINGS-1520 unblock-types 1, 3 and 5; LEAD-ANSWERS-ledger 4): Go keeps the members of one upstream
// type on several Go declarations, and a reviewed entry in placements-reviewed.json names them. A property the name rules do not find
// on the row's own Go owner is searched, by the same name rules, on each placement target in order:
//
//   - a Go type: its fields and methods, judged by the member rules and P1 like the owner's own members;
//   - a Go function or func type: its parameters (the upstream options bag spread over positional parameters), judged by the type rules
//     and exercised when the function or func type is.
//
// The entry may give a member its Go form explicitly (`members`: "toolCall": "toolCallID toolName" for parameters, "isError":
// "result.IsError" for a field of a parameter, a `file#Owner.Member` for a field elsewhere) or design it out with a citation
// (`designedOut`). An upstream type with no Go type of its own whose first target is a function or func type is that target. A member
// no target holds stays a gap: the placement moves where the rules look, never what they accept. An entry keyed by an upstream function (pkg:name) or a function-typed property
// (pkg:Owner.property) may instead give `folds`, the folded behaviour of its signature (see foldedSignature).
type reviewedPlacement struct {
	Go          []string          `json:"go"`
	Members     map[string]string `json:"members,omitempty"`
	Folds       map[string]string `json:"folds,omitempty"`
	DesignedOut map[string]string `json:"designedOut,omitempty"`
	Reason      string            `json:"reason"`
}

// placementTarget resolves a `file#Name` target to its top-level Go symbol.
func (d *detector) placementTarget(target string) *sym {
	file, name, _ := strings.Cut(target, "#")
	dir := path.Dir(file)
	if _, ok := d.ix.pkgs[dir]; !ok {
		return nil
	}
	for _, s := range d.ix.topLevel([]string{dir}) {
		if s.Name == name && s.File == file {
			return s
		}
	}
	return nil
}

// placementOf returns the reviewed placement of a property's owner type.
func (d *detector) placementOf(prop *upstreamEntry) (reviewedPlacement, bool) {
	owner := d.l.byID[prop.ParentID]
	if owner == nil {
		return reviewedPlacement{}, false
	}
	p, ok := d.placements[parseID(owner.ID).Pkg+":"+owner.Name]
	return p, ok
}

// placedMember applies RP to a property of a Go type: the Go field or method a placement target holds for it, or nil.
func (d *detector) placedMember(prop *upstreamEntry) *sym {
	p, ok := d.placementOf(prop)
	if !ok {
		return nil
	}
	if target, ok := p.Members[prop.Shape.Name]; ok {
		if strings.Contains(target, "#") {
			return d.renamedMember(target, prop.Shape.Name)
		}
		return nil // a parameter form: only a func placement holds it
	}
	for _, t := range p.Go {
		s := d.placementTarget(t)
		if s == nil || s.Kind != "type" {
			continue
		}
		tn := s.Obj.(*types.TypeName)
		if _, isFunc := tn.Type().Underlying().(*types.Signature); isFunc {
			continue
		}
		// Only the exported members are the target's public API: an unexported field behind an accessor is not the upstream member.
		exported := slices.DeleteFunc(d.ix.members(tn, s.Dir, s.Rank), func(m *sym) bool { return !m.Exported() })
		if m := d.memberFor(prop, exported); m != nil {
			return m
		}
	}
	return nil
}

// placedDesignedOut applies RP's designed-out members: the citation for a property, or "".
func (d *detector) placedDesignedOut(prop *upstreamEntry) string {
	p, ok := d.placementOf(prop)
	if !ok || p.DesignedOut[prop.Shape.Name] == "" {
		return ""
	}
	return "RP: " + p.DesignedOut[prop.Shape.Name]
}

// signatureOf returns the parameters of a Go function or func type symbol.
func signatureOf(s *sym) *types.Signature {
	switch obj := s.Obj.(type) {
	case *types.Func:
		return obj.Type().(*types.Signature)
	case *types.TypeName:
		sig, _ := obj.Type().Underlying().(*types.Signature)
		return sig
	}
	return nil
}

// placedTop applies RP to an upstream interface with no Go type of its own whose placement's first target is a Go function or func
// type: every property is a parameter of it. It reports whether it decided the row.
func (d *detector) placedTop(e *upstreamEntry) bool {
	if e.Kind != "interface" {
		return false
	}
	p, ok := d.placements[parseID(e.ID).Pkg+":"+e.Name]
	if !ok || len(p.Go) == 0 {
		return false
	}
	fn := d.placementTarget(p.Go[0])
	if fn == nil || signatureOf(fn) == nil {
		return false
	}
	self := d.exercise(fn, []string{fn.Key}, selfExclude(fn.Name))
	out := map[string]*decision{e.ID: self}
	for _, prop := range d.l.properties(e.ID) {
		out[prop.ID] = d.placedParameter(prop, p, fn, self)
		for _, c := range d.l.callRows(prop.ID) {
			same := *out[prop.ID] // a call row of a parameter is the parameter
			out[c.ID] = &same
		}
	}
	d.foldChildren(e, self, out)
	for id, r := range out {
		d.set(id, r)
	}
	d.fillMissing(e.ID, out[e.ID])
	return true
}

// placedParameter decides one property of a function placement: its parameter (or the reviewed parameters or parameter field), its
// designed-out citation, or a gap.
func (d *detector) placedParameter(prop *upstreamEntry, p reviewedPlacement, fn *sym, self *decision) *decision {
	if why := p.DesignedOut[prop.Shape.Name]; why != "" {
		return &decision{Candidate: fn.Target(), Evidence: ruleDesignedOut, DesignedOut: "RP: " + why}
	}
	sig := signatureOf(fn)
	param := func(name string) *types.Var {
		for v := range sig.Params().Variables() {
			if v.Name() == name || nameRule(name, v.Name()) != "" {
				return v
			}
		}
		return nil
	}
	ok := func() *decision {
		if self.Gap {
			return gap(self.Reason, self.Detail, fn.Target())
		}
		return &decision{Candidate: fn.Target(), Evidence: self.Evidence, Sym: self.Sym, Info: self.Info}
	}
	if form, reviewed := p.Members[prop.Shape.Name]; reviewed {
		owner, field, isField := strings.Cut(form, ".")
		if isField {
			// A field of a parameter's type: the parameter must exist and its type must have the field, with an agreeing type.
			v := param(owner)
			if v == nil {
				return gap(reasonMember, "RP: "+fn.Target()+" has no parameter "+owner, fn.Target())
			}
			obj, _, _ := types.LookupFieldOrMethod(v.Type(), true, v.Pkg(), field)
			fv, isVar := obj.(*types.Var)
			if !isVar {
				return gap(reasonMember, "RP: parameter "+owner+" of "+fn.Target()+" has no field "+field, fn.Target())
			}
			if g := refute(d.checker(prop).agree(prop.Shape.Type, fv.Type()), reasonType, fn.Target()); g != nil {
				return g
			}
			return ok()
		}
		// Several parameters that together carry the member: each must exist.
		for name := range strings.FieldsSeq(form) {
			if param(name) == nil {
				return gap(reasonMember, "RP: "+fn.Target()+" has no parameter "+name, fn.Target())
			}
		}
		return ok()
	}
	v := param(prop.Shape.Name)
	if v == nil {
		return gap(reasonMember, "RP: no parameter of "+fn.Target()+" for property "+prop.Shape.Name, fn.Target())
	}
	if g := refute(d.checker(prop).agree(prop.Shape.Type, v.Type()), reasonType, fn.Target()); g != nil {
		return g
	}
	return ok()
}

// checkPlacements rejects a placements-reviewed.json entry that names no upstream interface or class, has no reason, or names a Go
// target that does not exist, so a stale entry fails the run instead of silently moving nothing.
func (d *detector) checkPlacements() error {
	for _, key := range slices.Sorted(maps.Keys(d.placements)) {
		p := d.placements[key]
		pkg, name, _ := strings.Cut(key, ":")
		found := false
		owner, member, isProp := strings.Cut(name, ".")
		for _, e := range d.l.entries {
			if parseID(e.ID).Pkg != pkg {
				continue
			}
			switch {
			case isProp:
				// A property's folds: the owner declares the property.
				if parent := d.l.byID[e.ParentID]; e.Role == "property" && e.Shape.Name == member && parent != nil && parent.Name == owner && len(p.Folds) > 0 {
					found = true
				}
			case e.Role == "" && e.Name == name && (e.Kind == "interface" || e.Kind == "class"):
				found = true
			case e.Role == "" && e.Name == name && e.Kind == "function" && len(p.Folds) > 0:
				found = true // a function's folds
			}
			if found {
				break
			}
		}
		switch {
		case !found:
			return fmt.Errorf("%s: %s names no upstream interface or class (or, with folds, function or property)", placementsFile, key)
		case strings.TrimSpace(p.Reason) == "":
			return fmt.Errorf("%s: %s has no reason", placementsFile, key)
		}
		for _, t := range p.Go {
			if d.placementTarget(t) == nil {
				return fmt.Errorf("%s: %s: Go target %s does not exist", placementsFile, key, t)
			}
		}
		for member, t := range p.Members {
			if strings.Contains(t, "#") && d.renamedMember(t, member) == nil {
				return fmt.Errorf("%s: %s: member %s target %s does not exist", placementsFile, key, member, t)
			}
		}
	}
	return nil
}

// inheritedErrorStub applies rule ER (LEAD-RULINGS-1520 lg-imla-2 census 4, the D103-style inherited-member handling) to a property
// whose Go member no production code reads: an unset cause or a stack that a class inherits from the TypeScript library's Error (rule
// L1's condition) is designed out when its Go member is a constant stand-in (L4: one return of constants or nil), because the stand-in
// carries nothing beyond the value the JavaScript runtime leaves unset. It returns the designed-out rationale, or "".
func (d *detector) inheritedErrorStub(prop *upstreamEntry, m *sym) string {
	if m.Kind != "method" || prop.Shape.Name != "cause" && prop.Shape.Name != "stack" || !d.errorRuntimeMember(prop) {
		return ""
	}
	if d.lib == nil {
		d.lib = newLibrary(d.ix.root)
	}
	if !d.constantStub(m) {
		return ""
	}
	return "ER: " + m.Target() + " returns only a constant, the value Pi's runtime leaves unset. " + errorRuntimeRationale(prop.Shape.Name) +
		" Applied to a constant Go stand-in by LEAD-RULINGS-1520 (lg-imla-2 census 4: inherited Error members as D103)."
}

// foldsOf returns the reviewed parameter folds (placements-reviewed.json `folds`) of an upstream function or function-typed property,
// keyed pkg:name or pkg:Owner.property.
func (d *detector) foldsOf(row *upstreamEntry) (map[string]string, bool) {
	key := parseID(row.ID).Pkg + ":"
	switch row.Role {
	case "call-overload":
		parent := d.l.byID[row.ParentID]
		if parent == nil {
			return nil, false
		}
		return d.foldsOf(parent)
	case "property":
		owner := d.l.byID[row.ParentID]
		if owner == nil {
			return nil, false
		}
		key += owner.Name + "." + row.Shape.Name
	default:
		key += row.Name
	}
	p, ok := d.placements[key]
	return p.Folds, ok && len(p.Folds) > 0
}

// foldedSignature applies RP's folds (LEAD-RULINGS-1520 unblock-types 5: folded behaviour) to an upstream call the signature rules
// refute: each upstream parameter is the Go parameter the review names (several may fold into one), the Go parameter of its own name,
// or a value the Go side binds elsewhere (`bound: <why>`). Every Go parameter after a leading context must be the image of an upstream
// parameter; a parameter that maps one to one under its own name is type-checked; the results are checked by the type rules.
func (d *detector) foldedSignature(row *upstreamEntry, up callShape, sig *types.Signature) (verdict, bool) {
	folds, ok := d.foldsOf(row)
	if !ok {
		return verdict{}, false
	}
	chk := d.checker(row)
	params := sig.Params()
	start := 0
	if params.Len() > 0 && isContext(params.At(0).Type()) {
		start = 1
	}
	find := func(name string) *types.Var {
		for i := start; i < params.Len(); i++ {
			if v := params.At(i); v.Name() == name || nameRule(name, v.Name()) != "" {
				return v
			}
		}
		return nil
	}
	images := map[*types.Var][]string{}
	for _, p := range up.Parameters {
		target, folded := folds[p.Name]
		if why, bound := strings.CutPrefix(target, "bound:"); bound {
			if strings.TrimSpace(why) == "" {
				return noV("RP folds: parameter %s is bound with no reason", p.Name), true
			}
			continue
		}
		if !folded {
			target = p.Name
		}
		v := find(target)
		if v == nil {
			return noV("RP folds: no Go parameter %s for upstream parameter %s", target, p.Name), true
		}
		images[v] = append(images[v], p.Name)
		if !folded {
			if w := chk.agree(p.Type, v.Type()); w.ok != yes {
				return w, true
			}
		}
	}
	for i := start; i < params.Len(); i++ {
		if len(images[params.At(i)]) == 0 {
			return noV("RP folds: Go parameter %s stands for no upstream parameter", params.At(i).Name()), true
		}
	}
	results := types.NewSignatureType(nil, nil, nil, nil, sig.Results(), false)
	return chk.signature(callShape{Returns: up.Returns}, results), true
}

var (
	// typeOperator finds a TypeScript type operator that computes a type from its operands: keyof, infer, a conditional type, a mapped
	// type, or a utility type that filters keys or members.
	typeOperator = regexp.MustCompile(`\bkeyof\b|\binfer\s+\w|\bextends\b[^?;]*\?[^:]*:|\[\s*\w+\s+in\s|\b(?:Exclude|Extract|Omit|Pick|Parameters|ReturnType|NonNullable)<`)
	// callableMember finds a function type or a method signature in a type body: such an alias describes a runtime object or function.
	callableMember = regexp.MustCompile(`=>|\w\s*\(|>\s*\(`)
)

// typeLevelAlias applies rule TL (LEAD-RULINGS-1520 principle: language mechanics with no Go meaning are designed out with a citation)
// to a type-alias row with no Go symbol. A generic alias whose body computes a type from its type parameters (keyof, infer, a
// conditional or mapped type, a key-filtering utility type) and declares no function or method exists only while TypeScript checks
// Pi's callers. It returns the designed-out rationale, or "".
func (d *detector) typeLevelAlias(e *upstreamEntry) string {
	s := e.Shape
	if e.Kind != "type-alias" || len(s.TypeParameters) == 0 || len(s.Properties) > 0 || len(s.Calls) > 0 || len(s.Constructs) > 0 {
		return ""
	}
	body := d.checker(e).alias(e.Name)
	if body == nil {
		return ""
	}
	b := commentText.ReplaceAllString(*body, " ")
	if callableMember.MatchString(b) || !typeOperator.MatchString(b) && !d.typeLevelReference(e, b) {
		return ""
	}
	names := make([]string, len(s.TypeParameters))
	for i, t := range s.TypeParameters {
		names[i] = t.Name
	}
	carrier := d.typeLevelCarrier(e, b, 0)
	if carrier == nil {
		return "" // no Go type carries a shape the alias computes from
	}
	return "TL: " + e.Name + "<" + strings.Join(names, ", ") + "> is a TypeScript type-level computation over its type parameters (" + truncate(strings.Join(strings.Fields(b), " "), 120) +
		"); it has no value, member or call, so nothing of it exists at run time and Go generics cannot express it. PiG's concrete Go types carry the shapes it computes where they are used, here " + carrier.Target() + ". " + rulingsDecl
}

// typeLevelReference reports whether a body is one instantiation of another type-level alias of the row's package
// (InferStartAttributes<D> = InferRequiredAndOptionalAttributes<D>).
func (d *detector) typeLevelReference(e *upstreamEntry, body string) bool {
	m := singleInstantiation.FindStringSubmatch(strings.TrimSpace(body))
	if m == nil || m[1] == e.Name || d.typeBusy["TL:"+m[1]] {
		return false
	}
	pkg := parseID(e.ID).Pkg
	for _, other := range d.l.entries {
		if other.Role == "" && other.Name == m[1] && parseID(other.ID).Pkg == pkg {
			d.typeBusy["TL:"+m[1]] = true
			ok := d.typeLevelAlias(other) != ""
			delete(d.typeBusy, "TL:"+m[1])
			return ok
		}
	}
	return false
}

// singleInstantiation matches a body that is one generic type reference, `Name<...>`.
var singleInstantiation = regexp.MustCompile(`^(\w+)<[^()]*>$`)

// commentText strips TypeScript comments from a type body.
var commentText = regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`)

// typeIdent finds the type names a body references.
var typeIdent = regexp.MustCompile(`\b[A-Z]\w*`)

// tsBuiltinTypes are TypeScript's own generic types, which no Go type stands for.
var tsBuiltinTypes = map[string]bool{"Record": true, "Exclude": true, "Extract": true, "Omit": true, "Pick": true, "Partial": true, "Required": true,
	"Readonly": true, "NonNullable": true, "Parameters": true, "ReturnType": true, "Promise": true, "Array": true, "ReadonlyArray": true}

// typeLevelCarrier returns the Go type of the first upstream type a type-level body computes from, following an instantiated
// type-level alias of the same package to its own body; nil when none has a Go form.
func (d *detector) typeLevelCarrier(e *upstreamEntry, body string, depth int) *sym {
	if depth > 4 {
		return nil
	}
	pkg := parseID(e.ID).Pkg
	params := map[string]bool{}
	for _, t := range e.Shape.TypeParameters {
		params[t.Name] = true
	}
	for _, name := range typeIdent.FindAllString(d.typeParamConstraints(e)+" "+commentText.ReplaceAllString(body, " "), -1) {
		if params[name] || tsBuiltinTypes[name] || name == e.Name {
			continue
		}
		if tn := d.goTypeNamed(pkg, name); tn != nil {
			if s := d.symOf(pkg, tn); s != nil {
				return s
			}
		}
		if inner := d.checker(e).alias(name); inner != nil {
			for _, other := range d.l.entries {
				if other.Role == "" && other.Name == name && parseID(other.ID).Pkg == pkg {
					if s := d.typeLevelCarrier(other, *inner, depth+1); s != nil {
						return s
					}
				}
			}
		}
	}
	return nil
}

// typeParamConstraints returns the type parameter list of an alias's declaration in Pi's source (`type Name<A extends X, B = Y> =`),
// where the constraints name the types the alias computes from; "" when the declaration is not found.
func (d *detector) typeParamConstraints(e *upstreamEntry) string {
	file := e.SourcePath
	if !strings.HasPrefix(file, "packages/") {
		f := upstreamSourceFile(file)
		if f == "" {
			return ""
		}
		file = "packages/" + f
	}
	data, err := os.ReadFile(filepath.Join(d.ix.root, ".upstream/current", filepath.FromSlash(file)))
	if err != nil {
		return ""
	}
	src := string(data)
	_, rest, ok := strings.Cut(src, "type "+e.Name+"<")
	if !ok {
		return ""
	}
	depth := 1
	for i, r := range rest {
		switch r {
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return rest[:i]
			}
		}
	}
	return ""
}

// accessorPair applies rule AP (LEAD-ANSWERS-ledger: members Go keeps in another shape close by a rule) to a writable upstream property
// that no Go field or method of its name holds: a mutable property p of type T is a Go getter and setter pair on the owner. The setter
// is SetP with one parameter whose type agrees with T; the getter takes no parameters (or only a context), returns one value whose type
// agrees with T, and is named P, PHook or PFunc (the suffix keeps a function-valued accessor apart from a same-named type or method), or
// is named for its own named result type when that name starts with P (ToolExecutionMode() for toolExecution). Both must exist and
// agree; the row is exercised when production code calls either. It reports whether it decided the row.
func (d *detector) accessorPair(prop *upstreamEntry, owner *sym, members []*sym, out map[string]*decision) bool {
	if prop.Shape.Readonly || len(d.l.callRows(prop.ID)) > 0 {
		return false
	}
	name := upperFirst(prop.Shape.Name)
	var getter, setter *sym
	for _, m := range members {
		if m.Kind != "method" || !m.Exported() {
			continue
		}
		sig, ok := m.Obj.Type().(*types.Signature)
		if !ok {
			continue
		}
		switch {
		case setter == nil && nameRule("set"+name, m.Name) != "" && sig.Params().Len() == 1 && sig.Results().Len() == 0:
			setter = m
		case getter == nil && sig.Results().Len() == 1 && (sig.Params().Len() == 0 || sig.Params().Len() == 1 && isContext(sig.Params().At(0).Type())):
			if getterName(name, m.Name, sig.Results().At(0).Type()) {
				getter = m
			}
		}
	}
	if getter == nil || setter == nil {
		return false
	}
	chk := d.checker(prop)
	gv := chk.agree(prop.Shape.Type, getter.Obj.Type().(*types.Signature).Results().At(0).Type())
	sv := chk.agree(prop.Shape.Type, setter.Obj.Type().(*types.Signature).Params().At(0).Type())
	if g := refute(gv, reasonType, getter.Target()); g != nil {
		out[prop.ID] = g
		return true
	}
	if g := refute(sv, reasonType, setter.Target()); g != nil {
		out[prop.ID] = g
		return true
	}
	d.row = prop
	self := d.exercise(setter, []string{setter.Key}, selfExclude(setter.Qualified()))
	if self.Gap {
		if r := d.exercise(getter, []string{getter.Key}, selfExclude(getter.Qualified())); !r.Gap {
			self = r
		}
	}
	d.row = nil
	if !self.Gap {
		self.Evidence = "AP: " + getter.Qualified() + "/" + setter.Qualified() + "; " + self.Evidence
	}
	out[prop.ID] = self
	return true
}

// getterName reports whether goName is a getter name for the upstream property name (already PascalCase).
func getterName(name, goName string, result types.Type) bool {
	for _, n := range []string{name, name + "Hook", name + "Func"} {
		if goName == n || nameRule(n, goName) != "" {
			return true
		}
	}
	if nt, ok := types.Unalias(result).(*types.Named); ok && nt.Obj().Name() == goName && strings.HasPrefix(goName, name) {
		return true
	}
	return false
}
