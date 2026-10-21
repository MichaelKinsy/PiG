package subprocess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeContentHashFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The check a cell build runs before it publishes reaches the extension the cell builder gets, for a packed Go cell and an isolated
// factory alike, and it fails once the source no longer hashes to the ContentHash the planner used.
func TestFillContentHashGivesCellBuildsACheckOfTheSourceTheyHashed(t *testing.T) {
	dir := writeContentHashFixture(t, map[string]string{"go.mod": "module racing\ngo 1.26\n", "main.go": "package main\n\nfunc main() {}\n"})
	cfg := ExtConfig{Name: "racing", Source: dir, Package: "racing", Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "factory", Factory: "Extension", ModulePath: "racing"}
	fillContentHash(&cfg)
	if cfg.ContentHash == "" || cfg.sourceCheck == nil {
		t.Fatalf("fillContentHash left ContentHash %q and a nil check %t", cfg.ContentHash, cfg.sourceCheck == nil)
	}
	packed, err := CellSpec{Strategy: CellStrategyPackedGo, Extensions: []ExtConfig{cfg}}.GoExtensions()
	if err != nil || len(packed) != 1 || packed[0].Verify == nil {
		t.Fatalf("packed cell extensions = %+v, %v; want one with the source check", packed, err)
	}
	isolated, err := goExtensionFromConfig(cfg)
	if err != nil || isolated.Verify == nil {
		t.Fatalf("isolated factory extension = %+v, %v; want the source check", isolated, err)
	}
	if err := packed[0].Verify(); err != nil {
		t.Fatalf("check of an unchanged source = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { println(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, check := range map[string]func() error{"packed": packed[0].Verify, "isolated": isolated.Verify} {
		if err := check(); err == nil || !strings.Contains(err.Error(), "changed while") {
			t.Errorf("%s check of an edited source = %v; want an error that says the source changed while it was built", name, err)
		}
	}
}

// A hash the caller supplied is not ours to check, and a source the cargo build writes into (Cargo.lock) is still the same source.
func TestFillContentHashChecksOnlyWhatItHashedAndIgnoresALockfileTheBuildWrites(t *testing.T) {
	supplied := ExtConfig{Name: "pinned", Source: t.TempDir(), ContentHash: "pinned-by-a-piglet"}
	fillContentHash(&supplied)
	if supplied.ContentHash != "pinned-by-a-piglet" || supplied.sourceCheck != nil {
		t.Fatalf("a supplied hash became %q with a check %t", supplied.ContentHash, supplied.sourceCheck != nil)
	}

	dir := writeContentHashFixture(t, map[string]string{"Cargo.toml": "[package]\nname = \"racing\"\n", "src/main.rs": "fn main() {}\n"})
	cfg := ExtConfig{Name: "racing", Source: dir}
	fillContentHash(&cfg)
	if cfg.sourceCheck == nil {
		t.Fatal("no check for a Rust crate")
	}
	if err := os.WriteFile(filepath.Join(dir, "Cargo.lock"), []byte("# written by cargo during the build\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.sourceCheck(); err != nil {
		t.Fatalf("check after cargo wrote Cargo.lock = %v; the source did not change", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.rs"), []byte("fn main() { println!(); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.sourceCheck(); err == nil {
		t.Fatal("check after a source edit passed")
	}
}
