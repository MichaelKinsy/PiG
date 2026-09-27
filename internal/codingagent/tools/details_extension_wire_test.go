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
