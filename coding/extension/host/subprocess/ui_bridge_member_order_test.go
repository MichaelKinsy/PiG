package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// appendEntry data and sendMessage details are `unknown` values the extension wrote (types.ts:1159-1163, 1450). The session stores them and later shows them to other extensions, the wire and the file as JSON.stringify writes them: in the order written.
func TestAppendEntryDataAndSendMessageDetailsKeepMemberOrder(t *testing.T) {
	const value = `{"zeta":1,"alpha":{"yy":2,"bb":3},"mid":[{"qq":1,"aa":2}]}`
	b := newTestBridge(&mockUIContext{})
	var data any
	var message extension.CustomMessageRef
	b.SetActions(&HostCallbacks{
		AppendEntry: func(_ string, got any, _ *DirectEntryAppend) error { data = got; return nil },
		SendMessage: func(got extension.CustomMessageRef, _ SendMessageOptions) error { message = got; return nil },
	})
	if _, err := call(b, "appendEntry", `{"customType":"note","data":`+value+`}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call(b, "sendMessage", `{"message":{"customType":"note","content":"x","details":`+value+`},"options":{}}`); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]any{"appendEntry data": data, "sendMessage details": message.Details} {
		if encoded, err := json.Marshal(got); err != nil || string(encoded) != value {
			t.Errorf("%s = %s, %v, want %s", name, encoded, err, value)
		}
	}
}
