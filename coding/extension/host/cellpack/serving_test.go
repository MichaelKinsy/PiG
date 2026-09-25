package cellpack

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// hideGo restricts PATH so the go toolchain is unreachable, modeling a
// consumer machine that installed a Piglet Binary without a compiler. It skips (not
// fails) when go happens to live on the restricted PATH, so the test never
// makes a false claim about being toolchain-free.
func hideGo(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", "/usr/bin:/bin")
	if p, err := exec.LookPath("go"); err == nil {
		t.Skipf("go still reachable at %s under restricted PATH; cannot prove toolchain-free serving", p)
	}
}

// shortSock points the extension socket at a short /tmp dir (macOS caps unix
// socket paths at 104 bytes; a t.TempDir under the test name overflows).
func shortSock(t *testing.T) {
	t.Helper()
	parent := "/tmp"
	if runtime.GOOS == "windows" {
		parent = os.TempDir()
	}
	dir, err := os.MkdirTemp(parent, "pig-serve-*")
	if err != nil {
		t.Fatalf("mkdir short socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
}

// writeHelloSource writes a minimal source-mode Go extension that registers one
// tool over the socket protocol. It is self-contained (stdlib only) so the
// forge-time build needs no module fetch.
func writeHelloSource(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module hello-ext\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(helloServeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// TestServing_PigletBinaryServesCellToolchainFree is the Milestone B acceptance
// proof: a Piglet Binary serves an embedded isolated cell end-to-end: extract →
// resolver → Host.Load → live subprocess → tool round-trip: with the go
// toolchain off PATH. It exercises the real cellpack resolver and a real
// subprocess Host, not a fake.
func TestServing_PigletBinaryServesCellToolchainFree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping serving integration test in short mode")
	}

	// 1. Forge time: build the extension from source (go available here) and
	//    capture the exact identity the runtime will request.
	src := writeHelloSource(t)
	res, err := subprocess.NewBuilder(t.TempDir()).Build("hello", src)
	if err != nil {
		t.Fatalf("forge-time build: %v", err)
	}
	binBytes, err := os.ReadFile(res.BinaryPath)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Embed + extract via the real cellpack path.
	rel := "isolated/" + res.Language + "/hello-" + res.Hash[:16]
	m := Manifest{PigCoreVersion: "test", Cells: []CellEntry{{
		Language: res.Language, Key: "hello", OS: runtime.GOOS, Arch: runtime.GOARCH,
		Binary: rel, Extensions: []ExtEntry{{Name: "hello", Hash: res.Hash}},
	}}}
	fsys := fstest.MapFS{
		"cells/manifest.json": {Data: mustJSON(t, m)},
		"cells/" + rel:        {Data: binBytes, Mode: 0o755},
	}
	dest := t.TempDir()
	if err := extract(fsys, m, dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	runtimecell.SetPrebuiltResolver(NewResolver(m, dest))
	defer runtimecell.SetPrebuiltResolver(nil)

	// 3. Consumer time: no toolchain, real Host loads the SAME source dir.
	shortSock(t)
	hideGo(t)

	h := subprocess.NewHost(t.TempDir())
	h.SetUIBridge(subprocess.NewUIBridge(func() {}))
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ext, err := h.Load(ctx, subprocess.ExtConfig{Name: "hello", Source: src, Enabled: true})
	if err != nil {
		t.Fatalf("serve prebuilt cell with go off PATH: %v", err)
	}
	if ext == nil {
		t.Fatal("Load returned nil extension")
	}

	// 4. The served process answers a real tool call.
	tool, ok := ext.Tools["hello"]
	if !ok {
		t.Fatalf("served extension registered no 'hello' tool: %#v", ext.Tools)
	}
	result, err := tool.Definition.Execute(ctx, "tc-1", json.RawMessage(`{"name":"Piglet Binary"}`), nil)
	if err != nil {
		t.Fatalf("tool execute through served binary: %v", err)
	}
	var got struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(mustJSON(t, result), &got)
	if got.Content != "Hello, Piglet Binary!" {
		t.Errorf("tool result content = %q, want %q", got.Content, "Hello, Piglet Binary!")
	}
}

// TestServing_NoResolverNeedsToolchain is the fail-able counterpart: with no
// prebuilt resolver and no toolchain, Load must fail trying to compile. This
// proves the resolver is the only reason the positive test succeeds.
func TestServing_NoResolverNeedsToolchain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping serving integration test in short mode")
	}
	src := writeHelloSource(t)
	runtimecell.SetPrebuiltResolver(nil)
	shortSock(t)
	hideGo(t)

	h := subprocess.NewHost(t.TempDir())
	h.SetUIBridge(subprocess.NewUIBridge(func() {}))
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := h.Load(ctx, subprocess.ExtConfig{Name: "hello", Source: src, Enabled: true}); err == nil {
		t.Fatal("expected Load to fail compiling source with no resolver and no toolchain")
	}
}

// TestServing_PackedGoCellServedToolchainFree proves the packed-go serving
// path (shared by shared packed-go cells and single isolated go-factory cells):
// build a packed cell → embed → resolver short-circuits BuildGoPackedCell to the
// extracted prebuilt with go off PATH → LoadGoPackedCell → tool round-trip.
func TestServing_PackedGoCellServedToolchainFree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping serving integration test in short mode")
	}
	const modulePath = "example.com/pgserve"
	goExt := runtimecell.GoExtension{
		Name: "packserve", Root: writeGoFactorySource(t, modulePath),
		ModulePath: modulePath, Package: modulePath, Factory: "Extension", Hash: "hp",
	}
	const key = "packed-go:serve-test"

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Forge time: build the packed cell (toolchain available).
	cell, err := runtimecell.BuildGoPackedCell(ctx, t.TempDir(), key, []runtimecell.GoExtension{goExt})
	if err != nil {
		t.Fatalf("forge-time packed build: %v", err)
	}
	binBytes, err := os.ReadFile(cell.BinaryPath)
	if err != nil {
		t.Fatal(err)
	}

	// Embed + extract via the real cellpack path.
	rel := "go/" + cell.Hash[:16] + "/runner"
	m := Manifest{PigCoreVersion: "test", Cells: []CellEntry{{
		Language: "go", Key: key, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Binary: rel, Extensions: []ExtEntry{{Name: "packserve", Hash: "hp"}},
	}}}
	fsys := fstest.MapFS{
		"cells/manifest.json": {Data: mustJSON(t, m)},
		"cells/" + rel:        {Data: binBytes, Mode: 0o755},
	}
	dest := t.TempDir()
	if err := extract(fsys, m, dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	runtimecell.SetPrebuiltResolver(NewResolver(m, dest))
	defer runtimecell.SetPrebuiltResolver(nil)

	shortSock(t)
	hideGo(t)

	// Consumer: re-resolving the same cell must short-circuit to the prebuilt
	// (no findSDKRoot, no go build) and hand back the extracted binary.
	served, err := runtimecell.BuildGoPackedCell(ctx, t.TempDir(), key, []runtimecell.GoExtension{goExt})
	if err != nil {
		t.Fatalf("serve packed cell with go off PATH: %v", err)
	}
	wantBin := extractedAt(dest, rel)
	if served.BinaryPath != wantBin {
		t.Fatalf("served binary = %q, want extracted prebuilt %q", served.BinaryPath, wantBin)
	}

	h := subprocess.NewHost(t.TempDir())
	h.SetUIBridge(subprocess.NewUIBridge(func() {}))
	defer h.Shutdown("test done")

	exts, err := h.LoadGoPackedCell(ctx, served)
	if err != nil {
		t.Fatalf("load served packed cell: %v", err)
	}
	if len(exts) != 1 {
		t.Fatalf("packed cell extensions = %d, want 1", len(exts))
	}
	tool, ok := exts[0].Tools["pg_tool"]
	if !ok {
		t.Fatalf("served packed extension registered no 'pg_tool': %#v", exts[0].Tools)
	}
	result, err := tool.Definition.Execute(ctx, "tc-1", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("execute tool through served packed binary: %v", err)
	}
	if got := string(mustJSON(t, result)); !strings.Contains(got, "packed-ok") {
		t.Errorf("packed tool result = %s, want it to contain %q", got, "packed-ok")
	}
}

// writeGoFactorySource writes a minimal Go factory extension (Extension) that
// registers one tool returning a fixed marker. Mirrors the subprocess package's
// packed-factory fixture; the packed-cell runner resolves the SDK in-checkout.
func writeGoFactorySource(t *testing.T, modulePath string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module "+modulePath+"\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package ext

import "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	e := sdk.New("packserve")
	e.Tool("pg_tool", "test tool", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return map[string]string{"content": "packed-ok"}, nil
	})
	return e
}
`
	if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// helloServeSource is a minimal source-mode extension that registers one tool
// and answers requests over the PIG_EXT_SOCKET framing protocol.
const helloServeSource = `package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
)

func main() {
	sockPath := os.Getenv("PIG_EXT_SOCKET")
	if sockPath == "" {
		fmt.Fprintln(os.Stderr, "PIG_EXT_SOCKET not set")
		os.Exit(1)
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer conn.Close()

	writeFrame(conn, map[string]any{
		"type": "register",
		"register": map[string]any{
			"name":             "hello",
			"tools": []map[string]any{{
				"name":        "hello",
				"description": "Say hello",
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
			}},
		},
	})

	for {
		data := readFrame(conn)
		if data == nil {
			return
		}
		var env map[string]any
		_ = json.Unmarshal(data, &env)
		switch env["type"] {
		case "ready":
		case "request":
			id, _ := env["id"].(string)
			req, _ := env["request"].(map[string]any)
			args, _ := req["args"].(map[string]any)
			name, _ := args["name"].(string)
			result, _ := json.Marshal(map[string]string{"content": "Hello, " + name + "!"})
			writeFrame(conn, map[string]any{
				"type":     "response",
				"id":       id,
				"response": map[string]any{"result": json.RawMessage(result)},
			})
		case "shutdown":
			return
		}
	}
}

func writeFrame(conn net.Conn, v any) {
	data, _ := json.Marshal(v)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	conn.Write(hdr[:])
	conn.Write(data)
}

func readFrame(conn net.Conn) []byte {
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return nil
	}
	size := binary.BigEndian.Uint32(hdr[:])
	data := make([]byte, size)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil
	}
	return data
}
`
