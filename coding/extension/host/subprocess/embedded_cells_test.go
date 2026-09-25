package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func TestHost_LoadEmbeddedCellsStartsIsolatedBinaryWithoutSource(t *testing.T) {
	binPath := buildFixtureExt(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return nil, nil })
	t.Cleanup(func() { h.Shutdown("test done") })
	loaded, errs := h.LoadEmbeddedCells(ctx, []EmbeddedCell{{
		Language:   "go",
		Strategy:   string(CellStrategyIsolated),
		Key:        "fixture",
		BinaryPath: binPath,
		Extensions: []EmbeddedExtension{{Name: "fixture", Hash: "h1"}},
	}})
	if len(errs) > 0 {
		t.Fatalf("LoadEmbeddedCells errors: %v", errs)
	}
	if len(loaded) != 1 || h.ExtensionCount() != 1 {
		t.Fatalf("extensions len/count = %d/%d", len(loaded), h.ExtensionCount())
	}
	if _, ok := h.Extensions()[0].Tools["greet"]; !ok {
		t.Fatalf("fixture tools = %v, want greet", h.Extensions()[0].Tools)
	}
	h.mu.Lock()
	oldManaged := h.exts["fixture"]
	h.mu.Unlock()
	reloaded, err := h.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	newManaged := h.exts["fixture"]
	h.mu.Unlock()
	if len(reloaded) != 1 || newManaged == nil || newManaged == oldManaged {
		t.Fatalf("embedded reload = %v managed=%p old=%p, want fresh retained extension", reloaded, newManaged, oldManaged)
	}
}

func TestEmbeddedPackedCellCanActivateOneLogicalMember(t *testing.T) {
	rootA := writePackedFactoryModule(t, "example.com/authpacked/a", "auth-owner", "owner-tool")
	rootB := writePackedFactoryModule(t, "example.com/authpacked/b", "unrelated", "unrelated-tool")
	configs := []ExtConfig{
		packedFactoryConfig("auth-owner", rootA, "example.com/authpacked/a", "ha"),
		packedFactoryConfig("unrelated", rootB, "example.com/authpacked/b", "hb"),
	}
	exts := make([]runtimecell.GoExtension, 0, len(configs))
	for _, config := range configs {
		extension, err := goExtensionFromConfig(config)
		if err != nil {
			t.Fatal(err)
		}
		exts = append(exts, extension)
	}
	cell, err := runtimecell.BuildGoPackedCell(t.Context(), t.TempDir(), "auth-packed", exts)
	if err != nil {
		t.Fatal(err)
	}
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, errs := host.LoadEmbeddedCells(t.Context(), []EmbeddedCell{{
		Language: "go", Key: cell.Key, Strategy: string(CellStrategyPackedGo), BinaryPath: cell.BinaryPath,
		Extensions: []EmbeddedExtension{{Name: "auth-owner", Hash: "ha"}},
	}})
	if len(errs) > 0 || len(loaded) != 1 || loaded[0].Name != "auth-owner" {
		t.Fatalf("loaded=%#v errors=%v", loaded, errs)
	}
	if host.ExtensionCount() != 1 || host.Extensions()[0].Name != "auth-owner" {
		t.Fatalf("embedded owner-only registry = %#v", host.Extensions())
	}
}

type embeddedOwnerCellFixture struct {
	cell       EmbeddedCell
	owner      string
	unrelated  string
	sentinel   string
	buildLabel string
}

func TestEmbeddedPackedOwnerActivationByLanguage(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T) embeddedOwnerCellFixture
	}{
		{name: "go", build: buildGoEmbeddedOwnerCell},
		{name: "rust", build: buildRustEmbeddedOwnerCell},
		{name: "python", build: buildPythonEmbeddedOwnerCell},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := tc.build(t)
			if fixture.buildLabel == "Go" || fixture.buildLabel == "Rust" {
				// Embedded native cells execute from their built artifact. Remove
				// compiler discovery before load so a runtime source rebuild cannot
				// accidentally satisfy this acceptance path.
				t.Setenv("PATH", t.TempDir())
			}
			t.Setenv("PIG_TEST_UNRELATED_SENTINEL", fixture.sentinel)
			t.Setenv("PIG_TEST_FAIL_UNRELATED", "1")

			ownerCell := fixture.cell
			ownerCell.Extensions = []EmbeddedExtension{fixture.cell.Extensions[0]}
			ownerHost := NewHost(t.TempDir())
			loaded, errs := ownerHost.LoadEmbeddedCells(t.Context(), []EmbeddedCell{ownerCell})
			if len(errs) != 0 || len(loaded) != 1 || loaded[0].Name != fixture.owner {
				ownerHost.Shutdown("owner load failed")
				t.Fatalf("%s owner load = %#v, errors = %v", fixture.buildLabel, loaded, errs)
			}
			if ownerHost.ExtensionCount() != 1 || ownerHost.Extensions()[0].Name != fixture.owner {
				ownerHost.Shutdown("owner registry mismatch")
				t.Fatalf("%s owner registry = %#v", fixture.buildLabel, ownerHost.Extensions())
			}
			tool, ok := loaded[0].Tools["owner-tool"]
			if !ok {
				ownerHost.Shutdown("owner tool missing")
				t.Fatalf("%s owner tools = %#v, want owner-tool", fixture.buildLabel, loaded[0].Tools)
			}
			if _, err := tool.Definition.Execute(t.Context(), "owner-call", json.RawMessage(`{}`), nil); err != nil {
				ownerHost.Shutdown("owner tool failed")
				t.Fatalf("%s embedded owner tool: %v", fixture.buildLabel, err)
			}
			if _, err := os.Stat(fixture.sentinel); !os.IsNotExist(err) {
				ownerHost.Shutdown("unrelated factory ran")
				t.Fatalf("%s unrelated registration sentinel exists: %v", fixture.buildLabel, err)
			}
			assertPackedCellStable(t, ownerHost, fixture.cell.Key, fixture.owner)

			ownerHost.mu.Lock()
			ownerConn := ownerHost.exts[fixture.owner].conn
			ownerHost.mu.Unlock()
			if err := ownerConn.Close("owner command complete"); err != nil && !errors.Is(err, net.ErrClosed) {
				ownerHost.Shutdown("close failed")
				t.Fatal(err)
			}
			waitForPackedQuarantine(t, ownerHost, fixture.cell.Key)
			if reason := ownerHost.QuarantinedCells()[fixture.cell.Key]; reason != "packed process exited" {
				ownerHost.Shutdown("unexpected owner exit")
				t.Fatalf("%s owner-only process exit = %q", fixture.buildLabel, reason)
			}
			if ownerHost.ExtensionCount() != 0 {
				ownerHost.Shutdown("test done")
				t.Fatalf("%s failed owner process left registry = %#v", fixture.buildLabel, ownerHost.Extensions())
			}
			ownerHost.Shutdown("test done")

			t.Setenv("PIG_TEST_FAIL_UNRELATED", "0")
			if err := os.Remove(fixture.sentinel); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			allHost := NewHost(t.TempDir())
			t.Cleanup(func() { allHost.Shutdown("test done") })
			loaded, errs = allHost.LoadEmbeddedCells(t.Context(), []EmbeddedCell{fixture.cell})
			if len(errs) != 0 || len(loaded) != 2 || allHost.ExtensionCount() != 2 {
				t.Fatalf("%s ordinary load = %#v, registry = %#v, errors = %v", fixture.buildLabel, loaded, allHost.Extensions(), errs)
			}
			if _, err := os.Stat(fixture.sentinel); err != nil {
				t.Fatalf("%s ordinary load did not activate unrelated member: %v", fixture.buildLabel, err)
			}
			allHost.mu.Lock()
			allProcess := allHost.exts[fixture.owner].proc
			allHost.mu.Unlock()
			if err := allProcess.Kill(); err != nil {
				t.Fatal(err)
			}
			waitForPackedQuarantine(t, allHost, fixture.cell.Key)
			if allHost.ExtensionCount() != 0 {
				t.Fatalf("%s failed packed process left members = %#v", fixture.buildLabel, allHost.Extensions())
			}
		})
	}
}

func buildGoEmbeddedOwnerCell(t *testing.T) embeddedOwnerCellFixture {
	owner, unrelated := "owner-go", "unrelated-go"
	ownerRoot := writePackedFactoryModule(t, "example.com/owner/go", owner, "owner-tool")
	unrelatedRoot := writePackedFactoryModule(t, "example.com/unrelated/go", unrelated, "unrelated-tool")
	addGoUnrelatedSentinel(t, filepath.Join(unrelatedRoot, "ext.go"))
	cell, err := runtimecell.BuildGoPackedCell(t.Context(), t.TempDir(), "owner-go-cell", []runtimecell.GoExtension{
		{Name: owner, Root: ownerRoot, ModulePath: "example.com/owner/go", Package: "example.com/owner/go", Factory: "Extension", Hash: "owner"},
		{Name: unrelated, Root: unrelatedRoot, ModulePath: "example.com/unrelated/go", Package: "example.com/unrelated/go", Factory: "Extension", Hash: "unrelated"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return embeddedOwnerCellFixture{cell: EmbeddedCell{Language: "go", Key: cell.Key, Strategy: string(CellStrategyPackedGo), BinaryPath: cell.BinaryPath, Extensions: []EmbeddedExtension{{Name: owner, Hash: "owner"}, {Name: unrelated, Hash: "unrelated"}}}, owner: owner, unrelated: unrelated, sentinel: filepath.Join(t.TempDir(), "go-sentinel"), buildLabel: "Go"}
}

func buildRustEmbeddedOwnerCell(t *testing.T) embeddedOwnerCellFixture {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skipf("cargo not found: %v", err)
	}
	owner, unrelated := "owner-rust", "unrelated-rust"
	ownerRoot := writePackedRustFactoryCrate(t, "owner_rust", owner, "owner-tool")
	unrelatedRoot := writePackedRustFactoryCrate(t, "unrelated_rust", unrelated, "unrelated-tool")
	addRustUnrelatedSentinel(t, filepath.Join(unrelatedRoot, "src", "lib.rs"))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	cell, err := runtimecell.BuildRustPackedCell(ctx, t.TempDir(), "owner-rust-cell", []runtimecell.RustExtension{
		{Name: owner, Root: ownerRoot, Package: "owner_rust", Factory: "new_extension", Hash: "owner"},
		{Name: unrelated, Root: unrelatedRoot, Package: "unrelated_rust", Factory: "new_extension", Hash: "unrelated"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return embeddedOwnerCellFixture{cell: EmbeddedCell{Language: "rust", Key: cell.Key, Strategy: string(CellStrategyPackedRust), BinaryPath: cell.BinaryPath, Extensions: []EmbeddedExtension{{Name: owner, Hash: "owner"}, {Name: unrelated, Hash: "unrelated"}}}, owner: owner, unrelated: unrelated, sentinel: filepath.Join(t.TempDir(), "rust-sentinel"), buildLabel: "Rust"}
}

func buildPythonEmbeddedOwnerCell(t *testing.T) embeddedOwnerCellFixture {
	python := findPythonExecutable(runtime.GOOS, exec.LookPath)
	if _, err := exec.LookPath(python); err != nil {
		t.Skipf("%s not found: %v", python, err)
	}
	owner, unrelated := "owner-python", "unrelated-python"
	ownerRoot := writePackedPythonFactoryModule(t, "owner_python", owner, "owner-tool")
	unrelatedRoot := writePackedPythonFactoryModule(t, "unrelated_python", unrelated, "unrelated-tool")
	addPythonUnrelatedSentinel(t, filepath.Join(unrelatedRoot, "unrelated_python.py"))
	cell, err := runtimecell.BuildPythonPackedCell(t.Context(), t.TempDir(), "owner-python-cell", []runtimecell.PythonExtension{
		{Name: owner, Root: ownerRoot, Package: "owner_python", Factory: "new_extension", Hash: "owner"},
		{Name: unrelated, Root: unrelatedRoot, Package: "unrelated_python", Factory: "new_extension", Hash: "unrelated"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return embeddedOwnerCellFixture{cell: EmbeddedCell{Language: "python", Key: cell.Key, Strategy: string(CellStrategyPackedPython), BinaryPath: cell.BinaryPath, Extensions: []EmbeddedExtension{{Name: owner, Hash: "owner"}, {Name: unrelated, Hash: "unrelated"}}}, owner: owner, unrelated: unrelated, sentinel: filepath.Join(t.TempDir(), "python-sentinel"), buildLabel: "Python"}
}

func addGoUnrelatedSentinel(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(data), "\"fmt\"", "\"fmt\"\n    \"os\"", 1)
	source = strings.Replace(source, "func Extension() *sdk.Extension {", `func Extension() *sdk.Extension {
	if path := os.Getenv("PIG_TEST_UNRELATED_SENTINEL"); path != "" {
		_ = os.WriteFile(path, []byte("called"), 0o644)
	}
	if os.Getenv("PIG_TEST_FAIL_UNRELATED") == "1" { panic("unrelated factory failure") }`, 1)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func addRustUnrelatedSentinel(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(data), "pub fn new_extension() -> Extension {", `pub fn new_extension() -> Extension {
    if let Ok(path) = std::env::var("PIG_TEST_UNRELATED_SENTINEL") { let _ = std::fs::write(path, "called"); }
    if std::env::var("PIG_TEST_FAIL_UNRELATED").as_deref() == Ok("1") { panic!("unrelated factory failure"); }`, 1)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func addPythonUnrelatedSentinel(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(data), "import pig_sdk", "import os\nimport pathlib\nimport pig_sdk", 1)
	source = strings.Replace(source, "def new_extension():", `def new_extension():
    sentinel = os.environ.get("PIG_TEST_UNRELATED_SENTINEL")
    if sentinel:
        pathlib.Path(sentinel).write_text("called")
    if os.environ.get("PIG_TEST_FAIL_UNRELATED") == "1":
        raise RuntimeError("unrelated factory failure")`, 1)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertPackedCellStable(t *testing.T, host *Host, key, owner string) {
	t.Helper()
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
	if reason := host.QuarantinedCells()[key]; reason != "" {
		t.Fatalf("owner-only packed cell exited after registration: %s", reason)
	}
	if host.ExtensionCount() != 1 || host.Extensions()[0].Name != owner {
		t.Fatalf("owner-only packed cell changed registry = %#v", host.Extensions())
	}
}

func waitForPackedQuarantine(t *testing.T, host *Host, key string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if host.QuarantinedCells()[key] != "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("packed cell %q was not quarantined", key)
}

// With no connect deadline, a packed member whose factory fails while the
// shared process keeps serving its siblings must still end the host's wait
// for it: the runner reports the failure on the member's socket, and the load
// returns a visible error naming the member instead of waiting forever.
func TestPackedMemberFactoryFailureEndsTheLoad(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T) embeddedOwnerCellFixture
	}{
		{name: "rust", build: buildRustEmbeddedOwnerCell},
		{name: "python", build: buildPythonEmbeddedOwnerCell},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := tc.build(t)
			t.Setenv("PIG_TEST_UNRELATED_SENTINEL", fixture.sentinel)
			t.Setenv("PIG_TEST_FAIL_UNRELATED", "1")
			host := NewHost(t.TempDir())
			t.Cleanup(func() { host.Shutdown("test done") })
			done := make(chan []error, 1)
			go func() {
				_, errs := host.LoadEmbeddedCells(t.Context(), []EmbeddedCell{fixture.cell})
				done <- errs
			}()
			select {
			case errs := <-done:
				failed := false
				for _, err := range errs {
					var loadErr *LoadError
					failed = failed || (errors.As(err, &loadErr) && loadErr.Extension == fixture.unrelated)
				}
				if !failed {
					t.Fatalf("%s load errors = %v, want a load error for %s", fixture.buildLabel, errs, fixture.unrelated)
				}
			case <-time.After(testbudget.Wait(t)):
				t.Fatalf("%s load never ended after a member factory failed", fixture.buildLabel)
			}
		})
	}
}
