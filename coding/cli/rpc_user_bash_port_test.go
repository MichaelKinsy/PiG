package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRPCUserBashFailurePort(t *testing.T) {
	t.Parallel()
	shell := filepath.Join(t.TempDir(), "bash-spy.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", shell, "./testdata/user-bash-shell-spy.go")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build shell spy: %v: %s", err, output)
	}
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/9068-user-bash-fail-closed.test.ts:183,148-173 (all three rpcCases rows).
	for _, tc := range []struct {
		name, mode, error string
		executions        int
	}{
		{"fails the request without executing bash when a handler throws", "throw", "Routing failed", 0},
		{"fails the request without executing bash when a handler returns an empty result", "empty", "Invalid user_bash handler result", 0},
		{"executes bash normally when a handler returns undefined", "undefined", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, home := t.TempDir(), t.TempDir()
			counter := filepath.Join(dir, "executions")
			// The upstream Session executor spy returns this same result. A local shell
			// spy keeps the RPC/Session/extension path real and records every invocation.
			agentDir := filepath.Join(home, "agent")
			if err := os.MkdirAll(agentDir, 0o700); err != nil {
				t.Fatal(err)
			}
			settings, err := json.Marshal(map[string]string{"shellPath": shell})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), settings, 0o600); err != nil {
				t.Fatal(err)
			}
			fixture := filepath.Join(dir, "user-bash.mjs")
			const source = `export default function(pi) { pi.on("user_bash", async (event) => { if(event.command !== "pwd") throw new Error("unexpected command"); if(process.env.PORT_USER_BASH === "throw") throw new Error("Routing failed"); if(process.env.PORT_USER_BASH === "empty") return {}; return undefined; }); }`
			if err := os.WriteFile(fixture, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			process := startRPCProcessAt(t, dir, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + home, "PORT_USER_BASH=" + tc.mode, "PORT_BASH_COUNTER=" + counter}, "--model", "test-faux/faux-1", "--no-session", "-e", fixture)
			process.sendJSON(map[string]any{"id": "bash-request", "type": "bash", "command": "pwd"})
			var response rpcRecord
			sawError := false
			process.await("bash-request", func(record rpcRecord) bool {
				if record["type"] == "extension_error" && record["event"] == "user_bash" {
					message, _ := record["error"].(string)
					if strings.Contains(message, tc.error) {
						sawError = true
					}
				}
				if record["type"] == "response" && record["id"] == "bash-request" {
					response = record
					return true
				}
				return false
			})
			if response["command"] != "bash" || response["success"] != (tc.error == "") {
				t.Fatalf("response = %+v", response)
			}
			if tc.error != "" {
				message, _ := response["error"].(string)
				if !strings.Contains(message, tc.error) || !sawError {
					t.Fatalf("response = %+v, extension_error=%v", response, sawError)
				}
			} else {
				data, ok := response["data"].(map[string]any)
				if !ok || data["output"] != "local output" || data["exitCode"] != float64(0) || data["cancelled"] != false || data["truncated"] != false {
					t.Fatalf("data = %#v", response["data"])
				}
			}
			calls, err := os.ReadFile(counter)
			if tc.executions == 0 {
				if !os.IsNotExist(err) {
					t.Fatalf("unexpected execution: %q, %v", calls, err)
				}
			} else if err != nil || string(calls) != "pwd\n" {
				t.Fatalf("executions = %q, %v; want one pwd", calls, err)
			}
			process.closeAndWait("after user_bash response")
		})
	}
}
