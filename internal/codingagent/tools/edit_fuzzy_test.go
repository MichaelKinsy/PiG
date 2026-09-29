package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runEdit writes content to a temp file, runs one edit call, and returns the
// result and the file's new content.
func runEdit(t *testing.T, content string, edits []editEntry) (string, *EditToolDetails, string, bool) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(editParams{Path: file, Edits: edits})
	res, err := (&EditTool{CWD: dir, Queue: NewFileMutationQueue()}).Execute(context.Background(), "", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	details, _ := res.Details.(*EditToolDetails)
	return res.Text(), details, string(after), res.IsError
}

// TOOL-03: a fuzzy match must rewrite only the lines it touches. Upstream
// copies every other line byte-for-byte from the original.
func TestEdit_FuzzyMatchPreservesUntouchedLines(t *testing.T) {
	original := "Title — with “quotes”  \nhard break  \nchange ‘me’\nx²\n"
	_, details, after, isErr := runEdit(t, original, []editEntry{{OldText: "change 'me'", NewText: "changed"}})
	if isErr {
		t.Fatal("edit failed")
	}
	want := "Title — with “quotes”  \nhard break  \nchanged\nx²\n"
	if after != want {
		t.Fatalf("file = %q, want %q", after, want)
	}
	if details == nil || !strings.Contains(details.Patch, "\n-change ‘me’\n+changed\n") || strings.Contains(details.Patch, "\n-Title") {
		t.Fatalf("diff/patch must cover only the changed line: %+v", details)
	}
}

// Ported from upstream test/tools.test.ts "edit tool fuzzy matching".
func TestEditFuzzyMatchingUpstreamCases(t *testing.T) {
	cases := []struct {
		name, content string
		edits         []editEntry
		want          string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1072
		{"should match text with trailing whitespace stripped", "line one   \nline two  \nline three\n",
			[]editEntry{{"line one\nline two\n", "replaced\n"}}, "replaced\nline three\n"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1088
		{"should match fullwidth punctuation in Chinese text", "你好，世界\n你好（世界）\n",
			[]editEntry{{"你好,世界\n你好(世界)\n", "你好，pi\n你好(pi)\n"}}, "你好，pi\n你好(pi)\n"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1102
		{"should match compatibility-equivalent Unicode forms", "ＡＢＣ１２３\ncafé\n",
			[]editEntry{{"ABC123\ncafé\n", "XYZ789\ncoffee\n"}}, "XYZ789\ncoffee\n"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1116
		{"should match smart single quotes to ASCII quotes", "console.log(‘hello’);\n",
			[]editEntry{{"console.log('hello');", "console.log('world');"}}, "console.log('world');\n"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1132
		{"should match smart double quotes to ASCII quotes", "const msg = “Hello World”;\n",
			[]editEntry{{`const msg = "Hello World";`, `const msg = "Goodbye";`}}, "const msg = \"Goodbye\";\n"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1148
		{"should match Unicode dashes to ASCII hyphen", "range: 1–5\nbreak—here\n",
			[]editEntry{{"range: 1-5\nbreak-here", "range: 10-50\nbreak--here"}}, "range: 10-50\nbreak--here\n"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1164
		{"should match non-breaking space to regular space", "hello world\n",
			[]editEntry{{"hello world", "hello universe"}}, "hello universe\n"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1180
		{"should prefer exact match over fuzzy match", "const x = 'exact';\nconst y = 'other';\n",
			[]editEntry{{"const x = 'exact';", "const x = 'changed';"}}, "const x = 'changed';\nconst y = 'other';\n"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1220
		{"should support fuzzy matching in multi-edit mode", "console.log(‘hello’);\nhello world\n",
			[]editEntry{{"console.log('hello');\n", "console.log('world');\n"}, {"hello world\n", "hello universe\n"}},
			"console.log('world');\nhello universe\n"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1235
		{"should preserve the correct occurrence when fuzzy replacement equals a nearby line", "replace me   \nafter   \n",
			[]editEntry{{"replace me\n", "after\n"}}, "after\nafter   \n"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1250
		{"should preserve untouched lines and produce an applicable patch for fuzzy multi-edits", "keep before  \nfirst target  \nfirst after\nkeep middle   \nsecond target  \nsecond after\nkeep after  \n",
			[]editEntry{{"first target\nfirst after", "FIRST\nFIRST2"}, {"second target\nsecond after", "SECOND\nSECOND2"}},
			"keep before  \nFIRST\nFIRST2\nkeep middle   \nSECOND\nSECOND2\nkeep after  \n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, details, after, isErr := runEdit(t, c.content, c.edits)
			if isErr || !strings.Contains(text, "Successfully replaced") {
				t.Fatalf("result = %q", text)
			}
			if after != c.want {
				t.Fatalf("file = %q, want %q", after, c.want)
			}
			if strings.HasPrefix(c.name, "should preserve") {
				if details == nil {
					t.Fatal("missing patch details")
				}
				assertApplicablePatch(t, c.content, details.Patch, c.want)
			}
		})
	}

	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1195
	t.Run("should still fail when text is not found even with fuzzy matching", func(t *testing.T) {
		text, _, _, isErr := runEdit(t, "completely different content\n", []editEntry{{"this does not exist", "replacement"}})
		if !isErr || !strings.Contains(text, "Could not find the exact text") {
			t.Errorf("not found: %q", text)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1207
	t.Run("should detect duplicates after fuzzy normalization", func(t *testing.T) {
		text, _, _, isErr := runEdit(t, "hello world   \nhello world\n", []editEntry{{"hello world", "replaced"}})
		if !isErr || !strings.Contains(text, "Found 2 occurrences") {
			t.Errorf("duplicates: %q", text)
		}
	})
}

// The patch of a fuzzy multi-edit removes only the targeted original lines.
func TestEditFuzzyPatchCoversOnlyTouchedLines(t *testing.T) {
	original := "keep before  \nfirst target  \nfirst after\nkeep middle   \nsecond target  \nsecond after\nkeep after  \n"
	_, details, _, isErr := runEdit(t, original, []editEntry{
		{"first target\nfirst after", "FIRST\nFIRST2"},
		{"second target\nsecond after", "SECOND\nSECOND2"},
	})
	if isErr || details == nil {
		t.Fatal("edit failed")
	}
	var removed []string
	for line := range strings.SplitSeq(details.Patch, "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			removed = append(removed, line[1:])
		}
	}
	want := []string{"first target  ", "first after", "second target  ", "second after"}
	if strings.Join(removed, "|") != strings.Join(want, "|") {
		t.Fatalf("removed lines = %q, want %q", removed, want)
	}
}

// CDT-001: untouched lines are copied byte-for-byte, trailing NULs included
// (upstream splitLinesWithEndings is content.match(/[^\n]*\n|[^\n]+/g)).
func TestFuzzyEditKeepsTrailingNULBytes(t *testing.T) {
	got, err := applyEditsToNormalizedContent("target x\nkeep\x00\x00", []editEntry{{OldText: "target x", NewText: "done"}}, "file")
	if err != nil {
		t.Fatal(err)
	}
	if want := "done\nkeep\x00\x00"; got.newContent != want {
		t.Fatalf("content = %q, want %q", got.newContent, want)
	}
	for in, want := range map[string][]string{
		"":           nil,
		"a":          {"a"},
		"a\n":        {"a\n"},
		"a\n\nb\x00": {"a\n", "\n", "b\x00"},
	} {
		if got := splitLinesWithEndings(in); strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("splitLinesWithEndings(%q) = %q, want %q", in, got, want)
		}
	}
}
