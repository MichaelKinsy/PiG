package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const sessionWritesExtension = `export default function (pi) {
  pi.on("session_info_changed", (e) => { process.stderr.write("event " + JSON.stringify(e.name) + "\n"); });
  pi.registerCommand("probe", { handler: async (a, ctx) => {
    const sm = ctx.sessionManager;
    const out = [];
    const before = sm.getBranch().length;
    pi.setSessionName("  spaced  ");
    out.push("name=" + JSON.stringify(pi.getSessionName()) + " sm=" + JSON.stringify(sm.getSessionName()) + " branch+" + (sm.getBranch().length - before) + " leaf=" + sm.getLeafEntry()?.type);
    const target = sm.getBranch().find((e) => e.type === "thinking_level_change");
    pi.setLabel(target.id, "mine");
    out.push("label=" + sm.getLabel(target.id) + " leaf=" + sm.getLeafEntry()?.type + " entries+" + (sm.getEntries().length - before));
    pi.setLabel(target.id, undefined);
    out.push("label=" + sm.getLabel(target.id));
    try { pi.setLabel("no-such-entry", "x"); out.push("unknown=ok"); } catch (e) { out.push("unknown=" + e.message); }
    process.stderr.write(out.join("\n") + "\n");
  }});
}
`

// Pi 1.1.0 appends the session_info and label entries to the in-process log inside pi.setSessionName and pi.setLabel (agent-session.ts setSessionName, session-manager.ts appendLabelChange), runs session_info_changed handlers inside setSessionName, and throws "Entry <id> not found" for an unknown label target. Pi prints exactly the lines below for this extension; the Node runtime sent both writes fire-and-forget, so reads in the same handler did not see them and an unknown entry only reached stderr.
func TestExtensionSessionWritesAreVisibleInTheSameHandlerAsPi(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	extPath := filepath.Join(t.TempDir(), "probe.mjs")
	if err := os.WriteFile(extPath, []byte(sessionWritesExtension), 0o600); err != nil {
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
	want := `event "spaced"
name="spaced" sm="spaced" branch+1 leaf=session_info
label=mine leaf=label entries+2
label=undefined
unknown=Entry no-such-entry not found
`
	if got := stderr.String(); got != want {
		t.Fatalf("stderr =\n%s\nwant (Pi 1.1.0)\n%s", got, want)
	}
}
