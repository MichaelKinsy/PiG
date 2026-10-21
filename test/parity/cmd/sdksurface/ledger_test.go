package main

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExportLedgerIsCurrent is the standing guard on the real repository: the
// ledger, its readable form and the generator agree, no export is unlisted or
// unclassified, every realized cell is reached, and no lane is stale.
func TestExportLedgerIsCurrent(t *testing.T) {
	in := realLedgerInput(t)
	if problems := checkLedger(in); len(problems) > 0 {
		t.Fatalf("%d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	if problems := checkVectors(repoRoot, in.Rows, currentPin()); len(problems) > 0 {
		t.Fatalf("vectors:\n  %s", strings.Join(problems, "\n  "))
	}
	typeOnly, err := typeOnlyCounts(repoRoot, in.Rows)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		path string
		want []byte
	}{{ledgerPath, regenerateLedger(in)}, {exportsDocPath, renderExportsDoc(in, typeOnly)}} {
		got, err := os.ReadFile(filepath.Join(repoRoot, f.path))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, f.want) {
			t.Errorf("%s is stale: run `go run ./test/parity/cmd/sdksurface`", f.path)
		}
	}
}

func realLedgerInput(t *testing.T) ledgerInput {
	t.Helper()
	m, err := buildMatrix(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	corpora, err := loadCorpora(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	in, err := loadLedgerInput(repoRoot, m, corpora, false)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// TestExportLedgerCoversTheServedModules pins the denominator, so a probe or
// inventory regression that drops rows cannot make the ledger vacuously green.
func TestExportLedgerCoversTheServedModules(t *testing.T) {
	in := realLedgerInput(t)
	keys := map[string]bool{}
	byModule := map[string]int{}
	for _, r := range in.Rows {
		keys[r.Key] = true
		byModule[r.Module]++
	}
	for _, m := range ledgerModules {
		if byModule[m] < 5 {
			t.Errorf("ledger has %d rows for %s", byModule[m], m)
		}
	}
	for _, want := range []string{
		"pi-coding-agent: createAgentSession", "pi-coding-agent: truncateHead", "pi-coding-agent: SessionManager",
		"pi-coding-agent: SessionManager.getBranch", "pi-tui: truncateToWidth", "pi-tui: matchesKey", "pi-tui: Text",
		"pi-ai: StringEnum", "pi-ai: Type", "pi-ai/compat: complete", "pi-agent-core: Agent",
	} {
		if !keys[want] {
			t.Errorf("denominator lacks %q", want)
		}
	}
}

// TestExportLedgerFailsOnAnUnlistedExport is the Pi-bump case on the real
// ledger: an export Pi added arrives with no row and fails until classified.
func TestExportLedgerFailsOnAnUnlistedExport(t *testing.T) {
	in := realLedgerInput(t)
	in.File.Export = maps.Clone(in.File.Export)
	delete(in.File.Export, "pi-tui: truncateToWidth")
	problems := strings.Join(checkLedger(in), "\n")
	if !strings.Contains(problems, "pi-tui: truncateToWidth is unlisted") {
		t.Fatalf("an unlisted export did not fail:\n%s", problems)
	}
}

// ── Seeded bad / near-miss / good cases ─────────────────────────────────────

func seedSymbols() map[string]symbolSet {
	return map[string]symbolSet{
		"go":     {"TruncateHead": true, "kit.Text": true, "Context.Complete": true},
		"rust":   {"truncate_head": true, "kit::Text": true, "Context::complete": true},
		"python": {"truncate_head": true, "kit.Text": true, "Context.complete": true},
	}
}

// seedInput is a small ledger that passes every check.
func seedInput() ledgerInput {
	rows := []ledgerRow{
		{Key: "pi-tui: truncateToWidth", Module: "pi-tui", Name: "truncateToWidth", Decl: "function", Node: "served"},
		{Key: "pi-coding-agent: truncateHead", Module: "pi-coding-agent", Name: "truncateHead", Decl: "function", Node: "served"},
		{Key: "pi-tui: Text", Module: "pi-tui", Name: "Text", Decl: "class", Node: "served"},
		{Key: "pi-tui: Text.render", Module: "pi-tui", Name: "Text.render", Decl: "member", Node: "served"},
		{Key: "pi-coding-agent: defineTool", Module: "pi-coding-agent", Name: "defineTool", Decl: "function", Node: "served"},
		{Key: "pi-coding-agent: KeybindingsManager", Module: "pi-coding-agent", Name: "KeybindingsManager", Decl: "class", Node: "missing"},
	}
	gap := func(l string) map[string]string { return map[string]string{"go": l, "rust": l, "python": l} }
	return ledgerInput{
		Rows: rows,
		File: ledgerFile{
			Policy: ledgerPolicy{ReleaseTrain: "0.5.2", Trains: []string{"0.5.1", "0.5.2"}, MaxGapTrains: 1, HighUse: 5, IdentityRuntime: []string{"pi-coding-agent: defineTool", "pi-coding-agent: KeybindingsManager"}},
			Lane:   map[string]ledgerLane{"SdkTuiHelpers": {Owner: "SdkTuiHelpers lane", Opened: "0.5.2", Closing: "vector tests from Pi"}},
			Export: map[string]map[string]string{
				"pi-tui: truncateToWidth":             gap("gap:SdkTuiHelpers"),
				"pi-coding-agent: truncateHead":       {"go": "native:TruncateHead", "rust": "native:truncate_head", "python": "native:truncate_head"},
				"pi-tui: Text":                        {"go": "kit:Text", "rust": "kit:Text", "python": "kit:Text"},
				"pi-tui: Text.render":                 gap("divergence:D107"),
				"pi-coding-agent: defineTool":         gap("identity"),
				"pi-coding-agent: KeybindingsManager": {"node": "identity", "go": "identity", "rust": "identity", "python": "identity"},
			},
		},
		Symbols: seedSymbols(),
		Corpus: map[string]string{
			"go":     "TruncateHead(x); kit.Text(\"a\")",
			"rust":   "truncate_head(x); kit::Text::new(\"a\")",
			"python": "truncate_head(x); kit.Text('a')",
		},
		Usage:       usageFile{Usage: map[string]map[string]int{"pi-tui": {"truncateToWidth": 4}}},
		Divergences: map[string]bool{"D107": true},
	}
}

func cloneInput(in ledgerInput) ledgerInput {
	out := in
	out.Rows = append([]ledgerRow(nil), in.Rows...)
	out.File.Export = map[string]map[string]string{}
	for k, v := range in.File.Export {
		out.File.Export[k] = maps.Clone(v)
	}
	out.File.Lane = maps.Clone(in.File.Lane)
	out.File.Unreached = append([]unreachedSpec(nil), in.File.Unreached...)
	out.File.Policy.IdentityRuntime = append([]string(nil), in.File.Policy.IdentityRuntime...)
	out.Corpus = maps.Clone(in.Corpus)
	out.Usage = usageFile{Usage: map[string]map[string]int{}}
	for m, v := range in.Usage.Usage {
		out.Usage.Usage[m] = maps.Clone(v)
	}
	return out
}

func TestExportLedgerGoodCasePasses(t *testing.T) {
	if p := checkLedger(seedInput()); len(p) > 0 {
		t.Fatalf("good case failed:\n  %s", strings.Join(p, "\n  "))
	}
}

func TestExportLedgerRejectsBadAndNearMissCases(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*ledgerInput)
		release bool
		want    string // substring of a problem; "" means the case must pass
	}{
		// New Pi export.
		{"unlisted export", func(in *ledgerInput) {
			in.Rows = append(in.Rows, ledgerRow{Key: "pi-tui: newHelper", Module: "pi-tui", Name: "newHelper", Decl: "function", Node: "served"})
		}, false, "pi-tui: newHelper is unlisted"},
		{"unclassified cell", func(in *ledgerInput) { in.File.Export["pi-tui: truncateToWidth"]["rust"] = "unclassified" }, false, "rust is unclassified"},
		{"row for an export Pi dropped", func(in *ledgerInput) { in.File.Export["pi-tui: gone"] = map[string]string{"go": "identity"} }, false, `"pi-tui: gone" is not an export`},
		// Free text is not a disposition.
		{"n/a", func(in *ledgerInput) { in.File.Export["pi-tui: truncateToWidth"]["go"] = "n/a" }, false, `"n/a" is not a disposition`},
		{"deferred", func(in *ledgerInput) { in.File.Export["pi-tui: truncateToWidth"]["go"] = "deferred" }, false, `"deferred" is not a disposition`},
		{"pending", func(in *ledgerInput) { in.File.Export["pi-tui: truncateToWidth"]["go"] = "pending" }, false, `"pending" is not a disposition`},
		{"gap with prose", func(in *ledgerInput) { in.File.Export["pi-tui: truncateToWidth"]["go"] = "gap:not yet" }, false, "without spaces"},
		{"missing cell", func(in *ledgerInput) { delete(in.File.Export["pi-tui: truncateToWidth"], "python") }, false, "has no python cell"},
		{"unknown field", func(in *ledgerInput) { in.File.Export["pi-tui: truncateToWidth"]["java"] = "identity" }, false, `unknown field "java"`},
		// Realizations are looked up.
		{"native symbol the SDK lacks", func(in *ledgerInput) {
			in.File.Export["pi-coding-agent: truncateHead"]["go"] = "native:TruncateTail"
		}, false, `does not define`},
		{"kit view the SDK lacks", func(in *ledgerInput) { in.File.Export["pi-tui: Text"]["python"] = "kit:Image" }, false, `does not define`},
		{"native on Node", func(in *ledgerInput) { in.File.Export["pi-coding-agent: KeybindingsManager"]["node"] = "native:X" }, false, "Node cell cannot be native"},
		// Divergences and lanes.
		{"divergence nobody approved", func(in *ledgerInput) { in.File.Export["pi-tui: Text.render"]["go"] = "divergence:D999" }, false, "D999"},
		{"divergence that is not a number", func(in *ledgerInput) { in.File.Export["pi-tui: Text.render"]["go"] = "divergence:soon" }, false, "not a D<n> number"},
		{"gap naming no lane", func(in *ledgerInput) { in.File.Export["pi-tui: truncateToWidth"]["go"] = "gap:Nobody" }, false, `lane "Nobody"`},
		{"lane without owner", func(in *ledgerInput) {
			in.File.Lane["SdkTuiHelpers"] = ledgerLane{Opened: "0.5.2", Closing: "x"}
		}, false, "owner and closing"},
		{"lane without closing row", func(in *ledgerInput) {
			in.File.Lane["SdkTuiHelpers"] = ledgerLane{Owner: "o", Opened: "0.5.2"}
		}, false, "owner and closing"},
		{"lane no gap names", func(in *ledgerInput) { in.File.Lane["Orphan"] = ledgerLane{Owner: "o", Opened: "0.5.2", Closing: "c"} }, false, "no gap names this lane"},
		// Identity is for types and listed helpers only.
		{"identity laundering a runtime export", func(in *ledgerInput) { in.File.Export["pi-tui: truncateToWidth"]["go"] = "identity" }, false, "only a type has no runtime"},
		// Node column.
		{"stale Node cell on a served export", func(in *ledgerInput) { in.File.Export["pi-tui: truncateToWidth"]["node"] = "gap:SdkTuiHelpers" }, false, "Node cell"},
		{"Node does not serve it and no Node cell", func(in *ledgerInput) { delete(in.File.Export["pi-coding-agent: KeybindingsManager"], "node") }, false, "no Node cell"},
		// Gap age: near miss one train old passes, two trains old fails.
		{"gap one train old", func(in *ledgerInput) {
			in.File.Policy.Trains = []string{"0.5.1", "0.5.2"}
			in.File.Lane["SdkTuiHelpers"] = ledgerLane{Owner: "o", Opened: "0.5.1", Closing: "c"}
		}, false, ""},
		{"gap older than the policy", func(in *ledgerInput) {
			in.File.Policy.Trains = []string{"0.5.0", "0.5.1", "0.5.2"}
			in.File.Lane["SdkTuiHelpers"] = ledgerLane{Owner: "o", Opened: "0.5.0", Closing: "c"}
		}, false, "train(s) old"},
		{"lane opened in an unknown train", func(in *ledgerInput) {
			in.File.Lane["SdkTuiHelpers"] = ledgerLane{Owner: "o", Opened: "9.9.9", Closing: "c"}
		}, false, "not in policy trains"},
		// High use: a development check allows it, the release gate does not;
		// one import short of the threshold passes the release gate.
		{"high-use gap, development check", func(in *ledgerInput) { in.Usage.Usage["pi-tui"]["truncateToWidth"] = 91 }, false, ""},
		{"high-use gap, release gate", func(in *ledgerInput) { in.Usage.Usage["pi-tui"]["truncateToWidth"] = 91 }, true, "91 scanned packages"},
		{"gap at the threshold, release gate", func(in *ledgerInput) { in.Usage.Usage["pi-tui"]["truncateToWidth"] = 5 }, true, "5 scanned packages"},
		{"gap one below the threshold, release gate", func(in *ledgerInput) { in.Usage.Usage["pi-tui"]["truncateToWidth"] = 4 }, true, ""},
		{"high-use export realized, release gate", func(in *ledgerInput) {
			in.Usage.Usage["pi-coding-agent"] = map[string]int{"truncateHead": 50}
		}, true, ""},
		// Reach: a realized cell needs a fixture or vector test.
		{"realized cell no fixture reaches", func(in *ledgerInput) { in.Corpus["rust"] = "kit::Text::new(\"a\")" }, false, "rust (native:truncate_head) is reached by no conformance fixture"},
		{"name only in a longer identifier is not reached", func(in *ledgerInput) { in.Corpus["go"] = "NotTruncateHeadAtAll(); kit.Text(1)" }, false, "go (native:TruncateHead) is reached by no"},
		{"kit view reached only by a bare word", func(in *ledgerInput) { in.Corpus["python"] = "truncate_head(); print('Text')" }, false, "python (kit:Text) is reached by no"},
		{"reached hole is stale", func(in *ledgerInput) {
			in.File.Unreached = []unreachedSpec{{Key: "pi-coding-agent: truncateHead", SDKs: []string{"go"}, Lane: "SdkTuiHelpers", Row: "r"}}
		}, false, "is stale"},
		{"listed hole passes", func(in *ledgerInput) {
			in.Corpus["rust"] = "kit::Text::new(\"a\")"
			in.File.Unreached = []unreachedSpec{{Key: "pi-coding-agent: truncateHead", SDKs: []string{"rust"}, Lane: "SdkTuiHelpers", Row: "r"}}
		}, false, ""},
		{"hole naming a gap cell", func(in *ledgerInput) {
			in.File.Unreached = []unreachedSpec{{Key: "pi-tui: truncateToWidth", SDKs: []string{"go"}, Lane: "SdkTuiHelpers", Row: "r"}}
		}, false, "names no realized cell"},
		{"hole without a closing row", func(in *ledgerInput) {
			in.Corpus["rust"] = ""
			in.File.Unreached = []unreachedSpec{{Key: "pi-coding-agent: truncateHead", SDKs: []string{"rust"}, Lane: "SdkTuiHelpers"}}
		}, false, "needs the row that closes it"},
		// The release train must be a known train.
		{"unknown release train", func(in *ledgerInput) { in.File.Policy.ReleaseTrain = "7.0.0" }, false, "release_train"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := cloneInput(seedInput())
			c.mutate(&in)
			in.Release = c.release
			problems := checkLedger(in)
			joined := strings.Join(problems, "\n")
			switch {
			case c.want == "" && len(problems) > 0:
				t.Fatalf("expected a pass, got:\n%s", joined)
			case c.want != "" && !strings.Contains(joined, c.want):
				t.Fatalf("expected a problem containing %q, got:\n%s", c.want, joined)
			}
		})
	}
}

func TestParseCell(t *testing.T) {
	good := map[string]ledgerCell{
		"native:TruncateHead": {cellNative, "TruncateHead"}, "kit:Text": {cellKit, "Text"}, "host:Context.Complete": {cellHost, "Context.Complete"},
		"identity": {Kind: cellIdentity}, "divergence:D107": {cellDivergence, "D107"}, "gap:SdkTuiHelpers": {cellGap, "SdkTuiHelpers"},
		"unclassified": {Kind: cellUnclassified},
	}
	for in, want := range good {
		got, err := parseCell(in)
		if err != nil || got != want {
			t.Errorf("parseCell(%q) = %v, %v; want %v", in, got, err, want)
		}
		if got.String() != in {
			t.Errorf("round trip of %q gave %q", in, got.String())
		}
	}
	for _, bad := range []string{"", "n/a", "N/A", "deferred", "pending", "none", "-", "native", "native:", "gap", "gap:", "gap: lane", "gap:a b", "identity:x", "divergence:107", "divergence:D", "Gap:x", "native: X"} {
		if c, err := parseCell(bad); err == nil {
			t.Errorf("parseCell(%q) = %v; want an error", bad, c)
		}
	}
}

// TestLedgerRenderKeepsCellsAndMarksNewRows pins the regeneration: it keeps
// what the author set, adds new exports unclassified and drops dead rows.
func TestLedgerRenderKeepsCellsAndMarksNewRows(t *testing.T) {
	in := seedInput()
	in.Rows = append(in.Rows[:1:1], ledgerRow{Key: "pi-tui: brandNew", Module: "pi-tui", Name: "brandNew", Decl: "function", Node: "served"})
	text := string(renderLedger(in.File, in.Rows))
	if !strings.Contains(text, `"pi-tui: truncateToWidth" = { go = "gap:SdkTuiHelpers"`) {
		t.Errorf("regeneration lost an author's cell:\n%s", text)
	}
	if !strings.Contains(text, `"pi-tui: brandNew" = { go = "unclassified", rust = "unclassified", python = "unclassified" }`) {
		t.Errorf("a new export was not added unclassified:\n%s", text)
	}
	if strings.Contains(text, "Text.render") {
		t.Errorf("a row Pi no longer exports survived:\n%s", text)
	}
}

// TestVectorFilesAreChecked seeds good, bad and near-miss vector trees.
func TestVectorFilesAreChecked(t *testing.T) {
	rows := []ledgerRow{{Key: "pi-tui: truncateToWidth", Module: "pi-tui"}, {Key: "pi-ai/compat: calculateCost", Module: "pi-ai/compat"}}
	good := `{"pin":"1.3.0+x","package":"pi-tui@1.3.0","module":"pi-tui","export":"truncateToWidth","cases":[{"name":"a","args":["x",1],"result":"x"},{"name":"b","args":[],"throws":"boom"}]}`
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"none", nil, ""},
		{"good", map[string]string{"pi-tui/truncateToWidth.json": good, "cases/pi-tui/truncateToWidth.mjs": "x", "README.md": "x"}, ""},
		{"compat dir", map[string]string{"pi-ai-compat/calculateCost.json": strings.Replace(strings.Replace(good, `"pi-tui"`, `"pi-ai-compat"`, 1), `truncateToWidth`, `calculateCost`, 1), "cases/pi-ai-compat/calculateCost.mjs": "x"}, ""},
		{"stale pin", map[string]string{"pi-tui/truncateToWidth.json": strings.Replace(good, "1.3.0+x", "1.0.3", 1), "cases/pi-tui/truncateToWidth.mjs": "x"}, "current pin"},
		{"hand written: no case source", map[string]string{"pi-tui/truncateToWidth.json": good}, "no case source"},
		{"unknown export", map[string]string{"pi-tui/nope.json": strings.Replace(good, "truncateToWidth", "nope", 1), "cases/pi-tui/nope.mjs": "x"}, "names no export"},
		{"unknown module dir", map[string]string{"pi-x/a.json": good}, "not a served module"},
		{"result and throws", map[string]string{"pi-tui/truncateToWidth.json": strings.Replace(good, `"result":"x"`, `"result":"x","throws":"y"`, 1), "cases/pi-tui/truncateToWidth.mjs": "x"}, "exactly one of result and throws"},
		{"neither", map[string]string{"pi-tui/truncateToWidth.json": strings.Replace(good, `"result":"x"`, `"x":1`, 1), "cases/pi-tui/truncateToWidth.mjs": "x"}, "exactly one of result and throws"},
		{"args not an array", map[string]string{"pi-tui/truncateToWidth.json": strings.Replace(good, `"args":["x",1]`, `"args":{}`, 1), "cases/pi-tui/truncateToWidth.mjs": "x"}, "args array"},
		{"duplicate case", map[string]string{"pi-tui/truncateToWidth.json": strings.Replace(good, `"name":"b"`, `"name":"a"`, 1), "cases/pi-tui/truncateToWidth.mjs": "x"}, "unique name"},
		{"mismatched path", map[string]string{"pi-tui/truncateToWidth.json": strings.Replace(good, `"export":"truncateToWidth"`, `"export":"other"`, 1), "cases/pi-tui/truncateToWidth.mjs": "x"}, "must match the path"},
		{"invalid json", map[string]string{"pi-tui/truncateToWidth.json": "{", "cases/pi-tui/truncateToWidth.mjs": "x"}, "invalid JSON"},
		{"case source without a vector", map[string]string{"cases/pi-tui/truncateToWidth.mjs": "x"}, "not generated"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			base := filepath.Join(root, vectorsDir)
			if err := os.MkdirAll(base, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, data := range c.files {
				path := filepath.Join(base, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := strings.Join(checkVectors(root, rows, "1.3.0+x"), "\n")
			if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
				t.Fatalf("want %q, got:\n%s", c.want, got)
			}
		})
	}
}

// TestReachOfGenericMemberNames pins the rule for member names common to unrelated objects (set, abort, get): a bare
// mention does not reach a cell, a class-qualified call does, and so does a call on a variable when the class is named.
func TestReachOfGenericMemberNames(t *testing.T) {
	cases := []struct {
		name   string
		cell   string
		corpus string
		want   bool
	}{
		{"class-qualified Python call", "native:ProjectTrustStore.set", "ProjectTrustStore.set(store, cwd, True)", true},
		{"class-qualified Rust call", "native:ProjectTrustStore::set", "ProjectTrustStore::set(&store, cwd, Some(true))?;", true},
		{"variable call, class named", "native:ProjectTrustStore.set", "store = ProjectTrustStore.open(p)\nstore.set(other, False)", true},
		{"variable call, class not named", "native:ProjectTrustStore.set", "cache.set(key, value)", false},
		{"class named, no call", "native:ProjectTrustStore.set", "ProjectTrustStore(p) # settings set", false},
		{"unrelated word", "native:ProjectTrustStore.set", "the set of paths", false},
		{"known receiver still works", "native:AgentSession.abort", "ctx.abort()", true},
		{"free function with a generic name", "native:tui.set", "x.set(1)", false},
	}
	for _, c := range cases {
		cell, err := parseCell(c.cell)
		if err != nil {
			t.Fatal(err)
		}
		if got := reached(cell, c.corpus); got != c.want {
			t.Errorf("%s: reached(%s) = %v, want %v", c.name, c.cell, got, c.want)
		}
	}
}
