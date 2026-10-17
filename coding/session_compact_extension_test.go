package coding

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// CompactForExtension answers ctx.compact's onComplete with upstream
// CompactionResult's JSON shape, and reports a session with nothing to
// compact as an error, as upstream's compact() rejects.
func TestCompactForExtensionReturnsUpstreamResultShape(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	sess.completer = &fakeCompleter{summary: "extension-requested summary"}
	result, err := sess.CompactForExtension(context.Background(), "keep decisions")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Summary, "extension-requested summary") || result.FirstKeptEntryID == "" || result.TokensBefore == 0 || result.EstimatedTokensAfter == nil {
		t.Fatalf("result = %#v", result)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"summary", "firstKeptEntryId", "tokensBefore", "estimatedTokensAfter"} {
		if _, ok := wire[key]; !ok {
			t.Fatalf("wire result lacks upstream key %s: %s", key, raw)
		}
	}

	if _, err := sess.CompactForExtension(context.Background(), ""); err == nil {
		t.Fatal("compacting an already compacted session must report an error")
	}
}
