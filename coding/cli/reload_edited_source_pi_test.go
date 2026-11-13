package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// reloadNodeForms are the source forms of a Node extension, each as an entry that logs the VERSION its local helper module defines and a /rl
// command that awaits the reload. pi and pig are the versions the factory logs at the load and after an edit of the helper and /rl.
var reloadNodeForms = []struct {
	name    string
	files   map[string]string
	entry   string
	version string
	pi, pig string
}{
	{
		name: "ts",
		files: map[string]string{
			"probe.ts":   "import { appendFileSync } from \"node:fs\";\nimport { VERSION } from \"./version\";\nexport default function (pi: any) {\n  appendFileSync(process.env.RELOAD_PROBE_LOG!, \"factory:\" + VERSION + \"\\n\");\n  pi.registerCommand(\"rl\", { description: \"Reload\", handler: async (_args: string, ctx: any) => { await ctx.reload(); } });\n}\n",
			"version.ts": "export const VERSION = \"v1\";\n",
		},
		entry: "probe.ts", version: "version.ts",
		pi: "factory:v1\nfactory:v2", pig: "factory:v1\nfactory:v2",
	},
	{
		name: "mjs",
		files: map[string]string{
			"probe.mjs":   reloadNodeESMProbe("./version.mjs"),
			"version.mjs": "export const VERSION = \"v1\";\n",
		},
		entry: "probe.mjs", version: "version.mjs",
		pi: "factory:v1\nfactory:v1", pig: "factory:v1\nfactory:v2",
	},
	{
		name: "type-module-js",
		files: map[string]string{
			"package.json": `{"type":"module"}`,
			"probe.js":     reloadNodeESMProbe("./version.js"),
			"version.js":   "export const VERSION = \"v1\";\n",
		},
		entry: "probe.js", version: "version.js",
		pi: "factory:v1\nfactory:v1", pig: "factory:v1\nfactory:v2",
	},
	{
		name: "cjs",
		files: map[string]string{
			"probe.cjs":   reloadNodeCommonJSProbe("./version.cjs"),
			"version.cjs": "exports.VERSION = \"v1\";\n",
		},
		entry: "probe.cjs", version: "version.cjs",
		pi: "factory:v1\nfactory:v1", pig: "factory:v1\nfactory:v1",
	},
	{
		name: "commonjs-js",
		files: map[string]string{
			"probe.js":   reloadNodeCommonJSProbe("./version.js"),
			"version.js": "exports.VERSION = \"v1\";\n",
		},
		entry: "probe.js", version: "version.js",
		pi: "factory:v1\nfactory:v1", pig: "factory:v1\nfactory:v1",
	},
	{
		name:  "cjs-entry",
		files: map[string]string{"probe.cjs": reloadNodeCommonJSEntryProbe},
		entry: "probe.cjs", version: "probe.cjs",
		pi: "factory:v1\nfactory:v1", pig: "factory:v1\nfactory:v1",
	},
	{
		name:  "commonjs-js-entry",
		files: map[string]string{"probe.js": reloadNodeCommonJSEntryProbe},
		entry: "probe.js", version: "probe.js",
		pi: "factory:v1\nfactory:v1", pig: "factory:v1\nfactory:v1",
	},
}

func reloadNodeCommonJSProbe(version string) string {
	return "const { appendFileSync } = require(\"node:fs\");\nconst { VERSION } = require(\"" + version + "\");\nmodule.exports = function (pi) {\n  appendFileSync(process.env.RELOAD_PROBE_LOG, \"factory:\" + VERSION + \"\\n\");\n  pi.registerCommand(\"rl\", { description: \"Reload\", handler: async (_args, ctx) => { await ctx.reload(); } });\n};\n"
}

// reloadNodeCommonJSEntryProbe defines VERSION itself, so the edit is one of the entry.
const reloadNodeCommonJSEntryProbe = "const { appendFileSync } = require(\"node:fs\");\nconst VERSION = \"v1\";\nmodule.exports = function (pi) {\n  appendFileSync(process.env.RELOAD_PROBE_LOG, \"factory:\" + VERSION + \"\\n\");\n  pi.registerCommand(\"rl\", { description: \"Reload\", handler: async (_args, ctx) => { await ctx.reload(); } });\n};\n"

func reloadNodeESMProbe(version string) string {
	return "import { appendFileSync } from \"node:fs\";\nimport { VERSION } from \"" + version + "\";\nexport default function (pi) {\n  appendFileSync(process.env.RELOAD_PROBE_LOG, \"factory:\" + VERSION + \"\\n\");\n  pi.registerCommand(\"rl\", { description: \"Reload\", handler: async (_args, ctx) => { await ctx.reload(); } });\n}\n"
}

// D93, both sides. An extension's local module is edited and the extension's /rl command reloads. The pinned Pi imports the extension through
// jiti with moduleCache: false (core/extensions/loader.ts:571-602, jiti at :588-593): a TypeScript extension runs the edit, and an
// ES module (.mjs, or .js under "type": "module") goes to Node's own import(), which keeps the module by its URL, so Pi's factory runs again
// from the old module. PiG runs an ES module's edit too. A CommonJS extension (.cjs, or .js without "type": "module") stays in Node's require
// cache in both: neither runs an edit of the entry or of a module it requires. The Pi rows fail the day Pi changes its side: that is when D93's remove-when is checked.
func TestReloadOfAnEditedNodeExtensionAgainstPi(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and runs the pinned Pi and Node extensions")
	}
	for _, form := range reloadNodeForms {
		for _, impl := range []struct {
			name  string
			want  string
			start func(t *testing.T, logPath, entry string) *rpcProcess
		}{
			{"pi", form.pi, func(t *testing.T, logPath, entry string) *rpcProcess {
				return startPiRPCFaux(t, []string{"RELOAD_PROBE_LOG=" + logPath}, "-e", entry)
			}},
			{"pig", form.pig, func(t *testing.T, logPath, entry string) *rpcProcess {
				home := t.TempDir()
				env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_OFFLINE=1", "RELOAD_PROBE_LOG=" + logPath}
				return startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1", "-e", entry)
			}},
		} {
			t.Run(form.name+"/"+impl.name, func(t *testing.T) {
				dir := t.TempDir()
				for name, content := range form.files {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				logPath := filepath.Join(t.TempDir(), "reload.log")
				process := impl.start(t, logPath, filepath.Join(dir, form.entry))
				awaitReloadLog(t, logPath, "factory:v1", process.budget)
				versionPath := filepath.Join(dir, form.version)
				if err := os.WriteFile(versionPath, []byte(strings.Replace(string(mustReadFile(versionPath)), "v1", "v2", 1)), 0o600); err != nil {
					t.Fatal(err)
				}
				process.sendJSON(map[string]any{"id": "reload", "type": "prompt", "message": "/rl"})
				process.await("the prompt response", func(r rpcRecord) bool { return isSuccessResponse(r, "reload") })
				// The reload ran the factory again before the command answered.
				awaitReloadLog(t, logPath, impl.want, testbudget.Wait(t))
				process.closeAndWait("reload source probe")
				if got := strings.Join(readReloadLog(t, logPath), "\n"); got != impl.want {
					t.Errorf("extension log:\n%s\nwant:\n%s", got, impl.want)
				}
			})
		}
	}
}
