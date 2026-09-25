package coding

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pi's getSessionStats returns an object literal whose first two keys are
// sessionFile then sessionId; RPC get_session_stats serializes it in that
// order.
func TestSessionStatsJSONKeyOrderMatchesPi(t *testing.T) {
	data, err := json.Marshal(SessionStats{SessionID: "id", SessionFile: "/s.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), `{"sessionFile":"/s.jsonl","sessionId":"id","userMessages":0`) {
		t.Fatalf("get_session_stats key order = %s", data)
	}
}
