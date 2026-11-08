package plugins

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// parseClassMap reads esbuild's mapping module; the cases are what esbuild writes for classes, composes and escaped names.
func TestParseClassMapReadsEsbuildsObjectLiteral(t *testing.T) {
	js := `var notes_default={card:"chord_1_notes_base chord_1_notes_card","caf\u00E9":"x\xE9y",d\u00E9j\u00E0:"a\u{1F600}b",card:"again",s:"q\"\\\n"};export{notes_default as default};`
	got, err := parseClassMap(js)
	if err != nil {
		t.Fatal(err)
	}
	want := []ClassName{
		{"card", "again"},
		{"café", "xéy"},
		{"déjà", "a\U0001F600b"},
		{"s", "q\"\\\n"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("class %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	for _, bad := range []string{``, `export{x as default};`, `var x={a:1};export{x as default};`, `var x={a:"b"`, `var x={a:"\u12"};export{x as default};`} {
		if _, err := parseClassMap(bad); err == nil {
			t.Errorf("parseClassMap(%q) accepted it", bad)
		}
	}
}

// A surrogate pair written as two escapes is the one character it spells.
func TestParseClassMapJoinsSurrogatePairs(t *testing.T) {
	got, err := parseClassMap(`var x={a:"\uD83D\uDE00"};export{x as default};`)
	if err != nil || len(got) != 1 || got[0].Scoped != "\U0001F600" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func FuzzParseClassMap(f *testing.F) {
	f.Add(`var x={card:"a b","caf\u00E9":"c\xE9"};export{x as default};`)
	f.Add(`var x = {\n  a: "b"\n};\nexport {\n  x as default\n};`)
	f.Add(`var x={"\uD83D\uDE00":"\u{1F600}"};export{x as default};`)
	f.Fuzz(func(t *testing.T, js string) {
		classes, err := parseClassMap(js)
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, class := range classes {
			if seen[class.Name] {
				t.Fatalf("%q listed twice", class.Name)
			}
			seen[class.Name] = true
			if !utf8.ValidString(class.Name) || !utf8.ValidString(class.Scoped) {
				t.Fatalf("invalid UTF-8 in %+v", class)
			}
		}
		// What the build embeds is always a literal of the same names.
		if object := classMapObject(classes); !strings.HasPrefix(object, "{") || !strings.HasSuffix(object, "}") {
			t.Fatalf("object = %q", object)
		}
	})
}
