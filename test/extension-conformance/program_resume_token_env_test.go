// SPDX-License-Identifier: MIT

package extensionconformance

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

const osc7577HelperEnv = "PIG_TEST_OSC7577_HELPER"

// TestOSC7577EnvHelperProcess prints whether OSC7577_TOKEN is present in its environment.
func TestOSC7577EnvHelperProcess(t *testing.T) {
	if os.Getenv(osc7577HelperEnv) != "1" || flag.NArg() != 1 || flag.Arg(0) != "check-osc7577" {
		return
	}
	v, ok := os.LookupEnv(tui.ProgramResumeTokenEnv)
	_, _ = fmt.Fprintf(os.Stdout, "present=%t;value=%s\n", ok, v)
	os.Exit(0)
}

// TestConformance_SubprocessExtensionsDoNotReceiveOSC7577Token proves the OSC 7577 security boundary
// across every subprocess SDK runtime (Go, Node, Rust, Python, Lisp): after TakeProgramResumeToken()
// removes OSC7577_TOKEN from the host process environment, no subprocess extension (nor a child command
// it spawns via pi.exec) receives OSC7577_TOKEN.
func TestConformance_SubprocessExtensionsDoNotReceiveOSC7577Token(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess SDK conformance in short mode")
	}
	const secretToken = "conformance-secret-osc7577-token"
	t.Setenv(tui.ProgramResumeTokenEnv, secretToken)
	t.Setenv(osc7577HelperEnv, "1")
	if got := tui.TakeProgramResumeToken(); got != secretToken {
		t.Fatalf("TakeProgramResumeToken() = %q, want %q", got, secretToken)
	}
	if _, ok := os.LookupEnv(tui.ProgramResumeTokenEnv); ok {
		t.Fatal("OSC7577_TOKEN still present after TakeProgramResumeToken")
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	probe, err := json.Marshal(map[string]any{
		"command": exe,
		"args":    []string{"-test.run=^TestOSC7577EnvHelperProcess$", "--", "check-osc7577"},
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []harnessCase{
		{name: "subprocess-go", make: makeSubprocessGoHarness, extName: "sdk-fixture"},
		{name: "subprocess-node", make: makeSubprocessNodeHarness, extName: "node-sdk-fixture"},
		{name: "subprocess-rust", make: makeSubprocessRustHarness, extName: "rust-sdk-fixture"},
		{name: "subprocess-python", make: makeSubprocessPythonHarness, extName: "python-sdk-fixture"},
		{name: "subprocess-lisp", make: makeSubprocessLispHarness, extName: lispFixtureName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			cwd := t.TempDir()
			h.bridge.SetHostAction("exec", func(ctx context.Context, command string, args []string, opts *extension.ExecOptions) (extension.ExecResult, error) {
				return extension.ExecCommand(ctx, cwd, command, args, opts)
			})
			cmd, ok := findCommand(h.runner, "exec-limits-probe")
			if !ok {
				t.Fatal("exec-limits-probe command missing")
			}
			h.ui.ClearRecorded()
			if err := cmd.Handler(h.runner.DispatchContext(t.Context()), string(probe)); err != nil {
				t.Fatalf("exec-limits-probe: %v", err)
			}
			joined := strings.Join(h.ui.Recorded(), "\n")
			if !strings.Contains(joined, "present=false;value=") {
				t.Fatalf("%s child output = %q, want present=false;value=", tc.name, joined)
			}
			if strings.Contains(joined, secretToken) {
				t.Fatalf("%s leaked OSC7577_TOKEN into subprocess child: %q", tc.name, joined)
			}
		})
	}
}
