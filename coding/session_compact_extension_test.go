package coding

import (
	"context"
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
	shape, ok := result.(map[string]any)
	summary, _ := shape["summary"].(string)
	if !ok || !strings.HasPrefix(summary, "extension-requested summary") || shape["firstKeptEntryId"] == "" {
		t.Fatalf("result = %#v", result)
	}
	if _, ok := shape["tokensBefore"]; !ok {
		t.Fatalf("result lacks tokensBefore: %#v", result)
	}

	if _, err := sess.CompactForExtension(context.Background(), ""); err == nil {
		t.Fatal("compacting an already compacted session must report an error")
	}
}
