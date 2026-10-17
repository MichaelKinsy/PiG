package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Without --session-dir, RPC session replacement must use the default
// <agent-dir>/sessions/<encoded cwd> directory, as upstream's SessionManager
// does. PiG used to pass an empty directory and failed with
// "sessionmanager: mkdir: mkdir : no such file or directory".
//
// A clone of the fresh Session that new_session created fails as in Pi 0.87.1
// (agent-session-runtime.ts fork): that Session has no assistant reply, so its
// file does not exist yet, and the current Session stays in place.
func TestRPCSessionReplacementUsesDefaultSessionDir(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	cwd := filepath.Join(home, "cwd")
	for _, dir := range []string{agentDir, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	input := strings.Join([]string{
		`{"id":"new","type":"new_session"}`,
		`{"id":"new-state","type":"get_state"}`,
		`{"id":"clone","type":"clone"}`,
		`{"id":"clone-state","type":"get_state"}`,
	}, "\n") + "\n"

	binary := buildPigBinaryForSignalTest(t)
	cmd := exec.Command(binary, "--mode", "rpc", "--model", "test-faux/echo", "--no-extensions", "--offline")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("RPC process: %v\n%s", err, stderr.String())
	}

	responses := map[string]map[string]any{}
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode RPC output: %v", err)
		}
		if id, _ := record["id"].(string); id != "" && record["type"] == "response" {
			responses[id] = record
		}
	}
	sessionsRoot := filepath.Join(agentDir, "sessions") + string(filepath.Separator)
	var files []string
	for _, id := range []string{"new", "new-state", "clone-state"} {
		response := responses[id]
		if response == nil || response["success"] != true {
			t.Fatalf("%s response = %v\nstderr:\n%s", id, response, stderr.String())
		}
		if strings.HasSuffix(id, "-state") {
			data, _ := response["data"].(map[string]any)
			file, _ := data["sessionFile"].(string)
			if !strings.HasPrefix(file, sessionsRoot) {
				t.Fatalf("%s sessionFile = %q, want under %s", id, file, sessionsRoot)
			}
			files = append(files, file)
		}
	}
	const unsaved = "This session has not been saved yet. Send a message before cloning or forking it."
	if response := responses["clone"]; response == nil || response["success"] != false || response["error"] != unsaved {
		t.Fatalf("clone of an unsaved Session = %v, want the Pi error %q", response, unsaved)
	}
	if files[0] != files[1] {
		t.Fatalf("a failed clone changed the session file: %q", files)
	}
}
