package codingagent

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// settings.json and trust.json are replaced through a temporary file and a rename, and Pi reads and rewrites the same files in the same agent directory. Run A is PiG's write followed by Pi's; run B is Pi's own two writes. Pi must read what PiG wrote, PiG must read what Pi wrote, and both runs must end with the same bytes.
func TestSettingsAndTrustShareTheAgentDirectoryWithPi(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent"))
	if err != nil {
		t.Fatal(err)
	}
	const script = `(async () => {
  const path = require('node:path');
  const [root, agentDir, cwd, first, second, step] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(path.join(root, rel)).href);
  const { ProjectTrustStore } = await load('dist/core/trust-manager.js');
  const { SettingsManager } = await load('dist/core/settings-manager.js');
  const settings = SettingsManager.create(cwd, agentDir);
  const trust = new ProjectTrustStore(agentDir);
  if (step === 'first') { trust.set(first, true); settings.setTheme('dark'); }
  trust.set(second, false); settings.setHideThinkingBlock(true);
  await settings.flush();
  console.log(JSON.stringify({ first: trust.get(first), second: trust.get(second), theme: settings.getTheme() }));
})()`
	runPi := func(agentDir, cwd, step string) string {
		t.Helper()
		out, err := exec.CommandContext(t.Context(), "node", "-e", script, root, agentDir, cwd, "/work/first", "/work/second", step).CombinedOutput()
		if err != nil {
			t.Fatalf("node (%s): %v\n%s", step, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// Run B: Pi writes both.
	piDir, piCwd := t.TempDir(), t.TempDir()
	runPi(piDir, piCwd, "first")
	// Run A: PiG writes the first change, Pi the second.
	pigDir, pigCwd := t.TempDir(), t.TempDir()
	if err := NewProjectTrustStore(pigDir).Set("/work/first", new(true)); err != nil {
		t.Fatal(err)
	}
	settings := NewSettingsManager(pigCwd, pigDir)
	if err := settings.SetTheme("dark"); err != nil {
		t.Fatal(err)
	}
	if err := settings.Flush(); err != nil {
		t.Fatal(err)
	}
	if got, want := runPi(pigDir, pigCwd, "second"), runPi(t.TempDir(), t.TempDir(), "first"); got != want {
		t.Fatalf("Pi after PiG's write reports %s, want what it reports after its own %s", got, want)
	}
	// PiG reads what Pi wrote after PiG's files were replaced.
	if decision, err := NewProjectTrustStore(pigDir).Get("/work/second"); err != nil || decision == nil || *decision {
		t.Fatalf("PiG reads Pi's trust decision as %v, %v, want false", decision, err)
	}
	if theme := NewSettingsManager(pigCwd, pigDir).GetTheme(); theme != "dark" {
		t.Fatalf("PiG reads theme %q after Pi's write, want dark", theme)
	}
	for _, name := range []string{"settings.json", "trust.json"} {
		a, errA := os.ReadFile(filepath.Join(piDir, name))
		b, errB := os.ReadFile(filepath.Join(pigDir, name))
		if errA != nil || errB != nil || !bytes.Equal(a, b) {
			t.Errorf("%s differs: Pi alone wrote %q (%v), PiG then Pi wrote %q (%v)", name, a, errA, b, errB)
		}
	}
	entries, _ := os.ReadDir(pigDir)
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") {
			t.Errorf("a temporary file is left in the agent directory: %s", entry.Name())
		}
	}
}
