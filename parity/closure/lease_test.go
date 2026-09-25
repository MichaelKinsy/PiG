package closure

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/parity/correspondence"
)

// behaviorBundle builds one open behavior root with an origin pin (carrying a
// semantic boundary), a prod-reachable production root pin (a write path), and
// one open obligation. It is the minimum a work unit is derived from.
func behaviorBundle(snapshot *Snapshot, name, boundary, writePath string) []Record {
	h := func(value string) string { return HashBytes([]byte(name + value)) }
	originPin := &Pin{Kind: KindPin, ID: "pin:" + name + ":origin", SnapshotID: snapshot.ID, Repository: "upstream", Commit: snapshot.UpstreamCommit, Path: "packages/x/" + name + ".ts", SemanticID: boundary, StartLine: 1, EndLine: 1, QuoteHash: h("q")}
	prodPin := &Pin{Kind: KindPin, ID: "pin:" + name + ":prod", SnapshotID: snapshot.ID, Repository: "pig", Commit: snapshot.TargetCommit, Path: writePath, SemanticID: "sym." + name, StartLine: 1, EndLine: 1, QuoteHash: h("p"), APIHash: h("a"), BodyHash: h("b")}
	target := &Target{Kind: KindTarget, ID: "target:" + name, SnapshotID: snapshot.ID, PinIDs: []string{prodPin.ID}, Language: "go", Symbol: "sym." + name}
	behavior := &Behavior{Kind: KindBehavior, ID: "behavior:" + name, Name: name, OriginPinIDs: []string{originPin.ID}, Profile: "application"}
	facet := &Facet{Kind: KindFacet, ID: "facet:" + name, Name: "wire"}
	rule := &Rule{Kind: KindRule, ID: "rule:" + name, Name: name, DefinitionHash: h("r")}
	obligation := &Obligation{Kind: KindObligation, ID: "obligation:" + name, BehaviorID: behavior.ID, FacetID: facet.ID, RuleID: rule.ID, OriginPinIDs: []string{originPin.ID}}
	reachability := &Reachability{Kind: KindReachability, ID: "reachability:" + name, BehaviorID: behavior.ID, TargetID: target.ID, Class: "prod-reachable", Method: "production-call-path", RootPinIDs: []string{prodPin.ID}}
	return []Record{originPin, prodPin, target, behavior, facet, rule, obligation, reachability}
}

func planUnits(t *testing.T, records []Record) (*Graph, map[string]WorkUnit) {
	t.Helper()
	graph, err := Build(records)
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}
	units, err := PlanWorkUnits(graph)
	if err != nil {
		t.Fatalf("PlanWorkUnits() = %v", err)
	}
	index := make(map[string]WorkUnit, len(units))
	for _, unit := range units {
		index[unit.ID] = unit
	}
	return graph, index
}

func unitFor(t *testing.T, units map[string]WorkUnit, behaviorID string) WorkUnit {
	t.Helper()
	for _, unit := range units {
		if unit.BehaviorID == behaviorID {
			return unit
		}
	}
	t.Fatalf("no work unit for %s", behaviorID)
	return WorkUnit{}
}

func newLeaseSnapshot() *Snapshot {
	return &Snapshot{Kind: KindSnapshot, ID: "snapshot:lease", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: HashBytes([]byte("tc")), EnvironmentHash: HashBytes([]byte("env"))}
}

func TestPlanWorkUnitsDerivesOnePerOpenBehaviorWithPathsAndBoundaries(t *testing.T) {
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:shapeX", "ai/openai.go")...)
	records = append(records, behaviorBundle(snapshot, "beta", "wire:shapeY", "ai/anthropic.go")...)
	_, units := planUnits(t, records)

	if len(units) != 2 {
		t.Fatalf("units = %d, want 2", len(units))
	}
	alpha := unitFor(t, units, "behavior:alpha")
	if !slices.Equal(alpha.WritePaths, []string{"ai/openai.go"}) {
		t.Fatalf("alpha writes = %v", alpha.WritePaths)
	}
	if !slices.Equal(alpha.SemanticBoundaryIDs, []string{"wire:shapeX"}) {
		t.Fatalf("alpha boundaries = %v", alpha.SemanticBoundaryIDs)
	}
	if !slices.Equal(alpha.ObligationIDs, []string{"obligation:alpha"}) {
		t.Fatalf("alpha obligations = %v", alpha.ObligationIDs)
	}
}

func TestPlanWorkUnitsIsDeterministic(t *testing.T) {
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:shapeX", "ai/openai.go")...)
	records = append(records, behaviorBundle(snapshot, "beta", "wire:shapeY", "ai/anthropic.go")...)
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	first, err := PlanWorkUnits(graph)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanWorkUnits(graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("nondeterministic length %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].ID != second[i].ID || !slices.Equal(first[i].WritePaths, second[i].WritePaths) {
			t.Fatalf("nondeterministic unit at %d: %#v vs %#v", i, first[i], second[i])
		}
	}
}

func TestGrantLeaseRejectsTextualWriteOverlap(t *testing.T) {
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:shapeX", "ai/openai.go")...)
	// beta writes the same production file as alpha but has a distinct boundary.
	records = append(records, behaviorBundle(snapshot, "beta", "wire:shapeY", "ai/openai.go")...)
	graph, units := planUnits(t, records)
	alpha := unitFor(t, units, "behavior:alpha")
	beta := unitFor(t, units, "behavior:beta")

	lease, err := GrantLease(graph, nil, units, alpha.ID, "worker-1")
	if err != nil {
		t.Fatalf("first grant = %v", err)
	}
	if _, err := GrantLease(graph, []Lease{lease}, units, beta.ID, "worker-2"); err == nil || !strings.Contains(err.Error(), "path ai/openai.go") {
		t.Fatalf("overlapping grant error = %v, want write path conflict", err)
	}
}

func TestGrantLeaseRejectsSharedSemanticBoundaryAcrossDisjointFiles(t *testing.T) {
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:shapeX", "ai/openai.go")...)
	// beta writes a different file but shares alpha's open wire boundary.
	records = append(records, behaviorBundle(snapshot, "beta", "wire:shapeX", "ai/anthropic.go")...)
	graph, units := planUnits(t, records)
	alpha := unitFor(t, units, "behavior:alpha")
	beta := unitFor(t, units, "behavior:beta")

	lease, err := GrantLease(graph, nil, units, alpha.ID, "worker-1")
	if err != nil {
		t.Fatalf("first grant = %v", err)
	}
	if _, err := GrantLease(graph, []Lease{lease}, units, beta.ID, "worker-2"); err == nil || !strings.Contains(err.Error(), "semantic boundary wire:shapeX") {
		t.Fatalf("shared-boundary grant error = %v, want semantic boundary conflict", err)
	}
}

func TestGrantLeaseAdmitsFullyDisjointUnitsConcurrently(t *testing.T) {
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:shapeX", "ai/openai.go")...)
	records = append(records, behaviorBundle(snapshot, "beta", "wire:shapeY", "ai/anthropic.go")...)
	graph, units := planUnits(t, records)
	alpha := unitFor(t, units, "behavior:alpha")
	beta := unitFor(t, units, "behavior:beta")

	lease, err := GrantLease(graph, nil, units, alpha.ID, "worker-1")
	if err != nil {
		t.Fatalf("first grant = %v", err)
	}
	if _, err := GrantLease(graph, []Lease{lease}, units, beta.ID, "worker-2"); err != nil {
		t.Fatalf("disjoint grant = %v, want success", err)
	}
}

func TestGrantLeaseRejectsRepeatLeaseOfSameUnit(t *testing.T) {
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:shapeX", "ai/openai.go")...)
	graph, units := planUnits(t, records)
	alpha := unitFor(t, units, "behavior:alpha")

	lease, err := GrantLease(graph, nil, units, alpha.ID, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GrantLease(graph, []Lease{lease}, units, alpha.ID, "worker-2"); err == nil || !strings.Contains(err.Error(), "already leased") {
		t.Fatalf("repeat grant error = %v, want already leased", err)
	}
}

func TestWorkUnitBaseFingerprintIsStableAndSupportSensitive(t *testing.T) {
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:shapeX", "ai/openai.go")...)
	graph, units := planUnits(t, records)
	alpha := unitFor(t, units, "behavior:alpha")

	first, err := WorkUnitBaseFingerprint(graph, alpha)
	if err != nil {
		t.Fatal(err)
	}
	second, err := WorkUnitBaseFingerprint(graph, alpha)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("fingerprint not stable: %s vs %s", first, second)
	}
	// Mutating the obligation's support must move the fingerprint, so a lease
	// built on the old support is detectably stale at integration.
	verdict := graph.Verdicts["obligation:alpha"]
	verdict.SupportHashes = append(slices.Clone(verdict.SupportHashes), HashBytes([]byte("new-support")))
	graph.Verdicts["obligation:alpha"] = verdict
	moved, err := WorkUnitBaseFingerprint(graph, alpha)
	if err != nil {
		t.Fatal(err)
	}
	if moved == first {
		t.Fatal("fingerprint did not change when support changed")
	}
}

func translatorPatch(production, test []string) *correspondence.TranslatorBundle {
	edits := func(paths []string) []correspondence.TranslationEdit {
		out := make([]correspondence.TranslationEdit, 0, len(paths))
		for _, path := range paths {
			out = append(out, correspondence.TranslationEdit{Path: path, OriginalHash: HashBytes([]byte(path)), Replacement: "x"})
		}
		return out
	}
	return &correspondence.TranslatorBundle{
		Role: correspondence.TranslatorRole, PacketID: "packet:x",
		Translations: []correspondence.TranslationProposal{{
			QuestionID: "q", ProductionEdits: edits(production), TestEdits: edits(test),
		}},
	}
}

func leaseUnitWithTests() WorkUnit {
	return WorkUnit{
		ID: "work-unit:alpha", BehaviorID: "behavior:alpha",
		WritePaths: []string{"ai/openai.go"}, TestPaths: []string{"ai/openai_test.go"}, FixturePaths: []string{"ai/testdata/x.json"},
	}
}

func TestConfinePatchToLeaseAcceptsInBoundsEdits(t *testing.T) {
	unit := leaseUnitWithTests()
	lease := Lease{ID: "lease:x", WorkUnitID: unit.ID}
	bundle := translatorPatch([]string{"ai/openai.go"}, []string{"ai/openai_test.go", "ai/testdata/x.json"})
	if err := ConfinePatchToLease(lease, unit, bundle); err != nil {
		t.Fatalf("in-bounds patch rejected: %v", err)
	}
}

func TestConfinePatchToLeaseRejectsProductionEditOutsideWriteSet(t *testing.T) {
	unit := leaseUnitWithTests()
	lease := Lease{ID: "lease:x", WorkUnitID: unit.ID}
	bundle := translatorPatch([]string{"ai/anthropic.go"}, []string{"ai/openai_test.go"})
	if err := ConfinePatchToLease(lease, unit, bundle); err == nil || !strings.Contains(err.Error(), "ai/anthropic.go is outside") {
		t.Fatalf("out-of-bounds production edit error = %v", err)
	}
}

func TestConfinePatchToLeaseRejectsTestEditOutsideTestSet(t *testing.T) {
	unit := leaseUnitWithTests()
	lease := Lease{ID: "lease:x", WorkUnitID: unit.ID}
	bundle := translatorPatch([]string{"ai/openai.go"}, []string{"ai/other_test.go"})
	if err := ConfinePatchToLease(lease, unit, bundle); err == nil || !strings.Contains(err.Error(), "ai/other_test.go is outside") {
		t.Fatalf("out-of-bounds test edit error = %v", err)
	}
}

func TestConfinePatchToLeaseRejectsLeaseUnitMismatch(t *testing.T) {
	unit := leaseUnitWithTests()
	lease := Lease{ID: "lease:x", WorkUnitID: "work-unit:beta"}
	bundle := translatorPatch([]string{"ai/openai.go"}, []string{"ai/openai_test.go"})
	if err := ConfinePatchToLease(lease, unit, bundle); err == nil || !strings.Contains(err.Error(), "not work-unit:alpha") {
		t.Fatalf("lease/unit mismatch error = %v", err)
	}
}

func TestIntegrateLeaseRejectsStaleBase(t *testing.T) {
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:shapeX", "ai/openai.go")...)
	graph, units := planUnits(t, records)
	alpha := unitFor(t, units, "behavior:alpha")

	lease, err := GrantLease(graph, nil, units, alpha.ID, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	// A fresh graph with the same records integrates fine.
	if err := IntegrateLease(graph, lease, units); err != nil {
		t.Fatalf("integrate on matching base = %v", err)
	}
	// Advance canonical support (as if an earlier lease integrated), moving the
	// base; the lease is now stale and must not integrate.
	verdict := graph.Verdicts["obligation:alpha"]
	verdict.SupportHashes = append(slices.Clone(verdict.SupportHashes), HashBytes([]byte("advanced")))
	graph.Verdicts["obligation:alpha"] = verdict
	if err := IntegrateLease(graph, lease, units); err == nil || !strings.Contains(err.Error(), "base is stale") {
		t.Fatalf("stale integrate error = %v, want stale base", err)
	}
}

func TestDeltaBriefsNotifyExactlyAffectedLeases(t *testing.T) {
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:shapeX", "ai/openai.go")...)
	records = append(records, behaviorBundle(snapshot, "beta", "wire:shapeY", "ai/anthropic.go")...)
	before, units := planUnits(t, records)
	alpha := unitFor(t, units, "behavior:alpha")
	beta := unitFor(t, units, "behavior:beta")
	leaseA, err := GrantLease(before, nil, units, alpha.ID, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	leaseB, err := GrantLease(before, []Lease{leaseA}, units, beta.ID, "worker-2")
	if err != nil {
		t.Fatal(err)
	}
	// Rebuild an "after" graph and move only alpha's support.
	after, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	moved := after.Verdicts["obligation:alpha"]
	moved.SupportHashes = append(slices.Clone(moved.SupportHashes), HashBytes([]byte("alpha-moved")))
	after.Verdicts["obligation:alpha"] = moved

	briefs, err := DeltaBriefs(before, after, []Lease{leaseA, leaseB}, units)
	if err != nil {
		t.Fatal(err)
	}
	if len(briefs) != 1 || briefs[0].WorkUnitID != alpha.ID {
		t.Fatalf("delta briefs = %#v, want exactly alpha", briefs)
	}
	if briefs[0].OldFingerprint == briefs[0].NewFingerprint {
		t.Fatal("brief did not record a moved fingerprint")
	}
}
