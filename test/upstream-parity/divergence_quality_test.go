package parity

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestDivergenceQualityRejectsOutOfOrderRecords(t *testing.T) {
	repoRoot := testenv.ModuleRoot(t)
	script := filepath.Join(repoRoot, "automation", "ci", "check-divergence-quality.py")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "parity"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "coding", "pigversion"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "coding", "pigversion", "pigversion.go"), []byte("package pigversion\nconst UpstreamPackageVersion = \"0.84.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	core := "## D2 second\n\nRemove when: fixed.\nEvidence: test.\nSCRUTINIZED:approved\n\n## D1 first\n\nRemove when: fixed.\nEvidence: test.\nSCRUTINIZED:approved\n"
	if err := os.WriteFile(filepath.Join(root, "docs/parity/DIVERGENCES.md"), []byte(core), 0o644); err != nil {
		t.Fatal(err)
	}
	additive := "## D3 additive\n\nStock disposition: inert capability.\nRemove when: removed.\nEvidence: test.\nSCRUTINIZED:approved\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "additive-features.md"), []byte(additive), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", script, "--root", root)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("out-of-order divergence records passed:\n%s", output)
	}
	if !strings.Contains(string(output), "records must be in ascending numeric order: [2, 1]") {
		t.Fatalf("output did not identify numeric order:\n%s", output)
	}
}

func TestDivergenceQualityRejectsPendingAndStaleEvidence(t *testing.T) {
	repoRoot := testenv.ModuleRoot(t)
	script := filepath.Join(repoRoot, "automation", "ci", "check-divergence-quality.py")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "parity"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "coding", "pigversion"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "coding", "pigversion", "pigversion.go"), []byte("package pigversion\nconst UpstreamPackageVersion = \"0.83.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "## D1 stale\n\nWhat: differs.\nWhy: reason.\nRemove when: fixed.\nEvidence: pi v0.69.0 probe.\nSCRUTINIZED:pending\n"
	if err := os.WriteFile(filepath.Join(root, "docs/parity/DIVERGENCES.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	additive := "## D2 unused\n\nWhat: unused.\nRemove when: removed.\nEvidence: local test.\nStatus: caller-free.\nSCRUTINIZED:approved\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "additive-features.md"), []byte(additive), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", script, "--root", root)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("invalid divergence passed:\n%s", output)
	}
	for _, want := range []string{"missing SCRUTINIZED:approved", "stale upstream evidence", "caller-free code must be deleted", "additive record must have one Stock disposition"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestDivergenceQualityRejectsNodeOnlyParityProse(t *testing.T) {
	repoRoot := testenv.ModuleRoot(t)
	script := filepath.Join(repoRoot, "automation", "ci", "check-divergence-quality.py")
	for _, c := range []struct {
		name, row string
		fail      bool
	}{
		{"node runtime only", "| keybinding hints | x | y | partial: Node runtime only; Go/Rust/Python have no key-hint helpers |", true},
		{"node only", "| a | b | c | partial: Node only |", true},
		{"native sdks have none", "| a | b | c | complete; Go/Rust/Python have no tool here |", true},
		{"mapped to the ledger", "| a | b | c | partial: Node serves Pi's own code; the Go, Rust and Python cells are `gap:SdkTuiHelpers` in `docs/extension-sdk-exports.md` |", false},
		{"implementation detail", "Batched dispatch runs in Node realms; only the Node runtime batches, by design.", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"docs/parity", "internal/coding/pigversion"} {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{
				"internal/coding/pigversion/pigversion.go": "package pigversion\nconst UpstreamPackageVersion = \"0.84.0\"\n",
				"docs/parity/DIVERGENCES.md":               "## D1 first\n\nRemove when: fixed.\nEvidence: test.\nSCRUTINIZED:approved\n",
				"docs/additive-features.md":                "## D2 additive\n\nStock disposition: inert capability.\nRemove when: removed.\nEvidence: test.\nSCRUTINIZED:approved\n",
				"docs/extension-api-parity.md":             c.row + "\n",
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			output, err := exec.Command("python3", script, "--root", root).CombinedOutput()
			if c.fail && (err == nil || !strings.Contains(string(output), "docs/extension-sdk-exports.md")) {
				t.Fatalf("Node-only prose passed:\n%s", output)
			}
			if !c.fail && err != nil {
				t.Fatalf("legitimate row failed:\n%s", output)
			}
		})
	}
}
