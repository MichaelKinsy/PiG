package parity

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDivergenceQualityRejectsOutOfOrderRecords(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	script := filepath.Join(repoRoot, "automation", "ci", "check-divergence-quality.py")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "coding", "pigversion"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "coding", "pigversion", "pigversion.go"), []byte("package pigversion\nconst UpstreamVersion = \"0.84.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	core := "## D2 second\n\nRemove when: fixed.\nEvidence: test.\nSCRUTINIZED:approved\n\n## D1 first\n\nRemove when: fixed.\nEvidence: test.\nSCRUTINIZED:approved\n"
	if err := os.WriteFile(filepath.Join(root, "DIVERGENCES.md"), []byte(core), 0o644); err != nil {
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
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	script := filepath.Join(repoRoot, "automation", "ci", "check-divergence-quality.py")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "coding", "pigversion"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "coding", "pigversion", "pigversion.go"), []byte("package pigversion\nconst UpstreamVersion = \"0.83.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "## D1 stale\n\nWhat: differs.\nWhy: reason.\nRemove when: fixed.\nEvidence: pi v0.69.0 probe.\nSCRUTINIZED:pending\n"
	if err := os.WriteFile(filepath.Join(root, "DIVERGENCES.md"), []byte(body), 0o644); err != nil {
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
