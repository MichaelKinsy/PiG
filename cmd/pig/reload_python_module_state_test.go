package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// pythonModuleStateProbeSource is a Python extension whose factory logs the versions its helper modules define, joined by "+", the number of factory calls its module has seen and its process. Each probe writes its own log, because the probes' factories run on concurrent threads of one process.
const pythonModuleStateProbeSource = `import os

import pig_sdk
IMPORTS
calls = 0


def new_extension() -> pig_sdk.Extension:
    global calls
    calls += 1
    ext = pig_sdk.Extension("NAME")
    with open(os.path.join(os.environ["RELOAD_PROBE_LOG"], "NAME.log"), "a", newline="") as log:
        log.write("NAME:" + VERSIONS + ":" + str(calls) + ":" + str(os.getpid()) + "\n")
COMMAND    return ext
`

func pythonModuleStateProbe(name string, command bool, helpers ...string) string {
	register := ""
	if command {
		register = "    ext.command(\"rl\", \"Reload\", lambda ctx, args: ctx.reload())\n"
	}
	var imports strings.Builder
	versions := make([]string, len(helpers))
	for i, helper := range helpers {
		fmt.Fprintf(&imports, "import %s\n", helper)
		versions[i] = helper + ".VERSION"
	}
	return strings.NewReplacer("NAME", name, "IMPORTS", imports.String(), "VERSIONS", strings.Join(versions, ` + "+" + `), "COMMAND", register).Replace(pythonModuleStateProbeSource)
}

// The Python runner holds every packed Python extension in one process and re-imports an extension on /reload only when a module it uses changed. An edit to a module of one extension must not reset the module state of another: rp_b shares rp_a's directory and imports a different helper, and rp_c lies in a directory nested under it. rp_a and rp_b both import rp_shared, and their factories start on concurrent threads, so either may import it first; an edit to rp_shared re-imports both. Each /reload re-invokes every factory in the retained process; an extension that kept its module counts on.
func TestReloadKeepsTheModuleStateOfAnUneditedPythonExtensionBesideAnEditedOne(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Python extensions")
	}
	dir := t.TempDir()
	files := map[string]string{
		"rp_a.py":            pythonModuleStateProbe("rp_a", true, "rp_a_helper", "rp_shared"),
		"rp_a_helper.py":     "VERSION = \"v1\"\n",
		"rp_b.py":            pythonModuleStateProbe("rp_b", false, "rp_b_helper", "rp_shared"),
		"rp_b_helper.py":     "VERSION = \"v1\"\n",
		"rp_shared.py":       "VERSION = \"v1\"\n",
		"sub/rp_c.py":        pythonModuleStateProbe("rp_c", false, "rp_c_helper"),
		"sub/rp_c_helper.py": "VERSION = \"v1\"\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	home, cwd, sessionDir, logDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "pig")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"quietStartup":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "RELOAD_PROBE_LOG=" + logDir}
	process := startRPCProcessAt(t, cwd, env, "--no-extensions", "--model", "test-faux/faux-1", "--session-dir", sessionDir,
		"-e", filepath.Join(dir, "rp_a.py"), "-e", filepath.Join(dir, "rp_b.py"), "-e", filepath.Join(dir, "sub", "rp_c.py"))
	wait := testbudget.Wait(t)
	probes := []string{"rp_a", "rp_b", "rp_c"}
	steps := []struct {
		edit, version string
		want          map[string]string
	}{
		{want: map[string]string{"rp_a": "v1+v1:1", "rp_b": "v1+v1:1", "rp_c": "v1:1"}},
		// An edit beside rp_b re-imports rp_a alone; rp_a imports the rp_shared that rp_b still uses.
		{edit: "rp_a_helper.py", version: "v2", want: map[string]string{"rp_a": "v2+v1:1", "rp_b": "v1+v1:2", "rp_c": "v1:2"}},
		// An edit in the nested directory re-imports rp_c alone.
		{edit: "sub/rp_c_helper.py", version: "v2", want: map[string]string{"rp_a": "v2+v1:2", "rp_b": "v1+v1:3", "rp_c": "v2:1"}},
		// An edit to the module rp_a and rp_b both import re-imports both, and so does a later one.
		{edit: "rp_shared.py", version: "v2", want: map[string]string{"rp_a": "v2+v2:1", "rp_b": "v1+v2:1", "rp_c": "v2:2"}},
		{edit: "rp_shared.py", version: "v3", want: map[string]string{"rp_a": "v2+v3:1", "rp_b": "v1+v3:1", "rp_c": "v2:3"}},
	}
	pid := ""
	for i, step := range steps {
		if step.edit != "" {
			path := filepath.Join(dir, filepath.FromSlash(step.edit))
			if err := os.WriteFile(path, []byte("VERSION = \""+step.version+"\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			id := fmt.Sprintf("reload-%d", i)
			process.sendJSON(map[string]any{"id": id, "type": "prompt", "message": "/rl"})
			process.await("the prompt response", func(r rpcRecord) bool { return isSuccessResponse(r, id) })
		}
		for _, probe := range probes {
			logPath := filepath.Join(logDir, probe+".log")
			lines := awaitPythonProbeLines(t, logPath, i+1, wait)
			version, calls, linePID := splitPythonProbeLine(t, lines[i], probe)
			if got := version + ":" + calls; got != step.want[probe] {
				t.Fatalf("step %d (%s): %s logged %s, want %s; every log:\n%s", i, valueOrNone(step.edit), probe, got, step.want[probe], pythonProbeLogs(logDir, probes))
			}
			if pid == "" {
				pid = linePID
			} else if linePID != pid {
				t.Fatalf("step %d: %s ran in process %s, want the one packed process %s; every log:\n%s", i, probe, linePID, pid, pythonProbeLogs(logDir, probes))
			}
		}
	}
	process.closeAndWait("python module state probe")
}

// awaitPythonProbeLines waits up to wait until the log at logPath holds count lines and returns them.
func awaitPythonProbeLines(t *testing.T, logPath string, count int, wait time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		text := strings.TrimRight(string(mustReadFile(logPath)), "\n")
		lines := strings.Split(text, "\n")
		if text != "" && len(lines) >= count {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s holds %d lines, want %d:\n%s", logPath, len(lines), count, text)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func splitPythonProbeLine(t *testing.T, line, probe string) (version, calls, pid string) {
	t.Helper()
	fields := strings.Split(line, ":")
	if len(fields) != 4 || fields[0] != probe {
		t.Fatalf("%s logged %q, want name:version:calls:pid", probe, line)
	}
	return fields[1], fields[2], fields[3]
}

func pythonProbeLogs(logDir string, probes []string) string {
	var b strings.Builder
	for _, probe := range probes {
		fmt.Fprintf(&b, "%s.log:\n%s", probe, mustReadFile(filepath.Join(logDir, probe+".log")))
	}
	return b.String()
}

func valueOrNone(edit string) string {
	if edit == "" {
		return "no edit"
	}
	return "edited " + edit
}
