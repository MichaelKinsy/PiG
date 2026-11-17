//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/modfile"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// retirementExtName is the name every fixture of the rows registers under, and so the name its socket path derives from.
const retirementExtName = "retire-probe"

// retirementGatesEnv names the directory the fixtures read their gates from: factory-hold (set by the test) makes a factory announce itself with factory-holding and wait until the file is gone, release-command (set by the test) ends the command the fixture holds after its reload, and reloaded and pinged are the markers the fixture writes.
const retirementGatesEnv = "PIG_RETIREMENT_GATES"

// retirementRow is one runtime in one mode: the config that loads its fixture, and the cell strategy the host must plan for it, so a packed row cannot silently run isolated or the reverse.
type retirementRow struct {
	name     string
	strategy CellStrategy
	config   func(t *testing.T) ExtConfig
}

func retirementFixtureSource(t *testing.T, runtimeDir, file string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("testdata", "socket-retirement", runtimeDir, file))
	if err != nil {
		t.Fatal(err)
	}
	return string(source)
}

func writeRetirementFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func retirementRustCargo(t *testing.T, dir, packageName string) {
	t.Helper()
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk-rs"))
	if err != nil {
		t.Fatal(err)
	}
	writeRetirementFile(t, dir, "Cargo.toml", fmt.Sprintf("[package]\nname = %q\nversion = \"0.0.0\"\nedition = \"2024\"\n\n[dependencies]\npig-sdk = { path = %q }\nserde_json = \"1\"\n", packageName, filepath.ToSlash(sdkRoot)))
}

func retirementNodeConfig(t *testing.T, isolation string) ExtConfig {
	t.Helper()
	skipWithoutNodeExtensions(t)
	requireTool(t, "node")
	dir := t.TempDir()
	writeRetirementFile(t, dir, "index.mjs", retirementFixtureSource(t, "node", "index.mjs"))
	return ExtConfig{Name: retirementExtName, Source: filepath.Join(dir, "index.mjs"), Enabled: true, Isolation: isolation, RuntimeKind: "subprocess", RuntimeLanguage: "node", EntrypointKind: "factory", SDKName: "pi-node"}
}

func retirementLispConfig(t *testing.T, isolation string) ExtConfig {
	t.Helper()
	requireTool(t, toolchain.SBCLExecutable(runtime.GOOS, exec.LookPath))
	withLispHooks(t, true)
	dir := t.TempDir()
	writeRetirementFile(t, dir, "retire-probe.lisp", retirementFixtureSource(t, "lisp", "retire-probe.lisp"))
	return ExtConfig{Name: retirementExtName, Enabled: true, Isolation: isolation, Source: dir, Factory: "new-extension", SDKName: "pig-sdk-lisp", Package: "retire-probe", RuntimeKind: "subprocess", RuntimeLanguage: "lisp", EntrypointKind: "factory"}
}

var retirementRows = []retirementRow{
	{"go/isolated", CellStrategyIsolated, func(t *testing.T) ExtConfig {
		dir := t.TempDir()
		const module = "example.com/retire/goisolated"
		source := strings.Replace(retirementFixtureSource(t, "go", "ext.go"), "package ext\n", "package main\n", 1) + "\nfunc main() {\n\tif err := Extension().Run(); err != nil {\n\t\tpanic(err)\n\t}\n}\n"
		writeRetirementFile(t, dir, "main.go", source)
		sdkRoot := filepath.Join(findModuleRoot(t), "extensions", "sdk")
		writeRetirementFile(t, dir, "go.mod", fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", module, modfile.AutoQuote(filepath.ToSlash(sdkRoot))))
		return ExtConfig{Name: retirementExtName, Source: dir, Enabled: true, Isolation: "isolated", RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "standalone"}
	}},
	{"go/packed", CellStrategyPackedGo, func(t *testing.T) ExtConfig {
		dir := t.TempDir()
		const module = "example.com/retire/gopacked"
		writeRetirementFile(t, dir, "go.mod", fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", module))
		writeRetirementFile(t, dir, "ext.go", retirementFixtureSource(t, "go", "ext.go"))
		return packedFactoryConfig(retirementExtName, dir, module, "retire-go")
	}},
	{"node/isolated", CellStrategyIsolated, func(t *testing.T) ExtConfig { return retirementNodeConfig(t, "isolated") }},
	{"node/packed", CellStrategyPackedNode, func(t *testing.T) ExtConfig { return retirementNodeConfig(t, "") }},
	{"python/isolated", CellStrategyIsolated, func(t *testing.T) ExtConfig {
		requireTool(t, toolchain.PythonExecutable(runtime.GOOS, exec.LookPath))
		dir := t.TempDir()
		// A script run by path finds the SDK as the python-sdk-fixture does: its own sys.path entry.
		sdkRoot := filepath.Join(findModuleRoot(t), "extensions", "sdk-py")
		writeRetirementFile(t, dir, "retire_probe.py", fmt.Sprintf("import sys\n\nsys.path.insert(0, %q)\n\n", filepath.ToSlash(sdkRoot))+retirementFixtureSource(t, "python", "retire_probe.py"))
		return ExtConfig{Name: retirementExtName, Path: filepath.Join(dir, "retire_probe.py"), Enabled: true, RuntimeLanguage: "python"}
	}},
	{"python/packed", CellStrategyPackedPython, func(t *testing.T) ExtConfig {
		requireTool(t, toolchain.PythonExecutable(runtime.GOOS, exec.LookPath))
		dir := t.TempDir()
		writeRetirementFile(t, dir, "retire_probe.py", retirementFixtureSource(t, "python", "retire_probe.py"))
		return packedPythonFactoryConfig(retirementExtName, dir, "retire_probe", "retire-python")
	}},
	{"rust/isolated", CellStrategyIsolated, func(t *testing.T) ExtConfig {
		requireTool(t, "cargo")
		dir := t.TempDir()
		retirementRustCargo(t, dir, "retire_probe_rust_isolated")
		writeRetirementFile(t, dir, "src/main.rs", retirementFixtureSource(t, "rust", "lib.rs")+"\nfn main() {\n    if let Err(err) = new_extension().run() {\n        eprintln!(\"{err}\");\n        std::process::exit(1);\n    }\n}\n")
		return ExtConfig{Name: retirementExtName, Source: dir, Enabled: true, Isolation: "isolated", RuntimeKind: "subprocess", RuntimeLanguage: "rust", EntrypointKind: "standalone"}
	}},
	{"rust/packed", CellStrategyPackedRust, func(t *testing.T) ExtConfig {
		requireTool(t, "cargo")
		dir := t.TempDir()
		retirementRustCargo(t, dir, "retire_probe_rust_packed")
		writeRetirementFile(t, dir, "src/lib.rs", retirementFixtureSource(t, "rust", "lib.rs"))
		return packedRustFactoryConfig(retirementExtName, dir, "retire_probe_rust_packed", "retire-rust")
	}},
	{"lisp/isolated", CellStrategyIsolated, func(t *testing.T) ExtConfig { return retirementLispConfig(t, "isolated") }},
	{"lisp/packed", CellStrategyPackedLisp, func(t *testing.T) ExtConfig { return retirementLispConfig(t, "shared-ok") }},
}

func touchRetirementGate(t *testing.T, gates, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(gates, name), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// awaitRetirementGate waits until the fixture has written the marker name, and fails at once when one of the calls the row has in flight returns first: none may, until the row releases it.
func awaitRetirementGate(t *testing.T, gates, name, what string, inFlight ...<-chan error) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		if _, err := os.Stat(filepath.Join(gates, name)); err == nil {
			return
		}
		for _, call := range inFlight {
			select {
			case err := <-call:
				t.Fatalf("a call returned (error %v) while the row waited for %s", err, what)
			default:
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func awaitRetirementCall(t *testing.T, call <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-call:
		return err
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
}

// A command that awaits `ctx.reload()` keeps its generation running until it returns, which can be after the next reload staged a successor and began to listen for it. The deferred stop of the replaced generation must leave the successor's socket alone: when the two share a socket path, removing the file leaves the successor's process unable to connect (ENOENT), and the reload that staged it never completes (a prompt after /reload that never answers). Each row runs that sequence with the SDK's own process, ordered by gates the fixtures hold instead of by time: generation 1's command reloads (generation 2) and holds; the next reload stages generation 3, whose factory holds before its SDK connects, with the host listening; generation 1's command returns and its deferred stop has run; only then does generation 3's factory go on to connect, load, and answer a command.
func TestADeferredRetirementLeavesTheNextGenerationConnectableInEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts SDK fixtures")
	}
	for _, row := range retirementRows {
		t.Run(row.name, func(t *testing.T) {
			gates := t.TempDir()
			t.Setenv(retirementGatesEnv, gates)
			cfg := row.config(t)
			if cells := PlanCells([]ExtConfig{cfg}, nil); len(cells) != 1 || cells[0].Strategy != row.strategy {
				t.Fatalf("PlanCells = %+v, want one %s cell", cells, row.strategy)
			}
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			bridge := NewUIBridge(func() {})
			bridge.SetActions(&HostCallbacks{Reload: func(ctx context.Context) error { _, err := h.Reload(ctx); return err }})
			h.SetUIBridge(bridge)
			h.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{cfg}, nil })
			t.Cleanup(func() { h.Shutdown("test done") })
			// A row that fails must not leave a fixture waiting on a gate Shutdown cannot open.
			t.Cleanup(func() {
				_ = os.Remove(filepath.Join(gates, "factory-hold"))
				_ = os.WriteFile(filepath.Join(gates, "release-command"), nil, 0o644)
			})
			ctx, cancel := buildContext(t)
			defer cancel()

			// Generation 1 loads; its command reloads, then holds.
			loaded, errs := h.LoadAll(ctx, []ExtConfig{cfg})
			if len(errs) != 0 || len(loaded) != 1 {
				t.Fatalf("LoadAll loaded %d extensions, errors %v", len(loaded), errs)
			}
			hold, ok := loaded[0].Commands["retire-hold"]
			if !ok {
				t.Fatalf("the fixture did not register retire-hold")
			}
			held := make(chan error, 1)
			go func() { held <- hold.Handler(context.Background(), "") }()
			awaitRetirementGate(t, gates, "reloaded", "generation 1's command to return from its reload", held)

			// The next reload stages generation 3 with the host listening on the path generation 1 listened on, and holds its factory before its SDK connects.
			touchRetirementGate(t, gates, "factory-hold")
			reloaded := make(chan error, 1)
			var staged []extension.Extension
			go func() {
				var err error
				staged, err = h.Reload(ctx)
				reloaded <- err
			}()
			awaitRetirementGate(t, gates, "factory-holding", "generation 3's factory to hold", held, reloaded)

			// Generation 1's command returns, and the stop of generation 1 it deferred has run before the factory goes on.
			touchRetirementGate(t, gates, "release-command")
			if err := awaitRetirementCall(t, held, "generation 1's command to return"); err != nil {
				t.Fatalf("generation 1's command returned %v", err)
			}
			retired := make(chan error, 1)
			go func() { h.retirementTasks.Wait(); retired <- nil }()
			_ = awaitRetirementCall(t, retired, "the deferred stop of generation 1 to finish")

			// Generation 3 connects, loads, and answers a command.
			if err := os.Remove(filepath.Join(gates, "factory-hold")); err != nil {
				t.Fatal(err)
			}
			if err := awaitRetirementCall(t, reloaded, "the reload that staged generation 3 to complete"); err != nil {
				t.Fatalf("the reload that staged the generation after the retired one failed: %v", err)
			}
			report := h.LastReloadReport()
			if report == nil {
				t.Fatal("the reload left no report")
			}
			if len(report.Issues) != 0 || report.Error != "" {
				t.Fatalf("the reload that staged the generation after the retired one reported error %q and issues %q", report.Error, report.Issues)
			}
			if len(staged) != 1 || staged[0].Name != retirementExtName {
				t.Fatalf("the reload loaded %v, want %s", staged, retirementExtName)
			}
			ping, ok := staged[0].Commands["retire-ping"]
			if !ok {
				t.Fatalf("generation 3 did not register retire-ping")
			}
			if err := ping.Handler(ctx, ""); err != nil {
				t.Fatalf("generation 3 did not answer retire-ping: %v", err)
			}
			if _, err := os.Stat(filepath.Join(gates, "pinged")); err != nil {
				t.Fatalf("generation 3 acknowledged retire-ping without running it: %v", err)
			}
		})
	}
}
