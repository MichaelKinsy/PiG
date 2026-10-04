package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// reloadSourceProbe is one extension whose factory logs the VERSION its helper module defines and whose /rl command awaits ctx.reload(). Its first file is the entry that -e names; version is the file the test edits.
type reloadSourceProbe struct {
	name    string
	files   map[string]string
	entry   string
	version string
}

var reloadSourceProbes = []reloadSourceProbe{
	{
		name: "node-ts",
		files: map[string]string{
			"probe.ts":   "import { appendFileSync } from \"node:fs\";\nimport { VERSION } from \"./version\";\nexport default function (pi: any) {\n  appendFileSync(process.env.RELOAD_PROBE_LOG!, \"factory:\" + VERSION + \"\\n\");\n  pi.registerCommand(\"rl\", { description: \"Reload\", handler: async (_args: string, ctx: any) => { await ctx.reload(); } });\n}\n",
			"version.ts": "export const VERSION = \"v1\";\n",
		},
		entry:   "probe.ts",
		version: "version.ts",
	},
	{
		name: "python",
		files: map[string]string{
			"reload_probe.py":         "import os\n\nimport pig_sdk\nfrom reload_probe_version import VERSION\n\n\ndef new_extension() -> pig_sdk.Extension:\n    ext = pig_sdk.Extension(\"reload_probe\")\n    with open(os.environ[\"RELOAD_PROBE_LOG\"], \"a\", newline=\"\") as log:\n        log.write(\"factory:\" + VERSION + \"\\n\")\n    ext.command(\"rl\", \"Reload\", lambda ctx, args: ctx.reload())\n    return ext\n",
			"reload_probe_version.py": "VERSION = \"v1\"\n",
		},
		entry:   "reload_probe.py",
		version: "reload_probe_version.py",
	},
}

// Pi's loader imports extensions through jiti with moduleCache: false (core/extensions/loader.ts:496-499), so /reload runs each factory from the source as it is now, its local imports included: an -e extension edited after it loaded reloads the edit (probed with Pi 1.0.0 in RPC mode). PiG re-invokes the factory in the process that holds the extension, and the Python runner re-imports an extension whose source changed since its import, so a Python extension reloads its edit as a TypeScript one does.
func TestReloadRunsTheCurrentSourceOfACommandLineExtension(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Node and Python extensions")
	}
	for _, probe := range reloadSourceProbes {
		t.Run(probe.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range probe.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			home, cwd, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
			agentDir := filepath.Join(home, "pig")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"quietStartup":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(t.TempDir(), "reload.log")
			env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "RELOAD_PROBE_LOG=" + logPath}
			process := startRPCProcessAt(t, cwd, env, "--no-extensions", "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", filepath.Join(dir, probe.entry))
			awaitReloadLog(t, logPath, "factory:v1")

			versionPath := filepath.Join(dir, probe.version)
			edited := strings.Replace(string(mustReadFile(versionPath)), "v1", "v2", 1)
			if err := os.WriteFile(versionPath, []byte(edited), 0o600); err != nil {
				t.Fatal(err)
			}
			process.sendJSON(map[string]any{"id": "reload", "type": "prompt", "message": "/rl"})
			process.await("the prompt response", func(r rpcRecord) bool { return isSuccessResponse(r, "reload") })
			awaitReloadLog(t, logPath, "factory:v2")
			process.closeAndWait("reload source probe")

			if got, want := strings.Join(readReloadLog(t, logPath), "\n"), "factory:v1\nfactory:v2"; got != want {
				t.Fatalf("extension log:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// awaitReloadLog waits until the probe log holds line.
func awaitReloadLog(t *testing.T, logPath, line string) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for !strings.Contains(string(mustReadFile(logPath)), line) {
		if time.Now().After(deadline) {
			t.Fatalf("the probe never logged %q; log:\n%s", line, mustReadFile(logPath))
		}
		time.Sleep(25 * time.Millisecond)
	}
}
