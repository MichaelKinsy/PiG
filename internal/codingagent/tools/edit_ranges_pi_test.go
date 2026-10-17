package tools

import (
	"encoding/json"
	"errors"
	"testing"
)

// Pi edit-diff.ts getReplacementLineRange (:84-107): the half-open line range a replacement touches; a replacement that starts outside every line, or that ends after the last line, is
// "Replacement range is outside the base content."
// mutation-checked: `<=` for `<` in the walk to the end line and `>` for `>=` in the end-of-lines check fail it.
func TestGetReplacementLineRangeRejectsARangeThatEndsPastTheLastLine(t *testing.T) {
	lines := getLineSpans("ab\ncd\nef")
	for _, tc := range []struct {
		name        string
		index, size int
		start, end  int
		wantOutside bool
	}{
		{"inside the first line", 0, 1, 0, 1, false},
		{"ends exactly at the end of a line", 0, 3, 0, 1, false},
		{"spans two lines", 1, 3, 0, 2, false},
		{"ends exactly at the end of the content", 3, 5, 1, 3, false},
		{"ends one byte past the content", 6, 3, 0, 0, true},
		{"starts past the content", 8, 1, 0, 0, true},
		{"starts at the end of the content", 8, 0, 0, 0, true},
	} {
		start, end, err := getReplacementLineRange(lines, matchedEdit{matchIndex: tc.index, matchLength: tc.size})
		if tc.wantOutside {
			if !errors.Is(err, errReplacementOutsideBase) || err.Error() != "Replacement range is outside the base content." {
				t.Errorf("%s: err = %v, want the outside-the-base error", tc.name, err)
			}
			continue
		}
		if err != nil || start != tc.start || end != tc.end {
			t.Errorf("%s: getReplacementLineRange = %d, %d, %v, want %d, %d", tc.name, start, end, err, tc.start, tc.end)
		}
	}
}

// Pi truncate.ts truncateMiddle (:292-314) cuts only at character boundaries. Pi's strings never hold a lone continuation byte, but a Go string can, and the boundary walks must stop
// at the ends of the content instead of indexing outside it.
// mutation-checked: `>=` for `>` in the head walk panics on this input.
func TestTruncateMiddleStopsAtTheEndsOfContentWithoutCharacterStarts(t *testing.T) {
	content := "\x80\x80\x80\x80\x80\x80\x80\x80"
	got := TruncateMiddle(content, 1)
	if got.Content == "" || got.TotalBytes != len(content) {
		t.Errorf("TruncateMiddle of continuation bytes = %+v", got)
	}
}

// Pi edit.ts prepareEditArguments (:39-70) on the raw arguments: an edit needs BOTH oldText and newText strings; the legacy top-level pair is merged only when both are strings; one string
// alone leaves the arguments as they were.
// mutation-checked: `||` for `&&` in the legacy pair test and in isSingleEditInput fails it.
func TestPrepareEditArgumentsRawNeedsBothTextsToRepairAnEdit(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"top-level oldText alone", `{"path":"f","oldText":"a"}`, `{"path":"f","oldText":"a"}`},
		{"top-level newText alone", `{"path":"f","newText":"b"}`, `{"path":"f","newText":"b"}`},
		{"top-level pair with a numeric newText", `{"path":"f","oldText":"a","newText":2}`, `{"path":"f","oldText":"a","newText":2}`},
		{"top-level pair", `{"path":"f","oldText":"a","newText":"b"}`, `{"path":"f","edits":[{"oldText":"a","newText":"b"}]}`},
		{"edits object with only oldText", `{"path":"f","edits":{"oldText":"a"}}`, `{"path":"f","edits":{"oldText":"a"}}`},
		{"edits object with a numeric newText", `{"edits":{"oldText":"a","newText":1}}`, `{"edits":{"oldText":"a","newText":1}}`},
		{"edits object", `{"edits":{"oldText":"a","newText":"b"}}`, `{"edits":[{"oldText":"a","newText":"b"}]}`},
		{"edits string holding an incomplete edit", `{"edits":"{\"oldText\":\"a\"}"}`, `{"edits":"{\"oldText\":\"a\"}"}`},
		{"edits string holding an edit", `{"edits":"{\"oldText\":\"a\",\"newText\":\"b\"}"}`, `{"edits":[{"oldText":"a","newText":"b"}]}`},
	} {
		got := string(prepareEditArgumentsRaw(json.RawMessage(tc.in)))
		if got != tc.want {
			t.Errorf("%s: prepareEditArgumentsRaw(%s) = %s, want %s", tc.name, tc.in, got, tc.want)
		}
	}
}
