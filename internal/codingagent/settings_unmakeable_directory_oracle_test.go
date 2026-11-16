package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/settings-manager.ts

// FileSettingsStorage.withLock tests existsSync(path), which is false for every stat error (a regular file where a parent directory should be, a parent the process cannot search), so loading settings from an agent directory that cannot be made records no error (:228-232); the first write makes the directory (:238-241) and records Node's mkdir error for the setter that asked (enqueueWrite :576-588). PiG's load took the ENOTDIR of the stat as a load error and so refused the write. Each row runs Pi's SettingsManager on the same directory first.
func TestSettingsOfAnAgentDirectoryThatCannotBeMadeRecordNoLoadErrorAndTheFirstWriteReportsMkdir(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{"file where the directory should be": filepath.Join(file, "agent")}
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if probe, err := os.Create(filepath.Join(locked, "probe")); err == nil {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
	} else {
		cases["unwritable parent"] = filepath.Join(locked, "agent")
	}
	cwd := t.TempDir()
	for name, agentDir := range cases {
		t.Run(name, func(t *testing.T) {
			var want struct {
				Load  []string `json:"load"`
				Write []string `json:"write"`
			}
			pioracle.Run(t, `
const { SettingsManager } = await load("pi-coding-agent/core/settings-manager.js");
const describe = (errors) => errors.map((entry) => entry.scope + ": " + entry.error.message);
const settings = SettingsManager.create(input.cwd, input.agentDir);
const out = { load: describe(settings.drainErrors()), write: [] };
settings.setTheme("dark");
await settings.flush();
out.write = describe(settings.drainErrors());
emit(out);`, map[string]string{"cwd": cwd, "agentDir": agentDir}, &want)
			describe := func(errs []SettingsError) []string {
				out := []string{}
				for _, e := range errs {
					out = append(out, e.Scope+": "+e.Error.Error())
				}
				return out
			}
			sm := NewSettingsManager(cwd, agentDir)
			if got := describe(sm.DrainErrors()); !slices.Equal(got, want.Load) {
				t.Fatalf("errors after the load = %q, want Pi's %q", got, want.Load)
			}
			_ = sm.SetTheme("dark")
			if got := describe(sm.DrainErrors()); !slices.Equal(got, want.Write) {
				t.Fatalf("errors after the first write = %q, want Pi's %q", got, want.Write)
			}
		})
	}
}
