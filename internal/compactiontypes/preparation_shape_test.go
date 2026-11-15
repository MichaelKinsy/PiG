package compactiontypes

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

// CompactionPreparation is what an extension receives in session_before_compact, and coding/extension aliases it as an SDK type: its exported fields
// and its JSON keys are Pi's (compaction.ts:600) and nothing else, whatever the compaction pipeline carries beside them (D167 carries a token
// ratio).
func TestCompactionPreparationShowsOnlyPisFields(t *testing.T) {
	pi := []string{"fileOps", "firstKeptEntryId", "isSplitTurn", "messagesToSummarize", "previousSummary", "settings", "tokensBefore", "turnPrefixMessages"}

	typ := reflect.TypeFor[CompactionPreparation]()
	var exported []string
	for field := range typ.Fields() {
		if !field.IsExported() {
			continue
		}
		tag, _, _ := splitJSONTag(field.Tag.Get("json"))
		exported = append(exported, tag)
	}
	slices.Sort(exported)
	if !slices.Equal(exported, pi) {
		t.Fatalf("exported fields serialize as %v, want Pi's %v", exported, pi)
	}

	prep := CompactionPreparation{FirstKeptEntryID: "kept", PreviousSummary: "before", Settings: CompactionSettings{Enabled: true}}
	SetTokenRatio(&prep, 1.5)
	if got := TokenRatioOf(prep); got != 1.5 {
		t.Fatalf("token ratio %v, want 1.5", got)
	}
	raw, err := json.Marshal(prep)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for key := range wire {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, pi) {
		t.Fatalf("serialized preparation has keys %v, want Pi's %v", keys, pi)
	}
}

func splitJSONTag(tag string) (name, rest string, ok bool) {
	for i := range len(tag) {
		if tag[i] == ',' {
			return tag[:i], tag[i+1:], true
		}
	}
	return tag, "", false
}
