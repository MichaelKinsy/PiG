package codingagent

import (
	"slices"
	"strings"
	"testing"
)

func BenchmarkPromptTemplateExpansion(b *testing.B) {
	templates := []PromptTemplate{{Name: "review", Content: "Review $1 with ${@:2} ($ARGUMENTS)"}}
	for _, tc := range []struct{ name, input string }{
		{"ordinary", `/review src/main.go "check error handling"`},
		{"unicode", "/review\u2003src/日本語.go\ufeffcheck\u0085errors"},
		{"many arguments", "/review " + strings.Repeat("src/file.go ", 128)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, ok := ExpandPromptTemplate(tc.input, templates); !ok {
					b.Fatal("template not expanded")
				}
			}
		})
	}
}

// Pi's parseCommandArgs and expandPromptTemplate use ECMAScript \s, not Unicode White_Space.
// .upstream/v0.87.1/packages/coding-agent/src/core/prompt-templates.ts:40,321
func TestPromptTemplateECMAScriptWhitespace(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []string
	}{
		{"next-line is an argument character", "a\u0085b", []string{"a\u0085b"}},
		{"byte-order mark separates arguments", "a\ufeffb", []string{"a", "b"}},
		{"quoted whitespace is preserved", "\"a\ufeffb\" 'c\u0085d'", []string{"a\ufeffb", "c\u0085d"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParsePromptArgs(tc.input); !slices.Equal(got, tc.want) {
				t.Fatalf("args = %q; want %q", got, tc.want)
			}
		})
	}
	templates := []PromptTemplate{{Name: "unicode", Content: "$1|$2|${@:3}"}}
	for _, tc := range []struct {
		name, input, want string
		ok                bool
	}{
		{"argument separators", "/unicode a\u0085b\ufeffc", "a\u0085b|c|", true},
		{"command separated by BOM", "/unicode\ufeffa b", "a|b|", true},
		{"command separated by em space", "/unicode\u2003a b", "a|b|", true},
		{"next-line stays in command name", "/unicode\u0085a", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ExpandPromptTemplate(tc.input, templates)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("expansion = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
