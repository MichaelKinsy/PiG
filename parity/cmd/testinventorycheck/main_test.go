package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// writeRepo lays down an inventory, mapping, DIVERGENCES.md, and evidence files
// under a temp root and returns the root plus the inventory/mapping paths.
func writeRepo(t *testing.T, inv inventory, m mapping, evidence map[string]string, divergences string) (root, invPath, mapPath string) {
	t.Helper()
	root = t.TempDir()
	invPath = filepath.Join(root, "inventory.json")
	mapPath = filepath.Join(root, "mapping.json")
	writeJSON(t, invPath, inv)
	writeJSON(t, mapPath, m)
	if err := os.WriteFile(filepath.Join(root, "DIVERGENCES.md"), []byte(divergences), 0o644); err != nil {
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
	if err := check(invPath, mapPath, "DIVERGENCES.md", root, false); err != nil {
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
			m.Entries[0].Evidence = []string{"parity/scenarios/ai/x.toml"}
			e["parity/scenarios/ai/x.toml"] = "x"
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
			err := check(invPath, mapPath, "DIVERGENCES.md", root, tc.strict)
			if err == nil {
				t.Fatalf("expected rejection %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
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
	if err := check(invPath, mapPath, "DIVERGENCES.md", root, false); err != nil {
		t.Fatalf("divergence with a ledger entry rejected: %v", err)
	}
}
