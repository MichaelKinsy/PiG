package main

import (
	"strings"
	"testing"
)

// The ruling families of rulings.go. Each test runs the detector on a fixture module and checks the class closes, then mutates one
// condition (a wrong Go mapping, a stub, an upstream read) and checks the rows stay gaps.

func wantDesignedOut(t *testing.T, ds map[string]*decision, rule string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		d := ds[id]
		if d == nil || d.Gap || d.Evidence != ruleDesignedOut || !strings.Contains(d.DesignedOut, rule) && !strings.Contains(d.DesignedOut, "LEAD-RULINGS-1520") {
			t.Errorf("%s: want designed out by %s, got %+v", id, rule, d)
		}
		if d != nil && d.Candidate == "" {
			t.Errorf("%s: a designed-out row names its Go target", id)
		}
	}
}

func wantGap(t *testing.T, ds map[string]*decision, why string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if d := ds[id]; d == nil || !d.Gap || d.DesignedOut != "" {
			t.Errorf("%s: %s, so the row stays a gap, got %+v", id, why, d)
		}
	}
}

// Rule TG.

const tgLib = `package lib

// Ev is the sealed Go form of the upstream Ev union.
type Ev interface{ isEv() }

// EvA is the Go form of the upstream EvA member.
type EvA struct{ Name string }

func (EvA) isEv() {}

// EvB is a struct outside the union.
type EvB struct{}
`

func tgInventory(fnType string) func([]m) []m {
	return func([]m) []m {
		call := m{"parameters": []m{prm("e", "Ev", false)}, "returns": "boolean"}
		return []m{
			{"id": "pkg:fx/.#isEvA", "name": "isEvA", "kind": "function", "shape": m{"type": fnType, "calls": []m{call}}},
			{"id": "pkg:fx/.#isEvA::call:0", "parentId": "pkg:fx/.#isEvA", "role": "call-overload", "name": "isEvA call 0", "kind": "call-overload", "shape": call},
		}
	}
}

func tgRun(t *testing.T, lib, fnType string) map[string]*decision {
	t.Helper()
	return fixtureRun(t, map[string]string{"lib/lib.go": tgLib + lib, "lib/lib_test.go": "package lib\n", "lib/use.go": "package lib\n"}, tgInventory(fnType), map[string]bool{}, nil)
}

// TestTypeGuardOnASealedUnionIsDesignedOut: TG designs out `(e: Ev) => e is EvA` when Ev is a sealed Go interface that EvA implements.
func TestTypeGuardOnASealedUnionIsDesignedOut(t *testing.T) {
	ids := []string{"pkg:fx/.#isEvA", "pkg:fx/.#isEvA::call:0"}
	ds := tgRun(t, "", "(e: Ev) => e is EvA")
	wantDesignedOut(t, ds, "TG:", ids...)
	if d := ds[ids[0]]; d != nil && d.Candidate != "lib/lib.go#Ev" {
		t.Errorf("the designed-out row names the sealed union, got %q", d.Candidate)
	}
}

// TestTypeGuardMutantsStayGaps: a guard over an open interface, a narrowed type outside the union, a guard that validates unknown input
// (real runtime behaviour) and a narrowed type with no Go form stay gaps.
func TestTypeGuardMutantsStayGaps(t *testing.T) {
	ids := []string{"pkg:fx/.#isEvA", "pkg:fx/.#isEvA::call:0"}
	open := strings.Replace(tgLib, "type Ev interface{ isEv() }", "type Ev interface{ IsEv() }", 1)
	open = strings.Replace(open, "func (EvA) isEv() {}", "func (EvA) IsEv() {}", 1)
	cases := map[string]func() map[string]*decision{
		"the Go union is not sealed": func() map[string]*decision {
			return fixtureRun(t, map[string]string{"lib/lib.go": open, "lib/lib_test.go": "package lib\n", "lib/use.go": "package lib\n"}, tgInventory("(e: Ev) => e is EvA"), map[string]bool{}, nil)
		},
		"the narrowed Go type does not implement the union": func() map[string]*decision { return tgRun(t, "", "(e: Ev) => e is EvB") },
		"the guard validates unknown input":                 func() map[string]*decision { return tgRun(t, "", "(e: unknown) => e is EvA") },
		"the narrowed type has no Go form":                  func() map[string]*decision { return tgRun(t, "", "(e: Ev) => e is EvC") },
		"the predicate names another parameter":             func() map[string]*decision { return tgRun(t, "", "(e: Ev) => x is EvA") },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) { wantGap(t, run(), name, ids...) })
	}
}

// Rule TR.

const trLib = `package lib

import "testing"

// RegisterSuite is the Go form of the upstream registerSuite: it runs the cases as subtests.
func RegisterSuite(t *testing.T, name string) { t.Run(name, func(*testing.T) {}) }
`

func trInventory(props ...string) func([]m) []m {
	return func([]m) []m {
		r := "pkg:fx/testing#Runner"
		call := m{"parameters": []m{prm("runner", "Runner", false), prm("name", "string", false)}, "returns": "void"}
		inv := []m{
			{"id": r, "name": "Runner", "kind": "interface", "shape": m{"type": "Runner"}},
			{"id": "pkg:fx/testing#registerSuite", "name": "registerSuite", "kind": "function", "shape": m{"type": "(runner: Runner, name: string) => void", "calls": []m{call}}},
			{"id": "pkg:fx/testing#registerSuite::call:0", "parentId": "pkg:fx/testing#registerSuite", "role": "call-overload", "name": "registerSuite call 0", "kind": "call-overload", "shape": call},
		}
		for _, p := range props {
			fn := m{"parameters": []m{prm("name", "string", false)}, "returns": "unknown"}
			inv = append(inv,
				m{"id": r + "::property:" + p, "parentId": r, "role": "property", "name": "Runner." + p, "kind": "property", "shape": m{"name": p, "type": "(name: string) => unknown", "calls": []m{fn}}},
				m{"id": r + "::property:" + p + "::call:0", "parentId": r + "::property:" + p, "role": "call-overload", "name": "Runner." + p + " call 0", "kind": "call-overload", "shape": fn})
		}
		return inv
	}
}

func trRun(t *testing.T, lib, reps string, props ...string) map[string]*decision {
	t.Helper()
	files := map[string]string{"lib/lib.go": lib, "lib/lib_test.go": "package lib\n", "lib/use.go": "package lib\n"}
	if reps != "" {
		files[representationsFile] = reps
	}
	return fixtureRun(t, files, trInventory(props...), map[string]bool{}, nil)
}

const trReps = `{"fx:Runner": "testing.T"}`

func trIDs(props ...string) []string {
	ids := []string{"pkg:fx/testing#Runner"}
	for _, p := range props {
		ids = append(ids, "pkg:fx/testing#Runner::property:"+p, "pkg:fx/testing#Runner::property:"+p+"::call:0")
	}
	return ids
}

// TestTestRunnerAdapterIsDesignedOut: TR designs out an interface of test-runner globals represented by testing.T when the Go form of the
// function that takes it takes a *testing.T.
func TestTestRunnerAdapterIsDesignedOut(t *testing.T) {
	props := []string{"describe", "it", "expect"}
	ds := trRun(t, trLib, trReps, props...)
	wantDesignedOut(t, ds, "TR:", trIDs(props...)...)
	if d := ds["pkg:fx/testing#Runner"]; d != nil && d.Candidate != "lib/lib.go#RegisterSuite" {
		t.Errorf("the designed-out row names the Go function that takes the *testing.T, got %q", d.Candidate)
	}
}

// TestTestRunnerMutantsStayGaps: a member that is not a runner global, a Go counterpart without a *testing.T and a missing representation
// keep the rows gaps.
func TestTestRunnerMutantsStayGaps(t *testing.T) {
	noT := "package lib\n\n// RegisterSuite takes no *testing.T.\nfunc RegisterSuite(name string) {}\n"
	cases := map[string]struct {
		lib, reps string
		props     []string
	}{
		"a member is not a runner global":             {trLib, trReps, []string{"describe", "retries"}},
		"the Go counterpart takes no *testing.T":      {noT, trReps, []string{"describe", "it"}},
		"no documented testing.T representation":      {trLib, "", []string{"describe", "it"}},
		"the representation is another Go type":       {trLib, `{"fx:Runner": "lib.Suite"}`, []string{"describe", "it"}},
		"the interface declares no member to forward": {trLib, trReps, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { wantGap(t, trRun(t, c.lib, c.reps, c.props...), name, trIDs(c.props...)...) })
	}
}

// Rule CC.

const ccLib = `package lib

// Opts is the Go form of the upstream Opts options.
type Opts struct {
	Name     string
	TraceCtx string
}

// UseOpts is the production caller: it reads Name, and copies TraceCtx as Pi does.
func UseOpts(o Opts) string { return o.Name }
`

const ccTypes = "export interface Opts {\n\tname: string;\n\ttraceCtx?: string;\n}\n"

const ccCopy = "export function build(options?: Opts) {\n\treturn { name: options?.name, traceCtx: options?.traceCtx };\n}\n"

func ccInventory(member, source string) func([]m) []m {
	return func([]m) []m {
		o := "pkg:fx/.#Opts"
		return []m{
			{"id": o, "name": "Opts", "kind": "interface", "shape": m{"type": "Opts"}},
			{"id": o + "::property:name", "parentId": o, "role": "property", "name": "Opts.name", "kind": "property", "shape": m{"name": "name", "type": "string"}},
			{"id": o + "::property:" + member, "parentId": o, "role": "property", "name": "Opts." + member, "kind": "property",
				"shape": m{"name": member, "type": "string | undefined", "optional": true}, "source": m{"path": source}},
		}
	}
}

const ccSource = "node_modules/@earendil-works/pi-fx/dist/types.d.ts"

func ccRun(t *testing.T, lib, copySite, member, source string) map[string]*decision {
	t.Helper()
	return ccRunTypes(t, ccTypes, lib, copySite, member, source)
}

func ccRunTypes(t *testing.T, typesTS, lib, copySite, member, source string) map[string]*decision {
	t.Helper()
	saved := carriedDecls
	t.Cleanup(func() { carriedDecls = saved })
	carriedDecls = []carriedDecl{{pkg: "fx", file: "src/types.ts", owner: "Opts", member: "traceCtx", why: "CC fixture: carried with no consumer (LEAD-RULINGS-1520)."}}
	files := map[string]string{
		"lib/lib.go": lib, "lib/lib_test.go": "package lib\n", "lib/use.go": "package lib\n",
		".upstream/current/packages/fx/src/types.ts": typesTS,
		".upstream/current/packages/fx/src/build.ts": copySite,
	}
	return fixtureRun(t, files, ccInventory(member, source), map[string]bool{"lib/lib.go#UseOpts": true}, nil)
}

// TestCarriedWithoutConsumerIsDesignedOut: CC designs out a reviewed member Pi only copies when the Go owner carries it with the same type,
// and keeps the owner closed.
func TestCarriedWithoutConsumerIsDesignedOut(t *testing.T) {
	ds := ccRun(t, ccLib, ccCopy, "traceCtx", ccSource)
	wantDesignedOut(t, ds, "CC", "pkg:fx/.#Opts::property:traceCtx")
	if d := ds["pkg:fx/.#Opts"]; d == nil || d.Gap {
		t.Errorf("the owner closes once its carried member is designed out, got %+v", d)
	}
	if d := ds["pkg:fx/.#Opts::property:traceCtx"]; d != nil && d.Candidate != "lib/lib.go#Opts.TraceCtx" {
		t.Errorf("the designed-out row names the Go carrier, got %q", d.Candidate)
	}
}

// TestCarriedWithoutConsumerMutantsStayGaps: a Pi read (direct or destructured), a Go carrier of another type, no Go carrier, a member the
// table does not list, a member the reviewed owner does not declare or another declaration also has, and a declaration from another
// source file stay gaps.
func TestCarriedWithoutConsumerMutantsStayGaps(t *testing.T) {
	id := "pkg:fx/.#Opts::property:traceCtx"
	wrongType := strings.Replace(ccLib, "TraceCtx string", "TraceCtx int", 1)
	missing := strings.Replace(ccLib, "\tTraceCtx string\n", "", 1)
	cases := map[string]func() map[string]*decision{
		"Pi reads the member": func() map[string]*decision {
			return ccRun(t, ccLib, ccCopy+"export function trace(o: Opts) {\n\treturn start(o.traceCtx);\n}\n", "traceCtx", ccSource)
		},
		"Pi destructures the member": func() map[string]*decision {
			return ccRun(t, ccLib, ccCopy+"export function trace(o: Opts) {\n\tconst { traceCtx } = o;\n\treturn traceCtx;\n}\n", "traceCtx", ccSource)
		},
		"Pi never copies the member either": func() map[string]*decision { return ccRun(t, ccLib, "export const x = 1;\n", "traceCtx", ccSource) },
		"the Go carrier has another type":   func() map[string]*decision { return ccRun(t, wrongType, ccCopy, "traceCtx", ccSource) },
		"no Go carrier":                     func() map[string]*decision { return ccRun(t, missing, ccCopy, "traceCtx", ccSource) },
		"another declaration in the file has the member": func() map[string]*decision {
			return ccRunTypes(t, ccTypes+"export interface Span {\n\ttraceCtx: string;\n}\n", ccLib, ccCopy, "traceCtx", ccSource)
		},
		"the reviewed owner does not declare the member": func() map[string]*decision {
			return ccRunTypes(t, "export interface Opts {\n\tname: string;\n}\nexport interface Base {\n\ttraceCtx?: string;\n}\n", ccLib, ccCopy, "traceCtx", ccSource)
		},
		"the declaration is in another file": func() map[string]*decision {
			return ccRun(t, ccLib, ccCopy, "traceCtx", "node_modules/@earendil-works/pi-fx/dist/other.d.ts")
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) { wantGap(t, run(), name, id) })
	}
	if d := ccRun(t, ccLib, ccCopy, "traceCtx", ccSource)["pkg:fx/.#Opts::property:name"]; d == nil || d.Gap || d.DesignedOut != "" {
		t.Errorf("a member production reads is ported, not designed out: %+v", d)
	}
}

func TestUpstreamSourceFile(t *testing.T) {
	for in, want := range map[string]string{
		"node_modules/@earendil-works/pi-ai/dist/types.d.ts":      "ai/src/types.ts",
		"node_modules/@earendil-works/pi-ai/dist/api/openai.d.ts": "ai/src/api/openai.ts",
		"dist/types.d.ts":                                            "",
		"node_modules/typescript/lib/lib.es5.d.ts":                   "",
		"node_modules/@earendil-works/pi-ai/dist/types.js":           "",
		"node_modules/@earendil-works/pi-coding-agent/dist/x/y.d.ts": "coding-agent/src/x/y.ts",
	} {
		if got := upstreamSourceFile(in); got != want {
			t.Errorf("upstreamSourceFile(%q) = %q, want %q", in, got, want)
		}
	}
}

// Rule RP.

const rpLib = `package lib

import "context"

// Opts is the Go form of the upstream Options bag's per-call members.
type Opts struct{ Name string }

// Services holds the members Go keeps beside the options.
type Services struct{ cwd string }

// CWD is the working directory.
func (s *Services) CWD() string { return s.cwd }

// RunOpts holds the start members.
type RunOpts struct {
	Start   string
	Allowed map[string]struct{}
}

// Result is a tool result.
type Result struct{ IsError bool }

// Hook is the Go form of the upstream hook: its context is spread over the parameters.
type Hook func(ctx context.Context, callID, toolName string, args string, result Result)
`

const rpUse = `package lib

import "context"

// Run is the production caller.
func Run(h Hook) string {
	s := &Services{cwd: "/"}
	o := Opts{Name: "n"}
	r := RunOpts{Start: "s", Allowed: map[string]struct{}{"a": {}}}
	h(context.Background(), "id", "tool", o.Name, Result{IsError: len(r.Allowed) == 0})
	return s.CWD() + r.Start
}
`

const rpPlacements = `{
 "fx:Options": {"go": ["lib/lib.go#Services", "lib/lib.go#RunOpts"], "members": {"allowed": "lib/lib.go#RunOpts.Allowed"}, "reason": "fixture"},
 "fx:HookContext": {"go": ["lib/lib.go#Hook"], "members": {"toolCall": "callID toolName", "isError": "result.IsError"}, "designedOut": {"extra": "fixture: nothing reads it (LEAD-RULINGS-1520)"}, "reason": "fixture"}
}`

func rpInventory(cwdType, allowedType, argsType string) func([]m) []m {
	return func([]m) []m {
		o, h := "pkg:fx/.#Options", "pkg:fx/.#HookContext"
		prop := func(owner, name, typ string) m {
			return m{"id": owner + "::property:" + name, "parentId": owner, "role": "property", "name": name, "kind": "property", "shape": m{"name": name, "type": typ}}
		}
		return []m{
			{"id": o, "name": "Options", "kind": "interface", "shape": m{"type": "Options"}},
			prop(o, "name", "string"), prop(o, "cwd", cwdType), prop(o, "start", "string"), prop(o, "allowed", allowedType),
			{"id": h, "name": "HookContext", "kind": "interface", "shape": m{"type": "HookContext"}},
			prop(h, "toolCall", "ToolCall"), prop(h, "args", argsType), prop(h, "result", "Result"), prop(h, "isError", "boolean"), prop(h, "extra", "string"),
			{"id": "pkg:fx/.#Result", "name": "Result", "kind": "interface", "shape": m{"type": "Result"}},
			prop("pkg:fx/.#Result", "isError", "boolean"),
		}
	}
}

var rpOptions = []string{"pkg:fx/.#Options", "pkg:fx/.#Options::property:name", "pkg:fx/.#Options::property:cwd", "pkg:fx/.#Options::property:start", "pkg:fx/.#Options::property:allowed"}

var rpHook = []string{"pkg:fx/.#HookContext", "pkg:fx/.#HookContext::property:toolCall", "pkg:fx/.#HookContext::property:args", "pkg:fx/.#HookContext::property:result", "pkg:fx/.#HookContext::property:isError"}

type rpCase struct {
	lib, use, placements string
	cwd, allowed, args   string
	reach                map[string]bool
}

func rpRun(t *testing.T, c rpCase) map[string]*decision {
	t.Helper()
	def := func(v, d string) string {
		if v == "" {
			return d
		}
		return v
	}
	files := map[string]string{"lib/lib.go": def(c.lib, rpLib), "lib/use.go": def(c.use, rpUse), "lib/lib_test.go": "package lib\n", placementsFile: def(c.placements, rpPlacements)}
	reach := c.reach
	if reach == nil {
		reach = map[string]bool{"lib/use.go#Run": true}
	}
	return fixtureRun(t, files, rpInventory(def(c.cwd, "string"), def(c.allowed, "string[] | undefined"), def(c.args, "string")), reach, renameTable{"pkg:fx/.#Options": "lib/lib.go#Opts"})
}

// TestReviewedPlacementClosesMembersOnTheTargets: RP finds a member on the placement's Go types (exported members only), a reviewed
// member of another name, the parameters of a func-type placement (by name, by a reviewed parameter list and by a parameter's field), and a cited
// designed-out member.
func TestReviewedPlacementClosesMembersOnTheTargets(t *testing.T) {
	ds := rpRun(t, rpCase{})
	for _, id := range append(append([]string{}, rpOptions...), rpHook...) {
		if d := ds[id]; d == nil || d.Gap || d.DesignedOut != "" {
			t.Errorf("%s: want closed by the placement, got %+v", id, d)
		}
	}
	if d := ds["pkg:fx/.#Options::property:cwd"]; d != nil && d.Candidate != "lib/lib.go#Services.CWD" {
		t.Errorf("cwd is the exported accessor, not the unexported field, got %q", d.Candidate)
	}
	wantDesignedOut(t, ds, "RP:", "pkg:fx/.#HookContext::property:extra")
}

// TestReviewedPlacementMutantsStayGaps: without the entry, with a target member of another type, with no production use (a stub), with
// only an unexported member, with a set of another element type or an upstream list that is not of strings, and with a hook that lacks a
// reviewed parameter or field, or whose parameter type differs, or that nothing uses, the rows stay gaps.
func TestReviewedPlacementMutantsStayGaps(t *testing.T) {
	cwd, allowed, toolCall, isError, args := "pkg:fx/.#Options::property:cwd", "pkg:fx/.#Options::property:allowed", "pkg:fx/.#HookContext::property:toolCall",
		"pkg:fx/.#HookContext::property:isError", "pkg:fx/.#HookContext::property:args"
	cases := map[string]struct {
		c   rpCase
		ids []string
	}{
		"no placement entry":                   {rpCase{placements: "{}"}, []string{cwd, allowed, toolCall, args}},
		"the target member has another type":   {rpCase{lib: strings.Replace(rpLib, "func (s *Services) CWD() string { return s.cwd }", "func (s *Services) CWD() int { return len(s.cwd) }", 1), use: strings.Replace(rpUse, "return s.CWD() + r.Start", "_ = s.CWD()\n\treturn r.Start", 1)}, []string{cwd}},
		"the target member is a stub":          {rpCase{use: strings.Replace(rpUse, "return s.CWD() + r.Start", "return s.cwd + r.Start", 1)}, []string{cwd}},
		"only an unexported member":            {rpCase{lib: strings.Replace(rpLib, "func (s *Services) CWD() string { return s.cwd }", "func (s *Services) path() string { return s.cwd }", 1), use: strings.Replace(rpUse, "s.CWD()", "s.path()", 1)}, []string{cwd}},
		"a set of another element type":        {rpCase{lib: strings.Replace(rpLib, "Allowed map[string]struct{}", "Allowed map[string]bool", 1), use: strings.Replace(rpUse, "map[string]struct{}{\"a\": {}}", "map[string]bool{\"a\": true}", 1)}, []string{allowed}},
		"an upstream list of numbers":          {rpCase{allowed: "number[]"}, []string{allowed}},
		"the set names another member":         {rpCase{placements: strings.Replace(rpPlacements, "RunOpts.Allowed", "RunOpts.Start", 1)}, []string{allowed}},
		"the parameter field has another type": {rpCase{lib: strings.Replace(rpLib, "type Result struct{ IsError bool }", "type Result struct{ IsError string }", 1), use: strings.Replace(rpUse, "Result{IsError: len(r.Allowed) == 0}", `Result{IsError: "no"}`, 1)}, []string{isError}},
		"the hook lacks a reviewed parameter":  {rpCase{lib: strings.Replace(rpLib, "callID, toolName string", "callID string", 1), use: strings.Replace(rpUse, `"id", "tool", `, `"id", `, 1)}, []string{toolCall}},
		"the parameter type has no field":      {rpCase{lib: strings.Replace(rpLib, "type Result struct{ IsError bool }", "type Result struct{ Failed bool }", 1), use: strings.Replace(rpUse, "Result{IsError:", "Result{Failed:", 1)}, []string{isError}},
		"the parameter type differs":           {rpCase{args: "number"}, []string{args}},
		"nothing uses the hook":                {rpCase{reach: map[string]bool{}}, []string{toolCall, args, isError}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { wantGap(t, rpRun(t, c.c), name, c.ids...) })
	}
}

// TestStalePlacementEntriesFail: an entry for no upstream type, without a reason, or with a Go target that does not exist fails the run.
func TestStalePlacementEntriesFail(t *testing.T) {
	for name, placements := range map[string]string{
		"no upstream type": strings.Replace(rpPlacements, `"fx:Options"`, `"fx:Gone"`, 1),
		"no reason": strings.Replace(rpPlacements, `"reason": "fixture"},
 "fx:HookContext"`, `"reason": ""},
 "fx:HookContext"`, 1),
		"no Go target":     strings.Replace(rpPlacements, "lib/lib.go#Services", "lib/lib.go#Gone", 1),
		"no member target": strings.Replace(rpPlacements, "RunOpts.Allowed", "RunOpts.Gone", 1),
	} {
		t.Run(name, func(t *testing.T) {
			rpRun(t, rpCase{placements: placements})
			if err := fixtureLast.det.checkPlacements(); err == nil {
				t.Errorf("%s: want an error", name)
			}
		})
	}
	rpRun(t, rpCase{})
	if err := fixtureLast.det.checkPlacements(); err != nil {
		t.Errorf("the fixture placements are valid: %v", err)
	}
}

// Rule ER.

const erLib = `package lib

// ErrX is the Go form of the upstream ErrX class.
type ErrX struct{ msg string }

func (e *ErrX) Error() string { return e.msg }

// Cause is the cause property; no constructor sets one.
func (e *ErrX) Cause() error { return nil }

// Fail is the production caller.
func Fail() error { return &ErrX{msg: "x"} }
`

func erInventory(member, source, ctorParam string) func([]m) []m {
	return func([]m) []m {
		x := "pkg:fx/.#ErrX"
		typ := "unknown"
		if member == "name" {
			typ = "string"
		}
		shape := m{"type": "ErrX"}
		if ctorParam != "" {
			shape["constructs"] = []m{{"parameters": []m{prm("options", ctorParam, true)}, "returns": "ErrX"}}
		}
		return []m{
			{"id": x, "name": "ErrX", "kind": "class", "shape": shape},
			{"id": x + "::property:message", "parentId": x, "role": "property", "name": "ErrX.message", "kind": "property", "shape": m{"name": "message", "type": "string"},
				"source": m{"path": "node_modules/typescript/lib/lib.es5.d.ts"}},
			{"id": x + "::property:" + member, "parentId": x, "role": "property", "name": "ErrX." + member, "kind": "property", "shape": m{"name": member, "type": typ, "optional": true},
				"source": m{"path": source}},
		}
	}
}

func erRun(t *testing.T, lib, member, source, ctorParam string) map[string]*decision {
	t.Helper()
	files := map[string]string{"lib/lib.go": lib, "lib/lib_test.go": "package lib\n", "lib/use.go": "package lib\n"}
	return fixtureRun(t, files, erInventory(member, source, ctorParam), map[string]bool{"lib/lib.go#Fail": true}, nil)
}

const erTSLib = "node_modules/typescript/lib/lib.es2022.error.d.ts"

// TestInheritedErrorStubIsDesignedOut: ER designs out an inherited, unset Error cause whose Go member is a constant stand-in.
func TestInheritedErrorStubIsDesignedOut(t *testing.T) {
	wantDesignedOut(t, erRun(t, erLib, "cause", erTSLib, ""), "ER:", "pkg:fx/.#ErrX::property:cause")
}

// TestInheritedErrorStubMutantsStayGaps: a Go member that returns state, a cause a constructor can set (ErrorOptions), a member the class
// declares itself and a member that is not an Error runtime member stay gaps.
func TestInheritedErrorStubMutantsStayGaps(t *testing.T) {
	withState := strings.Replace(erLib, "type ErrX struct{ msg string }", "type ErrX struct {\n\tmsg   string\n\tcause error\n}", 1)
	withState = strings.Replace(withState, "func (e *ErrX) Cause() error { return nil }", "func (e *ErrX) Cause() error { return e.cause }", 1)
	code := strings.Replace(erLib, "func (e *ErrX) Cause() error { return nil }", "func (e *ErrX) Code() error { return nil }", 1)
	cases := map[string]struct {
		lib, member, source, ctor string
	}{
		"the Go member returns state":      {withState, "cause", erTSLib, ""},
		"a constructor takes ErrorOptions": {erLib, "cause", erTSLib, "ErrorOptions"},
		"the class declares cause itself":  {erLib, "cause", "node_modules/@earendil-works/pi-fx/dist/errors.d.ts", ""},
		"not an Error runtime member":      {code, "code", erTSLib, ""},
		"name is not an unset member":      {strings.Replace(erLib, "func (e *ErrX) Cause() error { return nil }", "func (e *ErrX) Name() string { return \"ErrX\" }", 1), "name", erTSLib, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			wantGap(t, erRun(t, c.lib, c.member, c.source, c.ctor), name, "pkg:fx/.#ErrX::property:"+c.member)
		})
	}
}

// RP folds.

const sfLib = `package lib

import "context"

// Builder carries the output and the event stream.
type Builder struct{ n int }

// Process is the Go form of the upstream process: src is the decoder, out and ev fold into the builder.
func Process(ctx context.Context, decoder string, builder *Builder, options int) { builder.n = len(decoder) + options }

// Hooks holds a callback whose model argument is bound per request.
type Hooks struct{ OnEvent func(data any) error }

// Run is the production caller.
func Run() int {
	b := &Builder{}
	Process(context.Background(), "x", b, 1)
	h := Hooks{OnEvent: func(any) error { return nil }}
	_ = h.OnEvent(b.n)
	return b.n
}
`

const sfPlacements = `{
 "fx:process": {"go": [], "folds": {"src": "decoder", "out": "builder", "ev": "builder"}, "reason": "fixture"},
 "fx:Hooks.onEvent": {"go": [], "folds": {"model": "bound: the fixture binds it"}, "reason": "fixture"}
}`

func sfInventory(options, returns string) func([]m) []m {
	return func([]m) []m {
		call := m{"parameters": []m{prm("src", "string", false), prm("out", "Out", false), prm("ev", "Ev", false), prm("options", options, true)}, "returns": returns}
		h := "pkg:fx/.#Hooks"
		return []m{
			{"id": "pkg:fx/.#process", "name": "process", "kind": "function", "shape": m{"type": "(...) => " + returns, "calls": []m{call}}},
			{"id": "pkg:fx/.#process::call:0", "parentId": "pkg:fx/.#process", "role": "call-overload", "name": "process call 0", "kind": "call-overload", "shape": call},
			{"id": h, "name": "Hooks", "kind": "interface", "shape": m{"type": "Hooks"}},
			{"id": h + "::property:onEvent", "parentId": h, "role": "property", "name": "Hooks.onEvent", "kind": "property",
				"shape": m{"name": "onEvent", "optional": true, "type": "((data: unknown, model: string) => void) | undefined"}},
		}
	}
}

func sfRun(t *testing.T, lib, placements, options, returns string) map[string]*decision {
	t.Helper()
	files := map[string]string{"lib/lib.go": lib, "lib/lib_test.go": "package lib\n", "lib/use.go": "package lib\n", placementsFile: placements}
	return fixtureRun(t, files, sfInventory(options, returns), map[string]bool{"lib/lib.go#Run": true}, nil)
}

var sfIDs = []string{"pkg:fx/.#process", "pkg:fx/.#process::call:0", "pkg:fx/.#Hooks", "pkg:fx/.#Hooks::property:onEvent"}

// TestFoldedSignaturesClose: RP folds accept a call whose upstream parameters fold into fewer Go parameters, and a callback whose
// upstream argument Go binds per request.
func TestFoldedSignaturesClose(t *testing.T) {
	ds := sfRun(t, sfLib, sfPlacements, "number", "Promise<void>")
	for _, id := range sfIDs {
		if d := ds[id]; d == nil || d.Gap {
			t.Errorf("%s: want closed by the folds, got %+v", id, d)
		}
	}
}

// TestFoldedSignatureMutantsStayGaps: no folds, a fold onto a missing Go parameter, a Go parameter no upstream parameter maps to, an
// unfolded parameter of another type, a bound parameter with no reason and a result of another type stay gaps.
func TestFoldedSignatureMutantsStayGaps(t *testing.T) {
	call, hook := "pkg:fx/.#process::call:0", "pkg:fx/.#Hooks::property:onEvent"
	cases := map[string]struct {
		lib, placements, options, returns string
		ids                               []string
	}{
		"no folds":                        {sfLib, "{}", "number", "Promise<void>", []string{call, hook}},
		"a fold onto a missing parameter": {sfLib, strings.Replace(sfPlacements, `"ev": "builder"`, `"ev": "events"`, 1), "number", "Promise<void>", []string{call}},
		"an unfolded parameter differs":   {sfLib, sfPlacements, "string", "Promise<void>", []string{call}},
		"a bound parameter has no reason": {sfLib, strings.Replace(sfPlacements, "bound: the fixture binds it", "bound: ", 1), "number", "Promise<void>", []string{hook}},
		"the result differs":              {sfLib, sfPlacements, "number", "Promise<number>", []string{call}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ds := sfRun(t, c.lib, c.placements, c.options, c.returns)
			wantGap(t, ds, name, c.ids...)
		})
	}
	// With out and ev folded into the decoder, the builder stands for no upstream parameter.
	noImage := strings.Replace(sfPlacements, `"out": "builder", "ev": "builder"`, `"out": "decoder", "ev": "decoder"`, 1)
	wantGap(t, sfRun(t, sfLib, noImage, "number", "Promise<void>"), "a Go parameter stands for no upstream parameter", call)
}

// TestFoldEntriesNameAFunctionOrProperty: a folds entry names an upstream function or Owner.property; a stale one fails the run.
func TestFoldEntriesNameAFunctionOrProperty(t *testing.T) {
	sfRun(t, sfLib, sfPlacements, "number", "Promise<void>")
	if err := fixtureLast.det.checkPlacements(); err != nil {
		t.Fatalf("the fold entries are valid: %v", err)
	}
	for name, placements := range map[string]string{
		"no such function": strings.Replace(sfPlacements, `"fx:process"`, `"fx:gone"`, 1),
		"no such property": strings.Replace(sfPlacements, `"fx:Hooks.onEvent"`, `"fx:Hooks.gone"`, 1),
		"no folds":         strings.Replace(sfPlacements, `"folds": {"src": "decoder", "out": "builder", "ev": "builder"}, `, "", 1),
	} {
		sfRun(t, sfLib, placements, "number", "Promise<void>")
		if err := fixtureLast.det.checkPlacements(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// Rule TL.

const tlLib = `package lib

// Spec is the Go form of the upstream Spec schema.
type Spec struct{ Items map[string]string }
`

func tlRun(t *testing.T, lib, ts string, params ...string) map[string]*decision {
	t.Helper()
	files := map[string]string{"lib/lib.go": lib, "lib/lib_test.go": "package lib\n", "lib/use.go": "package lib\n",
		".upstream/current/packages/fx/src/types.ts": ts}
	inv := func([]m) []m {
		var tps []m
		for _, p := range params {
			tps = append(tps, m{"name": p})
		}
		var rows []m
		for _, name := range []string{"KeysOf", "Alias2"} {
			rows = append(rows, m{"id": "pkg:fx/.#" + name, "name": name, "kind": "type-alias", "shape": m{"type": name, "typeParameters": tps}, "source": m{"path": "packages/fx/src/types.ts"}})
		}
		return rows
	}
	return fixtureRun(t, files, inv, map[string]bool{}, nil)
}

const tlTypes = "export interface Spec {\n\titems: Record<string, string>;\n}\n" +
	"export type KeysOf<S extends Spec> = keyof S[\"items\"] & string;\n" +
	"export type Alias2<S extends Spec> = KeysOf<S>;\n"

// TestTypeLevelAliasIsDesignedOut: TL designs out a generic alias that computes a type from its parameters, and an alias that only
// instantiates one, naming the Go type the constraint carries.
func TestTypeLevelAliasIsDesignedOut(t *testing.T) {
	ds := tlRun(t, tlLib, tlTypes, "S")
	wantDesignedOut(t, ds, "TL:", "pkg:fx/.#KeysOf", "pkg:fx/.#Alias2")
	if d := ds["pkg:fx/.#KeysOf"]; d != nil && d.Candidate != "lib/lib.go#Spec" {
		t.Errorf("the designed-out row names the Go carrier, got %q", d.Candidate)
	}
}

// TestTypeLevelAliasMutantsStayGaps: an alias that declares a method, one without a type operator, one with no type parameters, one
// whose types have no Go form, and one that instantiates an alias that is not type-level stay gaps.
func TestTypeLevelAliasMutantsStayGaps(t *testing.T) {
	keys := "pkg:fx/.#KeysOf"
	cases := map[string]struct {
		lib, ts string
		params  []string
		ids     []string
	}{
		"a method in the body": {tlLib, strings.Replace(tlTypes, `keyof S["items"] & string;`, `keyof S["items"] & { add(x: S): void };`, 1), []string{"S"}, []string{keys, "pkg:fx/.#Alias2"}},
		"no type operator":     {tlLib, strings.Replace(tlTypes, `keyof S["items"] & string;`, "S | number;", 1), []string{"S"}, []string{keys, "pkg:fx/.#Alias2"}},
		"no type parameters":   {tlLib, tlTypes, nil, []string{keys}},
		"no Go carrier":        {"package lib\n", tlTypes, []string{"S"}, []string{keys, "pkg:fx/.#Alias2"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { wantGap(t, tlRun(t, c.lib, c.ts, c.params...), name, c.ids...) })
	}
}

// Rule AP.

const apLib = `package lib

// Hook is the Go form of the upstream onStep callback.
type Hook func(n int) string

// Runner keeps onStep behind an accessor pair.
type Runner struct{ hook Hook }

// OnStepHook returns the onStep callback.
func (r *Runner) OnStepHook() Hook { return r.hook }

// SetOnStep replaces the onStep callback.
func (r *Runner) SetOnStep(h Hook) { r.hook = h }

// Use is the production caller.
func Use() string {
	r := &Runner{}
	r.SetOnStep(func(int) string { return "x" })
	return r.OnStepHook()(1)
}
`

func apRun(t *testing.T, lib string, readonly bool, reach map[string]bool) map[string]*decision {
	t.Helper()
	files := map[string]string{"lib/lib.go": lib, "lib/lib_test.go": "package lib\n", "lib/use.go": "package lib\n"}
	inv := func([]m) []m {
		r := "pkg:fx/.#Runner"
		return []m{
			{"id": r, "name": "Runner", "kind": "class", "shape": m{"type": "Runner"}},
			{"id": r + "::property:onStep", "parentId": r, "role": "property", "name": "Runner.onStep", "kind": "property",
				"shape": m{"name": "onStep", "optional": true, "readonly": readonly, "type": "((n: number) => string) | undefined"}},
		}
	}
	if reach == nil {
		reach = map[string]bool{"lib/lib.go#Use": true}
	}
	return fixtureRun(t, files, inv, reach, nil)
}

// TestAccessorPairClosesAWritableProperty: AP closes a writable property on a getter and setter that agree with its type.
func TestAccessorPairClosesAWritableProperty(t *testing.T) {
	if d := apRun(t, apLib, false, nil)["pkg:fx/.#Runner::property:onStep"]; d == nil || d.Gap || !strings.Contains(d.Evidence, "AP:") {
		t.Fatalf("want closed by AP, got %+v", d)
	}
}

// TestAccessorPairMutantsStayGaps: a readonly property, a pair no production code uses, a getter of another type, a missing setter and
// a getter of another name stay gaps.
func TestAccessorPairMutantsStayGaps(t *testing.T) {
	id := "pkg:fx/.#Runner::property:onStep"
	otherType := strings.Replace(apLib, "func (r *Runner) OnStepHook() Hook { return r.hook }", "func (r *Runner) OnStepHook() int { return 0 }", 1)
	otherType = strings.Replace(otherType, "return r.OnStepHook()(1)", "_ = r.OnStepHook()\n\treturn \"\"", 1)
	noSetter := strings.Replace(apLib, "func (r *Runner) SetOnStep(h Hook) { r.hook = h }", "func (r *Runner) ReplaceOnStep(h Hook) { r.hook = h }", 1)
	noSetter = strings.Replace(noSetter, "r.SetOnStep(", "r.ReplaceOnStep(", 1)
	otherName := strings.ReplaceAll(apLib, "OnStepHook", "StepCallback")
	cases := map[string]func() map[string]*decision{
		"readonly":                 func() map[string]*decision { return apRun(t, apLib, true, nil) },
		"no production use":        func() map[string]*decision { return apRun(t, apLib, false, map[string]bool{}) },
		"a getter of another type": func() map[string]*decision { return apRun(t, otherType, false, nil) },
		"no setter":                func() map[string]*decision { return apRun(t, noSetter, false, nil) },
		"a getter of another name": func() map[string]*decision { return apRun(t, otherName, false, nil) },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) { wantGap(t, run(), name, id) })
	}
}
