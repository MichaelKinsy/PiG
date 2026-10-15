package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const rpcCustomUIExtension = `export default function (pi) {
  pi.registerCommand("probe", { handler: async (a, ctx) => {
    const value = await ctx.ui.custom((tui, theme, keybindings, done) => {
      process.stderr.write("FACTORY RAN\n");
      done(7);
      return { render: () => [], invalidate() {} };
    });
    ctx.ui.notify("custom=" + String(value), "info");
  }});
}
`

// Pi 1.1.0's RPC UI context has no custom UI (rpc-mode.ts custom(): return undefined), so ctx.ui.custom resolves undefined without running the factory. PiG ran the factory, resolved its done value or failed the command with "no_ui: ui.custom requires an interactive TUI".
func TestRPCCustomUIResolvesUndefinedWithoutRunningTheFactory(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	dir := t.TempDir()
	extPath := filepath.Join(dir, "probe.mjs")
	if err := os.WriteFile(extPath, []byte(rpcCustomUIExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	agentDir := t.TempDir()
	cmd := exec.Command(bin, "--mode", "rpc", "--no-session", "--no-extensions", "--offline", "-e", extPath, "--model", "test-faux/faux-1")
	cmd.Dir = agentDir
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stdout strings.Builder
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The command answers after its handler settles; stdin stays open until then so input end cannot cut the handler short.
	answered := make(chan struct{})
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(nil, 16<<20)
		for scanner.Scan() {
			stdout.WriteString(scanner.Text() + "\n")
			var record struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			}
			if json.Unmarshal(scanner.Bytes(), &record) == nil && record.Type == "response" && record.ID == "c" {
				close(answered)
			}
		}
	}()
	if _, err := io.WriteString(stdin, `{"id":"c","type":"prompt","message":"/probe"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-answered:
	case <-time.After(testbudget.Wait(t)):
		_ = cmd.Process.Kill()
		<-readDone
		t.Fatalf("no response to the prompt\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	_ = stdin.Close()
	<-readDone
	if err := cmd.Wait(); err != nil {
		t.Fatalf("pig --mode rpc: %v\nstderr: %s", err, stderr.String())
	}
	var messages []string
	for line := range strings.SplitSeq(strings.TrimSpace(stdout.String()), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}
		if record["type"] == "extension_error" {
			t.Errorf("extension_error: %v", record)
		}
		if record["method"] == "notify" {
			message, _ := record["message"].(string)
			messages = append(messages, message)
		}
	}
	if len(messages) != 1 || messages[0] != "custom=undefined" {
		t.Errorf("notify messages = %q, want [custom=undefined]\nstdout:\n%s", messages, stdout.String())
	}
	if strings.Contains(stderr.String(), "FACTORY RAN") {
		t.Errorf("the custom UI factory ran in RPC mode\nstderr:\n%s", stderr.String())
	}
}
