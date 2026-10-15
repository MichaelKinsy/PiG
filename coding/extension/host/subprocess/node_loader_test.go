//go:build !pig_strip_node_extensions

package subprocess

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Ports packages/coding-agent/test/suite/regressions/6260-inline-extension-naming.test.ts:36,56,79,99.
// The imported DefaultResourceLoader is the shipped independent Node SDK, not a mock of the parent host's resource state.
func TestNodeInlineExtensionNamingUpstream(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	fixture, err := os.ReadFile(filepath.Join("testdata", "inline-naming.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			root := t.TempDir()
			entry := filepath.Join(root, "inline-naming.mjs")
			if err := os.WriteFile(entry, fixture, 0o644); err != nil {
				t.Fatal(err)
			}
			peer := filepath.Join(root, "peer.mjs")
			if err := os.WriteFile(peer, []byte("export default function () {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			host := NewHost(root)
			t.Cleanup(func() {
				host.Shutdown("test complete")
				if pids := nodeCellProcessesForMarker(t, root); len(pids) != 0 {
					t.Errorf("Node processes remain after shutdown: %v", pids)
				}
			})
			configs := []ExtConfig{
				{Name: "inline-naming", Source: entry, Enabled: true, Isolation: isolation},
				{Name: "peer", Source: peer, Enabled: true, Isolation: isolation},
			}
			loaded, errs := host.LoadAll(t.Context(), configs)
			if len(errs) != 0 || len(loaded) != len(configs) {
				t.Fatalf("load inline-naming and peer: extensions=%d errors=%v", len(loaded), errs)
			}
			wantProcesses := len(configs)
			if isolation == "shared-ok" {
				wantProcesses = 1
			}
			if pids := nodeCellProcessesForMarker(t, root); len(pids) != wantProcesses {
				t.Fatalf("Node processes=%v, want %d for %s", pids, wantProcesses, isolation)
			}
			command, ok := loaded[0].Commands["inline-naming"]
			if !ok {
				t.Fatal("inline-naming command was not registered")
			}
			for _, scenario := range []string{"bare", "named", "hidden", "mixed"} {
				t.Run(scenario, func(t *testing.T) {
					args, err := json.Marshal(map[string]string{"root": t.TempDir(), "scenario": scenario})
					if err != nil {
						t.Fatal(err)
					}
					if err := command.Handler(t.Context(), string(args)); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

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

// Every specifier in Pi's VIRTUAL_MODULES table is served by a loader shim, so a new upstream virtual module fails here.
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
		if !shimmed {
			t.Errorf("virtual module %q has no shim", spec)
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
	t.Parallel()
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
		"@earendil-works/pi-agent-core":       "agent/src/index.ts",
		"@mariozechner/pi-agent-core":         "agent/src/index.ts",
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

// Every specifier in Pi's own VIRTUAL_MODULES table (the pinned dist's core/extensions/virtual-modules.js) serves the same
// export names, with the same kinds of values, through the loader as through Pi, in both directions: a missing name fails
// an extension's ESM link, and an extra one is visible to `import * as` enumeration. Pi's namespaces are read in a
// process without the loader hooks: with them, the pinned dist's own `@earendil-works/*` imports resolve to Pig's shims
// and the comparison would be Pig against itself.
func TestNodeRuntimeVirtualModulesMatchPiNamespaces(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the loader fixture: %v", err)
	}
	modRoot := findModuleRoot(t)
	runtimeRoot := filepath.Join(modRoot, "coding", "extension", "host", "subprocess", "runtime-node")
	pinned := filepath.Join(modRoot, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "core", "extensions", "virtual-modules.js")
	if _, err := os.Stat(pinned); err != nil {
		t.Fatalf("pinned Pi package missing (run npm ci in extensions/sdk-ts): %v", err)
	}
	piScript := `import { pathToFileURL } from "node:url";
const pi = await import(pathToFileURL(process.argv[1]).href);
const out = {};
for (const [spec, ns] of Object.entries(pi.VIRTUAL_MODULES)) out[spec] = Object.fromEntries(Object.keys(ns).map((k) => [k, typeof ns[k]]));
process.stdout.write(JSON.stringify(out));`
	piCommand := exec.Command(node, "--input-type=module", "--eval", piScript, pinned)
	var piStderr strings.Builder
	piCommand.Stderr = &piStderr
	piNamespaces, err := piCommand.Output()
	if err != nil {
		t.Fatalf("read Pi's virtual modules: %v\n%s", err, piStderr.String())
	}
	script := `const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
const pi = JSON.parse(Buffer.concat(chunks).toString("utf8"));
let compared = 0;
for (const [spec, want] of Object.entries(pi)) {
  let got;
  try { got = await import(spec); } catch (e) { console.log(spec + ": import failed: " + String(e?.message).split("\n")[0]); process.exitCode = 1; continue; }
  const wantNames = Object.keys(want).sort(), gotNames = Object.keys(got).sort();
  const missing = wantNames.filter((k) => !gotNames.includes(k)), extra = gotNames.filter((k) => !wantNames.includes(k));
  const kinds = wantNames.filter((k) => k in got && want[k] !== typeof got[k]).map((k) => k + " " + want[k] + "/" + typeof got[k]);
  if (missing.length || extra.length || kinds.length) { console.log(spec + ": missing=[" + missing + "] extra=[" + extra + "] kinds=[" + kinds + "]"); process.exitCode = 1; }
  compared++;
}
if (compared < 20) { console.log("compared only " + compared + " specifiers"); process.exitCode = 2; }`
	command := exec.Command(node, "--import", registerLoaderURL(t, runtimeRoot), "--input-type=module", "--eval", script)
	command.Stdin = bytes.NewReader(piNamespaces)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("virtual modules differ from Pi's: %v\n%s", err, output)
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
