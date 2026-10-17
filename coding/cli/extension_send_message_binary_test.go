package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const sendMessageExtension = `export default function (pi) {
  pi.registerCommand("probe", { handler: async (a, ctx) => {
    pi.sendMessage({ customType: "note", content: "hello", display: true });
    const entries = ctx.sessionManager.getEntries().map((e) => e.type + (e.customType ? ":" + e.customType : ""));
    process.stderr.write("entries=" + JSON.stringify(entries) + " leaf=" + ctx.sessionManager.getLeafEntry()?.type + "\n");
  }});
}
`

// Pi 1.1.0's sendCustomMessage appends an idle, non-triggering message to the in-process log before pi.sendMessage returns (agent-session.ts sendCustomMessage, _appendCustomMessage), so the same handler reads it: Pi prints the line below. The Node runtime sent the message fire-and-forget and read the old log.
func TestExtensionSendMessageIsVisibleInTheSameHandlerAsPi(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	extPath := filepath.Join(t.TempDir(), "probe.mjs")
	if err := os.WriteFile(extPath, []byte(sendMessageExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	agentDir := t.TempDir()
	ctx := testbudget.Context(t)
	cmd := exec.CommandContext(ctx, bin, "-p", "--no-session", "--no-extensions", "--offline", "-e", extPath, "--model", "test-faux/faux-1", "/probe")
	cmd.Dir = agentDir
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("pig did not finish\nstderr: %s", stderr.String())
	}
	if err != nil {
		t.Fatalf("pig: %v\nstderr: %s", err, stderr.String())
	}
	want := `entries=["model_change","thinking_level_change","custom_message:note"] leaf=custom_message` + "\n"
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want Pi 1.1.0's %q", got, want)
	}
}

const sendTriggerExtension = `export default function (pi) {
  pi.registerCommand("probe", { handler: async (a, ctx) => {
    pi.sendMessage({ customType: "note", content: "What is 20+22?", display: true }, { triggerTurn: true });
    process.stderr.write("idle=" + ctx.isIdle() + "\n");
  }});
}
`

// Pi 1.1.0's sendCustomMessage with triggerTurn starts the run inside pi.sendMessage (it awaits _runAgentPrompt), so ctx.isIdle() right after is false; print mode then prints state.messages' last message (not yet an assistant one) and disposes the runtime without reporting the cancelled run. Pi's stderr is exactly "idle=false\n". The Node runtime sent the message fire-and-forget (idle=true) and print mode reported "Extension error (<runtime>): context canceled".
func TestExtensionSendMessageTriggerTurnStartsTheRunAsPi(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	extPath := filepath.Join(t.TempDir(), "probe.mjs")
	if err := os.WriteFile(extPath, []byte(sendTriggerExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	agentDir := t.TempDir()
	ctx := testbudget.Context(t)
	cmd := exec.CommandContext(ctx, bin, "-p", "--no-session", "--no-extensions", "--offline", "-e", extPath, "--model", "test-faux/faux-1", "/probe")
	cmd.Dir = agentDir
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("pig did not finish\nstderr: %s", stderr.String())
	}
	if err != nil {
		t.Fatalf("pig: %v\nstderr: %s", err, stderr.String())
	}
	if got := stderr.String(); got != "idle=false\n" {
		t.Fatalf("stderr = %q, want Pi 1.1.0's %q", got, "idle=false\n")
	}
}
