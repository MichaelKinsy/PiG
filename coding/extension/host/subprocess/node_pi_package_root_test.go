//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// A subagent extension finds the Pi CLI to start from the package root of
// "@earendil-works/pi-coding-agent": pi-open-agents 0.1.27
// (src/subagent/executor.ts resolvePiEntryPoint) takes
// import.meta.resolve("@earendil-works/pi-coding-agent"), goes up two
// directories (Pi's package.json "exports" names ./dist/index.js, so that is
// the package root) and appends dist/cli.js, which Pi's tsc build writes next to
// dist/index.js. It falls back to process.execPath and process.argv[1] only
// when the resolve throws. Whichever way the resolve goes in a PiG extension
// process, the entry the extension computes must start the harness with the
// arguments it is given, exactly as process.argv[1] does
// (TestNodeExtensionSeesPiProcessIdentity). The fixture is that function.
// upstream: packages/coding-agent/package.json main, exports and bin; pi-open-agents src/subagent/executor.ts.
func TestNodeExtensionStartsThePiCLIFromTheResolvedPackageRoot(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	entry := filepath.Join(t.TempDir(), "open-agents.ts")
	write(t, entry, `import { spawnSync } from "node:child_process";
import { realpathSync } from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
function resolvePiEntryPoint() {
	let packageEntryPoint: string;
	try {
		packageEntryPoint = fileURLToPath(import.meta.resolve("@earendil-works/pi-coding-agent"));
	} catch {
		return { resolved: false, entryPoint: realpathSync(process.argv[1]) };
	}
	const packageRoot = path.dirname(path.dirname(packageEntryPoint));
	return { resolved: true, entryPoint: path.join(packageRoot, "dist", "cli.js") };
}
export default function (pi: any) {
	const { resolved, entryPoint } = resolvePiEntryPoint();
	const child = spawnSync(process.execPath, [entryPoint, "-e", "process.stdout.write(JSON.stringify(process.argv.slice(1)))", "a b"], { encoding: "utf8" });
	pi.registerCommand("open-agents", {
		description: JSON.stringify({ resolved, relaunch: [child.status, child.stdout, (child.stderr ?? "").split("\n")[0]] }),
		handler: async () => {},
	});
}
`)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			h := NewHost(t.TempDir())
			// The harness binary here is node itself, so the started harness evaluates the script and prints its own arguments.
			h.harnessBinary = node
			h.harnessArgs = []string{"--model", "e2e/e2e model"}
			description := loadNodeCommandDescription(t, h, entry, isolation, "open-agents")
			var got struct {
				Resolved bool  `json:"resolved"`
				Relaunch []any `json:"relaunch"`
			}
			if err := json.Unmarshal([]byte(description), &got); err != nil {
				t.Fatalf("probe %q: %v", description, err)
			}
			want := []any{float64(0), `["a b"]`, ""}
			if !reflect.DeepEqual(got.Relaunch, want) {
				t.Fatalf("starting the CLI from the resolved package root (resolved=%v) = %v, want %v", got.Resolved, got.Relaunch, want)
			}
		})
	}
}
