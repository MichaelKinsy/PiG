package correspondence

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/test/parity/knowngaps"
)

func TestCompactionSettingsCorrespondenceCurrentPin(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	source, err := ExtractTypeScript(
		context.Background(), nodePath,
		filepath.Join(root, "test/parity/interface-extractor/src/extract-correspondence.mjs"),
		filepath.Join(root, ".upstream/current"), coding.UpstreamVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	commit := repositorySnapshotCommit(t, root)
	target, err := ExtractGo(context.Background(), root, commit)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(source, target, CompactionSettingsRules())
	if err != nil {
		t.Fatal(err)
	}
	rules := CompactionSettingsRules()
	wantMappings := len(source.Constants) + len(rules.CallContracts)
	for _, table := range source.Tables {
		wantMappings += len(table.Items) + len(rules.AdditiveTableItems[rules.TableTargets[table.ID]])
	}
	// Every finding must be a listed known gap, and every listed gap must still
	// be observed; test/parity/known-gaps.toml is the denominator.
	ledger, err := knowngaps.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	known := ledger.Scope("correspondence")
	// A Pi table item the Pig table lacks yields a missing-table-item finding
	// and no mapping, so each listed one leaves the mapping denominator.
	for key := range known {
		if strings.HasPrefix(key, "finding:table-item:missing:") {
			wantMappings--
		}
	}
	if len(report.Mappings) != wantMappings {
		t.Fatalf("mapping count = %d, want %d from Pi inventory and explicit additive lineage, less the listed missing table items", len(report.Mappings), wantMappings)
	}
	observed := make(map[string]struct{}, len(report.Findings))
	for _, finding := range report.Findings {
		observed[finding.ID] = struct{}{}
		if _, ok := known[finding.ID]; !ok {
			t.Errorf("unlisted finding %s: %s", finding.ID, finding.Detail)
		}
	}
	if stale := ledger.Stale("correspondence", observed); len(stale) > 0 {
		t.Errorf("known-gaps.toml lists closed correspondence gaps: %v", stale)
	}
}

func TestCompareDetectsOmittedAndReorderedSettingsAndPromptDrift(t *testing.T) {
	source, target := comparisonFixtures()
	target.Tables[0].Items = target.Tables[0].Items[1:]
	target.Tables[0].Items[0], target.Tables[0].Items[1] = target.Tables[0].Items[1], target.Tables[0].Items[0]
	target.Constants[0].ValueHash = hashString("changed")
	source.Constants = append(source.Constants, Constant{
		ID: "constant:pi#NEW_PROMPT", Path: "compaction", Name: "NEW_PROMPT", Value: "new", UTF16Length: 3,
		SourceHash: source.Constants[0].SourceHash, ValueHash: hashString("new"), StartLine: 2, EndLine: 2,
	})
	report, err := Compare(source, target, CompactionSettingsRules())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"finding:table-item:missing:autocompact":       false,
		"finding:table-order:table:settings-selector":  false,
		"finding:constant-value:SUMMARIZATION_PROMPT":  false,
		"finding:constant:unclaimed-source:NEW_PROMPT": false,
	}
	for _, finding := range report.Findings {
		if _, ok := want[finding.ID]; ok {
			want[finding.ID] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("missing expected finding %s: %#v", id, report.Findings)
		}
	}
}

func TestCompareDetectsCompactionOrderBranchAndCancellationDrift(t *testing.T) {
	source, target := comparisonFixtures()
	source.Functions[0].Calls = []FunctionCall{
		{Ordinal: 1, Callee: "generateSummaryWithUsage", Awaited: false, Arguments: []string{"signal"}, Conditions: []string{"split", "history"}, StartLine: 1},
		{Ordinal: 2, Callee: "generateTurnPrefixSummary", Awaited: true, Arguments: []string{"signal"}, Conditions: []string{"split"}, StartLine: 2},
		{Ordinal: 3, Callee: "combineUsage", Awaited: false, Arguments: []string{}, Conditions: []string{"split"}, StartLine: 3},
	}
	target.Functions[0].Calls = []FunctionCall{
		{Ordinal: 1, Callee: "generateTurnPrefixSummary", Awaited: false, Arguments: []string{"ctx"}, Conditions: []string{"split", "history"}, StartLine: 1},
		{Ordinal: 2, Callee: "generateSummary", Awaited: false, Arguments: []string{"ctx"}, Conditions: []string{}, StartLine: 2},
		{Ordinal: 3, Callee: "combineUsage", Awaited: false, Arguments: []string{}, Conditions: []string{"split"}, StartLine: 3},
	}
	rules := fixtureRules()
	rules.CalleeTargets = map[string]string{
		"generateSummaryWithUsage": "generateSummary", "generateTurnPrefixSummary": "generateTurnPrefixSummary", "combineUsage": "combineUsage",
	}
	rules.CancellableCalls = []string{"generateSummaryWithUsage", "generateTurnPrefixSummary"}
	report, err := Compare(source, target, rules)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"finding:function-order:compact":                 false,
		"finding:function-branches:compact":              false,
		"finding:function-cancellation-source:compact:1": false,
	}
	for _, finding := range report.Findings {
		if _, exists := want[finding.ID]; exists {
			want[finding.ID] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("missing expected finding %s: %#v", id, report.Findings)
		}
	}
}

func TestCompareDetectsCompactionTransitionDrift(t *testing.T) {
	source, target := comparisonFixtures()
	source.Functions[0].Transitions = []FunctionTransition{{Ordinal: 1, Kind: "error", Expression: "new Error(\"missing\")", Conditions: []string{"!first"}, StartLine: 2}}
	target.Functions[0].Transitions = []FunctionTransition{{Ordinal: 1, Kind: "return", Expression: "errors.New(\"missing\")", Conditions: []string{"first == empty"}, StartLine: 2}}
	rules := fixtureRules()
	rules.TransitionContracts = []TransitionContract{{
		ID: "missing", Function: "compact", SourceKind: "error", SourceContains: "missing",
		TargetKind: "return", TargetContains: "missing", Condition: "1:then",
	}}
	report, err := Compare(source, target, rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("baseline findings = %#v", report.Findings)
	}
	target.Functions[0].Transitions = []FunctionTransition{}
	report, err = Compare(source, target, rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 || report.Findings[0].ID != "finding:transition:missing" {
		t.Fatalf("transition findings = %#v", report.Findings)
	}
}

func comparisonFixtures() (*Inventory, *Inventory) {
	const hash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	items := []DataItem{
		{ID: "autocompact", Label: "Auto", Description: "Auto", CurrentValueExpression: "auto", Values: []string{"true", "false"}, Path: "settings", StartLine: 1, EndLine: 1, SourceHash: hash},
		{ID: "theme", Label: "Theme", Description: "Theme", CurrentValueExpression: "theme", Values: []string{"dark", "light"}, Path: "settings", StartLine: 2, EndLine: 2, SourceHash: hash},
		{ID: "transport", Label: "Transport", Description: "Transport", CurrentValueExpression: "transport", Values: []string{"auto"}, Path: "settings", StartLine: 3, EndLine: 3, SourceHash: hash},
	}
	source := &Inventory{
		Source:    SourceIdentity{Language: LanguageTypeScript, Revision: "0.84.0"},
		Tables:    []DataTable{{ID: "table:settings-selector", Path: "settings", Owner: "Selector", SourceHash: hash, OrderProfile: "all-capabilities", Items: items, Callbacks: []DispatchCase{}}},
		Constants: []Constant{{ID: "constant:pi#SUMMARIZATION_PROMPT", Path: "compaction", Name: "SUMMARIZATION_PROMPT", Value: "same", UTF16Length: 4, SourceHash: hash, ValueHash: hashString("same"), StartLine: 1, EndLine: 1}},
		Functions: []Function{
			{ID: "function:pi#compact", Path: "compaction", Name: "compact", Kind: "compaction", Async: true, CancellationInputs: []string{"signal"}, Calls: []FunctionCall{}, Transitions: []FunctionTransition{}, StartLine: 1, EndLine: 3, SourceHash: hash},
			{ID: "function:pi#generateTurnPrefixSummary", Path: "compaction", Name: "generateTurnPrefixSummary", Kind: "compaction", Async: true, CancellationInputs: []string{"signal"}, Calls: []FunctionCall{}, Transitions: []FunctionTransition{}, StartLine: 4, EndLine: 6, SourceHash: hash},
		},
	}
	target := &Inventory{
		Source:    SourceIdentity{Language: LanguageGo, Revision: "commit"},
		Tables:    []DataTable{{ID: "table:settings-selector", Path: "settings", Owner: "settingsItems", SourceHash: hash, OrderProfile: "all-capabilities", Items: append([]DataItem(nil), items...), Callbacks: []DispatchCase{}}},
		Constants: []Constant{{ID: "constant:go#SUMMARIZATION_PROMPT", Path: "compaction", Name: "SUMMARIZATION_PROMPT", Value: "same", UTF16Length: 4, SourceHash: hash, ValueHash: hashString("same"), StartLine: 1, EndLine: 1}},
		Functions: []Function{
			{ID: "function:go#Compact", Path: "compaction", Name: "compact", Kind: "compaction", Async: false, CancellationInputs: []string{"ctx"}, Calls: []FunctionCall{}, Transitions: []FunctionTransition{}, StartLine: 1, EndLine: 3, SourceHash: hash},
			{ID: "function:go#generateTurnPrefixSummary", Path: "compaction", Name: "generateTurnPrefixSummary", Kind: "compaction", Async: false, CancellationInputs: []string{"ctx"}, Calls: []FunctionCall{}, Transitions: []FunctionTransition{}, StartLine: 4, EndLine: 6, SourceHash: hash},
		},
	}
	return source, target
}
