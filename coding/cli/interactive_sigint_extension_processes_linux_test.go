//go:build linux

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// qsShutdownHolds is a Node extension that holds a timer from the moment it is loaded and whose session_shutdown handler writes the file @STARTED@ and never settles.
const qsShutdownHolds = `import { writeFileSync } from "node:fs";
export default function (pi) {
  setInterval(() => {}, 1 << 30);
  pi.on("session_shutdown", () => { writeFileSync("@STARTED@", "started"); return new Promise(() => {}); });
}
`

// TestInteractiveSigintExitEndsTheExtensionProcesses: a quit by keys whose session_shutdown handler never settles stays pending (pig emits it before its TUI stops); the ctrl+c that then ends pig (exit 130) must
// end the extension processes too. Pi's process is dead at the signal: nothing of it, and none of its extension runtimes, is left. pig 0.7.0 left the runtime of a Node
// extension that holds a timer running.
func TestInteractiveSigintExitEndsTheExtensionProcesses(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH:", err)
	}
	binary := buildPigBinaryForSignalTest(t)
	baseURL := "http://127.0.0.1:1/v1"
	root, _, project, sessions, env := qsScratch(t, baseURL)
	extension := filepath.Join(root, "shutdown-holds.mjs")
	started := filepath.Join(root, "shutdown-started")
	if err := os.WriteFile(extension, []byte(strings.ReplaceAll(qsShutdownHolds, "@STARTED@", started)), 0o600); err != nil {
		t.Fatal(err)
	}
	argv := []string{binary, "--no-extensions", "-e", extension, "--model", "localhold/hold-model", "--session-dir", sessions}
	p := startQSPane(t, project, filepath.Join(root, "stderr.log"), env, argv...)
	p.await("the model in the footer", func() bool { return strings.Contains(p.screen(), "hold-model") })
	children := qsChildren(p.pigPID())
	if len(children) == 0 {
		t.Fatalf("pig %d has no extension process", p.pig)
	}
	p.keys("C-d")
	p.await("the extension's session_shutdown handler", func() bool { _, err := os.Stat(started); return err == nil })
	if _, exited := p.status(); exited {
		t.Fatal("pig exited at the quit although the extension's session_shutdown handler never settles")
	}
	p.keys("C-c")
	status := 0
	p.await("pig's exit at ctrl+c", func() bool {
		code, exited := p.status()
		status = code
		return exited
	})
	if status != 130 {
		t.Errorf("pig exited with status %d at ctrl+c, want 130", status)
	}
	qsAwaitGone(t, children, "extension process")
}
