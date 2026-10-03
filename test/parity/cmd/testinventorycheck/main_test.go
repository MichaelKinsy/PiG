package main

import (
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// writeRepo lays down an inventory, mapping, docs/parity/DIVERGENCES.md, and evidence files
// under a temp root and returns the root plus the inventory/mapping paths.
func writeRepo(t *testing.T, inv inventory, m mapping, evidence map[string]string, divergences string) (root, invPath, mapPath string) {
	t.Helper()
	root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "parity"), 0o755); err != nil {
		t.Fatal(err)
	}
	invPath = filepath.Join(root, "inventory.json")
	mapPath = filepath.Join(root, "mapping.json")
	writeJSON(t, invPath, inv)
	writeJSON(t, mapPath, m)
	if err := os.WriteFile(filepath.Join(root, "docs/parity/DIVERGENCES.md"), []byte(divergences), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, contents := range evidence {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, invPath, mapPath
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

const hashA = "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const hashB = "sha256:" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// baseline is a fully-closed two-file mapping used as the mutation seed.
func baseline() (inventory, mapping, map[string]string, string) {
	inv := inventory{
		UpstreamVersion: coding.UpstreamVersion,
		Generator:       "extract-test-inventory.mjs",
		Files: []inventoryFile{
			{Path: "packages/ai/test/a.test.ts", Package: "ai", SHA256: hashA, CaseCount: 2},
			{Path: "packages/server/test/b.test.ts", Package: "server", SHA256: hashB, CaseCount: 1},
		},
	}
	m := mapping{
		UpstreamVersion: coding.UpstreamVersion,
		Entries: []mappingEntry{
			{Path: "packages/ai/test/a.test.ts", Disposition: "ported", UpstreamTestHash: hashA, Evidence: []string{"ai/a_test.go#packages/ai/test/a.test.ts"}, Rationale: "ported"},
			{Path: "packages/server/test/b.test.ts", Disposition: "designed-out", UpstreamTestHash: hashB, Rationale: "server package outside pig scope"},
		},
	}
	evidence := map[string]string{"ai/a_test.go": "// upstream: packages/ai/test/a.test.ts\nfunc TestA(t *testing.T){}"}
	return inv, m, evidence, "no divergences\n"
}

func TestCheckAcceptsClosedMapping(t *testing.T) {
	inv, m, evidence, div := baseline()
	root, invPath, mapPath := writeRepo(t, inv, m, evidence, div)
	if err := check(invPath, mapPath, "docs/parity/DIVERGENCES.md", root, false); err != nil {
		t.Fatalf("closed mapping rejected: %v", err)
	}
}

func TestCheckRejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*inventory, *mapping, map[string]string, *string)
		strict bool
		want   string
	}{
		{"hash drift reopens disposition", func(_ *inventory, m *mapping, _ map[string]string, _ *string) {
			m.Entries[0].UpstreamTestHash = hashB
		}, false, "does not match inventory"},
		{"orphan mapping entry", func(_ *inventory, m *mapping, _ map[string]string, _ *string) {
			m.Entries[0].Path = "packages/ai/test/ghost.test.ts"
		}, false, "names no inventory file"},
		{"uncovered inventory file", func(_ *inventory, m *mapping, _ map[string]string, _ *string) {
			m.Entries = m.Entries[:1]
		}, false, "no reviewed disposition"},
		{"ported evidence file missing", func(_ *inventory, _ *mapping, e map[string]string, _ *string) {
			delete(e, "ai/a_test.go")
		}, false, "evidence"},
		{"ported evidence fragment absent", func(_ *inventory, _ *mapping, e map[string]string, _ *string) {
			e["ai/a_test.go"] = "func TestA(t *testing.T){}"
		}, false, "no matching fragment"},
		{"ported without a go test", func(_ *inventory, m *mapping, e map[string]string, _ *string) {
			m.Entries[0].Evidence = []string{"test/parity/scenarios/ai/x.toml"}
			e["test/parity/scenarios/ai/x.toml"] = "x"
		}, false, "needs at least one Go test"},
		{"designed-out without rationale", func(_ *inventory, m *mapping, _ map[string]string, _ *string) {
			m.Entries[1].Rationale = ""
		}, false, "needs a rationale"},
		{"divergence absent from ledger", func(_ *inventory, m *mapping, _ map[string]string, _ *string) {
			m.Entries[1].Disposition = "divergence"
			m.Entries[1].Divergence = "D77"
			m.Entries[1].Rationale = "differs"
		}, false, "absent from the divergence ledger"},
		{"unsorted mapping", func(_ *inventory, m *mapping, _ map[string]string, _ *string) {
			m.Entries[0], m.Entries[1] = m.Entries[1], m.Entries[0]
		}, false, "not sorted"},
		{"unknown disposition", func(_ *inventory, m *mapping, _ map[string]string, _ *string) {
			m.Entries[0].Disposition = "maybe"
		}, false, "unsupported disposition"},
		{"strict rejects pending", func(_ *inventory, m *mapping, _ map[string]string, _ *string) {
			m.Entries[0].Disposition = "pending"
			m.Entries[0].Evidence = nil
			m.Entries[0].Rationale = ""
		}, true, "remains pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv, m, evidence, div := baseline()
			tc.mutate(&inv, &m, evidence, &div)
			root, invPath, mapPath := writeRepo(t, inv, m, evidence, div)
			err := check(invPath, mapPath, "docs/parity/DIVERGENCES.md", root, tc.strict)
			if err == nil {
				t.Fatalf("expected rejection %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestCheckVerifiesDesignedOutEvidence guards the closure hole where a
// designed-out entry skipped evidence resolution: a citation naming a missing
// file or fragment passed the gate and the hot-path release policy.
func TestCheckVerifiesDesignedOutEvidence(t *testing.T) {
	cases := []struct {
		name     string
		evidence []string
		files    map[string]string
		want     string
	}{
		{"missing file", []string{"server/gone_test.go#TestGone"}, nil, "evidence"},
		{"absent fragment", []string{"server/b_test.go#TestB"}, map[string]string{"server/b_test.go": "func TestOther(t *testing.T){}"}, "no matching fragment"},
		{"resolving evidence", []string{"server/b_test.go#TestB"}, map[string]string{"server/b_test.go": "func TestB(t *testing.T){}"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv, m, evidence, div := baseline()
			m.Entries[1].Evidence = tc.evidence
			maps.Copy(evidence, tc.files)
			root, invPath, mapPath := writeRepo(t, inv, m, evidence, div)
			err := check(invPath, mapPath, "docs/parity/DIVERGENCES.md", root, false)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("resolving designed-out evidence rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v does not contain %q", err, tc.want)
			}
		})
	}
}

// mixedClosure returns a ported file whose first case is designed out and whose
// second case stays covered by verified evidence.
func mixedClosure() (inventory, mapping, map[string]string, string) {
	inv, m, evidence, div := baseline()
	inv.Files[0].Cases = []struct {
		ID        string   `json:"id"`
		Kind      string   `json:"kind"`
		Modifiers []string `json:"modifiers"`
		Line      int      `json:"line"`
	}{{ID: "official › runs", Kind: "it", Line: 10}, {ID: "analytics › ported", Kind: "it", Line: 20}}
	m.Entries[0].DesignedOutCases = []string{"official › runs"}
	return inv, m, evidence, div
}

// TestCheckMixedClosure proves each designedOutCases rule rejects its own
// violation while the ported remainder keeps its evidence obligations.
func TestCheckMixedClosure(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*mapping, map[string]string)
		want   string
	}{
		{"accepts designed-out cases beside verified ported evidence", func(*mapping, map[string]string) {}, ""},
		{"ported remainder still needs resolving evidence", func(_ *mapping, e map[string]string) {
			delete(e, "ai/a_test.go")
		}, "evidence"},
		{"ported remainder still needs a Go test", func(m *mapping, e map[string]string) {
			m.Entries[0].Evidence = []string{"test/parity/scenarios/ai/x.toml"}
			e["test/parity/scenarios/ai/x.toml"] = "x"
		}, "needs at least one Go test"},
		{"unknown case id", func(m *mapping, _ map[string]string) {
			m.Entries[0].DesignedOutCases = []string{"official › typo"}
		}, "not an upstream case"},
		{"duplicate case id", func(m *mapping, _ map[string]string) {
			m.Entries[0].DesignedOutCases = []string{"official › runs", "official › runs"}
		}, "duplicate designedOutCases"},
		{"every case designed out", func(m *mapping, _ map[string]string) {
			m.Entries[0].DesignedOutCases = []string{"official › runs", "analytics › ported"}
		}, "classify the file designed-out"},
		// ported alone needs no rationale, so only the designedOutCases rule rejects this.
		{"ported needs a rationale for its designed-out cases", func(m *mapping, _ map[string]string) {
			m.Entries[0].Rationale = ""
		}, "lists designedOutCases and needs a rationale"},
		{"partial needs a rationale for its designed-out cases", func(m *mapping, _ map[string]string) {
			m.Entries[0].Rationale = ""
			m.Entries[0].Disposition = "partial"
		}, "lists designedOutCases and needs a rationale"},
		{"not allowed on pending", func(m *mapping, _ map[string]string) {
			m.Entries[0].Disposition = "pending"
			m.Entries[0].Evidence = nil
		}, "only ported or partial"},
		{"not allowed on designed-out", func(m *mapping, _ map[string]string) {
			m.Entries[0].Disposition = "designed-out"
			m.Entries[0].Evidence = nil
		}, "only ported or partial"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv, m, evidence, div := mixedClosure()
			tc.mutate(&m, evidence)
			root, invPath, mapPath := writeRepo(t, inv, m, evidence, div)
			err := check(invPath, mapPath, "docs/parity/DIVERGENCES.md", root, false)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("mixed closure rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v does not contain %q", err, tc.want)
			}
		})
	}
}

// TestCheckRejectsAmbiguousDesignedOutCase proves a case id shared by two source
// sites (packages/ai/test/total-tokens.test.ts:726 and :777 upstream) cannot be
// designed out: the schema names ids, so it cannot say which site is exempt.
func TestCheckRejectsAmbiguousDesignedOutCase(t *testing.T) {
	inv, m, evidence, div := mixedClosure()
	inv.Files[0].Cases = append(inv.Files[0].Cases, inv.Files[0].Cases[0])
	inv.Files[0].Cases[2].Line = 30
	root, invPath, mapPath := writeRepo(t, inv, m, evidence, div)
	err := check(invPath, mapPath, "docs/parity/DIVERGENCES.md", root, false)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error %v does not reject the ambiguous id", err)
	}
	// The unshared case of the same file stays designable.
	m.Entries[0].DesignedOutCases = []string{"analytics › ported"}
	root, invPath, mapPath = writeRepo(t, inv, m, evidence, div)
	if err := check(invPath, mapPath, "docs/parity/DIVERGENCES.md", root, false); err != nil {
		t.Fatalf("unshared id rejected: %v", err)
	}
}

// TestCheckDivergenceCloses proves a divergence disposition passes when the D<N>
// is present in the ledger (the positive complement to the absent-ledger case).
func TestCheckDivergenceCloses(t *testing.T) {
	inv, m, evidence, _ := baseline()
	m.Entries[1].Disposition = "divergence"
	m.Entries[1].Divergence = "D77"
	m.Entries[1].Rationale = "pig differs"
	root, invPath, mapPath := writeRepo(t, inv, m, evidence, "## D77 something\n")
	if err := check(invPath, mapPath, "docs/parity/DIVERGENCES.md", root, false); err != nil {
		t.Fatalf("divergence with a ledger entry rejected: %v", err)
	}
}

// harnessSource is the body of TestNodeVendoredTuiUpstreamTests as the production harness spells it.
func harnessSource(names string) string {
	return harnessWithSubtest(names, "")
}

// harnessWithSubtest spells the harness with subtestPrefix statements before the subtest's unconditional Node run.
func harnessWithSubtest(names, subtestPrefix string) string {
	return "func TestNodeVendoredTuiUpstreamTests(t *testing.T) {\n\tfor _, name := range []string{" + names + "} {\n\t\tt.Run(name, func(t *testing.T) {\n" + subtestPrefix + "\t\t\tout, err := cmd.CombinedOutput()\n\t\t})\n\t}\n}\n"
}

func TestNodeBridgeHarnessProblemsUseMappingAsDenominator(t *testing.T) {
	harness := func(names string) []byte {
		return []byte("package subprocess_test\n" + harnessSource(names))
	}
	row := func(path, disposition string, evidence ...string) mappingEntry {
		return mappingEntry{Path: path, Disposition: disposition, Evidence: evidence}
	}
	const a, b = "packages/tui/test/a.test.ts", "packages/tui/test/native-module-path.test.ts"
	m := mapping{Entries: []mappingEntry{row(a, "ported", nodeHarnessEvidence), row(b, "ported", nodeHarnessEvidence)}}
	if got := nodeBridgeHarnessProblems(harness(`"a", "native-module-path"`), m); len(got) != 0 {
		t.Fatalf("complete registration rejected: %v", got)
	}
	got := nodeBridgeHarnessProblems(harness(`"a"`), m)
	if len(got) != 1 || !strings.Contains(got[0], b) || !strings.Contains(got[0], "does not register") {
		t.Fatalf("removed harness name not reported: %v", got)
	}
	// A row that cites other evidence only is not the harness's obligation.
	other := mapping{Entries: []mappingEntry{row(a, "ported", nodeHarnessEvidence), row(b, "ported", "x_test.go#TestX")}}
	if got := nodeBridgeHarnessProblems(harness(`"a"`), other); len(got) != 0 {
		t.Fatalf("row without harness evidence reported: %v", got)
	}
	designed := mapping{Entries: []mappingEntry{row(a, "designed-out"), row(b, "ported", nodeHarnessEvidence)}}
	if got := nodeBridgeHarnessProblems(harness(`"a", "native-module-path"`), designed); len(got) != 1 || !strings.Contains(got[0], "designed-out") {
		t.Fatalf("designed-out registered file not reported: %v", got)
	}
	if got := nodeBridgeHarnessProblems([]byte("nothing"), m); len(got) != 1 {
		t.Fatalf("missing list not reported: %v", got)
	}
	// Only the executable registration counts. A stale list in a comment, a string, another function, or a loop that never calls t.Run must not satisfy the row, however early it appears in the file.
	for name, source := range map[string]string{
		"comment":               "// for _, name := range []string{\"a\", \"native-module-path\"} {\n" + harnessSource(`"a"`),
		"block comment":         "/* for _, name := range []string{\"a\", \"native-module-path\"} { */\n" + harnessSource(`"a"`),
		"string":                "var stale = `for _, name := range []string{\"a\", \"native-module-path\"} {`\n" + harnessSource(`"a"`),
		"other function":        "func helper(t *testing.T) {\n\tfor _, name := range []string{\"a\", \"native-module-path\"} {\n\t\tt.Run(name, nil)\n\t}\n}\n" + harnessSource(`"a"`),
		"unrun loop":            "func TestNodeVendoredTuiUpstreamTests(t *testing.T) {\n\tfor _, name := range []string{\"a\", \"native-module-path\"} {\n\t\t_ = name\n\t}\n}\n",
		"run of other receiver": "func TestNodeVendoredTuiUpstreamTests(t *testing.T) {\n\tfor _, name := range []string{\"a\", \"native-module-path\"} {\n\t\tgroup.Run(name, nil)\n\t}\n}\n",
		"run of other value":    "func TestNodeVendoredTuiUpstreamTests(t *testing.T) {\n\tfor _, name := range []string{\"a\", \"native-module-path\"} {\n\t\tt.Run(\"x\", nil)\n\t}\n}\n",
	} {
		got := nodeBridgeHarnessProblems([]byte("package subprocess_test\n"+source), m)
		if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), "cannot find") && !strings.Contains(strings.Join(got, "\n"), b) {
			t.Fatalf("%s: non-executable registration accepted or misreported: %v", name, got)
		}
	}
	// checkReferences accepts the file alone or any substring of the test name as the fragment, so each such spelling still binds the row to the harness.
	for _, reference := range []string{nodeBridgeHarnessPath, nodeBridgeHarnessPath + "#NodeVendoredTuiUpstream"} {
		variant := mapping{Entries: []mappingEntry{row(a, "ported", nodeHarnessEvidence), row(b, "ported", reference)}}
		if got := nodeBridgeHarnessProblems(harness(`"a"`), variant); len(got) != 1 || !strings.Contains(got[0], b) {
			t.Fatalf("unregistered row citing %q not reported: %v", reference, got)
		}
	}
	// checkReferences also accepts a fragment that occurs anywhere in the file, so a subtest name still present in the harness body (here a per-name branch left behind after the name left the loop) binds the row to the harness too.
	branch := []byte("package subprocess_test\nfunc TestNodeVendoredTuiUpstreamTests(t *testing.T) {\n\tfor _, name := range []string{\"a\"} {\n\t\tt.Run(name, func(t *testing.T) {\n\t\t\tif name == \"native-module-path\" {\n\t\t\t}\n\t\t\tout, err := cmd.CombinedOutput()\n\t\t})\n\t}\n}\n")
	for _, fragment := range []string{"native-module-path", `name == "native-module-path"`} {
		variant := mapping{Entries: []mappingEntry{row(a, "ported", nodeHarnessEvidence), row(b, "ported", nodeBridgeHarnessPath+"#"+fragment)}}
		if got := nodeBridgeHarnessProblems(branch, variant); len(got) != 1 || !strings.Contains(got[0], b) || !strings.Contains(got[0], "does not register") {
			t.Fatalf("unregistered row citing harness body fragment %q not reported: %v", fragment, got)
		}
	}
	// Another test in the harness file is not the harness's registration list.
	sibling := mapping{Entries: []mappingEntry{row(a, "ported", nodeHarnessEvidence), row(b, "ported", nodeBridgeHarnessPath+"#TestVendoredPiTuiContainsEveryRuntimeModuleAndNativeAsset")}}
	if got := nodeBridgeHarnessProblems(harness(`"a"`), sibling); len(got) != 0 {
		t.Fatalf("row citing a sibling test reported: %v", got)
	}
}

// TestNodeBridgeHarnessRequiresExecutionNotRegistration proves a name that is in the list but cannot execute fails the gate. Registration is syntactic; `go test` exits 0 with "no tests to run" when a continue, skip or return removes every subtest of a ported row (rev-sol-tp-guard-r P2).
func TestNodeBridgeHarnessRequiresExecutionNotRegistration(t *testing.T) {
	const names = `"a", "native-module-path", "native-clipboard-linux"`
	row := func(path, disposition string) mappingEntry {
		return mappingEntry{Path: path, Disposition: disposition, Evidence: []string{nodeHarnessEvidence}}
	}
	m := mapping{Entries: []mappingEntry{
		row("packages/tui/test/a.test.ts", "ported"),
		row("packages/tui/test/native-module-path.test.ts", "ported"),
		row("packages/tui/test/native-clipboard-linux.test.ts", "pending"),
	}}
	const subtest = "\t\tt.Run(name, func(t *testing.T) {\n\t\t\tout, err := cmd.CombinedOutput()\n\t\t})\n"
	wrap := func(before, loopBody, after string) string {
		return "package subprocess_test\nfunc TestNodeVendoredTuiUpstreamTests(t *testing.T) {\n" + before + "\tfor _, name := range []string{" + names + "} {\n" + loopBody + "\t}\n" + after + "}\n"
	}
	inSubtest := func(prefix string) string { return "package subprocess_test\n" + harnessWithSubtest(names, prefix) }
	const gateSkip = "\t\t\tif name == \"native-clipboard-linux\" {\n\t\t\t\tt.Skip(\"x\")\n\t\t\t}\n"
	for name, tc := range map[string]struct{ source, want string }{
		"continue before t.Run":             {wrap("", "\t\tif name == \"native-module-path\" {\n\t\t\tcontinue\n\t\t}\n"+subtest, ""), "branch statement outside"},
		"skip before t.Run":                 {wrap("", "\t\tt.Skip(\"x\")\n"+subtest, ""), "skip outside"},
		"skip before the loop":              {wrap("\tt.Skip(\"x\")\n", subtest, ""), "skip outside"},
		"return after the loop":             {wrap("", subtest, "\treturn\n"), "return outside"},
		"loop under a condition":            {"package subprocess_test\nfunc TestNodeVendoredTuiUpstreamTests(t *testing.T) {\n\tif ok {\n\t\tfor _, name := range []string{" + names + "} {\n" + subtest + "\t\t}\n\t}\n}\n", "nested in another statement"},
		"t.Run under a condition":           {wrap("", "\t\tif name != \"native-module-path\" {\n"+subtest+"\t\t}\n", ""), "not a direct statement"},
		"skip for a ported name":            {inSubtest("\t\t\tif name == \"native-module-path\" {\n\t\t\t\tt.Skip(\"x\")\n\t\t\t}\n"), `skip for "native-module-path"`},
		"skipf for a ported name":           {inSubtest("\t\t\tif name == \"native-module-path\" {\n\t\t\t\tt.Skipf(\"x\")\n\t\t\t}\n"), `skip for "native-module-path"`},
		"return for a ported name":          {inSubtest("\t\t\tif \"native-module-path\" == name {\n\t\t\t\treturn\n\t\t\t}\n"), `return for "native-module-path"`},
		"nested skip for a ported name":     {inSubtest("\t\t\tif name == \"native-module-path\" {\n\t\t\t\tif ok {\n\t\t\t\t\tt.SkipNow()\n\t\t\t\t}\n\t\t\t}\n"), `skip for "native-module-path"`},
		"skip for every name":               {inSubtest("\t\t\tt.Skip(\"x\")\n"), "applies to every upstream test"},
		"conditional skip for every name":   {inSubtest("\t\t\tif ok {\n\t\t\t\tt.Skip(\"x\")\n\t\t\t}\n"), "applies to every upstream test"},
		"skip in the else of a name gate":   {inSubtest("\t\t\tif name == \"native-clipboard-linux\" {\n\t\t\t} else {\n\t\t\t\tt.Skip(\"x\")\n\t\t\t}\n"), "applies to every upstream test"},
		"return unless the name is ported":  {inSubtest("\t\t\tif name != \"native-module-path\" {\n\t\t\t\treturn\n\t\t\t}\n"), "applies to every upstream test"},
		"skip through a compound name gate": {inSubtest("\t\t\tif name == \"native-clipboard-linux\" || ok {\n\t\t\t\tt.Skip(\"x\")\n\t\t\t}\n"), "applies to every upstream test"},
		"Node run under a condition":        {"package subprocess_test\nfunc TestNodeVendoredTuiUpstreamTests(t *testing.T) {\n\tfor _, name := range []string{" + names + "} {\n\t\tt.Run(name, func(t *testing.T) {\n\t\t\tif name != \"native-module-path\" {\n\t\t\t\tout, err := cmd.CombinedOutput()\n\t\t\t}\n\t\t})\n\t}\n}\n", "does not run Node"},
		// Each of the following passed both this gate and `go test` on the real harness while no native-module-path case ran (rev-tp-guard-r-r).
		"name reassigned before t.Run":    {wrap("", "\t\tif name == \"native-module-path\" {\n\t\t\tname = \"a\"\n\t\t}\n"+subtest, ""), `loop value "name" is reassigned`},
		"name shadowed in the subtest":    {inSubtest("\t\t\tname := strings.Replace(name, \"native-module-path\", \"a\", 1)\n"), `loop value "name" is reassigned`},
		"name shadowed by a var":          {inSubtest("\t\t\tvar name = \"a\"\n"), `loop value "name" is shadowed`},
		"name addressed":                  {inSubtest("\t\t\trename(&name)\n"), `loop value "name" is addressed`},
		"skip through a helper":           {inSubtest("\t\t\tskipUnported(t, name)\n"), "passing t in the subtest function applies to every upstream test"},
		"helper under a ported name gate": {inSubtest("\t\t\tif name == \"native-module-path\" {\n\t\t\t\tskipUnported(t)\n\t\t\t}\n"), `passing t for "native-module-path"`},
		"skip in a nested func literal":   {inSubtest("\t\t\tif name == \"native-module-path\" {\n\t\t\t\tfunc() { t.Skip(\"x\") }()\n\t\t\t}\n"), `skip for "native-module-path"`},
		"skip through a method value":     {inSubtest("\t\t\tskip := t.SkipNow\n\t\t\tif name == \"native-module-path\" {\n\t\t\t\tskip()\n\t\t\t}\n"), "skip in the subtest function applies to every upstream test"},
		"skip in a name gate's condition": {inSubtest("\t\t\tif name == \"native-clipboard-linux\" || skipped(t) {\n\t\t\t}\n"), "passing t in the subtest function applies to every upstream test"},
		"goto past the Node run":          {inSubtest("\t\t\tif name == \"native-module-path\" {\n\t\t\t\tgoto done\n\t\t\t}\n"), `goto for "native-module-path"`},
		"skip in a parent func literal":   {wrap("\tdefer func() { t.SkipNow() }()\n", subtest, ""), "skip outside"},
		"parent t passed to a helper":     {wrap("\tskipAll(t)\n", subtest, ""), "passing t outside"},
	} {
		got := strings.Join(nodeBridgeHarnessProblems([]byte(tc.source), m), "\n")
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: want a problem containing %q, got %q", name, tc.want, got)
		}
	}
	// A skip gated on a name whose row is not ported (the platform-conditional native clipboard qualification) is the harness's real shape and stays accepted.
	if got := nodeBridgeHarnessProblems([]byte(inSubtest(gateSkip+"\t\t\tif name == \"native-clipboard-linux\" {\n\t\t\t\tif !ok {\n\t\t\t\t\treturn\n\t\t\t\t}\n\t\t\t}\n")), m); len(got) != 0 {
		t.Fatalf("skip gated on a pending row rejected: %v", got)
	}
	// The real harness hands t to testenv.RequireDirectoryLink inside the pending native clipboard branch, uses t only as a method receiver elsewhere, and loops, breaks and returns inside nested literals; none of that can end a ported row's subtest.
	accepted := "\t\t\tif name == \"native-clipboard-linux\" {\n\t\t\t\ttestenv.RequireDirectoryLink(t, a, b)\n\t\t\t}\n\t\t\tdir := t.TempDir()\n\t\t\tctx := t.Context()\n\t\t\t_ = x.t\n\t\t\tfor _, s := range list {\n\t\t\t\tif s == \"\" {\n\t\t\t\t\tbreak\n\t\t\t\t}\n\t\t\t}\n\t\t\tf := func(name string) error { return nil }\n"
	if got := nodeBridgeHarnessProblems([]byte(inSubtest(accepted)), m); len(got) != 1 || !strings.Contains(got[0], `loop value "name" is shadowed`) {
		t.Fatalf("real harness shapes misreported (only the shadowing parameter is a problem): %v", got)
	}
	accepted = strings.Replace(accepted, "func(name string)", "func(value string)", 1)
	if got := nodeBridgeHarnessProblems([]byte(inSubtest(accepted)), m); len(got) != 0 {
		t.Fatalf("real harness shapes rejected: %v", got)
	}
}

// TestRunEnforcesNodeBridgeHarness drives the command entry point that every gate invocation uses (make test-inventory, test-inventory-strict and test-porting-release). An unregistered harness row fails the invocation before check prints its OK summary.
func TestRunEnforcesNodeBridgeHarness(t *testing.T) {
	const path = "packages/tui/test/native-module-path.test.ts"
	inv := inventory{UpstreamVersion: coding.UpstreamVersion, Files: []inventoryFile{{Path: path, SHA256: hashA, CaseCount: 2}}}
	m := mapping{UpstreamVersion: coding.UpstreamVersion, Entries: []mappingEntry{{Path: path, Disposition: "ported", UpstreamTestHash: hashA, Evidence: []string{nodeHarnessEvidence}}}}
	harness := func(names string) map[string]string {
		return map[string]string{nodeBridgeHarnessPath: "package subprocess_test\n" + harnessSource(names)}
	}
	gate := func(t *testing.T, names string, extra ...string) (string, error) {
		t.Helper()
		root, invPath, mapPath := writeRepo(t, inv, m, harness(names), "no divergences\n")
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout := os.Stdout
		os.Stdout = writer
		runErr := run(append([]string{"-inventory", invPath, "-mapping", mapPath, "-repo-root", root}, extra...))
		os.Stdout = stdout
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		out, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		return string(out), runErr
	}
	if out, err := gate(t, `"native-module-path"`); err != nil || !strings.Contains(out, "test inventory: OK") {
		t.Fatalf("registered harness row rejected: %v\n%s", err, out)
	}
	for _, extra := range [][]string{nil, {"-strict"}} {
		out, err := gate(t, `"layout"`, extra...)
		if err == nil || !strings.Contains(err.Error(), path+" cites") || !strings.Contains(err.Error(), "does not register") {
			t.Fatalf("run %v accepted an unregistered harness row: %v", extra, err)
		}
		if strings.Contains(out, "OK") {
			t.Fatalf("run %v printed an OK summary before failing:\n%s", extra, out)
		}
	}
}

// TestCheckNodeBridgeHarness drives the command's production check (the one main runs for every gate invocation) against the committed mapping and a repo root holding the harness source. The committed harness passes; removing a registered name, or removing the harness, fails.
func TestCheckNodeBridgeHarness(t *testing.T) {
	realRoot := filepath.Join("..", "..", "..", "..")
	mapPath := filepath.Join(realRoot, "test", "parity", "interfaces", "test-mapping-v"+coding.UpstreamVersion+".json")
	harness, err := os.ReadFile(filepath.Join(realRoot, filepath.FromSlash(nodeBridgeHarnessPath)))
	if err != nil {
		t.Fatal(err)
	}
	withHarness := func(t *testing.T, source []byte) string {
		t.Helper()
		root := t.TempDir()
		path := filepath.Join(root, filepath.FromSlash(nodeBridgeHarnessPath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, source, 0o644); err != nil {
			t.Fatal(err)
		}
		return root
	}
	if err := checkNodeBridgeHarness(mapPath, withHarness(t, harness)); err != nil {
		t.Fatalf("committed harness rejected: %v", err)
	}
	const registration = `"native-module-path"`
	if !strings.Contains(string(harness), registration) {
		t.Fatalf("harness no longer registers %s; update this test", registration)
	}
	stripped := strings.Replace(string(harness), registration, `"unregistered-placeholder"`, 1)
	err = checkNodeBridgeHarness(mapPath, withHarness(t, []byte(stripped)))
	if err == nil || !strings.Contains(err.Error(), "native-module-path.test.ts") || !strings.Contains(err.Error(), "does not register") {
		t.Fatalf("removed registration not rejected by the gate check: %v", err)
	}
	if err := checkNodeBridgeHarness(mapPath, t.TempDir()); err == nil || !strings.Contains(err.Error(), "read Node-bridge harness") {
		t.Fatalf("missing harness not rejected: %v", err)
	}
}

func TestCarryMappingKeepsOnlyEntriesWhoseUpstreamTestIsUnchanged(t *testing.T) {
	const oldHash = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	const sameHash = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	const newHash = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	previous := mapping{UpstreamVersion: "0.87.1", Entries: []mappingEntry{
		{Path: "packages/ai/test/kept.test.ts", Disposition: "ported", UpstreamTestHash: sameHash, Evidence: []string{"ai/kept_test.go"}},
		{Path: "packages/ai/test/changed.test.ts", Disposition: "ported", UpstreamTestHash: oldHash, Evidence: []string{"ai/changed_test.go"}},
		{Path: "packages/chord/test/removed.test.ts", Disposition: "designed-out", UpstreamTestHash: sameHash, Rationale: "outside scope"},
		{Path: "packages/ai/test/designed.test.ts", Disposition: "designed-out", UpstreamTestHash: sameHash, Rationale: "Go typing"},
	}}
	inv := inventory{UpstreamVersion: "0.99.2", Files: []inventoryFile{
		{Path: "packages/ai/test/added.test.ts", SHA256: newHash},
		{Path: "packages/ai/test/changed.test.ts", SHA256: newHash},
		{Path: "packages/ai/test/designed.test.ts", SHA256: sameHash},
		{Path: "packages/ai/test/kept.test.ts", SHA256: sameHash},
	}}
	got := carryMapping(inv, previous)
	want := mapping{UpstreamVersion: "0.99.2", Entries: []mappingEntry{
		{Path: "packages/ai/test/added.test.ts", Disposition: "pending", UpstreamTestHash: newHash},
		{Path: "packages/ai/test/changed.test.ts", Disposition: "pending", UpstreamTestHash: newHash},
		{Path: "packages/ai/test/designed.test.ts", Disposition: "designed-out", UpstreamTestHash: sameHash, Rationale: "Go typing"},
		{Path: "packages/ai/test/kept.test.ts", Disposition: "ported", UpstreamTestHash: sameHash, Evidence: []string{"ai/kept_test.go"}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("carried mapping = %+v\nwant %+v", got, want)
	}
}
