package extension

import (
	"encoding/json"
	"testing"
)

// packages/coding-agent/src/core/extensions/types.ts:1469 skipConversationRestore and types.ts:1468 cancel: a session_before_fork
// handler result carries both optional flags, and an SDK handler sends them as JSON, so the Go result decodes and re-encodes Pi's
// field names and omits an unset flag.
func TestSessionBeforeForkResultSkipConversationRestoreWire(t *testing.T) {
	for _, tc := range []struct {
		wire         string
		cancel, skip bool
	}{
		{`{}`, false, false},
		{`{"cancel":true}`, true, false},
		{`{"cancel":true,"skipConversationRestore":true}`, true, true},
	} {
		var result SessionBeforeForkResult
		if err := json.Unmarshal([]byte(tc.wire), &result); err != nil {
			t.Fatal(err)
		}
		if result.Cancel != tc.cancel || result.SkipConversationRestore != tc.skip {
			t.Errorf("%s decoded to %+v", tc.wire, result)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != tc.wire {
			t.Errorf("round trip of %s = %s", tc.wire, encoded)
		}
	}
}
