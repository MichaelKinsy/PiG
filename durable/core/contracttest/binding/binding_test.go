// SPDX-License-Identifier: MIT

// Package binding is the binding stage of the production core's gate (docs/plan/durable-core/CONTRACT.md section 5, ABI
// section 12): the same event scripts through the native core, the Go wasip1 build and
// the TinyGo wasip1 build must give byte-equal steps, equal to the checked-in golden steps; every Wasm build must
// import only the ABI's import table, export the ABI's exports, and report the native abi_id and sql_table.
//
// gate.sh builds the modules and sets DCORE_WASM_GO and DCORE_WASM_TINYGO; without them this test builds the Go
// module itself and skips TinyGo unless DCORE_REQUIRE_TINYGO=1.
package binding

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/MichaelKinsy/PiG/durable/core"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/abitest"
	"github.com/MichaelKinsy/PiG/durable/core/abi/spec"
)

// fixedUUID is the uuidv7 every binding run uses, so steps are comparable.
const fixedUUID = "0190a6b2-0000-7000-8000-000000000001"

func nativeSteps(t *testing.T, events []abi.Event) [][]byte {
	t.Helper()
	return abitest.RunScript(core.NewSession(func() string { return fixedUUID }), events)
}

// wasmCore runs one module instance.
type wasmCore struct {
	ctx context.Context
	mod api.Module
	rt  wazero.Runtime
}

func loadWasm(t *testing.T, path string) *wasmCore {
	t.Helper()
	ctx := context.Background()
	code, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rt := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = rt.Close(ctx) })
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)
	_, err = rt.NewHostModuleBuilder("pig").NewFunctionBuilder().
		WithFunc(func(_ context.Context, m api.Module, dst uint32) { m.Memory().Write(dst, []byte(fixedUUID)) }).
		Export("uuidv7").Instantiate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := rt.CompileModule(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	checkImportsExports(t, compiled)
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithStartFunctions("_initialize").
		WithStderr(os.Stderr).WithRandSource(rand.Reader))
	if err != nil {
		t.Fatal(err)
	}
	return &wasmCore{ctx: ctx, mod: mod, rt: rt}
}

func checkImportsExports(t *testing.T, m wazero.CompiledModule) {
	t.Helper()
	allowed := spec.Imports()
	for _, f := range m.ImportedFunctions() {
		mod, name, _ := f.Import()
		if !slices.Contains(allowed, mod+"."+name) {
			t.Errorf("import %s.%s is outside the ABI import table (ABI section 4.2)", mod, name)
		}
	}
	exported := m.ExportedFunctions()
	for _, name := range spec.Exports() {
		if _, ok := exported[name]; !ok {
			t.Errorf("export %s is missing (ABI section 4.1)", name)
		}
	}
	for name := range exported {
		if !slices.Contains(spec.Exports(), name) {
			t.Logf("extra export %s (toolchain runtime; not part of ABI 1)", name)
		}
	}
}

func (w *wasmCore) call(t *testing.T, name string, args ...uint64) uint64 {
	t.Helper()
	res, err := w.mod.ExportedFunction(name).Call(w.ctx, args...)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(res) == 0 {
		return 0
	}
	return res[0]
}

func (w *wasmCore) read(t *testing.T, ptr, n uint32) []byte {
	t.Helper()
	b, ok := w.mod.Memory().Read(ptr, n)
	if !ok {
		t.Fatalf("read %d bytes at %d out of range", n, ptr)
	}
	return bytes.Clone(b)
}

func (w *wasmCore) steps(t *testing.T, events []abi.Event) [][]byte {
	t.Helper()
	h := w.call(t, "session_new")
	out := make([][]byte, len(events))
	for i, ev := range events {
		wire := ev.Wire()
		ptr := uint32(w.call(t, "in_reserve", uint64(len(wire))))
		if !w.mod.Memory().Write(ptr, wire) {
			t.Fatal("in_reserve returned a pointer outside memory")
		}
		n := uint32(w.call(t, "step", h, uint64(ev.Kind), uint64(len(wire)), api.EncodeF64(ev.Now)))
		out[i] = w.read(t, uint32(w.call(t, "out_ptr")), n)
	}
	w.call(t, "session_free", h)
	return out
}

func (w *wasmCore) abiID(t *testing.T) string {
	return string(w.read(t, uint32(w.call(t, "abi_id")), 64))
}

func (w *wasmCore) sqlTable(t *testing.T) []string {
	ptr := uint32(w.call(t, "sql_table"))
	head := w.read(t, ptr, 2)
	n := int(binary.LittleEndian.Uint16(head))
	off := ptr + 2
	out := make([]string, 0, n)
	for range n {
		l := binary.LittleEndian.Uint32(w.read(t, off, 4))
		out = append(out, string(w.read(t, off+4, l)))
		off += 4 + l
	}
	return out
}

func wasmBuilds(t *testing.T) map[string]string {
	t.Helper()
	builds := map[string]string{}
	if p := os.Getenv("DCORE_WASM_GO"); p != "" {
		builds["go"] = p
	} else {
		out := filepath.Join(t.TempDir(), "core-go.wasm")
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-trimpath", "-o", out, "../../cmd/corewasm")
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go wasip1 build: %v\n%s", err, b)
		}
		builds["go"] = out
	}
	if p := os.Getenv("DCORE_WASM_TINYGO"); p != "" {
		builds["tinygo"] = p
	} else if os.Getenv("DCORE_REQUIRE_TINYGO") == "1" {
		t.Fatal("DCORE_REQUIRE_TINYGO=1 but DCORE_WASM_TINYGO is not set; run durable/core/contracttest/gate.sh")
	}
	return builds
}

func readScript(t *testing.T, name string) []abi.Event {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name+".events"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	events, err := abitest.ParseScript(f)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func renderSteps(steps [][]byte) []byte {
	var b strings.Builder
	for _, s := range steps {
		b.WriteString(hex.EncodeToString(s))
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// TestBindingConformance is ABI section 12's binding conformance for every script in testdata.
func TestBindingConformance(t *testing.T) {
	scripts, _ := filepath.Glob("testdata/*.events")
	builds := wasmBuilds(t)
	for _, path := range scripts {
		name := strings.TrimSuffix(filepath.Base(path), ".events")
		t.Run(name, func(t *testing.T) {
			events := readScript(t, name)
			native := renderSteps(nativeSteps(t, events))
			golden := filepath.Join("testdata", name+".steps")
			if os.Getenv("DCORE_UPDATE_GOLDEN") == "1" {
				if err := os.WriteFile(golden, native, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(native, want) {
				t.Errorf("native steps differ from %s (DCORE_UPDATE_GOLDEN=1 regenerates after a reviewed change)", golden)
			}
			for build, wasm := range builds {
				w := loadWasm(t, wasm)
				if got := w.abiID(t); got != abi.ID {
					t.Errorf("%s: abi_id %s, native %s", build, got, abi.ID)
				}
				if got := w.sqlTable(t); !slices.Equal(got, core.SQLTable()) {
					t.Errorf("%s: sql_table differs from native", build)
				}
				if got := renderSteps(w.steps(t, events)); !bytes.Equal(got, native) {
					t.Errorf("%s: steps differ from native\nwasm:\n%s\nnative:\n%s", build, got, native)
				}
			}
		})
	}
}
