package compaction

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func uuidv7SequenceOf(t *testing.T, id string) uint64 {
	t.Helper()
	raw, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if err != nil || len(raw) != 16 || raw[6]>>4 != 7 {
		t.Fatalf("%q is not a UUIDv7 (%v)", id, err)
	}
	return uint64(raw[6]&0x0f)<<37 | uint64(raw[7])<<29 | uint64(raw[8]&0x3f)<<23 | uint64(raw[9])<<15 | uint64(raw[10])<<7 | uint64(raw[11]>>1)
}

// upstream: packages/coding-agent/src/core/compaction/compaction.ts: sessionId: options.sessionId ?? uuidv7()
// The fresh routing session id comes from the shared process-wide generator, so its sequence follows the previous shared id.
func TestCompleteSummarizationRoutingSessionUsesTheSharedUUIDv7Generator(t *testing.T) {
	recorder := &summaryRecorder{}
	before, err := ai.UUIDv7(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := completeSummarization(t.Context(), createSummaryModel(false, 8192, nil), recorder, nil, nil, ai.RetryCallbacks{}, "Summarize", nil, ai.StreamOptions{}); err != nil {
		t.Fatal(err)
	}
	sessionID := recorder.calls[0].options.SessionID
	if step := uuidv7SequenceOf(t, sessionID) - uuidv7SequenceOf(t, before); step == 0 || step > 1<<16 {
		t.Fatalf("session id %q does not follow the shared generator's %q (sequence step %d)", sessionID, before, step)
	}
}
