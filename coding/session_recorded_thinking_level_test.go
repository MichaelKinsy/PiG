package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// recordDecodedThinkingLevel is how the level was read before recordedThinkingLevel read a session's own entry directly: every
// thinking_level_change entry from the decode of its record.
func recordDecodedThinkingLevel(branch []icodingagent.SessionEntry) (ai.ModelThinkingLevel, bool) {
	for _, b := range slices.Backward(branch) {
		if b.Base().Type != "thinking_level_change" {
			continue
		}
		var entry struct {
			ThinkingLevel string `json:"thinkingLevel"`
		}
		if json.Unmarshal(b.Raw(), &entry) != nil {
			continue
		}
		return ai.ModelThinkingLevel(entry.ThinkingLevel), true
	}
	return "", false
}

// The level recorded on a branch is the one the decode of the last thinking_level_change record gives, whether the entry was appended by
// the session, read from a file, or is a record whose level is not a string (which is passed over for the one before it).
func TestRecordedThinkingLevelIsTheLevelItsRecordDecodesTo(t *testing.T) {
	record := func(member string) icodingagent.SessionEntry {
		return sessionentry.DecodeSessionEntry(json.RawMessage(`{"type":"thinking_level_change","id":"0a1b2c3d","parentId":null,"timestamp":"2026-11-07T08:00:00.000Z"` + member + `}`))
	}
	message := sessionentry.DecodeSessionEntry(json.RawMessage(`{"type":"message","id":"4e5f6a7b","parentId":"0a1b2c3d","timestamp":"2026-11-07T08:00:01.000Z","message":{"role":"user","content":"go","timestamp":1}}`))
	session := icodingagent.NewSession("recorded-level", t.TempDir())
	if _, err := session.AppendThinkingLevelChange("low"); err != nil {
		t.Fatal(err)
	}
	appended := session.GetContextBranch()
	if len(appended) != 1 {
		t.Fatalf("the session's branch holds %d entries, want the appended one", len(appended))
	}
	if _, typed := appended[0].(icodingagent.ThinkingLevelEntry); !typed {
		t.Fatalf("the appended entry is a %T, want the session's own entry type", appended[0])
	}
	cases := []struct {
		name   string
		branch []icodingagent.SessionEntry
		want   ai.ModelThinkingLevel
		found  bool
	}{
		{"no entry", []icodingagent.SessionEntry{message}, "", false},
		{"empty branch", nil, "", false},
		{"appended by the session", appended, "low", true},
		{"read from a record", []icodingagent.SessionEntry{record(`,"thinkingLevel":"high"`), message}, "high", true},
		{"the last of two", []icodingagent.SessionEntry{record(`,"thinkingLevel":"high"`), message, appended[0]}, "low", true},
		{"an escaped level", []icodingagent.SessionEntry{record(`,"thinkingLevel":"x\u0068igh"`)}, "xhigh", true},
		{"a record with no level", []icodingagent.SessionEntry{record(``)}, "", true},
		{"a level that is not a string", []icodingagent.SessionEntry{record(`,"thinkingLevel":"medium"`), record(`,"thinkingLevel":7`)}, "", false},
		{"a record with a member the entry does not name", []icodingagent.SessionEntry{record(`,"thinkingLevel":"off","extra":{"a":1}`)}, "off", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, wantFound := recordDecodedThinkingLevel(c.branch)
			got, found := recordedThinkingLevel(c.branch)
			if got != want || found != wantFound {
				t.Fatalf("recorded level %q (found %v), the records decode to %q (found %v)", got, found, want, wantFound)
			}
			if c.name != "a level that is not a string" && (got != c.want || found != c.found) {
				t.Fatalf("recorded level %q (found %v), want %q (found %v)", got, found, c.want, c.found)
			}
		})
	}
}
