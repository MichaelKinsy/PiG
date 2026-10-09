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

// rpc-mode.ts set_auto_compaction calls session.setAutoCompactionEnabled, which writes settingsManager.setCompactionEnabled
// (agent-session.ts:3235), and get_state reports session.autoCompactionEnabled, steeringMode and followUpMode
// (rpc-mode.ts get_state; agent-session.ts:1623-1630,3239). A later get_state therefore shows each setter's value.
func TestRPCStateSettersReachGetState(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	cwd := filepath.Join(home, "cwd")
	for _, dir := range []string{agentDir, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	input := strings.Join([]string{
		`{"id":"before","type":"get_state"}`,
		`{"id":"compaction","type":"set_auto_compaction","enabled":false}`,
		`{"id":"steering","type":"set_steering_mode","mode":"all"}`,
		`{"id":"after","type":"get_state"}`,
	}, "\n") + "\n"

	binary := buildPigBinaryForSignalTest(t)
	cmd := exec.Command(binary, "--mode", "rpc", "--model", "test-faux/echo", "--no-extensions", "--offline", "--no-session")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+home, "PI_HOME="+home, "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("RPC process: %v\n%s", err, stderr.String())
	}

	states := map[string]map[string]any{}
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode RPC output: %v", err)
		}
		id, _ := record["id"].(string)
		if record["type"] != "response" || id == "" {
			continue
		}
		if record["success"] != true {
			t.Fatalf("%s failed: %v\nstderr:\n%s", id, record, stderr.String())
		}
		data, _ := record["data"].(map[string]any)
		states[id] = data
	}
	before, after := states["before"], states["after"]
	if before == nil || after == nil {
		t.Fatalf("missing get_state responses: %v\nstderr:\n%s", states, stderr.String())
	}
	if before["autoCompactionEnabled"] != true || before["steeringMode"] != "one-at-a-time" {
		t.Fatalf("initial state = %v, want autoCompactionEnabled true and steeringMode one-at-a-time", before)
	}
	if after["autoCompactionEnabled"] != false || after["steeringMode"] != "all" || after["followUpMode"] != "one-at-a-time" {
		t.Fatalf("state after the setters = %v", after)
	}
	settings, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil || !strings.Contains(string(settings), `"enabled": false`) {
		t.Fatalf("settings.json = %s, %v; want compaction.enabled false persisted", settings, err)
	}
}
