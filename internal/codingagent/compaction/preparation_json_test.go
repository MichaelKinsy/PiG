package compaction

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// session_before_compact hands extensions upstream's CompactionPreparation
// (compaction.ts): camelCase keys, optional previousSummary, array message
// lists, and settings {enabled, reserveTokens, keepRecentTokens}. fileOps
// holds upstream's three Sets; the subprocess wire carries each as a sorted
// string array.
func TestCompactionPreparationWireFormatMatchesUpstreamFieldNames(t *testing.T) {
	ops := NewFileOps()
	ops.Read["b.go"] = struct{}{}
	ops.Read["a.go"] = struct{}{}
	ops.Edited["c.go"] = struct{}{}
	prep := CompactionPreparation{
		FirstKeptEntryID:    "e1",
		MessagesToSummarize: []agent.AgentMessage{},
		TurnPrefixMessages:  []agent.AgentMessage{},
		IsSplitTurn:         true,
		TokensBefore:        42,
		PreviousSummary:     "prev",
		FileOps:             ops,
		Settings:            CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000},
	}
	got, err := json.Marshal(prep)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"firstKeptEntryId":"e1","messagesToSummarize":[],"turnPrefixMessages":[],"isSplitTurn":true,"tokensBefore":42,"previousSummary":"prev","fileOps":{"read":["a.go","b.go"],"written":[],"edited":["c.go"]},"settings":{"enabled":true,"reserveTokens":16384,"keepRecentTokens":20000}}`
	if string(got) != want {
		t.Fatalf("wire =\n%s\nwant\n%s", got, want)
	}

	prep.PreviousSummary = ""
	prep.FileOps = FileOperations{}
	got, err = json.Marshal(prep)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["previousSummary"]; ok {
		t.Fatalf("previousSummary must be omitted when there is no previous compaction: %s", got)
	}
	if string(mustJSON(t, decoded["fileOps"])) != `{"edited":[],"read":[],"written":[]}` {
		t.Fatalf("empty fileOps = %s, want three empty arrays", mustJSON(t, decoded["fileOps"]))
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
