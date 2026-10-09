//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// loadNodeCommandDescription loads one Node extension through a Host in the
// given isolation mode and returns the description of the named command.
func loadNodeCommandDescription(t *testing.T, h *Host, entry, isolation, command string) string {
	t.Helper()
	nodeCellRequireNode(t)
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	loaded, errs := h.LoadAll(ctx, []ExtConfig{{Name: "probe", Source: entry, Enabled: true, Isolation: isolation}})
	if len(errs) != 0 {
		t.Fatalf("LoadAll errs = %v, want none", errs)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d extensions, want 1", len(loaded))
	}
	cmd, ok := loaded[0].Commands[command]
	if !ok {
		t.Fatalf("extension did not register /%s", command)
	}
	return cmd.Description
}

// Pi 0.87.1 loads extensions with the jiti release it pins, created with
// moduleCache false, tryNative false and its virtual modules. The fixture
// probes package.json "imports" and "exports" maps, extensionless and
// emitted-extension specifiers, index files, JSON, CommonJS interop,
// import.meta and the CommonJS globals; PiG must load it the same way
// (@gotgenes/pi-permission-system imports "#src/..." without an extension,
// confluence-cli reads __dirname).
func TestNodeExtensionModulesLoadLikePinnedPi(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	modRoot := findModuleRoot(t)
	entry := filepath.Join(modRoot, "test/parity", "scenarios", "extensions-runtime", "testdata", "module-loading", "index.ts")
	pinnedJiti := filepath.Join(modRoot, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "node_modules", "jiti", "lib", "jiti.cjs")
	if _, err := os.Stat(pinnedJiti); err != nil {
		t.Fatalf("pinned Pi package missing (run npm ci in extensions/sdk-ts): %v", err)
	}
	script := `const createJiti = require(process.argv[1]);
const jiti = createJiti(process.argv[1], { moduleCache: false, virtualModules: {}, tryNative: false });
jiti.import(process.argv[2], { default: true }).then(
  (factory) => factory({ registerCommand: (name, options) => process.stdout.write(options.description) }),
  (error) => { console.error(error); process.exit(1); });`
	out, err := exec.CommandContext(t.Context(), node, "-e", script, pinnedJiti, entry).Output()
	if err != nil {
		t.Fatalf("pinned jiti: %v", err)
	}
	var want map[string]any
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("pinned jiti probe %q: %v", out, err)
	}

	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			var got map[string]any
			description := loadNodeCommandDescription(t, NewHost(t.TempDir()), entry, isolation, "module-loading")
			if err := json.Unmarshal([]byte(description), &got); err != nil {
				t.Fatalf("probe %q: %v", description, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("module loading differs from Pi's jiti\n got: %s\nwant: %s", description, out)
			}
		})
	}
}

// Pi's setupCli titles the process "pi", sets PI_CODING_AGENT and AI_AGENT
// and silences process warnings, and process.argv is Pi's own: argv[1] is
// the CLI entry of the @earendil-works/pi-coding-agent package, and the
// arguments after it are the ones Pi was started with. @henryqw/pi-subagent
// requires the title and re-launches the harness with
// `process.execPath process.argv[1] ...`; in PiG that entry must run PiG.
// getPackageDir identifies the shipped SDK and assets (Pi config.ts:376-403),
// not the Node interpreter directory. Absolute host-SDK imports use that root.
func TestNodeExtensionSeesPiProcessIdentity(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	t.Setenv("PI_CODING_AGENT", "true")
	t.Setenv("AI_AGENT", "pi")
	entry := filepath.Join(t.TempDir(), "identity.ts")
	write(t, entry, `import { getPackageDir } from "@earendil-works/pi-coding-agent";
import { spawnSync } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import { pathToFileURL } from "node:url";
export default async function (pi: any) {
	const root = getPackageDir();
	const sdk = await import(pathToFileURL(path.join(root, "dist/index.js")).href);
	let warned = false;
	process.on("warning", () => { warned = true; });
	process.emitWarning("identity probe");
	await new Promise((resolve) => setImmediate(resolve));
	const entry = fs.realpathSync(process.argv[1]);
	const manifest = JSON.parse(fs.readFileSync(path.join(path.dirname(entry), "package.json"), "utf8"));
	const relaunch = spawnSync(process.execPath, [process.argv[1], "-e", "process.stdout.write(JSON.stringify(process.argv.slice(1)))", "a b"], { encoding: "utf8" });
	pi.registerCommand("identity", {
		description: JSON.stringify({
			title: process.title,
			args: process.argv.slice(2),
			warned,
			packageName: manifest.name,
			binIsEntry: path.resolve(path.dirname(entry), manifest.bin.pi) === entry,
			relaunch: [relaunch.status, relaunch.stdout, relaunch.stderr],
			packageDir: JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8")).name,
			sdk: typeof sdk.createAgentSession === "function" && typeof sdk.SessionManager.inMemory().getSessionId() === "string",
		}),
		handler: async () => {},
	});
}
`)
	want := map[string]any{
		"title":       "pi",
		"args":        []any{"--model", "e2e/e2e model"},
		"warned":      false,
		"packageName": "@earendil-works/pi-coding-agent",
		"binIsEntry":  true,
		// The harness binary here is node itself, so the re-launched
		// harness evaluates the script and prints its own arguments.
		"relaunch":   []any{float64(0), `["a b"]`, ""},
		"packageDir": "@earendil-works/pi-coding-agent",
		"sdk":        true,
	}
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			h := NewHost(t.TempDir())
			h.harnessBinary = node
			h.harnessArgs = []string{"--model", "e2e/e2e model"}
			description := loadNodeCommandDescription(t, h, entry, isolation, "identity")
			var got map[string]any
			if err := json.Unmarshal([]byte(description), &got); err != nil {
				t.Fatalf("probe %q: %v", description, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("process identity = %s, want %v", description, want)
			}
		})
	}
}
