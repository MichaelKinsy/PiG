package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestStripTSComments(t *testing.T) {
	for in, want := range map[string]string{
		"{ /** doc: x */ a: string }":     "{   a: string }",
		"{ a: string // tail\n; b: any }": "{ a: string \n; b: any }",
		`{ a: "http://x" }`:               `{ a: "http://x" }`,
		`{ a: "/* kept */" }`:             `{ a: "/* kept */" }`,
	} {
		if got := stripTSComments(in); got != want {
			t.Errorf("stripTSComments(%q) = %q, want %q", in, got, want)
		}
	}
}

const aliasLib = `
// KeyUp names the fx.up key; KeyN is not a string.
const KeyUp = "fx.up"

const KeyN = 3

// Hooked carries a callback the wire never encodes (json "-"), beside a plain value hidden the same way.
type Hooked struct {
	Name   string ` + "`json:\"name\"`" + `
	OnDone func(string) error ` + "`json:\"-\"`" + `
	Secret string ` + "`json:\"-\"`" + `
}

// Opts is the Go form of the upstream Opts alias.
type Opts struct {
	Name    string ` + "`json:\"name\"`" + `
	Retries int    ` + "`json:\"retries\"`" + `
}

// Base is the Go form of the upstream Base interface.
type Base struct {
	ID string ` + "`json:\"id\"`" + `
}

// Ext embeds Base: the Go form of the intersection Base & { extra: number }.
type Ext struct {
	Base
	Extra int ` + "`json:\"extra\"`" + `
}

// Flat repeats no Base field: the intersection Base & { extra: number } is not carried.
type Flat struct {
	Extra int ` + "`json:\"extra\"`" + `
}

type Shape interface{ Area() int }

type Sq struct{}

func (Sq) Area() int { return 1 }

type Ci struct{}

func (*Ci) Area() int { return 2 }

type Tri struct{}

// Plain has no methods, so it cannot stand for a union of classes.
type Plain struct{}

func UseAliases() int {
	o := Opts{Name: "n", Retries: 1}
	e := Ext{Base: Base{ID: "i"}, Extra: 2}
	f := Flat{Extra: 3}
	var s []Shape = []Shape{Sq{}, &Ci{}}
	_ = Plain{}
	h := Hooked{Name: "h", OnDone: func(string) error { return nil }, Secret: "s"}
	_, _, _ = h.Name, h.OnDone, h.Secret
	_ = Tri{}
	_, _ = KeyUp, KeyN
	return o.Retries + e.Extra + f.Extra + s[0].Area() + len(o.Name) + len(e.ID)
}
`

const aliasTest = `
func TestAliases(t *testing.T) {
	if UseAliases() == 0 {
		t.Fatal("aliases")
	}
}
`

func aliasFixture(t *testing.T, ts map[string]string, rows []m, renames renameTable) map[string]*decision {
	files := map[string]string{
		"lib/lib.go":      fxLib + aliasLib,
		"lib/lib_test.go": fxTest + aliasTest,
		"lib/use.go":      fxUse + "\nfunc init() { _ = UseAliases }\n",
	}
	for name, body := range ts {
		files[".upstream/current/packages/fx/src/"+strings.ToLower(name)+".ts"] = body
	}
	reach := map[string]bool{"lib/use.go#Members": true, "lib/lib.go#UseAliases": true}
	return fixtureRun(t, files, func(inv []m) []m { return rows }, reach, renames)
}

func aliasRow(name string) m {
	return m{"id": "pkg:fx/.#" + name, "name": name, "kind": "type-alias", "shape": m{"type": name, "aliasTarget": name}}
}

func ifaceRow(name string, props ...string) []m {
	rows := []m{{"id": "pkg:fx/.#" + name, "name": name, "kind": "interface", "shape": m{"type": name}}}
	for _, p := range props {
		rows = append(rows, m{"id": "pkg:fx/.#" + name + "::property:" + p, "parentId": "pkg:fx/.#" + name, "role": "property", "name": name + "." + p, "kind": "property", "shape": m{"name": p, "type": "string"}})
	}
	return rows
}

// TestRecordOfNeverIsTheEmptyStruct (T6n): Record<string, never> admits no key, the empty object (durable generation.ts:49
// GenerationInput = Record<string, never>), which is the Go struct{}; a struct with a field, or a record of a real value type, is not it.
func TestRecordOfNeverIsTheEmptyStruct(t *testing.T) {
	run := func(body, goType string) *decision {
		files := map[string]string{
			"lib/lib.go":      fxLib + aliasLib + "\n// Empty is the Go form.\ntype Empty " + goType + "\n\n// UseEmpty uses Empty.\nfunc UseEmpty() Empty { return Empty{} }\n",
			"lib/lib_test.go": fxTest + aliasTest + "\nfunc TestEmpty(t *testing.T) { _ = UseEmpty() }\n",
			"lib/use.go":      fxUse + "\nfunc init() { _ = UseAliases }\n",
			".upstream/current/packages/fx/src/empty.ts": "export type Empty = " + body + ";\n",
		}
		reach := map[string]bool{"lib/use.go#Members": true, "lib/lib.go#UseAliases": true, "lib/lib.go#UseEmpty": true}
		return fixtureRun(t, files, func([]m) []m { return []m{aliasRow("Empty")} }, reach, renameTable{"pkg:fx/.#Empty": "lib/lib.go#Empty"})["pkg:fx/.#Empty"]
	}
	if d := run("Record<string, never>", "struct{}"); d.Gap {
		t.Fatalf("Record<string, never> is the empty struct, got %+v", d)
	}
	if d := run("Record<string, never>", "struct{ A int }"); !d.Gap {
		t.Fatalf("a struct with a field is not the empty record, got %+v", d)
	}
	if d := run("Record<string, string>", "struct{}"); !d.Gap {
		t.Fatalf("a record of strings is not the empty struct, got %+v", d)
	}
}

// TestOrderedObjectIsARecord (T6r): a TypeScript object keeps its keys in insertion order, so its Go port is an ordered object (Get by
// key plus an All iterator in insertion order), as chord/delta.JsonObject is for durable types.ts:15 JsonObject = { [key: string]:
// JsonValue }. All alone, Get alone, or a different value type is not that record.
func TestOrderedObjectIsARecord(t *testing.T) {
	run := func(methods string) *decision {
		ordered := "package lib\n\nimport \"iter\"\n\nvar _ iter.Seq[int]\n\n// Ordered is an insertion-ordered object.\ntype Ordered struct{ keys []string }\n" + methods
		files := map[string]string{
			"lib/lib.go":      fxLib + aliasLib + "\n// Rec is the Go form.\ntype Rec = *Ordered\n\n// UseRec uses Rec.\nfunc UseRec() Rec { return &Ordered{} }\n",
			"lib/ordered.go":  ordered,
			"lib/lib_test.go": fxTest + aliasTest + "\nfunc TestRec(t *testing.T) { _ = UseRec() }\n",
			"lib/use.go":      fxUse + "\nfunc init() { _ = UseAliases }\n",
			".upstream/current/packages/fx/src/rec.ts": "export type Rec = { [key: string]: string };\n",
		}
		reach := map[string]bool{"lib/use.go#Members": true, "lib/lib.go#UseAliases": true, "lib/lib.go#UseRec": true}
		return fixtureRun(t, files, func([]m) []m { return []m{aliasRow("Rec")} }, reach, renameTable{"pkg:fx/.#Rec": "lib/lib.go#Rec"})["pkg:fx/.#Rec"]
	}
	const all = "\n// All yields members in insertion order.\nfunc (o *Ordered) All() iter.Seq2[string, string] { return func(func(string, string) bool) {} }\n"
	const get = "\n// Get returns a member.\nfunc (o *Ordered) Get(key string) (string, bool) { return \"\", false }\n"
	const getInt = "\n// Get returns a member.\nfunc (o *Ordered) Get(key string) (int, bool) { return 0, false }\n"
	if d := run(all + get); d.Gap {
		t.Fatalf("an ordered object with Get and All is the record, got %+v %s", d, d.Detail)
	}
	if d := run(all); !d.Gap {
		t.Fatalf("All alone is an iterable, not a record, got %+v", d)
	}
	if d := run(get); !d.Gap {
		t.Fatalf("Get alone is a lookup, not a record, got %+v", d)
	}
	if d := run(all + getInt); !d.Gap {
		t.Fatalf("Get and All disagreeing on the value type is not a record, got %+v", d)
	}
	const allInt = "\n// All yields members in insertion order.\nfunc (o *Ordered) All() iter.Seq2[string, int] { return func(func(string, int) bool) {} }\n"
	if d := run(allInt + getInt); !d.Gap {
		t.Fatalf("an ordered object of int is not Record<string, string>, got %+v", d)
	}
	// A record is keyed by string in both directions: an iterator over int keys, or a lookup by int, is a list or an index, not that record.
	const allIntKey = "\n// All yields members in insertion order.\nfunc (o *Ordered) All() iter.Seq2[int, string] { return func(func(int, string) bool) {} }\n"
	if d := run(allIntKey + get); !d.Gap {
		t.Fatalf("an All over int keys is not the record, got %+v", d)
	}
	const getIntKey = "\n// Get returns a member.\nfunc (o *Ordered) Get(key int) (string, bool) { return \"\", false }\n"
	if d := run(all + getIntKey); !d.Gap {
		t.Fatalf("a Get by int is not the record, got %+v", d)
	}
}

// TestFunctionMemberHiddenFromJSON (T12m): an upstream callback member is the Go func field even when json "-" keeps it off the
// wire (a function has no JSON form), while a data member hidden by json "-" is still not a member.
func TestFunctionMemberHiddenFromJSON(t *testing.T) {
	rn := renameTable{"pkg:fx/.#Hooked": "lib/lib.go#Hooked"}
	ds := aliasFixture(t, map[string]string{"hooked": "export type Hooked = { name: string; onDone?: (v: string) => Promise<void> };\n"}, []m{aliasRow("Hooked")}, rn)
	if d := ds["pkg:fx/.#Hooked"]; d.Gap {
		t.Fatalf("a callback member is the json-hidden func field: %+v", d)
	}
	ds = aliasFixture(t, map[string]string{"hooked": "export type Hooked = { name: string; secret: string };\n"}, []m{aliasRow("Hooked")}, rn)
	if d := ds["pkg:fx/.#Hooked"]; !d.Gap || !strings.Contains(d.Detail, "secret") {
		t.Fatalf("a data member hidden by json \"-\" stays a gap, got %+v", d)
	}
}

// TestObjectLiteralAlias (A6): an alias of an object literal is a Go struct with a field for each member; readonly, comments and
// members of type never do not hide or invent members.
func TestObjectLiteralAlias(t *testing.T) {
	body := "{ /** doc: the name */ readonly name: string; retries: number; readonly history?: never }"
	rn := renameTable{"pkg:fx/.#Opts": "lib/lib.go#Opts"}
	ds := aliasFixture(t, map[string]string{"opts": "export type Opts = " + body + ";\n"}, []m{aliasRow("Opts")}, rn)
	if d := ds["pkg:fx/.#Opts"]; d.Gap {
		t.Fatalf("an object literal alias with a field per member is not a gap: %+v detail=%s", d, d.Detail)
	}
	// A member with no Go field is refuted, not skipped.
	ds = aliasFixture(t, map[string]string{"opts": "export type Opts = { name: string; retries: number; timeoutMs: number };\n"}, []m{aliasRow("Opts")}, rn)
	if d := ds["pkg:fx/.#Opts"]; !d.Gap || !strings.Contains(d.Detail, "timeoutMs") {
		t.Fatalf("a member with no Go field stays a gap naming it, got %+v", d)
	}
	// A member of another type is refuted.
	ds = aliasFixture(t, map[string]string{"opts": "export type Opts = { name: number; retries: number };\n"}, []m{aliasRow("Opts")}, rn)
	if d := ds["pkg:fx/.#Opts"]; !d.Gap {
		t.Fatalf("a member of another type stays a gap, got %+v", d)
	}
	// A struct is required: a Go string type is not an object.
	ds = aliasFixture(t, map[string]string{"modeobj": "export type ModeObj = { name: string };\n"}, []m{aliasRow("ModeObj")}, renameTable{"pkg:fx/.#ModeObj": "lib/lib.go#Mode"})
	if d := ds["pkg:fx/.#ModeObj"]; !d.Gap || !strings.Contains(d.Detail, "A6") {
		t.Fatalf("an object literal against a Go string type is an A6 gap, got %+v", d)
	}
}

// TestIntersectionAlias (A7): every member of every operand is a field of the Go struct, own or promoted through an embedded struct.
func TestIntersectionAlias(t *testing.T) {
	rows := append(ifaceRow("Base", "id"), aliasRow("Ext"), aliasRow("Flat"))
	ts := map[string]string{
		"base": "export interface Base { id: string }\n",
		"ext":  "export type Ext = Base & { extra: number };\n",
		"flat": "export type Flat = Base & { extra: number };\n",
	}
	rn := renameTable{"pkg:fx/.#Ext": "lib/lib.go#Ext", "pkg:fx/.#Flat": "lib/lib.go#Flat", "pkg:fx/.#Base": "lib/lib.go#Base"}
	ds := aliasFixture(t, ts, rows, rn)
	if d := ds["pkg:fx/.#Ext"]; d.Gap {
		t.Fatalf("an intersection carried by an embedded struct plus a field is not a gap: %+v", d)
	}
	if d := ds["pkg:fx/.#Flat"]; !d.Gap || !strings.Contains(d.Detail, "A7") || !strings.Contains(d.Detail, "id") {
		t.Fatalf("an operand member with no Go field stays an A7 gap, got %+v", d)
	}
	// An operand with no recorded properties (Record, mapped or conditional types) is undecided, never accepted.
	ts["ext"] = "export type Ext = Base & Record<string, unknown>;\n"
	ds = aliasFixture(t, ts, rows, rn)
	if d := ds["pkg:fx/.#Ext"]; !d.Gap || d.Reason != reasonUndecided {
		t.Fatalf("an operand the rule cannot read leaves the row undecided, got %+v", d)
	}
	// A Go type that is not a struct cannot carry an intersection.
	ts["modeobj"] = "export type ModeObj = Base & { extra: number };\n"
	rn["pkg:fx/.#ModeObj"] = "lib/lib.go#Mode"
	ds = aliasFixture(t, ts, append(rows, aliasRow("ModeObj")), rn)
	if d := ds["pkg:fx/.#ModeObj"]; !d.Gap || !strings.Contains(d.Detail, "A7") {
		t.Fatalf("an intersection against a Go string type is an A7 gap, got %+v", d)
	}
}

// TestBrandedPrimitiveAlias (A8b): P & { readonly [brand]: ... } is the primitive P (the unique-symbol brand is erased), so a Go type of that
// primitive agrees, a Go type of another primitive does not, and a second ordinary member keeps the intersection rules.
func TestBrandedPrimitiveAlias(t *testing.T) {
	rows := []map[string]any{aliasRow("Tag")}
	rn := renameTable{"pkg:fx/.#Tag": "lib/lib.go#Mode"}
	run := func(body string) tri {
		ds := aliasFixture(t, map[string]string{"tag": body}, rows, rn)
		if ds["pkg:fx/.#Tag"].Gap {
			return no
		}
		return yes
	}
	if run("declare const b: unique symbol;\nexport type Tag = string & { readonly [b]: \"tag\" };\n") != yes {
		t.Fatal("a branded string against a Go string type is not a gap")
	}
	if run("declare const b: unique symbol;\nexport type Tag = number & { readonly [b]: \"tag\" };\n") != no {
		t.Fatal("a branded number against a Go string type is a gap")
	}
	if run("export type Tag = string & { readonly extra: number };\n") != no {
		t.Fatal("an ordinary second member is not a brand")
	}
}

// TestNamedUnionAlias (A8): a union of named upstream types is a Go interface with methods that each member's Go type implements.
func TestNamedUnionAlias(t *testing.T) {
	classes := func(names ...string) []m {
		var rows []m
		for _, n := range names {
			rows = append(rows, m{"id": "pkg:fx/.#" + n, "name": n, "kind": "class", "shape": m{"type": n}})
		}
		return rows
	}
	rn := renameTable{"pkg:fx/.#Shape": "lib/lib.go#Shape"}
	ok := aliasFixture(t, map[string]string{"shape": "export type Shape = Sq | Ci;\n"}, append(classes("Sq", "Ci"), aliasRow("Shape")), rn)
	if d := ok["pkg:fx/.#Shape"]; d.Gap {
		t.Fatalf("a union whose members implement the Go interface (value or pointer receiver) is not a gap: %+v", d)
	}
	// Tri has no Area method: a member that does not implement the interface refutes the mapping.
	bad := aliasFixture(t, map[string]string{"shape": "export type Shape = Sq | Tri;\n"}, append(classes("Sq", "Tri"), aliasRow("Shape")), rn)
	if d := bad["pkg:fx/.#Shape"]; !d.Gap || !strings.Contains(d.Detail, "A8") || !strings.Contains(d.Detail, "Tri") {
		t.Fatalf("a member that does not implement the interface stays an A8 gap, got %+v", d)
	}
	// A member with no ledger row is the Go type of the same name in the union's Go package (A8n).
	unlisted := aliasFixture(t, map[string]string{"shape": "export type Shape = Sq | Ci;\n"}, append(classes("Sq"), aliasRow("Shape")), rn)
	if d := unlisted["pkg:fx/.#Shape"]; d.Gap {
		t.Fatalf("a member with no row resolves by name in the Go package: %+v %s", d, d.Detail)
	}
	// A member with no Go type leaves the row undecided.
	none := aliasFixture(t, map[string]string{"shape": "export type Shape = Sq | Missing;\n"}, append(classes("Sq"), aliasRow("Shape")), rn)
	if d := none["pkg:fx/.#Shape"]; !d.Gap {
		t.Fatalf("a member with no Go type stays a gap, got %+v", d)
	}
	// An interface with no methods says nothing about its members: the Go type must be an interface with methods.
	rn2 := renameTable{"pkg:fx/.#Shape": "lib/lib.go#Plain"}
	plain := aliasFixture(t, map[string]string{"shape": "export type Shape = Sq | Ci;\n"}, append(classes("Sq", "Ci"), aliasRow("Shape")), rn2)
	if d := plain["pkg:fx/.#Shape"]; !d.Gap || !strings.Contains(d.Detail, "A8") {
		t.Fatalf("a union against a non-interface stays an A8 gap, got %+v", d)
	}
}

// TestNamedUnionAliasAsStruct (A8s): a union of named upstream types whose Go form is a struct is judged by the union rules (Pi
// mcp-servers.ts:150 `McpServerConfig = McpStdioServerConfig | McpHttpServerConfig` is one Go struct holding both members'
// properties, T10d). A struct that lacks a member's property stays the A8 gap.
func TestNamedUnionAliasAsStruct(t *testing.T) {
	rows := append(append(ifaceRow("Stdio", "command"), ifaceRow("Http", "url")...), aliasRow("Server"))
	run := func(fields string) *decision {
		files := map[string]string{
			"lib/lib.go":      fxLib + aliasLib + "\n// Server holds every member's properties.\ntype Server struct {\n" + fields + "}\n\nvar _ = Server{}\n",
			"lib/lib_test.go": fxTest + aliasTest,
			"lib/use.go":      fxUse + "\nfunc init() { _ = UseAliases }\n",
			".upstream/current/packages/fx/src/server.ts": "export interface Stdio { command: string }\nexport interface Http { url: string }\nexport type Server = Stdio | Http;\n",
		}
		reach := map[string]bool{"lib/use.go#Members": true, "lib/lib.go#UseAliases": true}
		return fixtureRun(t, files, func(inv []m) []m { return rows }, reach, renameTable{"pkg:fx/.#Server": "lib/lib.go#Server"})["pkg:fx/.#Server"]
	}
	both := "\tCommand string `json:\"command,omitempty\"`\n\tURL string `json:\"url,omitempty\"`\n"
	if d := run(both); d == nil || d.Gap && d.Reason == reasonType {
		t.Fatalf("a struct holding every member's properties is the union, got %+v", d)
	}
	if d := run("\tCommand string `json:\"command,omitempty\"`\n"); d == nil || !d.Gap || !strings.Contains(d.Detail, "A8") {
		t.Fatalf("a struct lacking a member's property stays the A8 gap, got %+v", d)
	}
}

// TestIntersectionOperandForms (A7): Pick, generic operands, aliases of intersections and AbortSignal members.
func TestIntersectionOperandForms(t *testing.T) {
	rows := append(ifaceRow("Base", "id", "other"), aliasRow("Ext"))
	rows = append(rows, m{"id": "pkg:fx/.#Base::property:signal", "parentId": "pkg:fx/.#Base", "role": "property", "name": "Base.signal", "kind": "property", "shape": m{"name": "signal", "type": "AbortSignal", "optional": true}})
	rn := renameTable{"pkg:fx/.#Ext": "lib/lib.go#Ext", "pkg:fx/.#Base": "lib/lib.go#Base"}
	run := func(body string) *decision {
		ts := map[string]string{"base": "export interface Base { id: string; other: string; signal?: AbortSignal }\n", "ext": "export type Ext = " + body + ";\n"}
		return aliasFixture(t, ts, rows, rn)["pkg:fx/.#Ext"]
	}
	// Pick keeps only the named members; Ext (Base{ID} + Extra) has fields for id and extra but not for other.
	if d := run(`Pick<Base, "id"> & { extra: number }`); d.Gap {
		t.Fatalf("Pick of a member the Go struct carries is not a gap: %+v", d)
	}
	if d := run(`Pick<Base, "id" | "other"> & { extra: number }`); !d.Gap || !strings.Contains(d.Detail, "other") {
		t.Fatalf("Pick of a member with no Go field stays a gap naming it, got %+v", d)
	}
	if d := run(`Pick<Base, "nope"> & { extra: number }`); !d.Gap {
		t.Fatalf("Pick of a member the interface does not record stays a gap, got %+v", d)
	}
	// An AbortSignal member is the context.Context argument when every Go consumer of Ext takes one (T12s): Ext has no signal field
	// and needs none. With no such consumer the member stays unaccounted.
	if d := run(`Pick<Base, "id" | "signal"> & { extra: number }`); d.Reason != reasonUndecided || !strings.Contains(d.Detail, "T12s") {
		t.Fatalf("an AbortSignal member with no context-taking consumer stays undecided: %+v", d)
	}
	ts := map[string]string{"base": "export interface Base { id: string; other: string; signal?: AbortSignal }\n", "ext": "export type Ext = Pick<Base, \"id\" | \"signal\"> & { extra: number };\n"}
	files := map[string]string{
		"lib/lib.go":      fxLib + aliasLib + "\n// RunExt runs e.\nfunc RunExt(ctx context.Context, e Ext) error { return ctx.Err() }\n",
		"lib/lib_test.go": fxTest + aliasTest,
		"lib/use.go":      fxUse + "\nfunc init() { _ = UseAliases }\n",
	}
	for name, body := range ts {
		files[".upstream/current/packages/fx/src/"+name+".ts"] = body
	}
	reach := map[string]bool{"lib/use.go#Members": true, "lib/lib.go#UseAliases": true}
	if d := fixtureRun(t, files, func([]m) []m { return rows }, reach, rn)["pkg:fx/.#Ext"]; d.Gap {
		t.Fatalf("an AbortSignal member carried by every consumer's context is not a field: %+v", d)
	}
	// An AbortSignal member the Go struct carries as a field needs no context-taking consumer.
	files["lib/lib.go"] = fxLib + aliasLib + "\n// Sig carries the signal as a field.\ntype Sig struct {\n\tBase\n\tExtra  int\n\tSignal context.Context\n}\n\n// UseSig uses Sig.\nfunc UseSig() Sig { return Sig{} }\n"
	files["lib/lib_test.go"] = fxTest + aliasTest + "\nfunc TestSig(t *testing.T) { _ = UseSig() }\n"
	reach["lib/lib.go#UseSig"] = true
	sig := renameTable{"pkg:fx/.#Ext": "lib/lib.go#Sig", "pkg:fx/.#Base": "lib/lib.go#Base"}
	if d := fixtureRun(t, files, func([]m) []m { return rows }, reach, sig)["pkg:fx/.#Ext"]; d.Gap {
		t.Fatalf("an AbortSignal member carried as a field: %+v %s", d, d.Detail)
	}
	// Generic arguments are dropped from an operand: Base<T> is Base's members.
	if d := run(`Base<string> & { extra: number }`); !d.Gap || !strings.Contains(d.Detail, "other") {
		t.Fatalf("a generic operand contributes its recorded members (other has no field), got %+v", d)
	}
	// A7 does not list Omit (it would hide inherited members); the general rules (A7d) judge it: Ext embeds the Go type of Base
	// (T15e, the operand's own rows judge its members) and Flat carries no field for id.
	if d := run(`Omit<Base, "other"> & { extra: number }`); d.Gap {
		t.Fatalf("Omit of an embedded operand is decided by the general rules, got %+v", d)
	}
	ts["ext"] = "export type Ext = Omit<Base, \"other\"> & { extra: number };\n"
	flat := renameTable{"pkg:fx/.#Ext": "lib/lib.go#Flat", "pkg:fx/.#Base": "lib/lib.go#Base"}
	if d := aliasFixture(t, ts, rows, flat)["pkg:fx/.#Ext"]; !d.Gap || d.Reason == reasonUndecided {
		t.Fatalf("Omit of an operand the Go struct does not carry is a gap, got %+v", d)
	}
	// A7o/A7k: an Omit operand the general rules leave undecided lists its members minus the omitted keys, and a conditional operand
	// whose branches name the same members (the never branch drops them) lists the other branch's members: id and extra are fields of Ext.
	cond := `Omit<Base, "other" | "signal"> & ([string] extends [never] ? { extra?: never } : { extra: number })`
	if d := run(cond); d.Gap {
		t.Fatalf("an Omit and a conditional operand whose members Ext carries are not a gap: %+v %s", d, d.Detail)
	}
	if d := run(`Omit<Base, "other" | "signal"> & ([string] extends [never] ? { nope?: never } : { nope: number })`); !d.Gap || !strings.Contains(d.Detail, "nope") {
		t.Fatalf("a conditional operand member Ext has no field for stays a gap naming it, got %+v", d)
	}
	// An open record operand is never read by A7d.
	if d := run(`Pick<Base, "id"> & Record<string, unknown>`); d.Reason != reasonUndecided {
		t.Fatalf("an open record operand stays undecided, got %+v %s", d, d.Detail)
	}
}

// TestKeyofAndRecordAliases: keyof T is U7, a record literal is a Go map.
func TestKeyofAndRecordAliases(t *testing.T) {
	lib := aliasLib + `
// Kind has a constant per property of the upstream Kinds interface.
type Kind string

const (
	KindA Kind = "a"
	KindB Kind = "b"
)

// KindShort lacks the constant for b.
type KindShort string

const KindShortA KindShort = "a"

// Dict is the Go form of { [key: string]: string }.
type Dict map[string]string

func UseKinds() int { return len(Dict{string(KindA): string(KindB), string(KindShortA): "x"}) }
`
	files := func(ts map[string]string) map[string]string {
		out := map[string]string{
			"lib/lib.go":      fxLib + lib,
			"lib/lib_test.go": fxTest + aliasTest + "\nfunc TestKinds(t *testing.T) { if UseKinds() == 0 { t.Fatal(1) } }\n",
			"lib/use.go":      fxUse + "\nfunc init() { _ = UseAliases; _ = UseKinds }\n",
		}
		for n, b := range ts {
			out[".upstream/current/packages/fx/src/"+n+".ts"] = b
		}
		return out
	}
	reach := map[string]bool{"lib/use.go#Members": true, "lib/lib.go#UseAliases": true, "lib/lib.go#UseKinds": true}
	rows := append(ifaceRow("Kinds", "a", "b"), aliasRow("KindUnion"), aliasRow("KindShortUnion"), aliasRow("Bag"))
	rn := renameTable{"pkg:fx/.#KindUnion": "lib/lib.go#Kind", "pkg:fx/.#KindShortUnion": "lib/lib.go#KindShort", "pkg:fx/.#Bag": "lib/lib.go#Dict"}
	ds := fixtureRun(t, files(map[string]string{
		"kinds": "export interface Kinds { a: true; b: true }\nexport type KindUnion = keyof Kinds;\nexport type KindShortUnion = keyof Kinds;\nexport type Bag = { [key: string]: string };\n",
	}), func([]m) []m { return rows }, reach, rn)
	if d := ds["pkg:fx/.#KindUnion"]; d.Gap {
		t.Fatalf("keyof with a constant per property is not a gap: %+v", d)
	}
	if d := ds["pkg:fx/.#KindShortUnion"]; !d.Gap || !strings.Contains(d.Detail, "U7") {
		t.Fatalf("keyof with a missing constant stays a U7 gap, got %+v ev=%s", d, d.Evidence)
	}
	if d := ds["pkg:fx/.#Bag"]; d.Gap {
		t.Fatalf("a record literal alias against a Go map is not a gap: %+v", d)
	}
}

// TestProviderOptionsRenameJudgesMembersByName: a per-provider options interface renamed to the one Go options struct is accepted only
// when every member (own or inherited as recorded) has a field in it, by name or json tag; a missing field stays a member gap.
func TestProviderOptionsRenameJudgesMembersByName(t *testing.T) {
	rows := ifaceRow("ProvOpts", "name", "retries", "extraKnob")
	rn := renameTable{"pkg:fx/.#ProvOpts": "lib/lib.go#Opts"}
	ts := map[string]string{"provopts": "export interface ProvOpts { name: string; retries: string; extraKnob: string }\n"}
	ds := aliasFixture(t, ts, rows, rn)
	if d := ds["pkg:fx/.#ProvOpts::property:name"]; d.Gap {
		t.Fatalf("a member with a Go field is closed: %+v", d)
	}
	d := ds["pkg:fx/.#ProvOpts::property:extraKnob"]
	if !d.Gap || d.Reason != reasonMember {
		t.Fatalf("a member with no StreamOptions-like field stays a member gap, got %+v", d)
	}
	if d := ds["pkg:fx/.#ProvOpts"]; !d.Gap {
		t.Fatalf("the options type stays a gap while a member has no field: %+v", d)
	}
}

// TestMemberRenameIsCheckedAgainstTheGoMember: a documented member rename to a field of a composed struct is accepted when the types
// agree and refuted for a Go member of another type or one that does not exist (a wrong Go symbol never closes the row).
func TestMemberRenameIsCheckedAgainstTheGoMember(t *testing.T) {
	rows := ifaceRow("Cfg", "label", "count")
	ts := map[string]string{"cfg": "export interface Cfg { label: string; count: string }\n"}
	rn := renameTable{
		"pkg:fx/.#Cfg":                 "lib/lib.go#Config",
		"pkg:fx/.#Cfg::property:label": "lib/lib.go#Config.Name",
		"pkg:fx/.#Cfg::property:count": "lib/lib.go#Config.Retries", // Retries is an int: Pi count is a string here
	}
	ds := aliasFixture(t, ts, rows, rn)
	if d := ds["pkg:fx/.#Cfg::property:label"]; d.Gap {
		t.Fatalf("a rename to a Go field of the same type is closed: %+v", d)
	}
	if d := ds["pkg:fx/.#Cfg::property:count"]; !d.Gap {
		t.Fatalf("a rename to a Go field of another type stays a gap: %+v", d)
	}
	rn["pkg:fx/.#Cfg::property:label"] = "lib/lib.go#Config.NoSuchField"
	ds = aliasFixture(t, ts, rows, rn)
	if d := ds["pkg:fx/.#Cfg::property:label"]; !d.Gap {
		t.Fatalf("a rename to a Go member that does not exist stays a gap: %+v", d)
	}
}

// TestWithoutLiteralLead (OV1): the event literal of an overloaded `on("event", handler)` is dropped when the overload is renamed to
// one Go method, and any other overload keeps its parameters.
func TestWithoutLiteralLead(t *testing.T) {
	c := &upstreamEntry{}
	c.Shape.Parameters = []param{{Name: "event", Type: `"session_start"`}, {Name: "handler", Type: "Handler"}}
	got := withoutLiteralLead(c)
	if len(got.Shape.Parameters) != 1 || got.Shape.Parameters[0].Name != "handler" || len(c.Shape.Parameters) != 2 {
		t.Fatalf("the literal lead must be dropped from a copy, got %+v (original %+v)", got.Shape.Parameters, c.Shape.Parameters)
	}
	c.Shape.Parameters = []param{{Name: "name", Type: "string"}, {Name: "handler", Type: "Handler"}}
	if got := withoutLiteralLead(c); len(got.Shape.Parameters) != 2 {
		t.Fatalf("a non-literal lead stays, got %+v", got.Shape.Parameters)
	}
}

// TestPhantomBrandProperty (T12p): an optional readonly property keyed by `declare const X: unique symbol` is a compile-time type marker
// and needs no Go member; an ordinary symbol-keyed property, a required one, or a symbol that is not declared unique is still judged.
func TestPhantomBrandProperty(t *testing.T) {
	prop := func(name string, optional, readonly bool) []m {
		rows := ifaceRow("Tok")
		return append(rows, m{"id": "pkg:fx/.#Tok::property:" + name, "parentId": "pkg:fx/.#Tok", "role": "property", "name": "Tok." + name, "kind": "property",
			"shape": m{"name": name, "optional": optional, "readonly": readonly, "type": "T | undefined"}})
	}
	rn := renameTable{"pkg:fx/.#Tok": "lib/lib.go#Config"}
	id := "pkg:fx/.#Tok::property:[Symbol.mark]"
	decl := "declare const mark: unique symbol;\nexport interface Tok<T> { readonly [mark]?: T }\n"
	if d := aliasFixture(t, map[string]string{"tok": decl}, prop("[Symbol.mark]", true, true), rn)[id]; d == nil || d.Gap || d.DesignedOut == "" {
		t.Fatalf("a phantom brand property is designed out, got %+v", d)
	}
	if d := aliasFixture(t, map[string]string{"tok": decl}, prop("[Symbol.mark]", false, true), rn)[id]; d == nil || !d.Gap {
		t.Fatalf("a required symbol property is a real member, got %+v", d)
	}
	if d := aliasFixture(t, map[string]string{"tok": "export interface Tok<T> { readonly [mark]?: T }\n"}, prop("[Symbol.mark]", true, true), rn)[id]; d == nil || !d.Gap {
		t.Fatalf("a symbol not declared unique is not a phantom, got %+v", d)
	}
}

// TestPropertyNamesPreferRecordedRows: the recorded property rows of an interface win over the properties read from its source.
func TestPropertyNamesPreferRecordedRows(t *testing.T) {
	c := &checker{propNames: func(n string) []string {
		if n == "R" {
			return []string{"a"}
		}
		return nil
	}, props: func(string) []propShape { return []propShape{{Name: "s", Type: "string"}} }}
	env := checkerEnv{c}
	if got := env.PropertyNames("R"); !slices.Equal(got, []string{"a"}) {
		t.Errorf("recorded rows: %v", got)
	}
	if got := env.PropertyNames("S"); !slices.Equal(got, []string{"s"}) {
		t.Errorf("source fallback: %v", got)
	}
	if got := (checkerEnv{&checker{}}).PropertyNames("S"); got != nil {
		t.Errorf("no source: %v", got)
	}
}

// TestKeywordUnionAliasAsPointer (A8s): a union of keyword types is judged by the union rules against any Go type that is not an
// interface with methods. Pi trust-manager.ts:9 `ProjectTrustDecision = boolean | null` is the Go alias *bool (S6, T | null is *T),
// while *string and int stay the A8 gap.
func TestKeywordUnionAliasAsPointer(t *testing.T) {
	run := func(goType string) *decision {
		files := map[string]string{
			"lib/lib.go":      fxLib + aliasLib + "\n// Decision is a saved decision; nil is undecided.\ntype Decision = " + goType + "\n\nvar _ Decision\n",
			"lib/lib_test.go": fxTest + aliasTest,
			"lib/use.go":      fxUse + "\nfunc init() { _ = UseAliases }\n",
			".upstream/current/packages/fx/src/decision.ts": "export type Decision = boolean | null;\n",
		}
		reach := map[string]bool{"lib/use.go#Members": true, "lib/lib.go#UseAliases": true}
		return fixtureRun(t, files, func(inv []m) []m { return []m{aliasRow("Decision")} }, reach, renameTable{"pkg:fx/.#Decision": "lib/lib.go#Decision"})["pkg:fx/.#Decision"]
	}
	if d := run("*bool"); d == nil || d.Gap && d.Reason == reasonType {
		t.Fatalf("boolean | null is *bool, got %+v", d)
	}
	for _, goType := range []string{"*string", "int"} {
		if d := run(goType); d == nil || !d.Gap || !strings.Contains(d.Detail, "A8") {
			t.Fatalf("boolean | null against %s stays the A8 gap, got %+v", goType, d)
		}
	}
}

// TestExampleSchemaDoesNotShadowTheSourceSchema: `export type ReadToolInput = Static<typeof readSchema>` is judged by its own file's schema even
// when an example of the package declares a schema of the same name (coding-agent examples/extensions/tool-override.ts), which is no declaration of the package.
func TestExampleSchemaDoesNotShadowTheSourceSchema(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, ".upstream/current/packages/fx", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/read.ts", "const readSchema = Type.Object({ path: Type.String() });\nexport type ReadInput = Static<typeof readSchema>;\n")
	write("examples/override.ts", "const readSchema = Type.Object({ path: Type.String(), other: Type.Number() });\n")
	body := tsAliases(root).lookup("fx", "ReadInput")
	if body == nil || strings.TrimSpace(*body) != "{ path: string }" {
		t.Fatalf("ReadInput = %v, want { path: string }", body)
	}
}

// TestConditionalAndOmitOperands: types.ts TypedEntryDraft = Omit<EntryDraft, "kind" | "data"> & ([D] extends [never] ? { data?: never } : { data: D }).
func TestConditionalAndOmitOperands(t *testing.T) {
	branches, ok := conditionalBranches(`[D] extends [never] ? { readonly data?: never } : { readonly data: D }`)
	if !ok || branches[0] != `{ readonly data?: never }` || branches[1] != `{ readonly data: D }` {
		t.Fatalf("conditionalBranches = %q, %v", branches, ok)
	}
	if _, ok := conditionalBranches(`{ a?: number }`); ok {
		t.Fatal("an object literal is not a conditional type")
	}
	for lit, want := range map[string]bool{`{ readonly data?: never }`: true, `{ a?: never; b: never }`: true, `{ data: D }`: false, `{ a: never; b?: string }`: false, `{}`: false, `string`: false} {
		if got := neverOnlyLiteral(lit); got != want {
			t.Errorf("neverOnlyLiteral(%q) = %v, want %v", lit, got, want)
		}
	}
	m := omitOperandRe.FindStringSubmatch(`Omit<EntryDraft, "kind" | "data">`)
	if m == nil || m[1] != "EntryDraft" || m[2] != `"kind" | "data"` {
		t.Fatalf("omitOperandRe = %q", m)
	}
	if omitOperandRe.MatchString(`Omit<Base, keyof X>`) {
		t.Fatal("a computed key set is not a list of literals")
	}
}

// TestOmitAndConditionalOperandsReachTheirRules copies durable's EntryRecord / EntryDraft / TypedEntryDraft (types.ts:335-346): the
// general rules cannot decide `head?: EntryId | "self"` or the conditional, so A7o lists the Omit operand's members (through the EntryDraft
// alias and the nested Omit) and A7k the conditional operand's, and the Go struct is judged on those member names.
func TestOmitAndConditionalOperandsReachTheirRules(t *testing.T) {
	const goDraft = `
// Draft is a TypedEntryDraft-shaped struct.
type Draft[D any] struct {
	Model    string
	Data     D
	Edits    string
	Head     *int
	HeadSelf bool
}

// NoModel lacks the model member.
type NoModel[D any] struct {
	Data D
	Edits string
	Head *int
}

// UseDraft uses the drafts.
func UseDraft() (Draft[int], NoModel[int]) { return Draft[int]{}, NoModel[int]{} }
`
	rows := append(ifaceRow("EntryRecord", "id", "conversationId", "kind", "model", "data", "edits", "head", "byTaskId"), aliasRow("Typed"))
	run := func(typed string, goType string) *decision {
		ts := map[string]string{
			"entryrecord": "export interface EntryRecord { id: string; conversationId: string; kind: string; model?: string; data?: string; edits?: string; head?: string; byTaskId?: string }\n",
			"entrydraft":  "export type EntryDraft = Omit<EntryRecord, \"id\" | \"conversationId\" | \"byTaskId\" | \"head\"> & {\n\treadonly head?: EntryId | \"self\";\n};\n",
			"typed":       "export type Typed<D> = " + typed + ";\n",
		}
		files := map[string]string{
			"lib/lib.go":      fxLib + aliasLib + goDraft,
			"lib/lib_test.go": fxTest + aliasTest + "\nfunc TestUseDraft(t *testing.T) { UseDraft() }\n",
			"lib/use.go":      fxUse + "\nfunc init() { _ = UseAliases }\n",
		}
		for name, body := range ts {
			files[".upstream/current/packages/fx/src/"+name+".ts"] = body
		}
		reach := map[string]bool{"lib/use.go#Members": true, "lib/lib.go#UseAliases": true, "lib/lib.go#UseDraft": true}
		rn := renameTable{"pkg:fx/.#Typed": "lib/lib.go#" + goType, "pkg:fx/.#EntryRecord": "lib/lib.go#Base"}
		return fixtureRun(t, files, func([]m) []m { return rows }, reach, rn)["pkg:fx/.#Typed"]
	}
	const conditional = `([D] extends [never] ? { readonly data?: never } : { readonly data: D })`
	typed := `Omit<EntryDraft, "kind" | "data"> & ` + conditional
	if d := run(typed, "Draft"); d.Gap {
		t.Fatalf("Draft carries model, edits, head and data: %+v %s", d, d.Detail)
	}
	// Omit keeps model: a Go struct without it is a gap, so the omitted keys are removed from the operand's members and no others.
	if d := run(typed, "NoModel"); !d.Gap || !strings.Contains(d.Detail, "model") {
		t.Fatalf("a kept member with no Go field stays a gap naming it, got %+v", d)
	}
	// Omit of kind and data and model leaves edits and head only: NoModel then carries every member.
	if d := run(`Omit<EntryDraft, "kind" | "data" | "model"> & `+conditional, "NoModel"); d.Gap {
		t.Fatalf("an omitted member needs no Go field: %+v %s", d, d.Detail)
	}
	// The conditional's branches must name the same members.
	if d := run(`Omit<EntryDraft, "kind" | "data" | "model"> & ([D] extends [never] ? { readonly data?: never } : { readonly other: D })`, "Draft"); !d.Gap {
		t.Fatalf("conditional branches that name different members are not listed, got %+v", d)
	}
	if d := run(`Omit<EntryDraft, "kind" | "data" | "model"> & ([D] extends [never] ? { readonly data: D; readonly extra: D } : { readonly data: D })`, "Draft"); !d.Gap {
		t.Fatalf("a first branch with real members is not skipped, got %+v", d)
	}
	if d := run(`Omit<EntryDraft, "kind" | "data" | "model"> & ([D] extends [never] ? Mystery<D> : { readonly data: D })`, "Draft"); !d.Gap {
		t.Fatalf("a first branch that is neither listable nor all-never is not skipped, got %+v", d)
	}
	if d := run(`Omit<EntryDraft, "kind" | "data" | "model"> & ([D] extends [never] ? { readonly data?: never } : { readonly nope: D })`, "NoModel"); !d.Gap {
		t.Fatalf("a branch member the Go struct lacks stays a gap, got %+v", d)
	}
}
