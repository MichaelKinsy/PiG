package coding

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A draft entry an extension proposes at a turn boundary is stored as the extension wrote it: Pi appends the objects themselves, and JSON.stringify writes
// their members in insertion order. The host reads a draft's `data`, `details` and `content` from the wire and must write them back in the order they were
// read, whichever SDK sent them (a Go map, which the host would otherwise build, has none).
func TestBoundaryDraftMembersAreStoredInTheOrderTheExtensionWroteThem(t *testing.T) {
	const content = `[{"type":"text","text":"hello"},{"type":"image","mimeType":"image/png","data":"aGk="}]`
	for _, test := range []struct {
		name, draft, member string
	}{
		{"custom data", `{"type":"custom","customType":"order-data","data":{"z":1,"a":{"y":1,"b":2}}}`, `"data":{"z":1,"a":{"y":1,"b":2}}`},
		{"custom message content blocks", `{"type":"custom_message","customType":"order-content","content":` + content + `,"display":true}`, `"content":` + content},
		{"custom message details", `{"type":"custom_message","customType":"order-details","content":"c","display":true,"details":{"z":1,"a":2}}`, `"details":{"z":1,"a":2}`},
		{"compaction details", `{"type":"compaction","summary":"s","firstKeptEntryId":null,"details":{"z":1,"a":2}}`, `"details":{"z":1,"a":2}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var draft extension.SessionBoundaryDraft
			if err := json.Unmarshal([]byte(test.draft), &draft); err != nil {
				t.Fatal(err)
			}
			handled := false
			ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"turn_end": {func(...any) (any, error) {
				if handled {
					return nil, nil
				}
				handled = true
				return boundaryDrafts(false, draft), nil
			}}}}
			h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("done", ai.StopReasonStop, 0))
			boundaryPrompt(t, h, "start")
			var stored []string
			for _, entry := range h.session.Inner().GetEntries() {
				if typ := entry.Base().Type; typ == string(draft.Type) {
					stored = append(stored, string(entry.Raw()))
				}
			}
			if len(stored) == 0 {
				t.Fatalf("no %s entry was stored", draft.Type)
			}
			if !strings.Contains(stored[len(stored)-1], test.member) {
				t.Errorf("the stored entry does not hold %s\n got %s", test.member, stored[len(stored)-1])
			}
		})
	}
}
