package rules

import (
	"go/types"
	"slices"
	"strings"
	"testing"
)

func TestParseLiteralUnion(t *testing.T) {
	aliases := map[string]string{"Level": `"low" | "high"`, "Open": `Level | (string & {})`, "Bad": `"a" | Foo`}
	lookup := func(n string) *string {
		if b, ok := aliases[n]; ok {
			return &b
		}
		return nil
	}
	cases := []struct {
		up   string
		want LiteralUnion
		ok   bool
	}{
		{`"a" | "b"`, LiteralUnion{Strings: []string{"a", "b"}}, true},
		{`"a" | "b" | undefined`, LiteralUnion{Strings: []string{"a", "b"}}, true},
		{`'a' | null`, LiteralUnion{Strings: []string{"a"}}, true},
		{`1 | 2 | -3`, LiteralUnion{Numbers: []string{"1", "2", "-3"}}, true},
		{`Level | "mid"`, LiteralUnion{Strings: []string{"low", "high", "mid"}}, true},
		{`Open`, LiteralUnion{Strings: []string{"low", "high"}, Open: true}, true},
		{`"a" | string`, LiteralUnion{Strings: []string{"a"}, Open: true}, true},
		{`string`, LiteralUnion{Open: true}, true},
		{`"a" | Bad`, LiteralUnion{}, false},
		{`"a" | number`, LiteralUnion{}, false},
		{`"a" | Unknown`, LiteralUnion{}, false},
		{`undefined`, LiteralUnion{}, false},
	}
	for _, c := range cases {
		got, ok := ParseLiteralUnion(c.up, lookup)
		if ok != c.ok || ok && (!slices.Equal(got.Strings, c.want.Strings) || !slices.Equal(got.Numbers, c.want.Numbers) || got.Open != c.want.Open) {
			t.Errorf("ParseLiteralUnion(%q) = %+v, %v; want %+v, %v", c.up, got, ok, c.want, c.ok)
		}
	}
}

const unionSrc = `
type Level string
const (
	LevelLow  Level = "low"
	LevelHigh Level = "high"
)
type Extra string
const (
	ExtraA Extra = "a"
	ExtraB Extra = "b"
	ExtraC Extra = "c"
)
type Count int
const (
	CountOne Count = 1
	CountTwo Count = 2
)
type Half float64
const HalfHalf Half = 0.5
type Missing string
const MissingA Missing = "a"
type Alias = Level
type Struct struct{ A int }
`

func TestCheckLiteralUnion(t *testing.T) {
	l := ouLoad(t, unionSrc)
	closed := LiteralUnion{Strings: []string{"low", "high"}}
	cases := []struct {
		name string
		u    LiteralUnion
		goT  string
		opts StringUnionOptions
		want Tri
		rule string
		note int
	}{
		{"typed string with every constant", closed, "Level", StringUnionOptions{}, Yes, "U1", 0},
		{"alias of a typed string", closed, "Alias", StringUnionOptions{}, Yes, "U1", 0},
		{"extra constants are notes", LiteralUnion{Strings: []string{"a", "b"}}, "Extra", StringUnionOptions{}, Yes, "U1", 1},
		{"missing constant", LiteralUnion{Strings: []string{"a", "b"}}, "Missing", StringUnionOptions{}, No, "U1", 0},
		{"wrong type of constants", closed, "Extra", StringUnionOptions{}, No, "U1", 0},
		{"numbers", LiteralUnion{Numbers: []string{"1", "2"}}, "Count", StringUnionOptions{}, Yes, "U1", 0},
		{"numbers missing", LiteralUnion{Numbers: []string{"1", "3"}}, "Count", StringUnionOptions{}, No, "U1", 0},
		{"float literal", LiteralUnion{Numbers: []string{"0.5"}}, "Half", StringUnionOptions{}, Yes, "U1", 0},
		{"numbers against string type", LiteralUnion{Numbers: []string{"1"}}, "Level", StringUnionOptions{}, No, "U1", 0},
		{"strings against number type", closed, "Count", StringUnionOptions{}, No, "U1", 0},
		{"mixed literals", LiteralUnion{Strings: []string{"a"}, Numbers: []string{"1"}}, "Level", StringUnionOptions{}, Unknown, "U1", 0},
		{"struct is not a literal type", closed, "Struct", StringUnionOptions{}, No, "U1", 0},
		{"open union is any string type", LiteralUnion{Strings: []string{"x"}, Open: true}, "Missing", StringUnionOptions{}, Yes, "U2", 0},
		{"open union against a struct", LiteralUnion{Open: true}, "Struct", StringUnionOptions{}, No, "U2", 0},
		{"open union against a number type", LiteralUnion{Open: true}, "Count", StringUnionOptions{}, No, "U2", 0},
	}
	for _, c := range cases {
		ty := l.typ(t, c.goT)
		got := CheckLiteralUnion(c.u, ty, ConstsOf(ty), c.opts)
		if got.Result != c.want || got.Rule != c.rule || len(got.Notes) != c.note {
			t.Errorf("%s: got %s %s (%s) notes=%v, want %s %s notes=%d", c.name, got.Rule, triName(got.Result), got.Why, got.Notes, c.rule, triName(c.want), c.note)
		}
	}
}

func TestBareStringIsStringlyTyped(t *testing.T) {
	l := ouLoad(t, `var S string`)
	ty := l.typ(t, "S")
	closed := LiteralUnion{Strings: []string{"a", "b"}}
	if v := CheckLiteralUnion(closed, ty, nil, StringUnionOptions{}); v.Result != No || v.Rule != "U1" {
		t.Fatalf("a closed union as bare string must be refuted: %+v", v)
	}
	if v := CheckLiteralUnion(closed, ty, nil, StringUnionOptions{AllowBareString: true}); v.Result != Yes {
		t.Fatalf("the option accepts a bare string: %+v", v)
	}
	if v := CheckLiteralUnion(LiteralUnion{Strings: []string{"a"}, Open: true}, ty, nil, StringUnionOptions{}); v.Result != Yes || v.Rule != "U2" {
		t.Fatalf("an open union is a bare string: %+v", v)
	}
}

func TestParseDiscriminated(t *testing.T) {
	named := map[string][]UnionProp{
		"Text":  {{"type", `"text"`, false}, {"text", "string", false}},
		"Image": {{"type", `"image"`, false}, {"data", "string", false}},
		"Loose": {{"type", "string", false}},
		"Opt":   {{"type", `"opt"`, true}},
	}
	lookup := func(n string) []UnionProp { return named[n] }
	cases := []struct {
		name    string
		members []string
		wantD   string
		tags    []string
		want    Tri
	}{
		{"named members", []string{"Text", "Image"}, "type", []string{"text", "image"}, Yes},
		{"inline members", []string{`{ type: "a"; x: number }`, `{ type: "b"; y?: number }`}, "type", []string{"a", "b"}, Yes},
		{"undefined member skipped", []string{"Text", "Image", "undefined"}, "type", []string{"text", "image"}, Yes},
		{"parenthesised", []string{"(Text)", "(Image)"}, "type", []string{"text", "image"}, Yes},
		{"preferred over alphabetical", []string{`{ a: "x"; type: "p" }`, `{ a: "y"; type: "q" }`}, "type", []string{"p", "q"}, Yes},
		{"alphabetical without preference", []string{`{ b: "x"; a: "p" }`, `{ b: "y"; a: "q" }`}, "a", []string{"p", "q"}, Yes},
		{"duplicate literal", []string{`{ type: "a" }`, `{ type: "a"; z: 1 }`}, "", nil, Unknown},
		{"non-literal discriminator", []string{"Text", "Loose"}, "", nil, Unknown},
		{"optional discriminator", []string{"Text", "Opt"}, "", nil, Unknown},
		{"unknown member", []string{"Text", "Nope"}, "", nil, Unknown},
		{"single member", []string{"Text"}, "", nil, Unknown},
		{"member without the property", []string{"Text", `{ other: "z" }`}, "", nil, Unknown},
		{"primitive member", []string{"Text", "string"}, "", nil, Unknown},
	}
	for _, c := range cases {
		d, v := ParseDiscriminated(c.members, lookup)
		if v.Result != c.want || v.Rule != "U3" {
			t.Errorf("%s: got %s %s (%s), want %s", c.name, v.Rule, triName(v.Result), v.Why, triName(c.want))
			continue
		}
		if c.want != Yes {
			continue
		}
		var tags []string
		for _, m := range d.Members {
			tags = append(tags, m.Tag)
		}
		if d.Discriminator != c.wantD || !slices.Equal(tags, c.tags) {
			t.Errorf("%s: discriminator %q tags %v, want %q %v", c.name, d.Discriminator, tags, c.wantD, c.tags)
		}
	}
	_, v := ParseDiscriminated([]string{`{ type: "a"; kind: "x" }`, `{ type: "b"; kind: "y" }`}, nil)
	if len(v.Notes) != 1 {
		t.Errorf("two candidates must note the other: %+v", v)
	}
}

func TestParseInlineProps(t *testing.T) {
	props, ok := ParseInlineProps(`{ type: "text"; text: string, id?: Record<string, A | B>; "quoted"?: number }`)
	want := []UnionProp{{"type", `"text"`, false}, {"text", "string", false}, {"id", "Record<string, A | B>", true}, {"quoted", "number", true}}
	if !ok || !slices.Equal(props, want) {
		t.Fatalf("got %v %v", props, ok)
	}
	for _, bad := range []string{"Foo", "{ nocolon }", "{}"} {
		if _, ok := ParseInlineProps(bad); ok {
			t.Errorf("%q must not parse", bad)
		}
	}
}

func ouUnion(tags ...string) DiscriminatedUnion {
	d := DiscriminatedUnion{Discriminator: "type"}
	for _, tag := range tags {
		d.Members = append(d.Members, UnionMember{Tag: tag, Props: []UnionProp{{"type", `"` + tag + `"`, false}, {tag + "Value", "string", false}}})
	}
	return d
}

const sealedSrc = `
type Block interface{ isBlock() }
type TextBlock struct{ TextValue string }
func (TextBlock) isBlock() {}
func (TextBlock) Type() string { return "text" }
type ImageBlock struct{ ImageValue string }
func (*ImageBlock) isBlock() {}
func (*ImageBlock) Type() string { return "image" }
type CustomBlock struct{ CustomValue string }
func (CustomBlock) isBlock() {}
func (CustomBlock) MarshalJSON() ([]byte, error) { return []byte("{\"type\":\"custom\"}"), nil }

const typeKey = "type"
type KeyedBlock struct{ KeyedValue string }
func (KeyedBlock) isBlock() {}
func (KeyedBlock) MarshalJSON() ([]byte, error) {
	m := map[string]string{typeKey: "keyed"}
	_ = m
	return nil, nil
}
type TaggedBlock struct{ TaggedValue string }
func (TaggedBlock) isBlock() {}
func (TaggedBlock) MarshalJSON() ([]byte, error) {
	return nil, any(struct{ T string ` + "`json:\"type\"`" + ` }{"tagged"}).(error)
}
type AmbiguousBlock struct{ AmbiguousValue string }
func (AmbiguousBlock) isBlock() {}
func (AmbiguousBlock) MarshalJSON() ([]byte, error) { _ = "type"; _ = "one"; _ = "two"; return nil, nil }
type ComputedBlock struct{ ComputedValue string; T string }
func (ComputedBlock) isBlock() {}
func (c ComputedBlock) Type() string { return c.T }
type DupBlock struct{ DupValue string }
func (DupBlock) isBlock() {}
func (DupBlock) Type() string { return "text" }
type Any interface{}
type SuffixBlock struct{}
func (SuffixBlock) BlockType() string { return "suffix" }
type TwoSuffix struct{}
func (TwoSuffix) AType() string { return "a" }
func (TwoSuffix) BType() string { return "b" }
type LowSuffix struct{}
func (LowSuffix) blockType() string { return "low" }
type ExactFirst struct{}
func (ExactFirst) BlockType() string { return "other" }
func (ExactFirst) Type() string { return "exact" }
type ParamSuffix struct{}
func (ParamSuffix) BlockType(n int) string { return "param" }
`

func sealed(t *testing.T) (ouLoaded, UnionOptions) {
	l := ouLoad(t, sealedSrc)
	return l, UnionOptions{Resolver: NewASTTags(l.files, l.info)}
}

func implsNamed(t *testing.T, l ouLoaded, names ...string) []*types.Named {
	t.Helper()
	var out []*types.Named
	for _, n := range names {
		out = append(out, l.named(t, n))
	}
	return out
}

func TestASTTags(t *testing.T) {
	l, opts := sealed(t)
	cases := []struct {
		impl, want, evidence string
		ok                   bool
	}{
		{"TextBlock", "text", "method Type", true},
		{"ImageBlock", "image", "method Type", true},
		{"CustomBlock", "custom", "MarshalJSON", true},
		{"KeyedBlock", "keyed", "MarshalJSON", true},
		{"TaggedBlock", "tagged", "MarshalJSON", true},
		{"AmbiguousBlock", "", "", false},
		{"ComputedBlock", "", "", false},
		{"SuffixBlock", "suffix", "method BlockType", true},
		{"TwoSuffix", "", "", false},
		{"LowSuffix", "low", "method blockType", true}, // a sealing method (durable storageWriteType) is unexported
		{"ExactFirst", "exact", "method Type", true},
		{"ParamSuffix", "", "", false},
	}
	for _, c := range cases {
		got, ev, ok := opts.Resolver.Tag(l.named(t, c.impl), "type")
		if got != c.want || ev != c.evidence || ok != c.ok {
			t.Errorf("%s: got %q %q %v, want %q %q %v", c.impl, got, ev, ok, c.want, c.evidence, c.ok)
		}
	}
}

func TestCheckSealedInterface(t *testing.T) {
	l, opts := sealed(t)
	iface := l.typ(t, "Block")
	all := Implementers(iface.Underlying().(*types.Interface), l.pkg)
	if len(all) != 8 {
		t.Fatalf("implementers = %d", len(all))
	}
	pick := func(names ...string) []*types.Named { return implsNamed(t, l, names...) }
	cases := []struct {
		name  string
		u     DiscriminatedUnion
		impls []*types.Named
		want  Tri
		notes int
	}{
		{"one struct per member", ouUnion("text", "image"), pick("TextBlock", "ImageBlock"), Yes, 0},
		{"marshal discriminators", ouUnion("text", "custom", "keyed", "tagged"), pick("TextBlock", "CustomBlock", "KeyedBlock", "TaggedBlock"), Yes, 0},
		{"missing member", ouUnion("text", "video"), pick("TextBlock", "ImageBlock"), No, 0},
		{"implementer outside the union is a note", ouUnion("text"), pick("TextBlock", "ImageBlock"), Yes, 1},
		{"unreadable implementer is a note", ouUnion("text", "image"), pick("TextBlock", "ImageBlock", "ComputedBlock"), Yes, 1},
		{"member only an unreadable type could carry", ouUnion("text", "computed"), pick("TextBlock", "ComputedBlock"), Unknown, 1},
		{"two structs one discriminator", ouUnion("text", "image"), pick("TextBlock", "ImageBlock", "DupBlock"), No, 0},
	}
	for _, c := range cases {
		got := CheckSealedInterface(c.u, iface, c.impls, opts)
		if got.Result != c.want || got.Rule != "U4" || len(got.Notes) != c.notes {
			t.Errorf("%s: got %s %s (%s) notes=%v, want %s notes=%d", c.name, got.Rule, triName(got.Result), got.Why, got.Notes, triName(c.want), c.notes)
		}
	}
	if v := CheckSealedInterface(ouUnion("text"), l.typ(t, "Any"), nil, opts); v.Result != Unknown {
		t.Errorf("an empty interface says nothing: %+v", v)
	}
	if v := CheckSealedInterface(ouUnion("text"), l.typ(t, "TextBlock"), nil, opts); v.Result != Unknown {
		t.Errorf("a struct is not an interface: %+v", v)
	}
	if v := CheckSealedInterface(ouUnion("text", "image"), iface, pick("TextBlock", "ImageBlock"), UnionOptions{}); v.Result != Unknown {
		t.Errorf("without a resolver the rule cannot decide: %+v", v)
	}
}

func TestCheckTaggedStruct(t *testing.T) {
	l := ouLoad(t, `
type Kind string
const (
	KindText  Kind = "text"
	KindImage Kind = "image"
)
type Part struct {
	Type       Kind   `+"`json:\"type\"`"+`
	TextValue  string `+"`json:\"textValue\"`"+`
	ImageValue string
}
type Bare struct {
	Type       string `+"`json:\"type\"`"+`
	TextValue  string
	ImageValue string
}
type NoTag struct {
	TextValue  string
	ImageValue string
}
type Short struct {
	Type      Kind
	TextValue string
}
type Partial struct {
	Type Kind
	TextValue string
	ImageValue string
}
`)
	u := ouUnion("text", "image")
	st := func(n string) *types.Struct { return l.named(t, n).Underlying().(*types.Struct) }
	cases := []struct {
		name string
		st   string
		u    DiscriminatedUnion
		opts StringUnionOptions
		want Tri
	}{
		{"tagged struct", "Part", u, StringUnionOptions{}, Yes},
		{"discriminator is a bare string", "Bare", u, StringUnionOptions{}, No},
		{"bare string by option", "Bare", u, StringUnionOptions{AllowBareString: true}, Yes},
		{"no discriminator field", "NoTag", u, StringUnionOptions{}, No},
		{"member property has no field", "Short", u, StringUnionOptions{}, No},
		{"field name folded", "Partial", u, StringUnionOptions{}, Yes},
		{"constant missing for a literal", "Part", ouUnion("text", "image", "video"), StringUnionOptions{}, No},
	}
	for _, c := range cases {
		got := CheckTaggedStruct(c.u, st(c.st), c.opts)
		if got.Result != c.want || got.Rule != "U5" {
			t.Errorf("%s: got %s %s (%s), want %s", c.name, got.Rule, triName(got.Result), got.Why, triName(c.want))
		}
	}
}

func TestFamilyUnionsRulesAreRegisteredUnderTheFamily(t *testing.T) {
	if got := strings.Join(Registered(), ","); !strings.Contains(got, "unions/U1t") {
		t.Errorf("U1t is not registered: %s", got)
	}
}

func TestLiteralUnionTypeRule(t *testing.T) {
	l := ouLoad(t, unionSrc+"\nvar Bare string\ntype Struct2 struct{}\n")
	env := basicEnv(map[string]string{"Counts": "1 | 2", "Lvl": `"low" | "high"`})
	cases := []struct {
		name string
		up   string
		goT  string
		want Tri
		ok   bool
	}{
		{"numbers with constants", "1 | 2", "Count", Yes, true},
		{"alias of numbers", "Counts", "Count", Yes, true},
		{"numbers with a missing constant", "1 | 3", "Count", No, true},
		{"strings with constants", `"low" | "high"`, "Level", Yes, true},
		{"alias of strings", "Lvl | undefined", "Level", Yes, true},
		{"strings against a struct", `"low" | "high"`, "Struct", No, true},
		{"closed union as bare string", `"low" | "high"`, "Bare", No, true},
		{"mixed literals cannot be decided", `"a" | 1`, "Level", 0, false},
		{"not a literal union", "string | number", "Level", 0, false},
		{"plain type name", "Level", "Level", 0, false},
	}
	for _, c := range cases {
		v, ok := literalUnionType{}.Type(env, c.up, l.typ(t, c.goT))
		if ok != c.ok || ok && v.OK != c.want {
			t.Errorf("%s: got %+v %v, want %s %v", c.name, v, ok, triName(c.want), c.ok)
		}
	}
	if v, ok := CheckType(env, "1 | 2", l.typ(t, "Count")); !ok || v.OK != Yes || !strings.HasPrefix(v.Why, "U1:") {
		t.Errorf("the registry must reach the rule and name it: %+v %v", v, ok)
	}
}

// A readonly modifier belongs to the member, not to its name: { readonly type: "a" } has the discriminator property type. Before this
// the property was called "readonly type" and no Go struct had a field for it.
func TestParseInlinePropsDropsReadonly(t *testing.T) {
	props, ok := ParseInlineProps(`{ readonly kind: "a"; readonly size?: number; readonlyish: string }`)
	want := []UnionProp{{"kind", `"a"`, false}, {"size", "number", true}, {"readonlyish", "string", false}}
	if !ok || !slices.Equal(props, want) {
		t.Fatalf("got %v %v", props, ok)
	}
	d, f := ParseDiscriminated([]string{`{ readonly kind: "a" }`, `{ readonly kind: "b"; readonly x: number }`}, nil)
	if f.Result != Yes || d.Discriminator != "kind" {
		t.Fatalf("a readonly discriminator is the property kind: %+v %+v", d, f)
	}
}

// TestNameEncodedTags: U7 reads the discriminator from implementer names when no implementer's value is readable.
func TestNameEncodedTags(t *testing.T) {
	l := ouLoad(t, `
type Ev interface{ isEv() }
type AgentStartEvent struct{}
func (AgentStartEvent) isEv() {}
type AgentEndEvent struct{}
func (AgentEndEvent) isEv() {}
type ExtraEvent struct{}
func (ExtraEvent) isEv() {}
type Res interface{ isRes() }
type ResContinue struct{}
func (ResContinue) isRes() {}
type ResStop struct{}
func (ResStop) isRes() {}
type StopRes struct{}
func (StopRes) isRes() {}
type Amb interface{ isAmb() }
type PreStop struct{}
func (PreStop) isAmb() {}
type StopPost struct{}
func (StopPost) isAmb() {}
type PreGo struct{}
func (PreGo) isAmb() {}
type GoPost struct{}
func (GoPost) isAmb() {}
type Suf interface{ isSuf() }
type StartA struct{}
func (StartA) isSuf() {}
type EndA struct{}
func (EndA) isSuf() {}
type StartB struct{}
func (StartB) isSuf() {}
type EndB struct{}
func (EndB) isSuf() {}
type Pre interface{ isPre() }
type AStart struct{}
func (AStart) isPre() {}
type AEnd struct{}
func (AEnd) isPre() {}
type BStart struct{}
func (BStart) isPre() {}
type BEnd struct{}
func (BEnd) isPre() {}
type Tagged interface{ isTagged() }
type TaggedStart struct{}
func (TaggedStart) isTagged() {}
func (TaggedStart) Type() string { return "start" }
type TaggedEnd struct{}
func (TaggedEnd) isTagged() {}
`)
	opts := UnionOptions{Resolver: NewASTTags(l.files, l.info)}
	check := func(iface string, u DiscriminatedUnion) Finding {
		typ := l.typ(t, iface)
		return CheckSealedInterface(u, typ, Implementers(typ.Underlying().(*types.Interface), l.pkg), opts)
	}
	for _, tc := range []struct {
		name, iface string
		u           DiscriminatedUnion
		want        Tri
		rule        string
	}{
		{"prefix-free names with a shared suffix", "Ev", ouUnion("agent_start", "agent_end"), Yes, "U7"},
		{"a shared prefix", "Res", ouUnion("continue", "stop"), Yes, "U7"}, // StopRes has another affix pair, which ResContinue lacks
		{"two affix pairs for every member", "Amb", ouUnion("stop", "go"), Unknown, "U4"},
		{"two suffixes for every member", "Suf", ouUnion("start", "end"), Unknown, "U4"},
		{"two prefixes for every member", "Pre", ouUnion("start", "end"), Unknown, "U4"},
		{"a member no name encodes", "Ev", ouUnion("agent_start", "agent_pause"), Unknown, "U4"},
		{"a readable tag disables names", "Tagged", ouUnion("start", "end"), Unknown, "U4"},
	} {
		got := check(tc.iface, tc.u)
		if got.Result != tc.want || got.Rule != tc.rule {
			t.Errorf("%s: got %v %s (%s), want %v %s", tc.name, got.Result, got.Rule, got.Why, tc.want, tc.rule)
		}
	}
}
