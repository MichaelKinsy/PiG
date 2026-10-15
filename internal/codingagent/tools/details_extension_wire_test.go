package tools

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 0.87.1 grep.ts:291 and ls.ts:148 expose numeric effectiveLimit unchanged to tool_result subscribers.
func TestBuiltinFractionalDetailsExtensionProjection(t *testing.T) {
	grep, ok := extension.ToolResultDetailsFor(&GrepDetails{MatchLimitReached: 1.5}).(*extension.GrepToolDetails)
	if !ok || grep.MatchLimitReached != 1.5 {
		t.Fatalf("grep projection=%#v", grep)
	}
	ls, ok := extension.ToolResultDetailsFor(&LsDetails{EntryLimitReached: 1.5}).(*extension.LsToolDetails)
	if !ok || ls.EntryLimitReached != 1.5 {
		t.Fatalf("ls projection=%#v", ls)
	}
	for _, details := range []any{grep, ls} {
		data, err := json.Marshal(details)
		if err != nil {
			t.Fatal(err)
		}
		switch details.(type) {
		case *extension.GrepToolDetails:
			var decoded extension.GrepToolDetails
			if err := json.Unmarshal(data, &decoded); err != nil || decoded.MatchLimitReached != 1.5 {
				t.Fatalf("grep decoding=%#v %v", decoded, err)
			}
		case *extension.LsToolDetails:
			var decoded extension.LsToolDetails
			if err := json.Unmarshal(data, &decoded); err != nil || decoded.EntryLimitReached != 1.5 {
				t.Fatalf("ls decoding=%#v %v", decoded, err)
			}
		}
	}
}

// Pi find.ts:149-165 builds FindToolDetails (find.ts:43-46: truncation?, resultLimitReached?) only for what happened: resultLimitReached is the effective
// limit when the result cap was hit (a fractional limit stays fractional, a reached zero stays present), truncation is the byte truncation when the output
// was cut, and a search that hit neither sends no details (find.ts:165 `details: Object.keys(details).length > 0 ? details : undefined`). The extension
// wire form omits an absent member.
// mutation-checked: dropping the limit, the truncation, the empty-details nil, the zero-limit presence or a wire tag fails it.
func TestFindDetailsExtensionProjection(t *testing.T) {
	if got := extension.ToolResultDetailsFor(&FindDetails{}); got != nil {
		t.Fatalf("empty find details project to %#v, want nil", got)
	}
	if got := extension.ToolResultDetailsFor(&FindDetails{Truncation: &TruncationResult{Truncated: false, Content: "kept"}}); got != nil {
		t.Fatalf("an untruncated result projects to %#v, want nil", got)
	}
	for _, tc := range []struct {
		name    string
		details *FindDetails
		want    string
	}{
		{"limit", &FindDetails{ResultLimitReached: new(1000.0)}, `{"resultLimitReached":1000}`},
		{"fractional limit", &FindDetails{ResultLimitReached: new(1.5)}, `{"resultLimitReached":1.5}`},
		{"zero limit stays present", &FindDetails{ResultLimitReached: new(0.0)}, `{"resultLimitReached":0}`},
		{"truncation", &FindDetails{Truncation: &TruncationResult{Content: "a\nb", Truncated: true, TruncatedBy: "bytes", TotalLines: 9, TotalBytes: 90, OutputLines: 2, OutputBytes: 3, MaxLines: 1<<53 - 1, MaxBytes: 51200}},
			`{"truncation":{"content":"a\nb","truncated":true,"truncatedBy":"bytes","totalLines":9,"totalBytes":90,"outputLines":2,"outputBytes":3,"lastLinePartial":false,"firstLineExceedsLimit":false,"maxLines":9007199254740991,"maxBytes":51200}}`},
		{"both", &FindDetails{ResultLimitReached: new(5.0), Truncation: &TruncationResult{Truncated: true, TruncatedBy: "bytes"}},
			`{"truncation":{"content":"","truncated":true,"truncatedBy":"bytes","totalLines":0,"totalBytes":0,"outputLines":0,"outputBytes":0,"lastLinePartial":false,"firstLineExceedsLimit":false,"maxLines":0,"maxBytes":0},"resultLimitReached":5}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projected, ok := extension.ToolResultDetailsFor(tc.details).(*extension.FindToolDetails)
			if !ok {
				t.Fatalf("projection = %#v, want *extension.FindToolDetails", extension.ToolResultDetailsFor(tc.details))
			}
			data, err := json.Marshal(projected)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.want {
				t.Fatalf("wire form = %s\nwant        %s", data, tc.want)
			}
			var decoded extension.FindToolDetails
			if err := json.Unmarshal(data, &decoded); err != nil || (decoded.ResultLimitReached == nil) != (tc.details.ResultLimitReached == nil) {
				t.Fatalf("decoded = %#v, %v", decoded, err)
			}
		})
	}
}
