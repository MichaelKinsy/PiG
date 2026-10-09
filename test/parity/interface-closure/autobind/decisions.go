package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
)

// reviewedDecisionsFile holds every hand-reviewed designed-out and divergence row, keyed by interface ID. The mapping file is regenerated
// from it, so a merge or a regeneration of the mapping cannot reset a reviewed decision: the file is an input, never a field the
// regeneration rewrites. A decision carries the row's upstreamShapeHash and applies only while the upstream shape is unchanged; a changed
// shape makes the row pending again and its decision awaits re-review.
const reviewedDecisionsFile = "test/parity/interface-closure/autobind/reviewed-decisions.json"

// reviewedDecisions maps an interface ID to its mapping row without the id key.
type reviewedDecisions map[string]orderedRow

// decisionStats reports what applyReviewedDecisions did.
type decisionStats struct {
	Applied, Restored, Stale, Imported, Reset int
}

func isReviewedDisposition(disposition string) bool {
	return disposition == "designed-out" || disposition == "divergence"
}

// withoutID drops the id key of a row.
func withoutID(row orderedRow) orderedRow {
	out := make(orderedRow, 0, len(row))
	for _, p := range row {
		if p.key != "id" {
			out = append(out, p)
		}
	}
	return out
}

// applyReviewedDecisions makes every mapping row with a current reviewed decision that decision. A designed-out or divergence row the file
// does not hold is an error, unless the rules close it themselves (derived reports whether, and whether as designed-out): such a row has no
// hand decision to lose, and a row the rules close another way is derived again.
// With importMissing the file gains those rows instead. The stale IDs (shape changed) are counted, not applied.
func applyReviewedDecisions(doc *mappingDoc, reviewed reviewedDecisions, derived func(id string) (closed, designedOut bool), importMissing bool) (decisionStats, error) {
	var st decisionStats
	var missing []string
	for i, row := range doc.Mappings {
		id, disposition, hash := row.get("id"), row.get("disposition"), row.get("upstreamShapeHash")
		if dec, ok := reviewed[id]; ok {
			if dec.get("upstreamShapeHash") != hash {
				st.Stale++
				continue
			}
			if disposition != dec.get("disposition") {
				st.Restored++
			}
			doc.Mappings[i] = append(orderedRow{{"id", jsonValue(id)}}, dec...)
			st.Applied++
			continue
		}
		if !isReviewedDisposition(disposition) {
			continue
		}
		if closed, designedOut := derived(id); closed {
			if !designedOut {
				// The rules close the row without the hand decision (a registry key a rule now derives): it is derived again, not kept.
				doc.Mappings[i] = pendingRow(id, hash)
				st.Reset++
			}
			continue
		}
		if importMissing {
			reviewed[id] = withoutID(row)
			st.Imported++
			continue
		}
		missing = append(missing, id)
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		shown := missing[:min(len(missing), 5)]
		return st, fmt.Errorf("%d designed-out or divergence rows are not in %s (a hand decision lives there so a regeneration keeps it; add the row or run autobind -import-decisions): %s", len(missing), reviewedDecisionsFile, strings.Join(shown, ", "))
	}
	return st, nil
}

// encodeReviewedDecisions writes one line per ID in ID order, so two lanes that add different decisions merge without a conflict.
func encodeReviewedDecisions(reviewed reviewedDecisions) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("{\n")
	ids := slices.Sorted(maps.Keys(reviewed))
	for i, id := range ids {
		key, err := json.Marshal(id)
		if err != nil {
			return nil, err
		}
		val, err := reviewed[id].MarshalJSON()
		if err != nil {
			return nil, err
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, val); err != nil {
			return nil, err
		}
		b.WriteString(" ")
		b.Write(key)
		b.WriteString(": ")
		b.Write(compact.Bytes())
		if i < len(ids)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}

func readReviewedDecisions(path string) (reviewedDecisions, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return reviewedDecisions{}, nil
	}
	if err != nil {
		return nil, err
	}
	var reviewed reviewedDecisions
	if err := json.Unmarshal(data, &reviewed); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for id, row := range reviewed {
		if !isReviewedDisposition(row.get("disposition")) || row.get("upstreamShapeHash") == "" {
			return nil, fmt.Errorf("%s: %s must be a designed-out or divergence row with an upstreamShapeHash", path, id)
		}
	}
	return reviewed, nil
}

// checkDistinctDecisions fails when two rows share one decision value or when an ID has two decisions. A decision records its row's ID, so a
// value shared by several rows takes the ID of whichever row set it last, and the ledger then depends on map iteration order.
func checkDistinctDecisions(ds []*decision) error {
	seen := make(map[string]bool, len(ds))
	for _, d := range ds {
		if seen[d.ID] {
			return fmt.Errorf("the detector produced two decisions for %s (a rule shared one decision value between rows)", d.ID)
		}
		seen[d.ID] = true
	}
	return nil
}
