package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func decisionDoc(t *testing.T, rows ...string) *mappingDoc {
	t.Helper()
	doc := &mappingDoc{}
	for _, r := range rows {
		var row orderedRow
		if err := row.UnmarshalJSON([]byte(r)); err != nil {
			t.Fatal(err)
		}
		doc.Mappings = append(doc.Mappings, row)
	}
	return doc
}

const reviewedRow = `{"disposition":"designed-out","upstreamShapeHash":"sha256:aa","rationale":"Go replaces it","pigTargets":["x/y.go#Z"]}`

func neverDerived(string) (bool, bool) { return false, false }

// TestReviewedDecisionSurvivesAMappingRegeneration is the regression for integrate-042 merges resetting reviewed designed-out rows to
// pending: a row that regeneration or a merge left pending (or ported) gets its reviewed decision back, byte for byte.
func TestReviewedDecisionSurvivesAMappingRegeneration(t *testing.T) {
	for _, reset := range []string{"pending", "ported"} {
		reviewed := reviewedDecisions{}
		if err := reviewed.UnmarshalJSONFor(t, `{"pkg:x/.#A": `+reviewedRow+`}`); err != nil {
			t.Fatal(err)
		}
		doc := decisionDoc(t, `{"id":"pkg:x/.#A","disposition":"`+reset+`","upstreamShapeHash":"sha256:aa"}`)
		st, err := applyReviewedDecisions(doc, reviewed, neverDerived, false)
		if err != nil {
			t.Fatal(err)
		}
		row := doc.Mappings[0]
		if row.get("disposition") != "designed-out" || row.get("rationale") != "Go replaces it" || len(row.list("pigTargets")) != 1 || st.Restored != 1 || st.Applied != 1 {
			t.Errorf("a %s row did not get its reviewed decision back: %v %+v", reset, row.get("disposition"), st)
		}
		out, err := encodeMapping(doc)
		if err != nil || !strings.Contains(string(out), `"id": "pkg:x/.#A"`) {
			t.Errorf("the restored row is not encoded: %v", err)
		}
	}
}

// A decision applies only to the shape it reviewed: a changed upstream shape leaves the row to the derivation.
func TestReviewedDecisionIsStaleWhenTheShapeChanges(t *testing.T) {
	reviewed := reviewedDecisions{}
	if err := reviewed.UnmarshalJSONFor(t, `{"pkg:x/.#A": `+reviewedRow+`}`); err != nil {
		t.Fatal(err)
	}
	doc := decisionDoc(t, `{"id":"pkg:x/.#A","disposition":"pending","upstreamShapeHash":"sha256:bb"}`)
	st, err := applyReviewedDecisions(doc, reviewed, neverDerived, false)
	if err != nil || st.Stale != 1 || st.Applied != 0 || doc.Mappings[0].get("disposition") != "pending" {
		t.Errorf("a decision for another shape was applied: %+v %v", st, err)
	}
}

// A hand designed-out or divergence row that is only in the mapping would be lost by the next regeneration: it is an error, unless a rule
// derives it, and -import-decisions moves it into the file.
func TestReviewedDispositionOutsideTheDecisionsFile(t *testing.T) {
	row := `{"id":"pkg:x/.#B","disposition":"divergence","upstreamShapeHash":"sha256:cc","rationale":"D12"}`
	if _, err := applyReviewedDecisions(decisionDoc(t, row), reviewedDecisions{}, neverDerived, false); err == nil || !strings.Contains(err.Error(), "pkg:x/.#B") {
		t.Errorf("a hand divergence row outside the file is accepted: %v", err)
	}
	if _, err := applyReviewedDecisions(decisionDoc(t, row), reviewedDecisions{}, func(id string) (bool, bool) { return id == "pkg:x/.#B", id == "pkg:x/.#B" }, false); err != nil {
		t.Errorf("a rule-derived row needs no decision: %v", err)
	}
	reviewed := reviewedDecisions{}
	st, err := applyReviewedDecisions(decisionDoc(t, row), reviewed, neverDerived, true)
	if err != nil || st.Imported != 1 || reviewed["pkg:x/.#B"].get("rationale") != "D12" || reviewed["pkg:x/.#B"].get("id") != "" {
		t.Errorf("the import did not record the row without its id: %+v %v", st, err)
	}
}

// The file has one line per ID in ID order, so two lanes that add different decisions merge without a conflict, and it round-trips.
func TestReviewedDecisionsFileIsOneLinePerIDAndRoundTrips(t *testing.T) {
	reviewed := reviewedDecisions{}
	if err := reviewed.UnmarshalJSONFor(t, `{"pkg:x/.#B": `+reviewedRow+`, "pkg:x/.#A": `+reviewedRow+`}`); err != nil {
		t.Fatal(err)
	}
	data, err := encodeReviewedDecisions(reviewed)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], ` "pkg:x/.#A"`) || !strings.HasPrefix(lines[2], ` "pkg:x/.#B"`) {
		t.Errorf("not one sorted line per ID:\n%s", data)
	}
	path := filepath.Join(t.TempDir(), "d.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	back, err := readReviewedDecisions(path)
	if err != nil || len(back) != 2 || back["pkg:x/.#A"].get("rationale") != "Go replaces it" {
		t.Errorf("round trip: %v %v", back, err)
	}
	if err := os.WriteFile(path, []byte(`{"pkg:x/.#C":{"disposition":"ported","upstreamShapeHash":"h"}}`), 0o644); err == nil {
		if _, err := readReviewedDecisions(path); err == nil {
			t.Errorf("a ported decision is accepted")
		}
	}
}

// UnmarshalJSONFor decodes a decisions object in a test.
func (r *reviewedDecisions) UnmarshalJSONFor(t *testing.T, s string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "d.json")
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		return err
	}
	got, err := readReviewedDecisions(path)
	*r = got
	return err
}

// TestDerivedMappingKeepsReviewedDecisionsOfAResetMapping drives the production path (derivedMapping, which -update and -check run): a
// mapping whose reviewed row was reset to pending, as an integrate-042 regeneration did, is derived back with the decision.
func TestDerivedMappingKeepsReviewedDecisionsOfAResetMapping(t *testing.T) {
	dir := t.TempDir()
	mapping := filepath.Join(dir, "mapping.json")
	decisions := filepath.Join(dir, "reviewed-decisions.json")
	if err := os.WriteFile(mapping, []byte(`{"upstreamVersion":"1.0.4","mappings":[{"id":"pkg:x/.#A","disposition":"pending","upstreamShapeHash":"sha256:aa"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decisions, []byte(`{"pkg:x/.#A": `+reviewedRow+"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gap := &decision{ID: "pkg:x/.#A", Gap: true, Reason: reasonUndecided}
	out, st, _, err := derivedMapping(&ledger{}, mapping, decisions, []*decision{gap}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"disposition": "designed-out"`) || strings.Contains(string(out), `"pending"`) || st.Decisions.Restored != 1 || st.Reopened != 0 {
		t.Errorf("the reviewed row was not kept through the regeneration: %+v\n%s", st, out)
	}
}

// TestSharedDecisionValueIsRejected is the regression for the nondeterministic ledger: a rule that stored one decision for a property and
// its call rows made the decision's ID depend on map iteration order, so two runs disagreed on which row was designed out.
func TestSharedDecisionValueIsRejected(t *testing.T) {
	shared := &decision{ID: "pkg:x/.#A::call:0"}
	if err := checkDistinctDecisions([]*decision{{ID: "pkg:x/.#A"}, shared, shared}); err == nil {
		t.Fatal("two rows with one decision value were accepted")
	}
	if err := checkDistinctDecisions([]*decision{{ID: "pkg:x/.#A"}, {ID: "pkg:x/.#A::call:0"}}); err != nil {
		t.Fatalf("distinct decisions rejected: %v", err)
	}
}

// A designed-out row the rules close another way (an M6 registry key that is a Go constant) is derived again; it needs no decision and is
// not an error.
func TestHandRowTheRulesCloseAnotherWayIsDerivedAgain(t *testing.T) {
	row := `{"id":"pkg:x/.#K","disposition":"designed-out","upstreamShapeHash":"sha256:cc","rationale":"old"}`
	doc := decisionDoc(t, row)
	st, err := applyReviewedDecisions(doc, reviewedDecisions{}, func(string) (bool, bool) { return true, false }, false)
	if err != nil || st.Reset != 1 || doc.Mappings[0].get("disposition") != "pending" {
		t.Errorf("a row the rules close as ported was kept as designed-out: %+v %v %v", st, err, doc.Mappings[0].get("disposition"))
	}
}

// TestWriteDecisionsIfChanged: an update rewrites a decisions file another lane saved in a different layout, leaves a file already in the
// compact one-line-per-ID form untouched, and writes a missing file.
func TestWriteDecisionsIfChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reviewed-decisions.json")
	want := []byte("{\n \"a\": {}\n}\n")
	if err := writeDecisionsIfChanged(path, want, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
		t.Fatalf("a missing file is written: %q", got)
	}
	if err := os.WriteFile(path, []byte("{\n  \"a\": {\n  }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeDecisionsIfChanged(path, want, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
		t.Fatalf("a differently laid out file is rewritten: %q", got)
	}
	stamp := time.Unix(1, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := writeDecisionsIfChanged(path, want, false); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); !info.ModTime().Equal(stamp) {
		t.Error("an identical file is left untouched")
	}
}
