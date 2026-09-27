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
	"@earendil-works/pi-agent-core": "no shim for the in-process Agent runtime",
	"@mariozechner/pi-agent-core":   "no shim for the in-process Agent runtime",
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

// Every runtime value Pi's packages export must be importable through the
// loader. An ESM import of a missing name fails the whole extension at link
// time (pi-rtk-optimizer's isToolCallEventType, for example), so the check is
// the full upstream index, not the names today's extensions happen to use.
func TestNodeRuntimeShimsExportEveryPinnedPiValue(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the loader fixture: %v", err)
	}
	modRoot := findModuleRoot(t)
	runtimeRoot := filepath.Join(modRoot, "coding", "extension", "host", "subprocess", "runtime-node")
	// Each specifier maps to the upstream module Pi serves for it
	// (core/extensions/virtual-modules.ts): the pi-ai root and its compat
	// entry are both compat.ts, a superset of pi-ai's index.ts.
	for spec, entry := range map[string]string{
		"@earendil-works/pi-coding-agent":     "coding-agent/src/index.ts",
		"@earendil-works/pi-tui":              "tui/src/index.ts",
		"@earendil-works/pi-ai":               "ai/src/compat.ts",
		"@earendil-works/pi-ai/compat":        "ai/src/compat.ts",
		"@earendil-works/pi-ai/providers/all": "ai/src/providers/all.ts",
	} {
		upstream := filepath.Join(modRoot, ".upstream", "current", "packages", filepath.FromSlash(entry))
		script := `import fs from "node:fs";
const spec = process.argv[1], upstream = process.argv[2];
import path from "node:path";
const names = new Set();
const seen = new Set();
function collect(file) {
  if (seen.has(file)) return;
  seen.add(file);
  const src = fs.readFileSync(file, "utf8").replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/[^\n]*/g, "");
  for (const m of src.matchAll(/export\s*\{([^}]*)\}/g)) {
    for (let n of m[1].split(",")) { n = n.trim(); if (!n || n.startsWith("type ")) continue; names.add(n.split(/\s+as\s+/).pop().trim()); }
  }
  for (const m of src.matchAll(/export\s+(?:declare\s+)?(?:abstract\s+)?(?:async\s+)?(?:const|function\*?|class|let|var|enum)\s+([A-Za-z0-9_$]+)/g)) names.add(m[1]);
  for (const m of src.matchAll(/export\s+\*\s+from\s+"(\.[^"]+)"/g)) collect(path.resolve(path.dirname(file), m[1]));
}
collect(upstream);
if (names.size < 8) { console.log("parsed only " + names.size + " upstream exports"); process.exit(2); }
const mod = await import(spec);
const missing = [...names].filter((n) => !(n in mod)).sort();
if (missing.length) { console.log(missing.join(" ")); process.exit(1); }`
		command := exec.Command(node, "--import", registerLoaderURL(t, runtimeRoot), "--input-type=module", "--eval", script, spec, upstream)
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("%s is missing upstream exports: %v\n%s", spec, err, output)
		}
	}
}

// parseFrontmatter as served to extensions returns what Pi's returns: YAML
// lists, nested maps, quoting, block scalars, CRLF and BOM input, and the
// same error for malformed YAML.
func TestNodeRuntimeParseFrontmatterMatchesPi(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the loader fixture: %v", err)
	}
	modRoot := findModuleRoot(t)
	runtimeRoot := filepath.Join(modRoot, "coding", "extension", "host", "subprocess", "runtime-node")
	pinned := filepath.Join(modRoot, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "utils", "frontmatter.js")
	if _, err := os.Stat(pinned); err != nil {
		t.Fatalf("pinned Pi package missing (run npm ci in extensions/sdk-ts): %v", err)
	}
	script := `import { pathToFileURL } from "node:url";
const pi = await import(pathToFileURL(process.argv[1]).href);
const pig = await import("@earendil-works/pi-coding-agent");
const inputs = [
  "---\nname: skill\ndescription: Does things\n---\nBody text\n",
  "---\ntags: [a, b, \"c d\"]\nitems:\n  - one\n  - two: 2\nmeta:\n  nested:\n    deep: true\n  n: 1.5\n---\nbody",
  "---\nquoted: \"a: b # not a comment\"\nsingle: 'it''s'\nempty:\nnull_value: ~\ndate: 2026-09-26\n---\n",
  "---\nliteral: |\n  line one\n  line two\nfolded: >\n  folded\n  text\n---\nafter",
  "\ufeff---\r\nname: crlf\r\nlist:\r\n  - x\r\n---\r\nbody\r\n",
  "no frontmatter here\n---\nname: x\n---\n",
  "---\nname: unterminated\nbody",
  "---\n---\nempty frontmatter",
  "---\nkey: [unclosed\n---\nbody",
  "---\n- just\n- a list\n---\nb",
];
const run = (fn, input) => { try { return { ok: fn(input) }; } catch (e) { return { error: String(e?.name) + ": " + String(e?.message) }; } };
for (const input of inputs) {
  for (const name of ["parseFrontmatter", "stripFrontmatter"]) {
    const want = JSON.stringify(run(pi[name], input));
    const got = JSON.stringify(run(pig[name], input));
    if (want !== got) { console.log(name + " " + JSON.stringify(input) + "\npi:  " + want + "\npig: " + got); process.exitCode = 1; }
  }
}`
	command := exec.Command(node, "--import", registerLoaderURL(t, runtimeRoot), "--input-type=module", "--eval", script, pinned)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("parseFrontmatter differs from Pi: %v\n%s", err, output)
	}
}
