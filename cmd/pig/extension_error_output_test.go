//go:build unix

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func failingHandlerFixture(t *testing.T) string {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("testdata", "failing-handler.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

// Upstream rpc-mode.ts binds onError to output({type: "extension_error",
// extensionPath, event, error}); the 9068 regression asserts that shape
// (type, event, error containing the handler's message). Pig registered no
// listener in RPC mode, so handler failures never reached the client.
func TestRPCModeOutputsExtensionErrors(t *testing.T) {
	fixture := failingHandlerFixture(t)
	p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + t.TempDir()}, "--model", "test-faux/faux-1", "--no-session", "-e", fixture)
	p.send(`{"id":"run","type":"prompt","message":"What is 20+22?"}`)
	p.await("extension_error for the failing agent_start handler", func(record rpcRecord) bool {
		if record["type"] != "extension_error" {
			return false
		}
		t.Logf("extension_error = %v", record)
		// Upstream reports ext.path, the extension's own path, not a build
		// artifact.
		if record["event"] != "agent_start" || !strings.Contains(record["error"].(string), "Routing failed") || record["extensionPath"] != fixture {
			t.Fatalf("extension_error = %v", record)
		}
		if _, ok := record["stack"]; ok {
			t.Fatalf("upstream's extension_error has no stack field: %v", record)
		}
		return true
	})
	p.closeAndWait("after the extension error")
}

// Upstream print-mode.ts binds onError to console.error(`Extension error
// (${err.extensionPath}): ${err.error}`) in text and json modes alike. Pig
// registered no listener in either, so handler failures were silent.
func TestPrintModeWritesExtensionErrorsToStderr(t *testing.T) {
	bin := buildPigBinaryForSignalTest(t)
	fixture := failingHandlerFixture(t)
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--no-session", "-e", fixture, "--mode", mode, "--print", "What is 20+22?")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				if _, ok := errors.AsType[*exec.ExitError](err); !ok {
					t.Fatal(err)
				}
				t.Fatalf("pig --print failed: %v\nstderr:\n%s", err, stderr.String())
			}
			if !strings.Contains(stdout.String(), "42") {
				t.Fatalf("the run did not complete; stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
			}
			var lines []string
			for line := range strings.SplitSeq(stderr.String(), "\n") {
				if strings.HasPrefix(line, "Extension error (") {
					lines = append(lines, line)
				}
			}
			if len(lines) != 1 || lines[0] != "Extension error ("+fixture+"): Routing failed" {
				t.Fatalf("want one upstream-format extension error line; stderr:\n%s", stderr.String())
			}
			if strings.Contains(stdout.String(), "Extension error") {
				t.Fatalf("extension errors belong on stderr; stdout:\n%s", stdout.String())
			}
		})
	}
}

// The listener's format is exactly upstream's, without the stack.
func TestPrintExtensionErrorListenerFormat(t *testing.T) {
	var out bytes.Buffer
	printExtensionErrorListener(&out)(&extension.ExtensionError{ExtensionPath: "/ext/a.ts", Event: "agent_start", Error: "boom", Stack: "Error: boom\n    at x"})
	if got, want := out.String(), "Extension error (/ext/a.ts): boom\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
