package parity

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func divergenceScript(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "automation", "ci", "check-divergence-consistency.sh")
}

func writeDivergenceFixture(t *testing.T, core, additive, markers string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"DIVERGENCES.md":            core,
		"docs/additive-features.md": additive,
		"marker.go":                 "package fixture\n" + markers,
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runDivergenceScript(t *testing.T, root string) (string, error) {
	t.Helper()
	cmd := exec.Command(testenv.Bash(t), divergenceScript(t))
	cmd.Env = append(os.Environ(), "PIG_DIVERGENCE_ROOT="+root)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func TestDivergenceConsistencyRejectsIDsReusedAcrossLedgers(t *testing.T) {
	root := writeDivergenceFixture(t, "## D1 core\n", "## D1 additive\n", "// pig divergence (D"+"1): fixture\n")
	output, err := runDivergenceScript(t, root)
	if err == nil {
		t.Fatalf("duplicate global ID passed:\n%s", output)
	}
	if !strings.Contains(output, "IDs must be globally unique") || !strings.Contains(output, "D1") {
		t.Fatalf("duplicate failure did not identify D1:\n%s", output)
	}
}

func TestDivergenceConsistencyRejectsUntypedMarker(t *testing.T) {
	root := writeDivergenceFixture(t, "", "", "// pig divergence: hidden difference\n")
	output, err := runDivergenceScript(t, root)
	if err == nil {
		t.Fatalf("untyped marker passed:\n%s", output)
	}
	if !strings.Contains(output, "untyped divergence/additive comments") {
		t.Fatalf("unexpected output:\n%s", output)
	}
}

func TestDivergenceConsistencyRejectsTestOnlyMarker(t *testing.T) {
	root := writeDivergenceFixture(t, "## D1 core\n", "", "")
	marker := "package fixture\n// pig divergence (D" + "1): test only\n"
	if err := os.WriteFile(filepath.Join(root, "marker_test.go"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := runDivergenceScript(t, root)
	if err == nil {
		t.Fatalf("test-only marker passed:\n%s", output)
	}
	if !strings.Contains(output, "core divergences without 'pig divergence' source markers") {
		t.Fatalf("unexpected output:\n%s", output)
	}
}

func TestDivergenceConsistencyRejectsWrongMarkerClass(t *testing.T) {
	root := writeDivergenceFixture(
		t,
		"## D1 core\n",
		"## D2 additive\n",
		"// pig additive (D"+"1): wrong class\n// pig divergence (D"+"2): wrong class\n",
	)
	output, err := runDivergenceScript(t, root)
	if err == nil {
		t.Fatalf("wrong marker classes passed:\n%s", output)
	}
	for _, want := range []string{
		"core divergences without 'pig divergence' source markers",
		"'pig divergence' markers without core divergence records",
		"additive features without 'pig additive' source markers",
		"'pig additive' markers without additive feature records",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}
