package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// reloadSourceProbe is one extension whose factory logs the version its helper source file defines and whose /rl command awaits the reload. entry is the path that -e names and version the file the test edits, both relative to the probe directory; build is the longest a cold build of the probe may take.
type reloadSourceProbe struct {
	name    string
	files   map[string]string
	entry   string
	version string
	build   time.Duration
}

func reloadSourceProbes(t *testing.T) []reloadSourceProbe {
	t.Helper()
	rustSDK, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-rs"))
	if err != nil {
		t.Fatal(err)
	}
	return []reloadSourceProbe{{
		name: "node-ts",
		files: map[string]string{
			"probe.ts":   "import { appendFileSync } from \"node:fs\";\nimport { VERSION } from \"./version\";\nexport default function (pi: any) {\n  appendFileSync(process.env.RELOAD_PROBE_LOG!, \"factory:\" + VERSION + \"\\n\");\n  pi.registerCommand(\"rl\", { description: \"Reload\", handler: async (_args: string, ctx: any) => { await ctx.reload(); } });\n}\n",
			"version.ts": "export const VERSION = \"v1\";\n",
		},
		entry:   "probe.ts",
		version: "version.ts",
	}, {
		name: "node-mjs",
		files: map[string]string{
			"probe.mjs":   "import { appendFileSync } from \"node:fs\";\nimport { VERSION } from \"./version.mjs\";\nexport default function (pi) {\n  appendFileSync(process.env.RELOAD_PROBE_LOG, \"factory:\" + VERSION + \"\\n\");\n  pi.registerCommand(\"rl\", { description: \"Reload\", handler: async (_args, ctx) => { await ctx.reload(); } });\n}\n",
			"version.mjs": "export const VERSION = \"v1\";\n",
		},
		entry:   "probe.mjs",
		version: "version.mjs",
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
		{
			name: "go",
			files: map[string]string{
				"reloadprobego/go.mod":       "module example.test/reloadprobego\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n",
				"reloadprobego/extension.go": "package reloadprobego\n\nimport (\n\t\"os\"\n\n\tsdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n)\n\nfunc Extension() *sdk.Extension {\n\tif file, err := os.OpenFile(os.Getenv(\"RELOAD_PROBE_LOG\"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {\n\t\t_, _ = file.WriteString(\"factory:\" + Version + \"\\n\")\n\t\t_ = file.Close()\n\t}\n\te := sdk.New(\"reloadprobego\")\n\te.Command(\"rl\", \"Reload\", func(ctx sdk.Context, _ string) error { return ctx.Reload() })\n\treturn e\n}\n",
				"reloadprobego/version.go":   "package reloadprobego\n\nconst Version = \"v1\"\n",
			},
			entry:   "reloadprobego",
			version: "reloadprobego/version.go",
		},
		{
			name: "rust",
			files: map[string]string{
				"reload_probe_rs/Cargo.toml":     "[package]\nname = \"reload_probe_rs\"\nversion = \"0.0.0\"\nedition = \"2024\"\n\n[dependencies]\npig-sdk = { path = " + strconv.Quote(filepath.ToSlash(rustSDK)) + " }\n",
				"reload_probe_rs/src/lib.rs":     "use pig_sdk::{CommandResult, Extension};\nuse std::io::Write;\n\nmod version;\n\npub fn new_extension() -> Extension {\n    if let Ok(mut log) = std::fs::OpenOptions::new().create(true).append(true).open(std::env::var(\"RELOAD_PROBE_LOG\").unwrap_or_default()) {\n        let _ = writeln!(log, \"factory:{}\", version::VERSION);\n    }\n    let mut ext = Extension::new(\"reload_probe_rs\");\n    ext.command(\"rl\", \"Reload\", |ctx, _| match ctx.reload() {\n        Ok(()) => CommandResult::Ok,\n        Err(error) => CommandResult::Error(error.to_string()),\n    });\n    ext\n}\n",
				"reload_probe_rs/src/version.rs": "pub const VERSION: &str = \"v1\";\n",
			},
			entry:   "reload_probe_rs",
			version: "reload_probe_rs/src/version.rs",
			// A cold build compiles the SDK crate and its dependencies.
			build: 10 * time.Minute,
		}}
}

// Pi's loader imports extensions through jiti with moduleCache: false (core/extensions/loader.ts:496-499), so /reload runs a TypeScript extension's factory from the source as it is now, its local imports included: an -e extension edited after it loaded reloads the edit (probed with Pi 1.0.0 in RPC mode). Pi keeps an edited .mjs extension's old code, which Node's ES module cache holds; PiG evaluates it again (D93). PiG re-invokes the factory in the process that holds the extension, and the Python runner re-imports an extension whose source changed since its import, so a Python extension reloads its edit as a TypeScript one does. A Go or Rust extension's source identifies its build, so an edit rebuilds it.
func TestReloadRunsTheCurrentSourceOfACommandLineExtension(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Node, Python, Go and Rust extensions")
	}
	for _, probe := range reloadSourceProbes(t) {
		t.Run(probe.name, func(t *testing.T) {
			if _, err := exec.LookPath("cargo"); err != nil && probe.name == "rust" {
				t.Skipf("cargo not found: %v", err)
			}
			dir := t.TempDir()
			for name, content := range probe.files {
				path := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
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
			wait := max(testbudget.Wait(t), probe.build)
			awaitReloadLog(t, logPath, "factory:v1", wait)

			versionPath := filepath.Join(dir, filepath.FromSlash(probe.version))
			edited := strings.Replace(string(mustReadFile(versionPath)), "v1", "v2", 1)
			if err := os.WriteFile(versionPath, []byte(edited), 0o600); err != nil {
				t.Fatal(err)
			}
			process.sendJSON(map[string]any{"id": "reload", "type": "prompt", "message": "/rl"})
			process.await("the prompt response", func(r rpcRecord) bool { return isSuccessResponse(r, "reload") })
			awaitReloadLog(t, logPath, "factory:v2", wait)
			process.closeAndWait("reload source probe")

			if got, want := strings.Join(readReloadLog(t, logPath), "\n"), "factory:v1\nfactory:v2"; got != want {
				t.Fatalf("extension log:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// awaitReloadLog waits up to wait until the probe log holds line.
func awaitReloadLog(t *testing.T, logPath, line string, wait time.Duration) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for !strings.Contains(string(mustReadFile(logPath)), line) {
		if time.Now().After(deadline) {
			t.Fatalf("the probe never logged %q; log:\n%s", line, mustReadFile(logPath))
		}
		time.Sleep(25 * time.Millisecond)
	}
}
