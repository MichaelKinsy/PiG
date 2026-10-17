package aotruntime

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The integration tests run the real Pi-Bolt compiler. They skip unless PIG_AOT_TEST_COMPILER names it and PIG_AOT_TEST_RUNTIME_TREE names a runtime tree.
func realBuildInputs(t *testing.T) (compiler, tree string) {
	t.Helper()
	compiler, tree = os.Getenv("PIG_AOT_TEST_COMPILER"), os.Getenv("PIG_AOT_TEST_RUNTIME_TREE")
	if compiler == "" || tree == "" {
		t.Skip("set PIG_AOT_TEST_COMPILER and PIG_AOT_TEST_RUNTIME_TREE to run the compiler")
	}
	return compiler, tree
}

func writeExtension(t *testing.T, dir, source string) string {
	t.Helper()
	entry := filepath.Join(dir, "index.ts")
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry
}

const registeringExtension = `
import { Type } from "@earendil-works/pi-ai";
export default function (pi: any) {
  pi.registerCommand("aot-probe", { description: "probe", handler: async () => {} });
}
`

func TestBuildCompilesAnExtensionSetAndTheHostRegistersIt(t *testing.T) {
	compiler, tree := realBuildInputs(t)
	extDir := t.TempDir()
	entry := writeExtension(t, extDir, registeringExtension)
	work := t.TempDir()
	var log strings.Builder
	result, err := build(context.Background(), buildOptions{
		Compiler: compiler, Platform: "linux-x64", CPU: "native", Version: "test",
		Request: Request{Key: strings.Repeat("a", 64), Members: []Member{{Name: "probe", Entry: entry}}, RuntimeTree: tree},
		WorkDir: work, Log: &log,
	})
	if err != nil {
		t.Fatalf("build: %v\n%s", err, log.String())
	}
	t.Logf("mode %s, %d bytes, phases %v\n%s", result.Mode, result.Bytes, result.Phases, log.String())
	if _, err := os.Stat(filepath.Join(result.Dir, "licenses", "NOTICE.txt")); err != nil {
		t.Errorf("the image has no notices: %v", err)
	}
	if result.Mode != modeLean {
		t.Errorf("mode = %s, want lean", result.Mode)
	}
	found := false
	for _, input := range result.Inputs {
		found = found || input.Path == entry
	}
	if !found {
		t.Errorf("inputs %v do not list the extension entry %s", result.Inputs, entry)
	}

	sock := filepath.Join(work, "t.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	cmd := exec.Command(result.Executable, "cli", entry)
	cmd.Env = append(os.Environ(), "PIG_EXT_SOCKET="+sock, "PIG_EXT_NAME=probe", "PIG_EXT_CWD="+work, "PIG_TS_RUNTIME_TREE="+tree)
	stdin, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	_ = listener.(*net.UnixListener).SetDeadline(time.Now().Add(20 * time.Second))
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("the compiled host never connected: %v", err)
	}
	buf := make([]byte, 64<<10)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := conn.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), "aot-probe") {
		t.Fatalf("registration = %q, %v; want the aot-probe command", buf[:n], err)
	}
	_ = stdin.Close()
	_ = conn.Close()
}
