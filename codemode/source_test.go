package codemode_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
)

// Ports packages/codemode/test/source.test.ts (v0.99.1).

func parse(t *testing.T, input string) codemode.ParsedSource {
	t.Helper()
	parsed, err := codemode.ParseCodemodeSource(input)
	if err != nil {
		t.Fatalf("ParseCodemodeSource(%q): %v", input, err)
	}
	return parsed
}

func sameOptions(a, b codemode.SourceOptions) bool {
	eq := func(x, y *int64) bool { return (x == nil && y == nil) || (x != nil && y != nil && *x == *y) }
	return eq(a.MaxOutputTokens, b.MaxOutputTokens) && eq(a.TimeoutMs, b.TimeoutMs)
}

func TestParseCodemodeSourceReturnsPlainCodeUnchanged(t *testing.T) {
	for _, input := range []string{"text('hi')", "// just a comment\nreturn 1"} {
		if got := parse(t, input); got.Code != input || !sameOptions(got.Options, codemode.SourceOptions{}) {
			t.Errorf("ParseCodemodeSource(%q) = %+v", input, got)
		}
	}
}

func TestParseCodemodeSourceParsesTheOptionsLineAndKeepsLineNumbers(t *testing.T) {
	got := parse(t, "// @options: {\"timeout_ms\": 10}\nconst a = 1;\ntext(a)")
	if got.Code != "\nconst a = 1;\ntext(a)" || !sameOptions(got.Options, codemode.SourceOptions{TimeoutMs: new(int64(10))}) {
		t.Errorf("got %+v", got)
	}
	got = parse(t, "  // @options:{\"max_output_tokens\":0,\"timeout_ms\":1500}\r\ntext(1)")
	if !sameOptions(got.Options, codemode.SourceOptions{MaxOutputTokens: new(int64(0)), TimeoutMs: new(int64(1500))}) {
		t.Errorf("options = %+v", got.Options)
	}
	got = parse(t, "// @options: {}\ntext(1)")
	if got.Code != "\ntext(1)" || !sameOptions(got.Options, codemode.SourceOptions{}) {
		t.Errorf("got %+v", got)
	}
}

func TestParseCodemodeSourceOnlyTreatsTheFirstLineAsAnOptionsLine(t *testing.T) {
	input := "text(1)\n// @options: {\"timeout_ms\": 1}"
	if got := parse(t, input); got.Code != input || !sameOptions(got.Options, codemode.SourceOptions{}) {
		t.Errorf("got %+v", got)
	}
	if got := parse(t, "// @optionsx {}\ntext(1)"); !sameOptions(got.Options, codemode.SourceOptions{}) {
		t.Errorf("options = %+v", got.Options)
	}
}

func TestParseCodemodeSourceRejectsEmptyInputAndInvalidOptions(t *testing.T) {
	cases := []struct{ input, message string }{
		{"", "Expected JavaScript source text (non-empty)"},
		{"  \n", "Expected JavaScript source text (non-empty)"},
		{"// @options:\ntext(1)", "@options must be a JSON object with supported fields"},
		{"// @options: {timeout_ms: 1}\ntext(1)", "@options must be valid JSON with supported fields"},
		{"// @options: [1]\ntext(1)", "@options must be a JSON object with supported fields"},
		{"// @options: {\"yield\": 1}\ntext(1)", "@options only supports `max_output_tokens` and `timeout_ms`; got `yield`"},
		{"// @options: {\"max_output_tokens\": 1.5}\ntext(1)", "@options field `max_output_tokens` must be a non-negative safe integer"},
		{"// @options: {\"timeout_ms\": 0}\ntext(1)", "@options field `timeout_ms` must be a positive integer"},
		{"// @options: {\"timeout_ms\": 1}", "The @options line must be followed by JavaScript source on subsequent lines"},
		{"// @options: {\"timeout_ms\": 1}\n  \n", "The @options line must be followed by JavaScript source on subsequent lines"},
	}
	for _, c := range cases {
		_, err := codemode.ParseCodemodeSource(c.input)
		if _, ok := errors.AsType[*codemode.SourceError](err); !ok {
			t.Errorf("%q: error = %v, want *SourceError", c.input, err)
			continue
		}
		if !strings.Contains(err.Error(), c.message) {
			t.Errorf("%q: error = %q, want it to contain %q", c.input, err, c.message)
		}
	}
}
