package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// TestMappingRoundTripIsByteIdentical keeps sync from churning the ledger: reading and writing the committed mapping changes nothing.
func TestMappingRoundTripIsByteIdentical(t *testing.T) {
	path := filepath.Join("..", "..", "..", "interfaces", "mapping-v"+pigversion.UpstreamVersion+".json")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Skip("mapping not present")
	}
	doc, err := readMapping(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := encodeMapping(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip changed the mapping (%d bytes before, %d after)", len(want), len(got))
	}
}

// A held row stays pending with its recorded reason although every shape rule passes, and a hold for an unknown ID fails.
func TestHoldKeepsAPassingRowPendingWithItsReason(t *testing.T) {
	passing := &decision{ID: "pkg:x/.#f", Sym: &sym{}, Info: &exerciseInfo{Call: "x/f.go#F"}}
	if err := applyHolds([]*decision{passing}, map[string]string{"pkg:x/.#f": "Pi throws here"}); err != nil {
		t.Fatal(err)
	}
	if !passing.Gap || passing.Reason != reasonHeld || passing.Sym != nil {
		t.Fatalf("held decision = %+v, want a held gap", passing)
	}
	doc := &mappingDoc{Mappings: []orderedRow{{{"id", jsonValue("pkg:x/.#f")}, {"disposition", jsonValue("ported")}, {"upstreamShapeHash", jsonValue("sha256:0")}}}}
	st := syncMapping(&ledger{}, doc, map[string]*decision{"pkg:x/.#f": passing})
	row := doc.Mappings[0]
	if row.get("disposition") != "pending" || row.get("rationale") != heldPrefix+"Pi throws here" || st.Reopened != 1 {
		t.Fatalf("synced row = %s %q (stats %+v), want pending with the hold reason", row.get("disposition"), row.get("rationale"), st)
	}
	failing := &decision{ID: "pkg:x/.#g", Gap: true, Reason: "undecidable"}
	if err := applyHolds([]*decision{failing}, map[string]string{"pkg:x/.#g": "Pi throws here too"}); err != nil || failing.Reason != "undecidable" || failing.Held == "" {
		t.Fatalf("held gap = %+v (err %v), want its own reason kept and the hold recorded", failing, err)
	}
	if err := applyHolds([]*decision{passing}, map[string]string{"pkg:x/.#missing": "reason"}); err == nil {
		t.Fatal("a hold for an unknown ID was accepted")
	}
	if err := applyHolds([]*decision{passing}, map[string]string{"pkg:x/.#f": " "}); err == nil {
		t.Fatal("a hold without a reason was accepted")
	}
}

// A held member keeps its declaration and the declarations above it open, so the ledger never reports a ported owner with a pending
// child; a designed-out ancestor stays designed out.
func TestHoldReopensTheRowsAboveIt(t *testing.T) {
	owner := &decision{ID: "pkg:x/.#T", Sym: &sym{}, Info: &exerciseInfo{Call: "x/t.go#T"}}
	prop := &decision{ID: "pkg:x/.#T::property:p", Sym: &sym{}, Info: &exerciseInfo{Call: "x/t.go#T.P"}}
	call := &decision{ID: "pkg:x/.#T::property:p::call:0", Sym: &sym{}, Info: &exerciseInfo{Call: "x/t.go#T.P"}}
	sibling := &decision{ID: "pkg:x/.#T::property:q", Sym: &sym{}, Info: &exerciseInfo{Call: "x/t.go#T.Q"}}
	if err := applyHolds([]*decision{owner, prop, call, sibling}, map[string]string{call.ID: "reviewed"}); err != nil {
		t.Fatal(err)
	}
	if call.Reason != reasonHeld || prop.Reason != reasonChild || owner.Reason != reasonChild || owner.Sym != nil {
		t.Fatalf("call %+v, property %+v, owner %+v: want the held call and child gaps above it", call, prop, owner)
	}
	if sibling.Gap {
		t.Fatalf("a sibling of the held row was reopened: %+v", sibling)
	}
	out := &decision{ID: "pkg:x/.#U", Evidence: ruleDesignedOut}
	child := &decision{ID: "pkg:x/.#U::property:p", Sym: &sym{}, Info: &exerciseInfo{Call: "x/u.go#U.P"}}
	if err := applyHolds([]*decision{out, child}, map[string]string{child.ID: "reviewed"}); err != nil || out.Gap {
		t.Fatalf("designed-out owner = %+v (err %v), want it kept", out, err)
	}
}

func TestHandRowIsKeptOnlyWhileItsReferencesResolve(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package lib\n\ntype Box[T any] struct{ Name string }\n\nfunc (b *Box[T]) Open() {}\n\nfunc Make() {}\n\nconst Limit = 3\n"
	if err := os.WriteFile(filepath.Join(root, "lib/lib.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib/parity_harness.go"), []byte("package lib\n\nfunc probeMake() { Make() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &ledger{root: root}
	row := func(targets, production string) orderedRow {
		return orderedRow{{"pigTargets", json.RawMessage(targets)}, {"production", json.RawMessage(production)}}
	}
	for _, ok := range []orderedRow{
		row(`["lib/lib.go#Box","lib/lib.go#Box.Open","lib/lib.go#Box.Name","lib/lib.go#Make","lib/lib.go#Limit"]`, `["call:lib/lib.go#Make","tested:lib/lib.go#Box.Open"]`),
		row(`["docs/x.md#heading"]`, `[]`),
	} {
		if !l.referencesResolve(ok) {
			t.Errorf("references that resolve must keep the hand row: %s", ok.get("pigTargets"))
		}
	}
	for _, bad := range []orderedRow{
		row(`["lib/lib.go#Removed"]`, `[]`),
		row(`["lib/lib.go#Box"]`, `["call:lib/lib.go#customBody.Render"]`),
		row(`["lib/lib.go#Box.Missing"]`, `[]`),
		row(`["lib/gone.go#Make"]`, `[]`),
		row(`["lib/lib.go#Make"]`, `["call:lib/parity_harness.go#probeMake"]`),
	} {
		if l.referencesResolve(bad) {
			t.Errorf("a reference to a removed declaration must not keep the hand row: %v %v", bad.list("pigTargets"), bad.list("production"))
		}
	}
}

// A row that cites behavior contracts keeps them whether the sync derives it as ported or leaves it pending: rule B1 reads them and the
// validator requires them on a handleInput row that claims behavior complete.
func TestSyncKeepsBehaviorContracts(t *testing.T) {
	contracts := jsonValue([]string{"tui/x/cancel-input"})
	prev := func(id string) orderedRow {
		return orderedRow{{"id", jsonValue(id)}, {"disposition", jsonValue("pending")}, {"upstreamShapeHash", jsonValue("sha256:0")}, {"behaviorContracts", contracts}}
	}
	doc := &mappingDoc{Mappings: []orderedRow{prev("pkg:x/.#A::property:handleInput"), prev("pkg:x/.#B::property:handleInput")}}
	ds := map[string]*decision{
		"pkg:x/.#A::property:handleInput": {ID: "pkg:x/.#A::property:handleInput", Sym: &sym{}, Info: &exerciseInfo{Call: "x/a.go#A.HandleInput"}},
		"pkg:x/.#B::property:handleInput": {ID: "pkg:x/.#B::property:handleInput", Gap: true, Reason: "undecidable"},
	}
	syncMapping(&ledger{}, doc, ds)
	for i, want := range []string{"ported", "pending"} {
		row := doc.Mappings[i]
		if row.get("disposition") != want || len(row.list("behaviorContracts")) != 1 {
			t.Errorf("row %d = %s with contracts %v, want %s citing its contract", i, row.get("disposition"), row.list("behaviorContracts"), want)
		}
	}
}

// A handleInput call row cites its property row's contracts: the validator requires one on every handleInput row that claims behavior
// complete, and the call row is the property's one signature.
func TestSyncGivesAHandleInputCallRowItsPropertysContracts(t *testing.T) {
	property, call := "pkg:x/.#A::property:handleInput", "pkg:x/.#A::property:handleInput::call:0"
	row := func(id string, contracts ...string) orderedRow {
		r := orderedRow{{"id", jsonValue(id)}, {"disposition", jsonValue("pending")}, {"upstreamShapeHash", jsonValue("sha256:0")}}
		if len(contracts) > 0 {
			r = append(r, kv{"behaviorContracts", jsonValue(contracts)})
		}
		return r
	}
	doc := &mappingDoc{Mappings: []orderedRow{row(property, "tui/x/cancel-input"), row(call)}}
	ds := map[string]*decision{
		property: {ID: property, Sym: &sym{}, Info: &exerciseInfo{Call: "x/a.go#A.HandleInput"}},
		call:     {ID: call, Sym: &sym{}, Info: &exerciseInfo{Call: "x/a.go#A.HandleInput"}},
	}
	syncMapping(&ledger{}, doc, ds)
	if got := doc.Mappings[1].list("behaviorContracts"); len(got) != 1 || got[0] != "tui/x/cancel-input" {
		t.Fatalf("call row contracts = %v, want its property's", got)
	}
	other := &mappingDoc{Mappings: []orderedRow{row("pkg:x/.#B::property:render", "tui/x/render"), row("pkg:x/.#B::property:render::call:0")}}
	syncMapping(&ledger{}, other, map[string]*decision{
		"pkg:x/.#B::property:render":         {ID: "pkg:x/.#B::property:render", Sym: &sym{}, Info: &exerciseInfo{Call: "x/b.go#B.Render"}},
		"pkg:x/.#B::property:render::call:0": {ID: "pkg:x/.#B::property:render::call:0", Sym: &sym{}, Info: &exerciseInfo{Call: "x/b.go#B.Render"}},
	})
	if got := other.Mappings[1].list("behaviorContracts"); len(got) != 0 {
		t.Fatalf("a non-handleInput call row inherited contracts: %v", got)
	}
}

// B1: a handleInput row closes only when every contract it cites is ported or a divergence in behavior-contracts.toml; a pending or
// unknown contract keeps it a gap, because the validator rejects behavior complete over it.
func TestBehaviorContractMustBePortedToCloseHandleInput(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "test/parity"), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "[[contract]]\nid = \"a/done\"\nstatus = \"ported\"\n\n[[contract]]\nid = \"a/div\"\nstatus = \"divergence\"\n\n[[contract]]\nid = \"a/wait\"\nstatus = \"pending\"\n"
	if err := os.WriteFile(filepath.Join(root, "test/parity/behavior-contracts.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &ledger{root: root, mapping: map[string]*mappingRow{}}
	d := &detector{l: l}
	for id, tc := range map[string]struct {
		cites []string
		want  bool
	}{
		"none": {nil, false}, "ported": {[]string{"a/done"}, true}, "both": {[]string{"a/done", "a/div"}, true},
		"pending": {[]string{"a/wait"}, false}, "mixed": {[]string{"a/done", "a/wait"}, false}, "unknown": {[]string{"a/zzz"}, false},
	} {
		l.mapping[id] = &mappingRow{BehaviorContracts: tc.cites}
		if got := d.hasBehaviorContract(id); got != tc.want {
			t.Errorf("%s: hasBehaviorContract(%v) = %v, want %v", id, tc.cites, got, tc.want)
		}
	}
}

// A function in parity_harness.go is not a production caller: its probes run only under PIG_PARITY_HARNESS=1, and the validator
// rejects them as production references.
func TestReachSetDropsParityHarnessProbes(t *testing.T) {
	got := withoutHarnessProbes(map[string]bool{"internal/codingagent/parity_harness.go#InteractiveMode.probeX": true, "internal/codingagent/mode.go#InteractiveMode.Run": true})
	if len(got) != 1 || !got["internal/codingagent/mode.go#InteractiveMode.Run"] {
		t.Fatalf("reach after dropping probes = %v", got)
	}
	if withoutHarnessProbes(nil) != nil {
		t.Fatal("a nil reach set stays nil: the caller did not provide one")
	}
}

// A reviewed gap gives an undecided row its reviewed reason; an entry without a reason, for an unknown row, or for a row a rule
// decides fails.
func TestReviewedGapDecidesAnUndecidedRow(t *testing.T) {
	und := func() *decision {
		return &decision{ID: "pkg:x/.#u", Gap: true, Reason: reasonUndecided, Detail: "T9: ..."}
	}
	d := und()
	if stale, err := applyReviewedGaps([]*decision{d}, map[string]string{"pkg:x/.#u": "port-needed (shard 1, Pi x.ts:1)"}); err != nil || len(stale) != 0 {
		t.Fatalf("stale = %v, err = %v", stale, err)
	}
	if !d.Gap || d.Reason != reasonReviewedGap || d.Detail != "port-needed (shard 1, Pi x.ts:1)" {
		t.Fatalf("reviewed decision = %+v", d)
	}
	for name, tc := range map[string]struct {
		d    *decision
		gaps map[string]string
		want string
	}{
		"no reason": {und(), map[string]string{"pkg:x/.#u": " "}, "has no reason"},
	} {
		if _, err := applyReviewedGaps([]*decision{tc.d}, tc.gaps); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
	for name, tc := range map[string]struct {
		d    *decision
		gaps map[string]string
	}{
		"unknown row":  {und(), map[string]string{"pkg:x/.#gone": "r"}},
		"rule decides": {&decision{ID: "pkg:x/.#u", Gap: true, Reason: reasonType}, map[string]string{"pkg:x/.#u": "r"}},
		"closed row":   {&decision{ID: "pkg:x/.#u"}, map[string]string{"pkg:x/.#u": "r"}},
	} {
		stale, err := applyReviewedGaps([]*decision{tc.d}, tc.gaps)
		if err != nil || len(stale) != 1 {
			t.Errorf("%s: stale = %v, err = %v; want one stale entry", name, stale, err)
		}
	}
}

// Every stale entry is returned together and an update prunes them from the file; the others survive byte for byte.
func TestStaleReviewedGapsArePrunedTogether(t *testing.T) {
	gaps := map[string]string{"pkg:x/.#a": "keep & <this>", "pkg:x/.#b": "stale", "pkg:x/.#c": "stale too"}
	ds := []*decision{{ID: "pkg:x/.#a", Gap: true, Reason: reasonUndecided}, {ID: "pkg:x/.#b"}}
	stale, err := applyReviewedGaps(ds, gaps)
	if err != nil || !slices.Equal(stale, []string{"pkg:x/.#b", "pkg:x/.#c"}) {
		t.Fatalf("stale = %v, err = %v", stale, err)
	}
	path := filepath.Join(t.TempDir(), "reviewed-gaps.json")
	if err := pruneReviewedGaps(path, gaps, stale); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n \"pkg:x/.#a\": \"keep & <this>\"\n}\n"; string(got) != want {
		t.Fatalf("pruned file = %q, want %q", got, want)
	}
}

// A row that cites state-transition contracts keeps them through a sync, and a pending contract holds the handleInput row pending.
func TestSyncKeepsBehaviorContractsAndHoldsWhileTheyArePending(t *testing.T) {
	root := t.TempDir()
	toml := "[[contract]]\nid = \"a/open\"\nstatus = \"pending\"\n\n[[contract]]\nid = \"a/done\"\nstatus = \"ported\"\n"
	if err := os.MkdirAll(filepath.Join(root, "test/parity"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "test/parity/behavior-contracts.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &ledger{root: root, mapping: map[string]*mappingRow{
		"pkg:x/.#C::property:handleInput": {BehaviorContracts: []string{"a/open"}},
		"pkg:x/.#D::property:handleInput": {BehaviorContracts: []string{"a/done"}},
	}}
	d := &detector{l: l}
	if d.hasBehaviorContract("pkg:x/.#C::property:handleInput") {
		t.Fatal("a pending contract closed the handleInput behavior layer")
	}
	if !d.hasBehaviorContract("pkg:x/.#D::property:handleInput") {
		t.Fatal("a ported contract did not satisfy the handleInput behavior layer")
	}
	cited := func(id, contract string) orderedRow {
		return orderedRow{{"id", jsonValue(id)}, {"disposition", jsonValue("pending")}, {"upstreamShapeHash", jsonValue("sha256:0")}, {"behaviorContracts", jsonValue([]string{contract})}}
	}
	doc := &mappingDoc{Mappings: []orderedRow{cited("pkg:x/.#C::property:handleInput", "a/open")}}
	syncMapping(l, doc, map[string]*decision{"pkg:x/.#C::property:handleInput": {ID: "pkg:x/.#C::property:handleInput", Gap: true, Reason: reasonUndecided}})
	if got := doc.Mappings[0].list("behaviorContracts"); len(got) != 1 || got[0] != "a/open" || doc.Mappings[0].get("disposition") != "pending" {
		t.Fatalf("synced row = %s with contracts %v, want pending with a/open kept", doc.Mappings[0].get("disposition"), got)
	}
}

// Every reviewed type no row uses is listed together, the check names all of them, and an update rewrites the file without them.
func TestStaleReviewedTypesArePrunedTogether(t *testing.T) {
	d := &detector{
		reviewed:     map[string]reviewedType{"pkg:x/.#a": {Go: "x.go#A", Reason: "keep"}, "pkg:x/.#b": {Go: "x.go#B", Reason: "old"}, "pkg:x/.#c": {Go: "x.go#C", Reason: "older"}},
		reviewedUsed: map[string]bool{"pkg:x/.#a": true},
	}
	stale := d.staleReviewed()
	if !slices.Equal(stale, []string{"pkg:x/.#b", "pkg:x/.#c"}) {
		t.Fatalf("stale = %v", stale)
	}
	if err := d.checkReviewed(); err == nil || !strings.Contains(err.Error(), "#b is stale") || !strings.Contains(err.Error(), "#c is stale") {
		t.Fatalf("check = %v, want both stale entries named", err)
	}
	path := filepath.Join(t.TempDir(), "reviewed-types.json")
	if err := pruneReviewedTypes(path, d.reviewed, stale); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"pkg:x/.#a\": {\n    \"go\": \"x.go#A\",\n    \"reason\": \"keep\"\n  }\n}\n"; string(got) != want {
		t.Fatalf("pruned file = %q, want %q", got, want)
	}
	if err := d.checkReviewed(); err != nil {
		t.Fatalf("check after pruning = %v", err)
	}
}

// An ExtensionAPI.on overload that a test exercises still needs the eleven extension API layers: until a hand ported row carries them it
// counts as open, so its owner is a child-gap and not a derived ported row that the parent-closure check rejects. Once the hand row
// exists the overload is closed, so its owner closes with it. Any other overload follows its own decision.
func TestExtensionAPIOnOverloadStaysOpenWhileUnderivable(t *testing.T) {
	const agentEnd, agentStart = "pkg:coding-agent/.#ExtensionAPI::property:on::call:agent_end", "pkg:coding-agent/.#ExtensionAPI::property:on::call:agent_start"
	d := &detector{l: &ledger{mapping: map[string]*mappingRow{
		agentEnd:   {ID: agentEnd, Disposition: "ported", Rationale: "Reviewed hand row."},
		agentStart: {ID: agentStart, Disposition: "ported", Rationale: derivedPrefix + ": stale"},
	}}}
	exercised := &decision{Candidate: "coding/extension/api.go#API.OnAgentEnd"}
	if d.openOverload(agentEnd, exercised) {
		t.Fatal("an ExtensionAPI.on overload with a hand ported row kept its owner open")
	}
	if !d.openOverload(agentStart, exercised) {
		t.Fatal("an exercised ExtensionAPI.on overload without a hand row closed its owner")
	}
	if !d.openOverload(agentEnd, &decision{Gap: true}) {
		t.Fatal("a gap overload with a hand row did not count as open")
	}
	if d.openOverload("pkg:x/.#Box::property:on::call:agent_end", exercised) {
		t.Fatal("an unrelated on overload counted as open")
	}
	if !d.openOverload("pkg:x/.#Box::property:on::call:agent_end", &decision{Gap: true}) {
		t.Fatal("a gap overload did not count as open")
	}
}
