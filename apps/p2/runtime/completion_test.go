package runtime

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// Ports apps/p2/test/p2/completion.test.ts, test "prefix extraction, quoting and insertion follow pi-tui" (the runtime
// text helpers). The other three tests of that file drive the TUI provider (src/tui/completion.ts), the chat editor and
// the file index's bounds: they belong to lane E (tui/completion) and to plugins/file-index and are ported there.

func TestCompletionPrefixExtractionQuotingAndInsertion(t *testing.T) {
	at := func(text string) (string, bool) { return ExtractAtPrefix(text) }
	for _, tc := range []struct {
		name, text string
		want       string
		ok         bool
	}{
		{"at prefix", "look at @src/ma", "@src/ma", true},
		{"quoted at prefix", `see @"my fi`, `@"my fi`, true},
		{"not at a token start", "mail me@example", "", false},
		{"opening wrappers are stripped", "(@src", "@src", true},
	} {
		got, ok := at(tc.text)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: ExtractAtPrefix(%q) = %q, %v; want %q, %v", tc.name, tc.text, got, ok, tc.want, tc.ok)
		}
	}
	for _, tc := range []struct {
		text  string
		force bool
		want  string
		ok    bool
	}{
		{"edit src/ma", false, "src/ma", true},
		{"hello", false, "", false},
		{"hello", true, "hello", true}, // forced Tab takes any token
	} {
		got, ok := ExtractPathPrefix(tc.text, tc.force)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ExtractPathPrefix(%q, %v) = %q, %v; want %q, %v", tc.text, tc.force, got, ok, tc.want, tc.ok)
		}
	}
	for _, prefix := range []string{"/etc", "~", "~/x", "..", "../x"} {
		if !OutOfScope(prefix) {
			t.Errorf("OutOfScope(%q) = false", prefix)
		}
	}
	for _, prefix := range []string{"src", ".env", "./src", "..a", "...", ""} {
		if OutOfScope(prefix) {
			t.Errorf("OutOfScope(%q) = true", prefix)
		}
	}
	if got, want := AtItems([]IndexedPath{{Path: "my dir/a b.ts"}}, false), []CompletionItem{{Value: `@"my dir/a b.ts"`, Label: "a b.ts", Description: "my dir/a b.ts"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("AtItems = %+v, want %+v", got, want)
	}
	got := PathItems([]DirectoryEntry{{"main.ts", false}, {"maps", true}, {"other.ts", false}}, "src/ma", false, false)
	if want := []CompletionItem{{Value: "src/maps/", Label: "maps/"}, {Value: "src/main.ts", Label: "main.ts"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("PathItems = %+v, want %+v", got, want)
	}
	// Files after `@` get a trailing space; directories none (completion continues).
	line, col := ApplyFileCompletion("see @sr now", 7, CompletionItem{Value: "@src/a.ts", Label: "a.ts"}, "@sr")
	if line != "see @src/a.ts  now" || col != 14 {
		t.Errorf("ApplyFileCompletion file = %q, %d", line, col)
	}
	line, col = ApplyFileCompletion("@sr", 3, CompletionItem{Value: "@src/", Label: "src/"}, "@sr")
	if line != "@src/" || col != 5 {
		t.Errorf("ApplyFileCompletion directory = %q, %d", line, col)
	}
}

func TestCompletionQuotingAndMentions(t *testing.T) {
	if got := BuildCompletionValue("a b", CompletionValueOptions{IsAtPrefix: true}); got != `@"a b"` {
		t.Errorf("separator quotes: %q", got)
	}
	if got := BuildCompletionValue("ab", CompletionValueOptions{IsQuotedPrefix: true}); got != `"ab"` {
		t.Errorf("quoted prefix keeps quotes: %q", got)
	}
	if got := BuildCompletionValue("日本，語", CompletionValueOptions{}); got != `"日本，語"` {
		t.Errorf("CJK punctuation is a separator: %q", got)
	}
	// A closing quote after the cursor is consumed, a directory's cursor stays inside the quotes.
	line, col := ApplyFileCompletion(`open @"my d" x`, 11, CompletionItem{Value: `@"my dir/"`, Label: "dir/"}, `@"my d`)
	if line != `open @"my dir/" x` || col != 14 {
		t.Errorf("directory in quotes: %q, %d", line, col)
	}
	if got := ParsePathPrefix(`@"a`); got != (PathPrefix{"a", true, true}) {
		t.Errorf("ParsePathPrefix @\": %+v", got)
	}
	if got := ParsePathPrefix(`"a`); got != (PathPrefix{"a", false, true}) {
		t.Errorf("ParsePathPrefix \": %+v", got)
	}
	if got := ParsePathPrefix("@a"); got != (PathPrefix{"a", true, false}) {
		t.Errorf("ParsePathPrefix @: %+v", got)
	}
	if got := PathCompletion("./sr"); got != (PathCompletionParts{Dir: "", Name: "sr", Display: "./"}) {
		t.Errorf("PathCompletion ./sr: %+v", got)
	}
	if got := PathCompletion("a/b/c"); got != (PathCompletionParts{Dir: "a/b", Name: "c", Display: "a/b/"}) {
		t.Errorf("PathCompletion a/b/c: %+v", got)
	}
	if !IsMentionToken("@PI", DefaultMentionName) || IsMentionToken("@pip", DefaultMentionName) || !IsMentionToken("@Ada", "ada") {
		t.Error("IsMentionToken")
	}
	if got := MentionItems("a", "Ada"); !reflect.DeepEqual(got, []CompletionItem{{Value: "@ada", Label: "@ada", Description: "mention Ada"}}) {
		t.Errorf("MentionItems = %+v", got)
	}
	if MentionItems("b", "Ada") != nil || MentionItems("", "") != nil {
		t.Error("MentionItems without a match or name")
	}
	if !ShouldTriggerFileCompletion("/model src") || ShouldTriggerFileCompletion("/mod") || !ShouldTriggerFileCompletion("fix") {
		t.Error("ShouldTriggerFileCompletion")
	}
}

func FuzzCompletionPrefixes(f *testing.F) {
	for _, seed := range []string{"look at @src/ma", `see @"my fi`, "(@src", "app/[slug]/pa", "`src", "日本，@x", `a"b"c"`, ""} {
		f.Add(seed, true)
	}
	f.Fuzz(func(t *testing.T, text string, force bool) {
		if !utf8.ValidString(text) {
			t.Skip()
		}
		if prefix, ok := ExtractAtPrefix(text); ok && (!strings.HasSuffix(text, prefix) || !strings.HasPrefix(prefix, "@")) {
			t.Fatalf("ExtractAtPrefix(%q) = %q is not an @ suffix", text, prefix)
		}
		prefix, ok := ExtractPathPrefix(text, force)
		if ok && !strings.HasSuffix(text, prefix) {
			t.Fatalf("ExtractPathPrefix(%q) = %q is not a suffix", text, prefix)
		}
		if ok {
			parts := ParsePathPrefix(prefix)
			PathCompletion(parts.RawPrefix)
			item := CompletionItem{Value: BuildCompletionValue(parts.RawPrefix, CompletionValueOptions{IsAtPrefix: parts.IsAtPrefix, IsQuotedPrefix: parts.IsQuotedPrefix}), Label: "x"}
			line, col := ApplyFileCompletion(text, len(text), item, prefix)
			if !utf8.ValidString(line) || col < 0 || col > len(line) {
				t.Fatalf("ApplyFileCompletion(%q) = %q, %d", text, line, col)
			}
		}
	})
}
