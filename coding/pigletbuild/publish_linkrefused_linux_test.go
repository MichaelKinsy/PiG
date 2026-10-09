//go:build linux

package pigletbuild

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Android refuses hard links in Termux's data directory, so a Piglet script, record, and managed Binary artifact publish by a no-replace rename there; under Android's common umask 077 they keep their modes and leave no stage behind.
func TestPigletOutputsPublishWhereLinksAreRefused(t *testing.T) {
	if !testenv.RunWithHardLinksRefused(t) {
		return
	}
	syscall.Umask(0o077)
	dir := t.TempDir()
	assertPublished := func(path, want string, mode os.FileMode) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("%s = %q (err %v), want %q", path, data, err, want)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s mode = %v (err %v), want %v", path, info.Mode().Perm(), err, mode)
		}
		stages, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*.stage"))
		if len(stages) != 0 {
			t.Fatalf("stages remain after publishing %s: %v", path, stages)
		}
	}

	script := filepath.Join(dir, "out", "demo")
	if _, err := writeSourceScript(script, []byte("#!/bin/sh\n")); err != nil {
		t.Fatalf("writeSourceScript: %v", err)
	}
	assertPublished(script, "#!/bin/sh\n", 0o755)
	if _, err := writeSourceScript(script, []byte("other\n")); err == nil {
		t.Fatal("writeSourceScript over an existing script succeeded")
	}
	assertPublished(script, "#!/bin/sh\n", 0o755)

	record := pigletartifact.Record{Kind: pigletartifact.RecordKindResolution, Piglet: "demo", CreatedAt: "2026-01-01T00:00:00Z"}
	recordPath := filepath.Join(dir, "records", "resolution.json")
	if created, err := writeRecordFile(recordPath, record); err != nil || !created {
		t.Fatalf("writeRecordFile = %v, %v; want created", created, err)
	}
	data, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	assertPublished(recordPath, string(data), 0o644)

	source := filepath.Join(dir, "build", "demo")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, _, err := hashFile(source)
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dir, "artifacts", "demo")
	if created, err := writeManagedArtifact(source, artifact, digest); err != nil || !created {
		t.Fatalf("writeManagedArtifact = %v, %v; want created", created, err)
	}
	assertPublished(artifact, "binary", 0o755)
}
