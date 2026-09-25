package runtimecell_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestBuildRustPackedCellBuildsCachedRunner(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skipf("cargo not found: %v", err)
	}
	rootA := writeRustFactoryCrate(t, "packed_a", "a-ext", "a")
	rootB := writeRustFactoryCrate(t, "packed_b", "b-ext", "b")
	cache := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()

	cell, err := runtimecell.BuildRustPackedCell(ctx, cache, "cell:rust-test", []runtimecell.RustExtension{
		{Name: "b-ext", Root: rootB, Package: "packed_b", Factory: "new_extension", Hash: "hb"},
		{Name: "a-ext", Root: rootA, Package: "packed_a", Factory: "new_extension", Hash: "ha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cell.Cached || cell.BinaryPath == "" || cell.Hash == "" {
		t.Fatalf("first rust cell = %+v", cell)
	}
	cached, err := runtimecell.BuildRustPackedCell(ctx, cache, "cell:rust-test", []runtimecell.RustExtension{
		{Name: "a-ext", Root: rootA, Package: "packed_a", Factory: "new_extension", Hash: "ha"},
		{Name: "b-ext", Root: rootB, Package: "packed_b", Factory: "new_extension", Hash: "hb"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cached.Cached || cached.BinaryPath != cell.BinaryPath || cached.Hash != cell.Hash {
		t.Fatalf("cached rust cell = %+v, want cached same artifact as %+v", cached, cell)
	}

	sockA, cleanupA := unixListenerRust(t)
	defer cleanupA()
	sockB, cleanupB := unixListenerRust(t)
	defer cleanupB()
	cmdCtx, cmdCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cmdCancel()
	cmd := exec.CommandContext(cmdCtx, cell.BinaryPath)
	cmd.Env = append(os.Environ(), runtimecell.SocketEnvName("a-ext")+"="+sockA.Addr().String(), runtimecell.SocketEnvName("b-ext")+"="+sockB.Addr().String())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start rust runner: %v", err)
	}
	regA := acceptRegisterRust(t, sockA)
	regB := acceptRegisterRust(t, sockB)
	seen := map[string]bool{regA.Name: true, regB.Name: true}
	if !seen["a-ext"] || !seen["b-ext"] {
		t.Fatalf("registered names = %v/%v", regA.Name, regB.Name)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("rust runner wait: %v\nstderr:\n%s", err, stderr.String())
	}
}

func writeRustFactoryCrate(t *testing.T, packageName, extName, toolName string) string {
	t.Helper()
	dir := t.TempDir()
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk-rs"))
	if err != nil {
		t.Fatal(err)
	}
	cargo := fmt.Sprintf("[package]\nname = %q\nversion = \"0.0.0\"\nedition = \"2024\"\n\n[dependencies]\npig-sdk = { path = %q }\n", packageName, filepath.ToSlash(sdkRoot))
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`use pig_sdk::{empty_schema, Extension, ToolResult};

pub fn new_extension() -> Extension {
    let mut ext = Extension::new(%q);
    ext.tool(%q, "test tool", empty_schema(), |_ctx, _params| ToolResult::text("ok"));
    ext
}
`, extName, toolName)
	if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func unixListenerRust(t *testing.T) (net.Listener, func()) {
	t.Helper()
	// A private directory per listener: time-derived names can repeat on
	// hosts with a coarse clock (Windows), and two listeners then collide.
	dir, err := os.MkdirTemp("", "pig-rust-packed-")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cell.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	return ln, func() { _ = ln.Close(); _ = os.RemoveAll(dir) }
}

func acceptRegisterRust(t *testing.T, ln net.Listener) *subprocess.RegisterPayload {
	t.Helper()
	// A runner that exits before connecting must fail the test, not block Accept.
	if unix, ok := ln.(*net.UnixListener); ok {
		if err := unix.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := ln.Accept()
	if err != nil {
		t.Fatalf("runner did not connect: %v", err)
	}
	defer func() { _ = conn.Close() }()
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		t.Fatalf("read header: %v", err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(conn, data); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	var env subprocess.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Type != subprocess.MsgRegister || env.Register == nil {
		t.Fatalf("envelope = %+v, want register", env)
	}
	ready := subprocess.Envelope{Type: subprocess.MsgReady, Ready: &subprocess.ReadyPayload{Cwd: t.TempDir(), Width: 80}}
	readyData, _ := json.Marshal(ready)
	binary.BigEndian.PutUint32(hdr[:], uint32(len(readyData)))
	_, _ = conn.Write(hdr[:])
	_, _ = conn.Write(readyData)
	return env.Register
}
