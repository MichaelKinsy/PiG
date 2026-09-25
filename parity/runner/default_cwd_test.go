//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMkdirTempFixedWidth(t *testing.T) {
	dir, err := mkdirTempFixed("parity-snap-cwd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	base := filepath.Base(dir)
	digits := strings.TrimPrefix(base, "parity-snap-cwd-")
	if len(digits) != snapshotIDDigits || strings.Trim(digits, "0123456789") != "" {
		t.Fatalf("snapshot dir %q: want %d-digit suffix", base, snapshotIDDigits)
	}
}

func TestMaskCWDSnapshotIDsKeepsWidth(t *testing.T) {
	pig := "/tmp/parity-snap-cwd-0123456789 (main)\n~/x/parity-snap-cwd-98..."
	pi := "/tmp/parity-snap-cwd-9876543210 (main)\n~/x/parity-snap-cwd-12..."
	gotPig, gotPi := maskCWDSnapshotIDs(pig), maskCWDSnapshotIDs(pi)
	if gotPig != gotPi {
		t.Fatalf("masked outputs differ:\n%q\n%q", gotPig, gotPi)
	}
	if len(gotPig) != len(pig) {
		t.Fatalf("masking changed width: %d -> %d", len(pig), len(gotPig))
	}
	if want := "/tmp/parity-snap-cwd-0000000000 (main)\n~/x/parity-snap-cwd-00..."; gotPig != want {
		t.Fatalf("masked = %q, want %q", gotPig, want)
	}
	if other := "parity-snap-PIG_HOME-0123456789"; maskCWDSnapshotIDs(other) != other {
		t.Fatalf("masked a non-cwd snapshot name")
	}
}

func TestDefaultCWDIsOutsideCheckout(t *testing.T) {
	dir, err := defaultCWD(t)
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	checkout, err = filepath.EvalSymlinks(checkout)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(dir, checkout+string(filepath.Separator)) {
		t.Fatalf("default cwd %s is inside the checkout %s", dir, checkout)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatalf("default cwd lacks the fixture AGENTS.md: %v", err)
	}
}

func TestPiPackageRootFindsOwningPackage(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "node_modules", "@earendil-works", "pi-coding-agent")
	if err := os.MkdirAll(filepath.Join(pkg, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"@earendil-works/pi-coding-agent"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(pkg, "dist", "cli.js")
	if err := os.WriteFile(cli, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "pi")
	if err := os.Symlink(cli, bin); err != nil {
		t.Fatal(err)
	}
	got, err := piPackageRoot(bin)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(pkg)
	if got != want {
		t.Fatalf("piPackageRoot = %s, want %s", got, want)
	}
}

func TestPromptPathLengthsBalance(t *testing.T) {
	digits := strings.Repeat("0", snapshotIDDigits)
	pigHome := filepath.Join(promptPathRoot, pigHomePrefix+digits)
	piPackage := filepath.Join(promptPathRoot, piPackagePrefix+digits, piPackageLink)
	pig := 2*(len(pigHome)+len("/docs")) + d22ConstantGap
	pi := 3 * len(piPackage)
	if pig != pi {
		t.Fatalf("docs path characters: pig %d, pi %d; the system prompts would differ in length", pig, pi)
	}
	if pigHomePrefix != "parity-snap-PIG_HOME-" {
		t.Fatalf("pigHomePrefix %q must match snapshotEnvDirs naming", pigHomePrefix)
	}
}
