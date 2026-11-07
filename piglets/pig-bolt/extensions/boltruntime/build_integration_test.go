package boltruntime

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

// The integration tests run the real Pi-Bolt compiler. They skip unless the environment names one:
//
//	PIG_AOT_TEST_COMPILER      the Pi-Bolt runtime executable; or
//	PIG_AOT_TEST_DOWNLOAD=1    install the pinned release with this Piglet's own installer (signature, archive and compiler digests verified against
//	                           the pins in lock.go) into PIG_AOT_TEST_TOOLCHAIN_DIR, else the user cache directory, and reuse it while it verifies
//	                           (make test-pig-bolt-build, and the nightly job that runs it, set this);
//	PIG_AOT_TEST_RUNTIME_TREE  the runtime tree to stage; default: coding/extension/host/subprocess/runtime-node of this checkout.
//
// These are the guard against a compiler release that crashes on PiG's runtime tree (Pi-Bolt 0.7.4 segfaulted in the ahead-of-time step).
func realBuildInputs(t *testing.T) (compiler, tree string) {
	t.Helper()
	compiler, tree = os.Getenv("PIG_AOT_TEST_COMPILER"), os.Getenv("PIG_AOT_TEST_RUNTIME_TREE")
	if tree == "" {
		if candidate, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "coding", "extension", "host", "subprocess", "runtime-node")); err == nil {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				tree = candidate
			}
		}
	}
	if compiler == "" && os.Getenv("PIG_AOT_TEST_DOWNLOAD") != "" {
		compiler = pinnedCompiler(t)
	}
	if compiler == "" || tree == "" {
		t.Skip("set PIG_AOT_TEST_COMPILER (or PIG_AOT_TEST_DOWNLOAD=1) and PIG_AOT_TEST_RUNTIME_TREE to run the compiler")
	}
	return compiler, tree
}

// pinnedCompiler returns the pinned Pi-Bolt compiler for this platform, installing it with the Piglet's installer when the toolchain directory has no
// verified copy.
func pinnedCompiler(t *testing.T) string {
	t.Helper()
	platform := platformName()
	if !supportedPlatform(platform) {
		t.Skipf("Pi-Bolt %s has no compiler for %s", toolchainVersion, platform)
	}
	dir := os.Getenv("PIG_AOT_TEST_TOOLCHAIN_DIR")
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		dir = filepath.Join(cache, "pig-bolt-test")
	}
	ctx := context.Background()
	if exe, err := verifyToolchain(ctx, dir, platform, archives); err == nil {
		return exe
	}
	exe, err := installer{providerDir: dir, platform: platform, baseURL: releaseURL}.install(ctx)
	if err != nil {
		t.Fatalf("install the pinned Pi-Bolt %s compiler: %v", toolchainVersion, err)
	}
	return exe
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

	// A Unix socket address holds about 104 bytes of path and a test directory under a long TMPDIR (macOS's is 49 bytes) overflows it, so the socket
	// lives in a short directory of its own, as the training run's does.
	sockDir, err := os.MkdirTemp("", "pbt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "s")
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
