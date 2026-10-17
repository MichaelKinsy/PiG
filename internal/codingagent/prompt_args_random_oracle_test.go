package codingagent

import (
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// TestPromptArgumentHandlingMatchesPiOnRandomInputs compares parseCommandArgs, substituteArgs and expandPromptTemplate of Pi with Pig over argument strings, templates and command lines built from random token sequences: quotes, backslashes, JavaScript and Unicode whitespace, `$` forms with digits, `@`, `ARGUMENTS`, `${…:-…}` defaults and `${@:n:m}` slices, newlines and non-BMP text.
func TestPromptArgumentHandlingMatchesPiOnRandomInputs(t *testing.T) {
	rng := rand.New(rand.NewSource(20261010))
	build := func(tokens []string, count int) string {
		var out strings.Builder
		for range 1 + rng.Intn(count) {
			out.WriteString(tokens[rng.Intn(len(tokens))])
		}
		return out.String()
	}
	argTokens := []string{"a", "b", "é", "😀", `"`, `'`, `\`, " ", " ", "\t", "\n", "\u00a0", "\u2003", "\ufeff", "\u0085", "\u2028", "$1", "x y", `""`, `''`, "-", "0"}
	tmplTokens := []string{"$1", "$2", "$3", "$0", "$00", "$10", "$9", "$@", "$ARGUMENTS", "$ARGUMENTSx", "${1}", "${2:-d}", "${1:-}", "${@:-all}", "${ARGUMENTS:-z}", "${@:2}", "${@:2:1}", "${@:0}", "${@:0:2}", "${@:3:0}", "${@:}", "${@:2:}", "${:-x}", "${1:-$2}", "${99999999999999999999:-d}", "$", "$$", "${", "}", ":", "-", "\\", "\n", " ", "a", "é", "😀", "{", "@", "ARGUMENTS"}
	lineTokens := []string{"/known", "/other", "/unknown", "/", " ", "\t", "\n", "\u00a0", "\u2003", "\u0085", "\ufeff", "a", `"b c"`, `'d'`, "😀", "$1", "$@"}
	var argStrings, templates, lines []string
	var argLists [][]string
	// The pinned Pi (1.2.0+3daa2028) drops an empty quoted argument; PiG carries Pi 9fe3f371, which keeps it
	// (TestUpstreamPromptTemplatesArguments). Inputs where the two parsers differ are left out of this comparison.
	withoutEmptyQuotes := func(s string) string {
		if !slices.Equal(pinnedParsePromptArgs(s), ParsePromptArgs(s)) {
			return ""
		}
		return s
	}
	for range 1500 {
		argStrings = append(argStrings, withoutEmptyQuotes(build(argTokens, 10)))
		templates = append(templates, build(tmplTokens, 8))
		lines = append(lines, withoutEmptyQuotes("/"+[]string{"known", "other", "unknown", ""}[rng.Intn(4)]+build(lineTokens, 6)))
	}
	for range 12 {
		args := []string{}
		for range rng.Intn(5) {
			args = append(args, build(argTokens, 3))
		}
		argLists = append(argLists, args)
	}
	known := []string{"known", "other"}
	var want struct {
		Parsed      [][]string `json:"parsed"`
		Substituted []string   `json:"substituted"`
		Expanded    []string   `json:"expanded"`
	}
	pioracle.Run(t, promptArgsOracleBody, map[string]any{"argStrings": argStrings, "templates": templates, "argLists": argLists, "lines": lines, "known": known}, &want)
	failures := 0
	report := func(format string, args ...any) {
		if failures++; failures <= 8 {
			t.Errorf(format, args...)
		}
	}
	for i, s := range argStrings {
		if got := ParsePromptArgs(s); !reflect.DeepEqual(append([]string{}, got...), append([]string{}, want.Parsed[i]...)) {
			report("ParsePromptArgs(%q) = %q, Pi %q", s, got, want.Parsed[i])
		}
	}
	n := 0
	for _, tmpl := range templates {
		for _, args := range argLists {
			if got := SubstitutePromptArgs(tmpl, args); got != want.Substituted[n] {
				report("SubstitutePromptArgs(%q, %q) = %q, Pi %q", tmpl, args, got, want.Substituted[n])
			}
			n++
		}
	}
	var promptTemplates []PromptTemplate
	for _, name := range known {
		promptTemplates = append(promptTemplates, PromptTemplate{Name: name, Content: "[" + name + " $1|$@|${@:2}]"})
	}
	for i, line := range lines {
		expanded, ok := ExpandPromptTemplate(line, promptTemplates)
		if !ok {
			expanded = line
		}
		if expanded != want.Expanded[i] {
			report("ExpandPromptTemplate(%q) = %q, Pi %q", line, expanded, want.Expanded[i])
		}
	}
	if failures > 8 {
		t.Errorf("%d differences from Pi", failures)
	}
}

// pinnedParsePromptArgs is parseCommandArgs of the pinned Pi, before Pi 9fe3f371: a token exists only once it holds a character.
func pinnedParsePromptArgs(s string) []string {
	args := []string{}
	var cur strings.Builder
	var quote rune
	for _, char := range s {
		switch {
		case quote != 0 && char == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(char)
		case char == '"' || char == '\'':
			quote = char
		case isJSWhitespace(char):
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(char)
		}
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}
