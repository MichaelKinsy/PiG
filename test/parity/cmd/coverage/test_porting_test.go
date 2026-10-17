package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

func TestCoverageCommandReportsSeparateStatusMetrics(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "parity"), 0o755); err != nil {
		t.Fatal(err)
	}
	portMap := filepath.Join(root, "docs/parity/PORT_MAP.md")
	if err := os.WriteFile(portMap, []byte("| `a.ts` | `a.go` | ✅ |\n| `b.ts` | `b.go` | ✅ |\n| `c.ts` | `c.go` | 🟡 |\n| `index.ts` | `(barrel)` | n/a |\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarios := filepath.Join(root, "scenarios")
	if err := os.Mkdir(scenarios, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scenarios, "behavior.toml"), []byte("name = 'behavior'\ncovers = ['a.ts']\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestLedger(t, root, "test-mapping", []map[string]any{
		{"path": "a.test.ts", "disposition": "ported"},
		{"path": "b.test.ts", "disposition": "partial"},
		{"path": "c.test.ts", "disposition": "pending"},
		{"path": "d.test.ts", "disposition": "designed-out"},
	})
	writeTestLedger(t, root, "test-porting-policy", []map[string]any{
		{"path": "a.test.ts", "tags": []string{"hot-path"}},
		{"path": "b.test.ts", "tags": []string{"deferred-0.3.x"}},
		{"path": "c.test.ts", "tags": []string{"hot-path"}},
		{"path": "d.test.ts", "tags": []string{}},
	})
	badgePath := filepath.Join(root, "badge.svg")
	cmd := exec.CommandContext(t.Context(), "go", "run", ".", "-port-map", portMap, "-scenarios", scenarios, "-upstream", "", "-badge", badgePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("coverage command: %v\n%s", err, output)
	}
	for _, want := range []string{
		"## Status",
		"**Porting:** 2 / 3 intended-portable entries ✅ (66.7%)",
		"**Behavior verification:** 1 / 2 ported entries (50.0%)",
		"**Upstream test porting:** 1 ported / 1 partial / 1 pending",
		"1 deferred to 0.3.x",
		"test-mapping-v" + coding.UpstreamVersion + ".json",
		"test-porting-policy-v" + coding.UpstreamVersion + ".json",
	} {
		if !strings.Contains(string(output), want) {
			t.Errorf("report lacks %q:\n%s", want, output)
		}
	}
	badge, err := os.ReadFile(badgePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(badge), "Pi "+coding.UpstreamVersion+" port: 67% · hardening") {
		t.Fatalf("command badge uses wrong metric:\n%s", badge)
	}
}

func TestLoadTestPortingStatsDeferrals(t *testing.T) {
	mapping := []map[string]any{
		{"path": "ported.test.ts", "disposition": "ported"},
		{"path": "partial.test.ts", "disposition": "partial"},
		{"path": "pending.test.ts", "disposition": "pending"},
		{"path": "scenario.test.ts", "disposition": "scenario-covered"},
		{"path": "designed-out.test.ts", "disposition": "designed-out"},
		{"path": "divergence.test.ts", "disposition": "divergence"},
	}
	for _, tc := range []struct {
		name    string
		policy  []map[string]any
		missing bool
		want    testPortingStats
	}{
		{"missing policy", nil, true, testPortingStats{Ported: 1, Partial: 1, Pending: 1, Other: 3}},
		{"absent tag", []map[string]any{{"path": "partial.test.ts", "tags": []string{"hot-path"}}}, false,
			testPortingStats{Ported: 1, Partial: 1, Pending: 1, Other: 3, PolicyAvailable: true}},
		{"deferred partial and pending", []map[string]any{
			{"path": "partial.test.ts", "tags": []string{"deferred-0.3.x"}},
			{"path": "pending.test.ts", "tags": []string{"deferred-0.3.x"}},
		}, false, testPortingStats{Ported: 1, Partial: 1, Pending: 1, Other: 3, Deferred: 2, PolicyAvailable: true}},
		{"closed file is no longer deferred work", []map[string]any{{"path": "ported.test.ts", "tags": []string{"deferred-0.3.x"}}}, false,
			testPortingStats{Ported: 1, Partial: 1, Pending: 1, Other: 3, PolicyAvailable: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "docs", "parity"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestLedger(t, root, "test-mapping", mapping)
			if !tc.missing {
				writeTestLedger(t, root, "test-porting-policy", tc.policy)
			}
			got, err := loadTestPortingStats(root)
			if err != nil || got != tc.want {
				t.Fatalf("stats = %+v, %v; want %+v", got, err, tc.want)
			}
			if tc.missing && !strings.Contains(got.summaryLine(), "Deferral count unavailable") {
				t.Fatalf("missing policy reported as zero deferrals: %s", got.summaryLine())
			}
			if !tc.missing && got.Deferred == 0 && !strings.Contains(got.summaryLine(), "0 deferred to 0.3.x") {
				t.Fatalf("absent deferral tag not reported: %s", got.summaryLine())
			}
		})
	}
}

func TestLoadTestPortingStatsEmptyMapping(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "parity"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestLedger(t, root, "test-mapping", []map[string]any{})
	writeTestLedger(t, root, "test-porting-policy", []map[string]any{})
	got, err := loadTestPortingStats(root)
	if err != nil || got != (testPortingStats{PolicyAvailable: true}) {
		t.Fatalf("empty stats = %+v, %v", got, err)
	}
}

func TestLoadTestPortingStatsRejectsInvalidLedgers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ledger string
		data   string
		want   string
	}{
		{"missing mapping", "test-mapping", "", ""},
		{"malformed mapping", "test-mapping", "{", "unexpected end"},
		{"malformed policy", "test-porting-policy", "{", "unexpected end"},
		{"stale mapping", "test-mapping", `{"upstreamVersion":"stale"}`, "upstreamVersion"},
		{"stale policy", "test-porting-policy", `{"upstreamVersion":"stale"}`, "upstreamVersion"},
		{"unknown disposition", "test-mapping", `{"entries":[{"path":"a","disposition":"done"}]}`, "unknown test disposition"},
		{"duplicate mapping", "test-mapping", `{"entries":[{"path":"a","disposition":"ported"},{"path":"a","disposition":"pending"}]}`, "duplicate test path"},
		{"empty path", "test-mapping", `{"entries":[{"path":"","disposition":"ported"}]}`, "empty or duplicate"},
		{"unknown policy path", "test-porting-policy", `{"entries":[{"path":"unknown","tags":["deferred-0.3.x"]}]}`, "unknown or duplicate"},
		{"duplicate policy path", "test-porting-policy", `{"entries":[{"path":"a","tags":[]},{"path":"a","tags":[]}]}`, "unknown or duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "docs", "parity"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestLedger(t, root, "test-mapping", []map[string]any{{"path": "a", "disposition": "pending"}})
			writeTestLedger(t, root, "test-porting-policy", []map[string]any{})
			path := filepath.Join(root, "test/parity", "interfaces", tc.ledger+"-v"+coding.UpstreamVersion+".json")
			if tc.data == "" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				data := strings.Replace(tc.data, `{"entries":`, `{"upstreamVersion":"`+coding.UpstreamVersion+`","entries":`, 1)
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := loadTestPortingStats(root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid ledger error = %v, want %q", err, tc.want)
			}
		})
	}
}

func writeTestLedger(t *testing.T, root, name string, entries []map[string]any) {
	t.Helper()
	path := filepath.Join(root, "test/parity", "interfaces", name+"-v"+coding.UpstreamVersion+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"upstreamVersion": coding.UpstreamVersion, "entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
