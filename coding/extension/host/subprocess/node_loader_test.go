package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNodeRuntimeLoader_SupportsLegacyAndCurrentNamespaces(t *testing.T) {
	t.Parallel()

	modRoot := findModuleRoot(t)
	loaderPath := filepath.Join(modRoot, "coding", "extension", "host", "subprocess", "runtime-node", "loader.mjs")
	data, err := os.ReadFile(loaderPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", loaderPath, err)
	}
	text := string(data)

	for _, spec := range []string{
		`"@earendil-works/pi-coding-agent"`,
		`"@mariozechner/pi-coding-agent"`,
		`"@earendil-works/pi-tui"`,
		`"@mariozechner/pi-tui"`,
		`"@earendil-works/pi-ai"`,
		`"@mariozechner/pi-ai"`,
	} {
		if !strings.Contains(text, spec) {
			t.Fatalf("loader missing shim alias %s", spec)
		}
	}
}

func TestNodeRuntimeLoaderProvidesUpstreamHelloExampleExports(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the loader fixture: %v", err)
	}
	modRoot := findModuleRoot(t)
	runtimeRoot := filepath.Join(modRoot, "coding", "extension", "host", "subprocess", "runtime-node")
	command := exec.Command(node,
		"--import", registerLoaderURL(t, runtimeRoot),
		"--input-type=module",
		"--eval", `import { Type, uuidv7 } from "@earendil-works/pi-ai"; import { defineTool } from "@earendil-works/pi-coding-agent"; import { CURSOR_MARKER, isKeyRelease } from "@earendil-works/pi-tui"; const tool = { name: "hello", parameters: Type.Object({ value: Type.String() }) }; if (defineTool(tool) !== tool || !/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(uuidv7()) || CURSOR_MARKER !== "\x1b_pi:c\x07" || !isKeyRelease("\x1b[65;1:3u")) process.exit(1);`,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("upstream hello example exports: %v\n%s", err, output)
	}
}

func TestNodeRuntimePureHelpersMatchPinnedPi(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the loader fixture: %v", err)
	}
	modRoot := findModuleRoot(t)
	runtimeRoot := filepath.Join(modRoot, "coding", "extension", "host", "subprocess", "runtime-node")
	upstream := filepath.Join(modRoot, ".upstream", "current", "packages", "coding-agent", "src", "core")
	script := `
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const shim = await import(pathToFileURL(process.argv[1]));
const messages = await import(pathToFileURL(process.argv[2]));
const compaction = await import(pathToFileURL(process.argv[3]));
const truncate = await import(pathToFileURL(process.argv[4]));
const samples = [
  { role: "user", content: [{ type: "text", text: "hello" }], timestamp: 1 },
  { role: "bashExecution", command: "false", output: "bad", exitCode: 1, cancelled: false, truncated: false, timestamp: 2 },
  { role: "custom", customType: "x", content: "custom", display: true, timestamp: 3 },
  { role: "branchSummary", summary: "branch", fromId: "a", timestamp: 4 },
  { role: "compactionSummary", summary: "compact", tokensBefore: 1, timestamp: 5 },
];
assert.deepEqual(shim.convertToLlm(samples), messages.convertToLlm(samples));
const llm = [{ role: "assistant", content: [{ type: "thinking", thinking: "why" }, { type: "text", text: "answer" }, { type: "toolCall", name: "read", arguments: { path: "x" } }] }];
assert.equal(shim.serializeConversation(llm), compaction.serializeConversation(llm));
for (const [content, options] of [["one\\ntwo\\nthree", { maxLines: 2 }], ["ééé", { maxBytes: 3 }], ["short", {}]]) {
  assert.deepEqual(shim.truncateHead(content, options), truncate.truncateHead(content, options));
}
`
	command := exec.Command(node,
		"--import", registerLoaderURL(t, runtimeRoot),
		"--input-type=module", "--eval", script,
		filepath.Join(runtimeRoot, "shims", "pi-coding-agent.mjs"),
		filepath.Join(upstream, "messages.ts"),
		filepath.Join(upstream, "compaction", "utils.ts"),
		filepath.Join(upstream, "tools", "truncate.ts"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("pure helper parity: %v\n%s", err, output)
	}
}

func TestNodeRuntimeLoaderResolvesExtensionlessTypeScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the loader fixture: %v", err)
	}
	modRoot := findModuleRoot(t)
	runtimeRoot := filepath.Join(modRoot, "coding", "extension", "host", "subprocess", "runtime-node")
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"type":"module"}`)
	write(t, filepath.Join(dir, "helper.ts"), `export const value: string = "resolved";`)
	entry := filepath.Join(dir, "entry.ts")
	write(t, entry, `import { value } from "./helper"; if (value !== "resolved") process.exit(1);`)
	command := exec.Command(node, "--import", registerLoaderURL(t, runtimeRoot), entry)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("extensionless TypeScript import: %v\n%s", err, output)
	}
}

func TestTSFixture_UsesCurrentPiAINamespace(t *testing.T) {
	t.Parallel()

	modRoot := findModuleRoot(t)
	fixturePath := filepath.Join(modRoot, "coding", "extension", "host", "subprocess", "testdata", "ts-fixture.ts")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", fixturePath, err)
	}
	text := string(data)
	if !strings.Contains(text, `from "@earendil-works/pi-ai"`) {
		t.Fatalf("fixture missing current pi-ai namespace: %s", fixturePath)
	}
	if strings.Contains(text, `from "@mariozechner/pi-ai"`) {
		t.Fatalf("fixture still imports legacy pi-ai namespace: %s", fixturePath)
	}
}

// registerLoaderURL names the runtime's loader hooks the way the launcher does:
// --import takes a module specifier, not a platform path.
func registerLoaderURL(t *testing.T, runtimeRoot string) string {
	t.Helper()
	loaderURL, err := nodeFileURL(filepath.Join(runtimeRoot, "register-loader.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	return loaderURL
}

// piVirtualModulesWithoutShim lists specifiers Pi serves from its own bundle
// (core/extensions/virtual-modules.ts) that the Node runtime does not shim
// yet. Imports of them resolve only through the extension's node_modules.
var piVirtualModulesWithoutShim = map[string]string{
	"@earendil-works/pi-agent-core":       "no shim for the in-process Agent runtime",
	"@mariozechner/pi-agent-core":         "no shim for the in-process Agent runtime",
	"@earendil-works/pi-ai/providers/all": "no shim for the provider factory bundle",
	"@mariozechner/pi-ai/providers/all":   "no shim for the provider factory bundle",
}

// Every specifier in Pi's VIRTUAL_MODULES table is served by a loader shim
// or listed above, so a new upstream virtual module fails here.
func TestNodeRuntimeLoaderCoversPiVirtualModules(t *testing.T) {
	t.Parallel()
	modRoot := findModuleRoot(t)
	upstream, err := os.ReadFile(filepath.Join(modRoot, ".upstream", "current", "packages", "coding-agent", "src", "core", "extensions", "virtual-modules.ts"))
	if err != nil {
		t.Fatal(err)
	}
	loader, err := os.ReadFile(filepath.Join(modRoot, "coding", "extension", "host", "subprocess", "runtime-node", "loader.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	entry := regexp.MustCompile(`(?m)^\t"?([@a-z][^":]*)"?: bundled`)
	matches := entry.FindAllStringSubmatch(string(upstream), -1)
	if len(matches) == 0 {
		t.Fatal("no VIRTUAL_MODULES entries parsed from upstream")
	}
	for _, match := range matches {
		spec := match[1]
		shimmed := strings.Contains(string(loader), `["`+spec+`", new URL(`)
		_, missing := piVirtualModulesWithoutShim[spec]
		if shimmed == missing {
			t.Errorf("virtual module %q: shimmed=%t, listed without shim=%t", spec, shimmed, missing)
		}
	}
}

// Pi resolves the pi-ai root and its compat entry point to one module, and
// the type-only pi-ai/oauth entry point still links as a bare import.
func TestNodeRuntimeLoaderServesPiAiCompatAndOAuth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the loader fixture: %v", err)
	}
	runtimeRoot := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	command := exec.Command(node,
		"--import", registerLoaderURL(t, runtimeRoot),
		"--input-type=module",
		"--eval", `import * as root from "@earendil-works/pi-ai"; import * as compat from "@earendil-works/pi-ai/compat"; import * as legacy from "@mariozechner/pi-ai/compat"; import "@earendil-works/pi-ai/oauth"; import * as oauth from "@mariozechner/pi-ai/oauth"; if (compat !== root || legacy !== root || typeof compat.uuidv7 !== "function" || Object.keys(oauth).length !== 0) process.exit(1);`,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("pi-ai virtual modules: %v\n%s", err, output)
	}
}
