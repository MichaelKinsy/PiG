package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/tsruntime"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func runRuntimeStatus(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runExtensionsRuntime(t.Context(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// `pig extensions runtime status` reports the request with its source, the runtime a launch would use with the typed reason of a
// fallback, and the slot's runtimes; the global setting selects the runtime and PIG_TS_RUNTIME overrides it.
func TestExtensionsRuntimeStatusReportsRequestSelectionAndSlot(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(codingagent.ENV_AGENT_DIR, agentDir)
	t.Setenv(tsruntime.EnvRoot, "")
	t.Setenv(tsruntime.EnvRuntime, "")
	t.Chdir(t.TempDir())
	slot := filepath.Join(agentDir, "runtimes", "typescript")

	code, out, _ := runRuntimeStatus(t, "status")
	want := "Requested: node (default)\nSelected:  node\nPlatform:  " + tsruntime.Platform() + "\nSlot:      " + slot + "\nInstalled: none\n"
	if code != 0 || out != want {
		t.Fatalf("default status = %d %q, want %q", code, out, want)
	}

	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"extensionsTypescriptRuntime":"bun"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runRuntimeStatus(t, "status")
	if code != 0 || !strings.HasPrefix(out, "Requested: bun (setting)\nSelected:  node\nSkipped:   bun (not_installed: "+filepath.Join(slot, "bun")+" has no runtime.json)\n") {
		t.Fatalf("setting status = %d %q", code, out)
	}

	// A Bun executable the lock does not pin is listed but not used.
	dir := filepath.Join(slot, "bun")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bun"), []byte("not bun"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"bun","kind":"bun","version":"1.4.2","platform":"` + tsruntime.Platform() + `","provider":"pig-bun","image":{"executable":"bun","sha256":"` + strings.Repeat("0", 64) + `"}}`
	if err := os.WriteFile(filepath.Join(dir, tsruntime.ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(tsruntime.EnvRuntime, "bun")
	code, out, _ = runRuntimeStatus(t, "status", "--json")
	var status extensionsRuntimeStatus
	if err := json.Unmarshal([]byte(out), &status); code != 0 || err != nil {
		t.Fatalf("json status = %d %q (%v)", code, out, err)
	}
	if status.Request != (tsruntime.Request{ID: "bun", Source: tsruntime.SourceEnv}) || !status.Selection.IsNode() || status.Selection.Fallback == nil || status.Selection.Fallback.Reason != tsruntime.ReasonVerifyFailed {
		t.Fatalf("json status = %+v (fallback %+v)", status, status.Selection.Fallback)
	}
	if len(status.Installed) != 1 || status.Installed[0].Manifest == nil || status.Installed[0].Manifest.Provider != "pig-bun" || status.Root != slot {
		t.Fatalf("installed = %+v", status.Installed)
	}

	if code, _, errOut := runRuntimeStatus(t, "list"); code != 2 || !strings.Contains(errOut, "pig extensions runtime status") {
		t.Fatalf("unknown subcommand = %d %q", code, errOut)
	}
}

// An executable runtime that has built nothing yet falls through to its fallback and then Node with a reason for each, and a set that started only on
// Node is listed with the reason: the user's view of the compiled-extensions runtime (gate G-UX 2).
func TestExtensionsRuntimeStatusListsSkippedRuntimesAndNodeOnlySets(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(codingagent.ENV_AGENT_DIR, agentDir)
	t.Setenv(tsruntime.EnvRoot, "")
	t.Setenv(tsruntime.EnvRuntime, "aot")
	t.Chdir(t.TempDir())
	slot := filepath.Join(agentDir, "runtimes", "typescript")
	if err := os.MkdirAll(filepath.Join(slot, "aot"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"aot","kind":"executable","version":"0.7.4","platform":"` + tsruntime.Platform() + `","provider":"pig-aot","fallback":"bun"}`
	if err := os.WriteFile(filepath.Join(slot, "aot", tsruntime.ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(t.TempDir(), "index.ts")
	if err := os.WriteFile(entry, []byte("export default () => {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	members := []tsruntime.Member{{Name: "inspector-ext", Entry: entry}}
	set := tsruntime.Set{Key: tsruntime.SetKey([]byte("tree"), members), Members: members}
	if err := tsruntime.RecordNodeOnly(slot, set, "node:inspector is not implemented"); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runRuntimeStatus(t, "status")
	for _, want := range []string{
		"Requested: aot (env)\n",
		"Selected:  node\n",
		"Skipped:   aot (no_image:",
		"Skipped:   bun (not_installed:",
		"  aot  executable, 0.7.4, " + tsruntime.Platform() + ", from pig-aot, falls back to bun\n",
		"Node-only extension sets (they run on Node until a file of the set changes):\n  inspector-ext  node:inspector is not implemented\n",
	} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("status = %d\n%s\nwant it to contain %q", code, out, want)
		}
	}
	code, out, _ = runRuntimeStatus(t, "status", "--json")
	var status extensionsRuntimeStatus
	if err := json.Unmarshal([]byte(out), &status); code != 0 || err != nil || len(status.NodeOnly) != 1 || status.NodeOnly[0].Members[0].Name != "inspector-ext" || len(status.Selection.Skipped) != 2 {
		t.Fatalf("json status = %d %+v (%v)", code, status, err)
	}
}
