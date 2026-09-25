//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ports the RPC half of upstream regressions/9068-user-bash-fail-closed:
// rpc-mode's bash command awaits emitUserBash first. A throwing handler or an
// invalid result fails the request with an extension_error and never runs the
// command; undefined runs it locally; a result override is returned without
// running it. Pig's RPC bash never dispatched user_bash at all.
func TestRPCBashDispatchesUserBash(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("testdata", "user-bash.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	p := startRPCProcessAt(t, cwd, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + t.TempDir()}, "--model", "test-faux/faux-1", "--no-session", "-e", fixture)

	cases := []struct {
		id, marker, wantError string
		ran                   bool
		wantOutput            string
	}{
		{id: "throw", marker: "THROW", wantError: "Routing failed"},
		{id: "empty", marker: "EMPTY", wantError: "Invalid user_bash handler result"},
		{id: "result", marker: "RESULT", wantOutput: "handled:"},
		{id: "local", marker: "LOCAL", ran: true, wantOutput: "local-ran"},
	}
	for _, tc := range cases {
		command := "touch ran-" + tc.id + " && echo local-ran # " + tc.marker
		p.sendJSON(map[string]any{"id": tc.id, "type": "bash", "command": command})
		var response rpcRecord
		sawExtensionError := false
		p.await("bash response "+tc.id, func(record rpcRecord) bool {
			if record["type"] == "extension_error" && record["event"] == "user_bash" && strings.Contains(record["error"].(string), tc.wantError) && tc.wantError != "" {
				if record["extensionPath"] != fixture {
					t.Errorf("%s: extension_error path = %v, want %s", tc.id, record["extensionPath"], fixture)
				}
				sawExtensionError = true
			}
			if record["type"] == "response" && record["id"] == tc.id {
				response = record
				return true
			}
			return false
		})
		_, statErr := os.Stat(filepath.Join(cwd, "ran-"+tc.id))
		if ran := statErr == nil; ran != tc.ran {
			t.Errorf("%s: command ran = %v, want %v", tc.id, ran, tc.ran)
		}
		if tc.wantError != "" {
			if response["success"] != false || !strings.Contains(response["error"].(string), tc.wantError) {
				t.Errorf("%s: response = %v, want a failure containing %q", tc.id, response, tc.wantError)
			}
			if !sawExtensionError {
				t.Errorf("%s: no user_bash extension_error before the response", tc.id)
			}
			continue
		}
		data, _ := response["data"].(map[string]any)
		if response["success"] != true || !strings.Contains(data["output"].(string), tc.wantOutput) {
			t.Errorf("%s: response = %v, want success with output containing %q", tc.id, response, tc.wantOutput)
		}
	}
	p.closeAndWait("after the bash commands")
}
