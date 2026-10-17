package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const notifyTypeExtension = `export default function (pi) {
  pi.registerCommand("probe", { handler: async (a, ctx) => {
    ctx.ui.notify("plain");
    ctx.ui.notify("warned", "warning");
  }});
}
`

// ExtensionUIContext.notify's type is optional, and Pi 1.1.0's RPC mode writes it as given (rpc-mode.ts notify: notifyType: type), so a notify without a type has no notifyType member. The Node runtime defaulted it to "info".
func TestRPCNotifyWithoutATypeHasNoNotifyType(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	dir := t.TempDir()
	extPath := filepath.Join(dir, "probe.mjs")
	if err := os.WriteFile(extPath, []byte(notifyTypeExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	stdinPath := filepath.Join(dir, "commands.jsonl")
	if err := os.WriteFile(stdinPath, []byte(`{"id":"c","type":"prompt","message":"/probe"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, err := os.Open(stdinPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	agentDir := t.TempDir()
	cmd := exec.Command(bin, "--mode", "rpc", "--no-session", "--no-extensions", "--offline", "-e", extPath, "--model", "test-faux/faux-1")
	cmd.Dir = agentDir
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := testenv.Run(t, cmd); err != nil {
		t.Fatalf("pig --mode rpc: %v\nstderr: %s", err, stderr.String())
	}
	var notifies []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(stdout.String()), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["method"] == "notify" {
			delete(record, "id")
			notifies = append(notifies, record)
		}
	}
	if len(notifies) != 2 {
		t.Fatalf("notify requests = %v\nstdout:\n%s", notifies, stdout.String())
	}
	if _, has := notifies[0]["notifyType"]; has || notifies[0]["message"] != "plain" {
		t.Errorf("notify without a type = %v, want no notifyType", notifies[0])
	}
	if notifies[1]["notifyType"] != "warning" {
		t.Errorf("notify with a type = %v, want notifyType warning", notifies[1])
	}
}
