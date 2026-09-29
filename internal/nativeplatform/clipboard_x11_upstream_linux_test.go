//go:build linux

package nativeplatform

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type nativeTestProcess struct {
	command *exec.Cmd
	output  *bufio.Reader
	stop    func()
}

func startNativeTestProcess(t testing.TB, name string, args, env []string) *nativeTestProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	command := exec.CommandContext(ctx, name, args...)
	command.Env = env
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err = command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	process := &nativeTestProcess{command: command, output: bufio.NewReader(stdout)}
	process.stop = sync.OnceFunc(func() { _ = command.Process.Kill(); _ = command.Wait(); cancel() })
	t.Cleanup(process.stop)
	return process
}
func nativeReady(t testing.TB, process *nativeTestProcess) string {
	t.Helper()
	line, err := process.output.ReadString('\n')
	if err != nil {
		t.Fatalf("process readiness: %v", err)
	}
	return strings.TrimSpace(line)
}

type nativeReaderResult struct {
	OK          bool            `json:"ok"`
	Value       json.RawMessage `json:"value,omitempty"`
	Unavailable bool            `json:"unavailable,omitempty"`
	Length      *int            `json:"length,omitempty"`
	Hash        string          `json:"hash,omitempty"`
	Error       string          `json:"error,omitempty"`
}

func readNativeTestClipboard(t *testing.T, method string, env []string) nativeReaderResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	result, err := invokeNativeTestClipboard(ctx, method, env)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func invokeNativeTestClipboard(ctx context.Context, method string, env []string) (nativeReaderResult, error) {
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeX11ReaderFixture$")
	// Race instrumentation's process-exit grace sleep is not clipboard transfer time and must not consume Pi's 3500ms assertion budget.
	command.Env = append(append([]string{}, env...), "PIG_NATIVE_X11_READER="+method, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	output, err := command.CombinedOutput()
	if err != nil {
		return nativeReaderResult{}, fmt.Errorf("native reader: %w\n%s", err, output)
	}
	line, rest, ok := bytes.Cut(output, []byte{'\n'})
	if !ok || string(rest) != "PASS\n" {
		return nativeReaderResult{}, fmt.Errorf("unexpected native reader output %q", output)
	}
	var result nativeReaderResult
	if err = json.Unmarshal(line, &result); err != nil {
		return result, err
	}
	return result, nil
}
func writeXclipTest(t testing.TB, env []string, target string, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-in", "-t", target)
	command.Env = env
	command.Stdin = bytes.NewReader(data)
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestUpstreamNativeX11Transfers(t *testing.T) {
	// These are the real display-server rows in native-clipboard-linux.test.ts. Missing task dependencies fail rather than silently marking the port green.
	for _, command := range []string{"cc", "Xvfb", "xclip"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("native clipboard suite requires %s: %v", command, err)
		}
	}
	directory := t.TempDir()
	producer := filepath.Join(directory, "x11-test")
	source := filepath.Join("..", "..", ".upstream", "v0.87.1", "packages", "tui", "test", "fixtures", "clipboard-x11-test.c")
	if output, err := exec.CommandContext(t.Context(), "cc", "-std=c11", "-D_POSIX_C_SOURCE=200809L", "-Wall", "-Wextra", "-Werror", "-pthread", source, "-lxcb", "-ldl", "-o", producer).CombinedOutput(); err != nil {
		t.Fatalf("C selection owner: %v\n%s", err, output)
	}
	startX11 := func(t *testing.T) (*nativeTestProcess, []string) {
		t.Helper()
		server := startNativeTestProcess(t, "Xvfb", []string{"-displayfd", "1", "-screen", "0", "640x480x24", "-nolisten", "tcp"}, os.Environ())
		display := nativeReady(t, server)
		return server, append(os.Environ(), "DISPLAY=:"+display)
	}
	// .upstream/v0.87.1/packages/tui/test/native-clipboard-linux.test.ts:159.
	t.Run("decodes X11 STRING as Latin-1", func(t *testing.T) {
		_, env := startX11(t)
		writeXclipTest(t, env, "STRING", []byte{'c', 'a', 'f', 0xe9, ' ', 0xa3, 0xff})
		result := readNativeTestClipboard(t, "getText", env)
		if !result.OK || string(result.Value) != `"café £ÿ"` {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/native-clipboard-linux.test.ts:172.
	t.Run("reads UTF8_STRING when X11 TARGETS is refused", func(t *testing.T) {
		_, env := startX11(t)
		owner := startNativeTestProcess(t, producer, []string{"direct-text"}, env)
		if nativeReady(t, owner) != "ready" {
			t.Fatal("owner not ready")
		}
		result := readNativeTestClipboard(t, "getText", env)
		if !result.OK || string(result.Value) != `"café 日本語"` {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/native-clipboard-linux.test.ts:180.
	t.Run("returns null for empty X11 clipboards and text-only selections", func(t *testing.T) {
		_, env := startX11(t)
		want := nativeReaderResult{OK: true, Value: json.RawMessage("null")}
		for _, method := range []string{"getText", "getImage"} {
			if got := readNativeTestClipboard(t, method, env); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s=%+v", method, got)
			}
		}
		writeXclipTest(t, env, "UTF8_STRING", []byte("text only"))
		if got := readNativeTestClipboard(t, "getImage", env); !reflect.DeepEqual(got, want) {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/native-clipboard-linux.test.ts:193.
	t.Run("preserves X11 Unicode and incremental text/image transfers", func(t *testing.T) {
		_, env := startX11(t)
		for _, row := range []struct {
			target, method string
			data           []byte
		}{{"UTF8_STRING", "getText", []byte("café 日本語")}, {"UTF8_STRING", "getText", bytes.Repeat([]byte{120}, 4*1024*1024)}, {"image/png", "getImage", bytes.Repeat([]byte{123}, 4*1024*1024)}} {
			writeXclipTest(t, env, row.target, row.data)
			result := readNativeTestClipboard(t, row.method, env)
			hash := sha256.Sum256(row.data)
			if !result.OK || result.Length == nil || *result.Length != len(row.data) || result.Hash != hex.EncodeToString(hash[:]) {
				t.Fatalf("%s result=%+v", row.method, result)
			}
		}
	})
	for _, mode := range []string{"disconnect", "timeout"} {
		// .upstream/v0.87.1/packages/tui/test/native-clipboard-linux.test.ts:214.
		t.Run("reports X11 transfer "+mode+" as an exception, not an unavailable display", func(t *testing.T) {
			server, env := startX11(t)
			owner := startNativeTestProcess(t, producer, []string{"idle"}, env)
			nativeReady(t, owner)
			resultCh := make(chan struct {
				result nativeReaderResult
				err    error
			}, 1)
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
			defer cancel()
			start := time.Now()
			go func() {
				result, err := invokeNativeTestClipboard(ctx, "getText", env)
				resultCh <- struct {
					result nativeReaderResult
					err    error
				}{result, err}
			}()
			if nativeReady(t, owner) != "requested" {
				t.Fatal("selection not requested")
			}
			if mode == "disconnect" {
				server.stop()
			}
			response := <-resultCh
			if response.err != nil {
				t.Fatal(response.err)
			}
			result := response.result
			if result.OK || !strings.Contains(result.Error, "X11 clipboard") || time.Since(start) >= 3500*time.Millisecond {
				t.Fatalf("failure=%+v elapsed=%v", result, time.Since(start))
			}
		})
	}
	for _, content := range []string{"text", "image"} {
		// .upstream/v0.87.1/packages/tui/test/native-clipboard-linux.test.ts:230.
		t.Run("frees partially received X11 "+content+" after invalid metadata", func(t *testing.T) {
			_, env := startX11(t)
			owner := startNativeTestProcess(t, producer, []string{"invalid"}, env)
			nativeReady(t, owner)
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeX11CleanupFixture$")
			command.Env = append(env, "PIG_NATIVE_X11_CLEANUP="+content)
			output, err := command.CombinedOutput()
			if err != nil || string(output) != "clean\nPASS\n" {
				t.Fatalf("cleanup: %v\n%s", err, output)
			}
		})
		// .upstream/v0.87.1/packages/tui/test/native-clipboard-linux.test.ts:237.
		t.Run("times out stalled X11 "+content+" after a partial transfer without blocking JS", func(t *testing.T) {
			_, env := startX11(t)
			owner := startNativeTestProcess(t, producer, []string{"partial"}, env)
			nativeReady(t, owner)
			method := "getText"
			if content == "image" {
				method = "getImage"
			}
			start := time.Now()
			result := readNativeTestClipboard(t, method, env)
			if result.OK || !strings.Contains(result.Error, "X11 clipboard") || time.Since(start) >= 3500*time.Millisecond {
				t.Fatalf("failure=%+v elapsed=%v", result, time.Since(start))
			}
		})
	}
}

type nativeAllocationProfile struct{ readAllocated, appendAllocated, inUse int64 }

func nativeClipboardAllocations() nativeAllocationProfile {
	for {
		n, _ := runtime.MemProfile(nil, true)
		records := make([]runtime.MemProfileRecord, n+16)
		n, ok := runtime.MemProfile(records, true)
		if !ok {
			continue
		}
		var result nativeAllocationProfile
		for _, record := range records[:n] {
			frames := runtime.CallersFrames(record.Stack())
			frame, more := frames.Next()
			for strings.HasPrefix(frame.Function, "runtime.") && more {
				frame, more = frames.Next()
			}
			switch {
			case strings.HasSuffix(frame.Function, ".(*x11Clipboard).readProperty"):
				result.readAllocated += record.AllocBytes
				result.inUse += record.InUseBytes()
			case strings.HasSuffix(frame.Function, ".(*x11Property).appendProperty"):
				result.appendAllocated += record.AllocBytes
				result.inUse += record.InUseBytes()
			}
		}
		return result
	}
}

func TestNativeX11CleanupFixture(t *testing.T) {
	content := os.Getenv("PIG_NATIVE_X11_CLEANUP")
	if content == "" {
		return
	}
	runtime.MemProfileRate = 1
	clipboard, err := openX11Clipboard(t.Context(), os.Getenv("DISPLAY"))
	if err != nil {
		t.Fatal(err)
	}
	defer clipboard.close()
	target, err := clipboard.preferredTarget(content == "image")
	if err != nil || target == 0 {
		t.Fatalf("target=%d err=%v", target, err)
	}
	runtime.GC()
	runtime.GC()
	before := nativeClipboardAllocations()
	result, err := clipboard.requestSelection(target)
	if err == nil || result.data != nil || result.items != 0 {
		t.Fatalf("failed selection retained %+v err=%v", result, err)
	}
	runtime.GC()
	runtime.GC()
	after := nativeClipboardAllocations()
	if after.readAllocated-before.readAllocated < 4096 || after.appendAllocated-before.appendAllocated < 4096 {
		t.Fatalf("did not allocate both original 4096-byte receive/accumulation buffers: before=%+v after=%+v", before, after)
	}
	if after.inUse != 0 {
		t.Fatalf("private clipboard bytes retained after invalid metadata: %d", after.inUse)
	}
	fmt.Println("clean")
}
