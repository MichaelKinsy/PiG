package subprocess_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/tui"
)

// pinnedPiPackages is where `npm ci` in extensions/sdk-ts installs the Pi
// release named by coding.UpstreamVersion.
var pinnedPiPackages = filepath.Join("..", "..", "..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")

func readPinned(t *testing.T, parts ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{pinnedPiPackages}, parts...)...))
	if err != nil {
		t.Fatalf("read the pinned Pi package (run npm ci in extensions/sdk-ts): %v", err)
	}
	return data
}

// The bundled TypeBox must be the release the pinned Pi depends on.
func TestVendoredTypeBoxMatchesThePinnedDependency(t *testing.T) {
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(readPinned(t, "package.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "automation", "gen", "vendor-typebox.sh"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^version="([^"]+)"$`).FindSubmatch(script)
	if match == nil {
		t.Fatal("automation/gen/vendor-typebox.sh does not declare version=")
	}
	if want := manifest.Dependencies["typebox"]; string(match[1]) != want {
		t.Fatalf("vendored TypeBox %s, pinned Pi depends on typebox %s: update automation/gen/vendor-typebox.sh and rerun it", match[1], want)
	}
}

// vendoredImportRewrites lists, per vendored file, the one import line
// automation/gen/vendor-pi-dist.sh points at a vendored third-party copy.
// Every other vendored Pi file is the pinned release byte for byte.
var vendoredImportRewrites = map[string][2]string{
	"pi-coding-agent/core/tools/edit-diff.js": {`import * as Diff from "diff";`, `import * as Diff from "../../../../diff/libesm/index.js";`},
	"pi-coding-agent/utils/child-process.js":  {`import crossSpawn from "cross-spawn";`, `import crossSpawn from "../../../cross-spawn/index.js";`},
	"pi-tui/index.js":                         {`export { Marked } from "marked";`, `export { Marked } from "../../marked/lib/marked.esm.js";`},
	"pi-tui/utils.js": {`import { eastAsianWidth } from "get-east-asian-width";`,
		`import { eastAsianWidth } from "../../get-east-asian-width/index.js";`},
	"pi-tui/components/markdown.js": {`import { Marked, Tokenizer } from "marked";`,
		`import { Marked, Tokenizer } from "../../../marked/lib/marked.esm.js";`},
	"pi-ai/utils/json-parse.js": {`import { parse as partialParse } from "partial-json";`,
		`import { parse as partialParse } from "../../../partial-json/dist/index.js";`},
	"pi-ai/utils/typebox-helpers.js": {`import { Type } from "typebox";`,
		`import { Type } from "../../../typebox.mjs";`},
	"pi-ai/index.js": {`export { Type } from "typebox";`,
		`export { Type } from "../../typebox.mjs";`},
	"pi-coding-agent/utils/shell.js": {`import { getBinDir } from "../config.js";`,
		`import { getBinDir } from "../../../pig-config.mjs";`},
	"pi-coding-agent/utils/frontmatter.js": {`import { parse } from "yaml";`,
		`import { parse } from "../../../yaml/index.js";`},
	"pi-coding-agent/modes/interactive/components/custom-editor.js": {`import { Editor, visibleWidth } from "@earendil-works/pi-tui";`,
		`import { Editor, visibleWidth } from "../../../../../pi-tui.mjs";`},
}

// settings-manager.js rewrites three imports: PiG's config paths (D2), the
// settings lock PiG's host shares, and pi-ai from the vendored root.
var vendoredSettingsManagerRewrites = [][2]string{
	{`import { DEFAULT_MAX_AGENT_RETRY_DELAY_MS } from "@earendil-works/pi-ai";`,
		`import { DEFAULT_MAX_AGENT_RETRY_DELAY_MS } from "../../pi-ai/index.js";`},
	{`import lockfile from "proper-lockfile";`, `import lockfile from "../../../proper-lockfile.mjs";`},
	{`import { CONFIG_DIR_NAME, getAgentDir } from "../config.js";`,
		`import { CONFIG_DIR_NAME, getAgentDir } from "../../../pig-config.mjs";`},
}

// vendoredBridgeStubs are pi-ai's builtin API implementations, which import
// vendor SDKs; each is a stub that loads PiG's bridge to its host providers.
var vendoredBridgeStubs = func() map[string]string {
	stubs := map[string]string{
		"pi-ai/api/openrouter-images.js": "// PiG: image generation has no host provider (D74); see automation/gen/vendor-pi-dist.sh.\n" +
			"import { bridgeImages } from \"../../../pi-ai-bridge.mjs\";\nexport const generateImages = bridgeImages(\"openrouter-images\");\n",
	}
	for _, api := range []string{"anthropic-messages", "azure-openai-responses", "bedrock-converse-stream", "google-generative-ai", "google-vertex",
		"mistral-conversations", "openai-codex-responses", "openai-completions", "openai-responses", "pi-messages"} {
		stubs["pi-ai/api/"+api+".js"] = "// PiG: the " + api + " implementation runs in PiG's host (D74); see automation/gen/vendor-pi-dist.sh.\n" +
			"import { bridgeApi } from \"../../../pi-ai-bridge.mjs\";\nexport const { stream, streamSimple } = bridgeApi(\"" + api + "\");\n"
	}
	return stubs
}()

// validation.js rewrites two imports.
var vendoredValidationRewrites = [][2]string{
	{`import { Compile } from "typebox/compile";`, `import { Compile } from "../../../typebox-compile.mjs";`},
	{`import { Value } from "typebox/value";`, `import { Value } from "../../../typebox-value.mjs";`},
}

// pinnedPackageDist is where each vendored package's dist/ lives in the
// pinned install, relative to pinnedPiPackages.
var pinnedPackageDist = map[string][]string{
	"pi-tui":          {"node_modules", "@earendil-works", "pi-tui", "dist"},
	"pi-ai":           {"node_modules", "@earendil-works", "pi-ai", "dist"},
	"pi-coding-agent": {"dist"},
	"pi-agent-core":   {"node_modules", "@earendil-works", "pi-agent-core", "dist"},
	"pi-codemode":     {"node_modules", "@earendil-works", "pi-codemode", "dist"},
	"pi-mcp":          {"node_modules", "@earendil-works", "pi-mcp", "dist"},
}

// closureVendoredPackages are copied as the module graph reachable from
// pi-agent-core's entry (automation/gen/vendor-pi-closure.mjs): verbatim
// except that each bare import specifier names the vendored copy by a
// relative path.
var closureVendoredPackages = map[string]bool{"pi-agent-core": true, "pi-codemode": true, "pi-mcp": true}

var importSpecifierLine = regexp.MustCompile(`^((?:.*?\bfrom\s*|.*\bimport\(\s*|import\s*))"([^"]+)"(.*)$`)

// sameExceptBareImports reports whether got is want with only bare import
// specifiers rewritten to relative paths of files that exist beside the
// vendored module at path.
func sameExceptBareImports(t *testing.T, path string, got, want []byte) bool {
	t.Helper()
	gotLines, wantLines := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	if len(gotLines) != len(wantLines) {
		return false
	}
	for i := range gotLines {
		if gotLines[i] == wantLines[i] {
			continue
		}
		g, w := importSpecifierLine.FindStringSubmatch(gotLines[i]), importSpecifierLine.FindStringSubmatch(wantLines[i])
		if g == nil || w == nil || g[1] != w[1] || g[3] != w[3] || strings.HasPrefix(w[2], ".") || !strings.HasPrefix(g[2], ".") {
			return false
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), filepath.FromSlash(g[2]))); err != nil {
			t.Errorf("%s imports %q for %q, which does not exist: %v", path, g[2], w[2], err)
			return false
		}
	}
	return true
}

// The runtime's Pi modules re-export Pi's own code copied from the pinned
// release: shims/pi-dist mirrors each package's dist/, and shims/yaml,
// shims/marked, shims/get-east-asian-width, shims/partial-json, shims/ignore
// and shims/diff are the third-party releases Pi depends on. A pin change must re-vendor them.
func TestVendoredPiDistMatchesThePinnedPackage(t *testing.T) {
	for pkg, dist := range pinnedPackageDist {
		var manifest struct{ Version string }
		if err := json.Unmarshal(readPinned(t, append(dist[:len(dist)-1:len(dist)-1], "package.json")...), &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Version != coding.UpstreamVersion {
			t.Fatalf("installed %s %s, pinned Pi %s: run npm ci in extensions/sdk-ts", pkg, manifest.Version, coding.UpstreamVersion)
		}
	}
	shims := filepath.Join("runtime-node", "shims")
	root := filepath.Join(shims, "pi-dist")
	vendored := map[string]bool{}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, relErr := filepath.Rel(root, path)
			vendored[filepath.ToSlash(rel)] = true
			return relErr
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(vendored) < 50 {
		t.Fatalf("shims/pi-dist holds %d files: run automation/gen/vendor-pi-dist.sh", len(vendored))
	}
	for rel := range vendored {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if rel == "package.json" {
			if string(got) != "{\n  \"type\": \"module\"\n}\n" {
				t.Errorf("shims/pi-dist/package.json = %q", got)
			}
			continue
		}
		if strings.HasPrefix(rel, "pi-ai/sdk-bundle/") || strings.HasPrefix(rel, "pi-tui/sdk-bundle/") {
			continue // TestNodeLibraryBundlesRegenerateExactly verifies these compiler outputs.
		}
		if file, ok := strings.CutPrefix(rel, "pi-coding-agent/"); ok {
			checkCodingAgentVendor(t, root, file, got)
			continue
		}
		if stub, ok := vendoredBridgeStubs[rel]; ok {
			// The pinned implementation imports a vendor SDK; PiG's host runs
			// it instead (D74).
			readPinned(t, append(append([]string{}, pinnedPackageDist["pi-ai"]...), strings.Split(strings.TrimPrefix(rel, "pi-ai/"), "/")...)...)
			if string(got) != stub {
				t.Errorf("shims/pi-dist/%s = %q, want the bridge stub: run automation/gen/vendor-pi-dist.sh", rel, got)
			}
			continue
		}
		pkg, file, _ := strings.Cut(rel, "/")
		dist, ok := pinnedPackageDist[pkg]
		if !ok {
			t.Errorf("shims/pi-dist/%s is not under a vendored Pi package", rel)
			continue
		}
		var want []byte
		if pkg == "pi-tui" && strings.HasPrefix(file, "native/") {
			want = readPinned(t, append(append([]string{}, dist[:len(dist)-1]...), strings.Split(file, "/")...)...)
		} else {
			want = readPinned(t, append(append([]string{}, dist...), strings.Split(file, "/")...)...)
		}
		if rel == "pi-tui/utils.js" {
			declaration := regexp.MustCompile(`(?m)^const rgiEmojiRegex = (.+);$`).FindSubmatch(want)
			if declaration == nil {
				t.Fatal("pinned RGI emoji expression declaration changed")
			}
			expression, err := os.ReadFile(filepath.Join(shims, "pi-tui-emoji.mjs"))
			if err != nil {
				t.Fatal(err)
			}
			if string(expression) != "export const rgiEmojiRegex = "+string(declaration[1])+";\n" {
				t.Fatal("deferred emoji expression differs from Pi")
			}
			want = bytes.ReplaceAll(want, declaration[0], []byte(`import { rgiEmojiRegex } from "../../pi-tui-emoji-lazy.mjs";`))
			want = bytes.ReplaceAll(want, []byte("const graphemeSegmenter = new Intl.Segmenter(undefined, { granularity: \"grapheme\" });\nconst wordSegmenter = new Intl.Segmenter(undefined, { granularity: \"word\" });"), []byte(`import { graphemeSegmenter, wordSegmenter, getGraphemeSegmenter as nativeGraphemeSegmenter, getWordSegmenter as nativeWordSegmenter } from "../../pi-tui-segmenters.mjs";`))
			want = bytes.ReplaceAll(want, []byte("return graphemeSegmenter;"), []byte("return nativeGraphemeSegmenter();"))
			want = bytes.ReplaceAll(want, []byte("return wordSegmenter;"), []byte("return nativeWordSegmenter();"))
		}
		if pkg == "pi-tui" && file != "utils.js" {
			captures := regexp.MustCompile(`(?m)^const (\w+) = get(Grapheme|Word)Segmenter\(\);$`)
			want = captures.ReplaceAllFunc(want, func(line []byte) []byte {
				match := captures.FindSubmatch(line)
				binding := strings.ToLower(string(match[2])) + "Segmenter"
				if binding != string(match[1]) {
					binding += " as " + string(match[1])
				}
				specifier, err := filepath.Rel(filepath.Dir(filepath.Join(root, rel)), filepath.Join(shims, "pi-tui-segmenters.mjs"))
				if err != nil {
					t.Fatal(err)
				}
				return []byte(`import { ` + binding + ` } from "` + filepath.ToSlash(specifier) + `";`)
			})
		}
		if closureVendoredPackages[pkg] {
			if !sameExceptBareImports(t, filepath.Join(root, filepath.FromSlash(rel)), got, want) {
				t.Errorf("shims/pi-dist/%s differs from the pinned release beyond its import specifiers: run automation/gen/vendor-pi-dist.sh", rel)
			}
			continue
		}
		rewrites := [][2]string{}
		if rw, ok := vendoredImportRewrites[rel]; ok {
			rewrites = append(rewrites, rw)
		}
		if rel == "pi-ai/utils/validation.js" {
			rewrites = append(rewrites, vendoredValidationRewrites...)
		}
		if rel == "pi-coding-agent/core/session-manager.js" {
			rewrites = append(rewrites, [2]string{`import { getCurrentSystemMessage, uuidv7, } from "@earendil-works/pi-ai";`, `import { getCurrentSystemMessage, uuidv7, } from "../../../pi-ai.mjs";`}, [2]string{`import { APP_NAME, getAgentDir as getDefaultAgentDir, getSessionsDir } from "../config.js";`, `import { APP_NAME, getAgentDir as getDefaultAgentDir, getSessionsDir } from "../../../pig-config.mjs";`})
		}
		if rel == "pi-coding-agent/core/provider-composer.js" {
			rewrites = append(rewrites, [2]string{`import { lazyStream, } from "@earendil-works/pi-ai";`, `import { lazyStream, } from "../../../pi-ai.mjs";`}, [2]string{`import { getApiProvider } from "@earendil-works/pi-ai/compat";`, `import { getApiProvider } from "../../../pi-ai.mjs";`})
		}
		if rel == "pi-coding-agent/core/settings-manager.js" {
			rewrites = append(rewrites, vendoredSettingsManagerRewrites...)
		}
		if rel == "pi-coding-agent/utils/syntax-highlight.js" {
			// Every highlight.js specifier points at the vendored package.
			if !bytes.Contains(want, []byte(`"highlight.js/lib/core.js"`)) {
				t.Errorf("pinned %s no longer imports highlight.js/lib/core.js: update automation/gen/vendor-pi-dist.sh", rel)
			}
			want = bytes.ReplaceAll(want, []byte(`"highlight.js/lib/`), []byte(`"../../../highlight.js/lib/`))
		}
		want = applyVendoredIdentityPatches(t, rel, want)
		for _, rw := range rewrites {
			line := []byte(rw[0] + "\n")
			if !bytes.Contains(want, line) {
				t.Errorf("pinned %s lacks %q: update automation/gen/vendor-pi-dist.sh", rel, rw[0])
			}
			want = bytes.Replace(want, line, []byte(rw[1]+"\n"), 1)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("shims/pi-dist/%s differs from the pinned release: run automation/gen/vendor-pi-dist.sh", rel)
		}
	}

	// Third-party packages, file for file.
	nodeModules := filepath.Join(pinnedPiPackages, "node_modules")
	yamlFiles := map[string]string{
		"index.js":     filepath.Join(nodeModules, "yaml", "browser", "index.js"),
		"package.json": filepath.Join(nodeModules, "yaml", "browser", "package.json"),
		"LICENSE":      filepath.Join(nodeModules, "yaml", "LICENSE"),
	}
	distRoot := filepath.Join(nodeModules, "yaml", "browser", "dist")
	if err := filepath.WalkDir(distRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(distRoot, path)
		yamlFiles[filepath.ToSlash(filepath.Join("dist", rel))] = path
		return err
	}); err != nil {
		t.Fatalf("read the pinned yaml build (run npm ci in extensions/sdk-ts): %v", err)
	}
	hljs := filepath.Join(nodeModules, "highlight.js")
	hljsFiles := map[string]string{
		"package.json": filepath.Join(hljs, "package.json"),
		"LICENSE":      filepath.Join(hljs, "LICENSE"),
	}
	hljsLib := filepath.Join(hljs, "lib")
	if err := filepath.WalkDir(hljsLib, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(hljsLib, path)
		hljsFiles[filepath.ToSlash(filepath.Join("lib", rel))] = path
		return err
	}); err != nil {
		t.Fatalf("read the pinned highlight.js (run npm ci in extensions/sdk-ts): %v", err)
	}
	jiti := filepath.Join(nodeModules, "jiti")
	jitiFiles := map[string]string{
		"package.json": filepath.Join(jiti, "package.json"),
		"LICENSE":      filepath.Join(jiti, "LICENSE"),
	}
	for _, dir := range []string{"lib", "dist"} {
		base := filepath.Join(jiti, dir)
		if err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(jiti, path)
			jitiFiles[filepath.ToSlash(rel)] = path
			return err
		}); err != nil {
			t.Fatalf("read the pinned jiti (run npm ci in extensions/sdk-ts): %v", err)
		}
	}
	jsdiff := filepath.Join(nodeModules, "diff")
	diffFiles := map[string]string{
		"package.json": filepath.Join(jsdiff, "package.json"),
		"LICENSE":      filepath.Join(jsdiff, "LICENSE"),
	}
	diffLib := filepath.Join(jsdiff, "libesm")
	if err := filepath.WalkDir(diffLib, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".d.ts") || strings.HasSuffix(path, ".d.ts.map") {
			return err
		}
		rel, err := filepath.Rel(diffLib, path)
		diffFiles[filepath.ToSlash(filepath.Join("libesm", rel))] = path
		return err
	}); err != nil {
		t.Fatalf("read the pinned diff (run npm ci in extensions/sdk-ts): %v", err)
	}
	ignore := filepath.Join(nodeModules, "ignore")
	marked := filepath.Join(nodeModules, "marked")
	eaw := filepath.Join(nodeModules, "get-east-asian-width")
	partial := filepath.Join(nodeModules, "partial-json")
	for dir, files := range map[string]map[string]string{
		"yaml":         yamlFiles,
		"highlight.js": hljsFiles,
		"jiti":         jitiFiles,
		"diff":         diffFiles,
		"ignore": {
			"index.js": filepath.Join(ignore, "index.js"), "package.json": filepath.Join(ignore, "package.json"),
			"LICENSE-MIT": filepath.Join(ignore, "LICENSE-MIT"),
		},
		"get-east-asian-width": {
			"index.js": filepath.Join(eaw, "index.js"), "lookup.js": filepath.Join(eaw, "lookup.js"),
			"lookup-data.js": filepath.Join(eaw, "lookup-data.js"), "utilities.js": filepath.Join(eaw, "utilities.js"),
			"license": filepath.Join(eaw, "license"), "package.json": filepath.Join(eaw, "package.json"),
		},
		"marked": {
			"lib/marked.esm.js": filepath.Join(marked, "lib", "marked.esm.js"),
			"LICENSE":           filepath.Join(marked, "LICENSE"), "package.json": filepath.Join(marked, "package.json"),
		},
		"partial-json": {
			"dist/index.js": filepath.Join(partial, "dist", "index.js"), "dist/options.js": filepath.Join(partial, "dist", "options.js"),
			"LICENSE": filepath.Join(partial, "LICENSE"), "package.json": filepath.Join(partial, "package.json"),
		},
	} {
		count := 0
		if err := filepath.WalkDir(filepath.Join(shims, dir), func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				count++
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if count != len(files) {
			t.Errorf("shims/%s has %d files, want %d: run automation/gen/vendor-pi-dist.sh", dir, count, len(files))
		}
		for rel, path := range files {
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read the pinned %s (run npm ci in extensions/sdk-ts): %v", dir, err)
			}
			got, err := os.ReadFile(filepath.Join(shims, dir, filepath.FromSlash(rel)))
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("shims/%s/%s differs from the pinned dependency: run automation/gen/vendor-pi-dist.sh", dir, rel)
			}
		}
	}
}

// runPinnedComparison runs script under Node with the pinned package's entry
// point and the runtime's module as process.argv[1] and [2]. The script
// prints a difference and exits non-zero when the two disagree.
func runPinnedComparison(t *testing.T, pinnedEntry []string, shim, script string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	readPinned(t, pinnedEntry...)
	pinned, err := filepath.Abs(filepath.Join(append([]string{pinnedPiPackages}, pinnedEntry...)...))
	if err != nil {
		t.Fatal(err)
	}
	shimPath, err := filepath.Abs(filepath.Join("runtime-node", "shims", shim))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "--eval", script,
		"file://"+filepath.ToSlash(pinned), "file://"+filepath.ToSlash(shimPath))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

// The runtime's pi-tui components render and handle input byte-for-byte like
// the pinned package's: extensions (pi-mcp-adapter, pi-rtk-optimizer,
// pi-web-access) compose panels from Box, Container, Text, Spacer and
// SettingsList, and prompt with Input, Editor and SelectList, resolving keys
// through the keybindings manager.
func TestPiTuiComponentsMatchThePinnedPackage(t *testing.T) {
	runPinnedComparison(t, []string{"node_modules", "@earendil-works", "pi-tui", "dist", "index.js"}, "pi-tui.mjs", `
const [pi, pig] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
const setCaps = (m, caps) => m.setCapabilities(caps);
const bg = (s) => "\x1b[44m" + s + "\x1b[49m";
const bold = (s) => "\x1b[1m" + s + "\x1b[22m";
const dim = (s) => "\x1b[2m" + s + "\x1b[22m";
const keys = {
  up: "\x1b[A", down: "\x1b[B", right: "\x1b[C", left: "\x1b[D", home: "\x1b[H", end: "\x1b[F",
  pageDown: "\x1b[6~", enter: "\r", escape: "\x1b", backspace: "\x7f", del: "\x1b[3~",
  ctrlA: "\x01", ctrlE: "\x05", ctrlK: "\x0b", ctrlU: "\x15", ctrlW: "\x17", ctrlY: "\x19",
  altB: "\x1bb", altF: "\x1bf", altD: "\x1bd", undo: "\x1f", newline: "\n", shiftEnter: "\x1b[13;2u",
  paste: (s) => "\x1b[200~" + s + "\x1b[201~",
};
async function scene(m) {
  const out = [];
  const log = (...v) => out.push(v);

  // Layout components.
  const box = new m.Box(2, 1, bg);
  box.addChild(new m.Text("Hello, \x1b[1mbold\x1b[22m wrapped text that runs past the width", 1, 0));
  box.addChild(new m.Spacer(2));
  const text = new m.Text("second", 0, 0);
  text.setCustomBgFn(bg);
  box.addChild(text);
  log(box.render(24));
  box.setBgFn(undefined);
  log(box.render(24));
  const spacer = new m.Spacer();
  spacer.setLines(3);
  const container = new m.Container();
  container.addChild(box);
  container.addChild(spacer);
  container.addChild(new m.TruncatedText("a line that is far too long for the width", 1, 0));
  log(container.render(30));
  container.clear();
  box.clear();
  log(container.render(30), box.render(30));
  log(new m.HStack([new m.Text("left", 0, 0), { component: new m.Text("right side", 0, 0), grow: 1 }], { gap: 1 }).render(20));
  log(new m.VStack([new m.Text("top", 0, 0), new m.Text("bottom", 0, 0)], { gap: 1 }).render(10));

  // KeybindingsManager.
  const kb = new m.KeybindingsManager(m.TUI_KEYBINDINGS, { "tui.select.up": ["k", "up"], "tui.select.down": "k", "mcp.panel.save": "ctrl+s" });
  log(kb.matches("k", "tui.select.up"), kb.matches(keys.up, "tui.select.up"), kb.matches(keys.down, "tui.select.down"),
    kb.getKeys("tui.select.up"), kb.getDefinition("tui.input.submit"), kb.getConflicts(), kb.getUserBindings(),
    kb.getResolvedBindings());
  kb.setUserBindings({});
  log(kb.matches(keys.down, "tui.select.down"), kb.getResolvedBindings()["tui.select.cancel"]);
  log(m.getKeybindings() instanceof m.KeybindingsManager, m.getKeybindings().matches(keys.escape, "tui.select.cancel"));
  log(m.fuzzyMatch("ctl", "control"), m.fuzzyFilter(["alpha", "beta", "alphabet"], "alp", (s) => s));

  // SelectList.
  const theme = { selectedPrefix: bold, selectedText: bold, description: dim, scrollInfo: dim, noMatch: dim };
  const items = Array.from({ length: 9 }, (_, i) => ({ value: "item" + i, label: "Item " + i, description: i % 2 ? "odd description " + i : undefined }));
  const list = new m.SelectList(items, 4, theme, { minPrimaryColumnWidth: 8 });
  list.onSelect = (item) => log("select", item.value);
  list.onCancel = () => log("cancel");
  list.onSelectionChange = (item) => log("change", item.value);
  log(list.render(40));
  for (const k of [keys.down, keys.down, keys.up, keys.pageDown, keys.down, keys.down, keys.down, keys.down, keys.down]) {
    list.handleInput(k);
    log(list.getSelectedItem()?.value, list.render(40));
  }
  list.handleInput(keys.enter);
  list.setFilter("item1");
  log(list.render(40));
  list.setFilter("zzz");
  log(list.render(40), list.getSelectedItem());
  list.setFilter("");
  list.setSelectedIndex(6);
  log(list.render(24));
  list.handleInput(keys.escape);

  // Input.
  const input = new m.Input();
  input.focused = true;
  input.onSubmit = (v) => log("submit", v);
  input.onEscape = () => log("escape");
  for (const ch of "hello brave new world") input.handleInput(ch);
  log(input.getValue(), input.render(12));
  for (const k of [keys.altB, keys.altB, keys.ctrlW, keys.left, "X", keys.ctrlA, keys.del, keys.altF, keys.altD, keys.ctrlE,
    keys.ctrlU, keys.ctrlY, keys.undo, keys.paste("pasted\ntext"), keys.home, keys.ctrlK, keys.ctrlY, keys.backspace, keys.right]) {
    input.handleInput(k);
    log(input.getValue(), input.render(12), input.render(40));
  }
  input.setValue("set value");
  input.focused = false;
  log(input.render(20));
  input.handleInput(keys.enter);
  input.handleInput(keys.escape);

  // Editor.
  const tui = { requestRender() {}, terminal: { rows: 24, columns: 80 } };
  const editor = new m.Editor(tui, { borderColor: dim, selectList: theme }, { paddingX: 1 });
  editor.focused = true;
  editor.onSubmit = (v) => log("editor submit", v);
  editor.onChange = (v) => log("editor change", v);
  for (const ch of "first line") editor.handleInput(ch);
  editor.handleInput(keys.shiftEnter);
  for (const ch of "second line that is long enough to wrap") editor.handleInput(ch);
  editor.handleInput(keys.newline);
  editor.handleInput(keys.paste("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl"));
  log(editor.getText(), editor.getExpandedText(), editor.getLines(), editor.getCursor(), editor.render(24));
  for (const k of [keys.up, keys.up, keys.ctrlA, keys.altF, keys.ctrlK, keys.down, keys.ctrlE, keys.backspace, keys.ctrlW, keys.ctrlY, keys.undo]) {
    editor.handleInput(k);
    log(editor.getText(), editor.getCursor(), editor.render(24));
  }
  editor.insertTextAtCursor("inserted");
  log(editor.getText(), editor.getCursor());
  editor.addToHistory("older prompt");
  editor.addToHistory("newer prompt");
  editor.setText("");
  editor.handleInput(keys.up);
  log(editor.getText());
  editor.handleInput(keys.up);
  log(editor.getText(), editor.render(30));
  editor.handleInput(keys.enter);
  log(editor.getText());
  editor.setText("line one\nline two");
  log(editor.getLines(), editor.getCursor(), editor.render(20));

  // SettingsList with search: filtering goes through Input and fuzzyFilter.
  const settings = new m.SettingsList(
    [{ id: "a", label: "Alpha", currentValue: "on", values: ["on", "off"], description: "The alpha setting" },
     { id: "b", label: "Beta", currentValue: "x", values: ["x", "y", "z"] },
     { id: "c", label: "Gamma", currentValue: "1" }],
    5, { label: (s, sel) => (sel ? bold(s) : s), value: (s) => s, description: dim, cursor: "> ", hint: dim },
    (id, value) => log("setting", id, value), () => log("settings cancel"), { enableSearch: true });
  log(settings.render(40));
  for (const k of [keys.down, keys.enter, " ", "g", "a", keys.backspace, keys.backspace, keys.up, keys.enter, keys.escape]) {
    settings.handleInput(k);
    log(settings.render(40));
  }

  // StdinBuffer splits raw input into key sequences and pastes.
  const stdin = new m.StdinBuffer();
  stdin.on("data", (d) => log("data", d));
  stdin.on("paste", (d) => log("paste", d));
  stdin.process("a\x1b[A\x1b[13;2ub" + keys.paste("pasted") + "\x1bb");
  stdin.destroy();

  // Markdown through marked, with hyperlinks on and off.
  const mdTheme = { heading: bold, link: (s) => "\x1b[4m" + s + "\x1b[24m", linkUrl: dim, code: (s) => "\x1b[33m" + s + "\x1b[39m",
    codeBlock: (s) => s, codeBlockBorder: dim, quote: dim, quoteBorder: dim, hr: dim, listBullet: bold, bold, italic: (s) => "\x1b[3m" + s + "\x1b[23m",
    strikethrough: (s) => "\x1b[9m" + s + "\x1b[29m", underline: (s) => "\x1b[4m" + s + "\x1b[24m" };
  const source = "# Title\n\nSome **bold**, *italic*, ~~gone~~ and \x60code\x60 with a [link](https://example.com) and https://bare.example.\n\n" +
    "- one\n- two\n  1. nested\n\n> quoted text that wraps across the narrow width\n\n\x60\x60\x60go\nfunc main() {}\n\x60\x60\x60\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n---\n\n$x^2$ inline math";
  for (const hyperlinks of [true, false]) {
    setCaps(m, { images: null, trueColor: true, hyperlinks });
    const md = new m.Markdown(source, 1, 0, mdTheme);
    log(md.render(40));
    md.setText("updated *text*");
    log(md.render(20));
  }
  log(new m.Marked().parse("**x**"));

  // CombinedAutocompleteProvider slash-command suggestions.
  const provider = new m.CombinedAutocompleteProvider([{ name: "help", description: "Show help" }, { name: "hello" }], "/");
  log(await provider.getSuggestions(["/he"], 0, 3, { signal: new AbortController().signal }));
  return JSON.stringify(out);
}
const want = await scene(pi), got = await scene(pig);
if (want !== got) { console.log("pinned: " + want + "\npig:    " + got); process.exit(1); }
`)
}

// The runtime's pi-ai utilities behave exactly like the pinned package's.
func TestPiAiUtilitiesMatchThePinnedPackage(t *testing.T) {
	runPinnedComparison(t, []string{"node_modules", "@earendil-works", "pi-ai", "dist", "index.js"}, "pi-ai.mjs", `
const [pi, pig] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
const model = { id: "m", provider: "p", api: "anthropic-messages", reasoning: true, thinkingLevelMap: { xhigh: "x", minimal: null },
  cost: { input: 3, output: 15, cacheRead: 0.3, cacheWrite: 3.75, tiers: [{ inputTokensAbove: 200000, input: 6, output: 22.5, cacheRead: 0.6, cacheWrite: 7.5 }] } };
const usage = (input) => ({ input, output: 1000, cacheRead: 500, cacheWrite: 300, cacheWrite1h: 100, cost: {} });
const err = new Error("boom", { cause: new TypeError("inner") });
err.status = 503;
async function scene(m) {
  const out = [];
  const log = (...v) => out.push(v);
  const safe = (fn) => { try { return fn(); } catch (e) { return "throws " + e.message; } };
  for (const s of ['{"a":1', '{"a": "x\ny"}', "{'a': 1,}", '[1, 2, {"b": tr', "", "not json"]) {
    log(safe(() => m.repairJson(s)), safe(() => m.parseJsonWithRepair(s)), safe(() => m.parseStreamingJson(s)));
  }
  log(m.calculateCost(model, usage(1000)), m.calculateCost(model, usage(300000)));
  log(m.getSupportedThinkingLevels(model), m.getSupportedThinkingLevels({ ...model, reasoning: false }),
    ["off", "minimal", "max", "bogus"].map((l) => m.clampThinkingLevel(model, l)));
  log(m.modelsAreEqual(model, { id: "m", provider: "p" }), m.modelsAreEqual(model, undefined), m.hasApi(model, "anthropic-messages"));
  const failed = (errorMessage, stopReason = "error") => ({ role: "assistant", content: [], stopReason, errorMessage,
    usage: { input: 190000, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 190000 } });
  for (const msg of [failed("prompt is too long: 250000 tokens > 200000 maximum"), failed("Overloaded"), failed("rate limit exceeded (429)"),
    failed("", "length"), failed("fetch failed: ECONNRESET"), failed("invalid api key")]) {
    log(m.isContextOverflow(msg, 200000), m.isRecoverableLength(msg, 8192), m.isRetryableAssistantError(msg));
  }
  log(m.getOverflowPatterns().map(String), m.DEFAULT_MAX_AGENT_RETRY_DELAY_MS);
  for (const attempt of [1, 2, 5, 12]) log(m.retryDelayMs({ baseDelayMs: 1000, maxDelayMs: 30000 }, attempt));
  log(m.formatThrownValue(err), m.formatThrownValue("plain"), m.formatThrownValue({ x: 1 }), m.extractDiagnosticError(err));
  const diag = m.createAssistantMessageDiagnostic("provider_error", err, { attempt: 2 });
  delete diag.timestamp;
  const message = m.fauxAssistantMessage([m.fauxText("hi"), m.fauxThinking("hmm"), m.fauxToolCall("read", { path: "a" }, { id: "t1" })],
    { timestamp: 1, stopReason: "toolUse" });
  m.appendAssistantMessageDiagnostic(message, diag);
  log(diag, message, m.fauxAssistantMessage("text", { timestamp: 2 }));

  const stream = m.createAssistantMessageEventStream();
  const partial = m.fauxAssistantMessage([], { timestamp: 3 });
  stream.push({ type: "start", partial });
  stream.push({ type: "text_start", contentIndex: 0, partial });
  stream.push({ type: "text_delta", contentIndex: 0, delta: "hel", partial });
  const done = m.fauxAssistantMessage("hello", { timestamp: 3 });
  stream.push({ type: "done", reason: "stop", message: done });
  const events = [];
  for await (const e of stream) events.push(e.type);
  log(events, await stream.result(), stream instanceof m.AssistantMessageEventStream, stream instanceof m.EventStream);

  const encoder = new m.AssistantMessageFrameEncoder();
  const frames = [];
  const at = (content, stopReason = "pending") => ({ ...m.fauxAssistantMessage(content, { timestamp: 4 }), stopReason });
  const call = (args) => m.fauxToolCall("read", args, { id: "t2" });
  for (const e of [
    { type: "start", partial: at([]) },
    { type: "text_start", contentIndex: 0, partial: at([m.fauxText("")]) },
    { type: "text_delta", contentIndex: 0, delta: "rea", partial: at([m.fauxText("rea")]) },
    { type: "text_end", contentIndex: 0, content: "reading", partial: at([m.fauxText("reading")]) },
    { type: "toolcall_start", contentIndex: 1, partial: at([m.fauxText("reading"), call({})]) },
    { type: "toolcall_delta", contentIndex: 1, delta: '{"path":"a', partial: at([m.fauxText("reading"), call({ path: "a" })]) },
    { type: "toolcall_end", contentIndex: 1, toolCall: call({ path: "a.go" }), partial: at([m.fauxText("reading"), call({ path: "a.go" })]) },
    { type: "done", reason: "toolUse", message: at([m.fauxText("reading"), call({ path: "a.go" })], "toolUse") },
  ]) {
    frames.push(safe(() => encoder.encode(e)));
  }
  log(frames, safe(() => m.reduceAssistantMessageFrames(frames.flat().filter(Boolean))));

  const tool = { name: "read", description: "Read", parameters: { type: "object", properties: { path: { type: "string" }, limit: { type: "integer" } }, required: ["path"] } };
  for (const args of [{ path: "a" }, { path: "a", limit: "5" }, { limit: 1 }, { path: 1 }]) {
    const call = { type: "toolCall", id: "c", name: "read", arguments: args };
    log(safe(() => m.validateToolArguments(tool, call)), safe(() => m.validateToolCall([tool], call)));
  }
  log(safe(() => m.validateToolCall([tool], { type: "toolCall", id: "c", name: "missing", arguments: {} })));
  log(JSON.stringify(m.StringEnum(["a", "b"], { description: "d", default: "a", title: "t" })), /^[0-9a-f-]{36}$/.test(m.uuidv7(5)), m.uuidv7(5).slice(0, 14));
  return JSON.stringify(out);
}
const want = await scene(pi), got = await scene(pig);
if (want !== got) { console.log("pinned: " + want + "\npig:    " + got); process.exit(1); }
`)
}

// The pi-coding-agent theme helpers color text as Pi's do for the same
// theme: Pi 0.87.1's own theme and keybinding-hints modules against the shim
// with ctx.ui.theme carrying PiG's dark theme palette, as the host sends it.
func TestPiThemeHelpersMatchThePinnedPackage(t *testing.T) {
	// The palette below is declared "truecolor", so the theme must resolve in truecolor whatever the terminal that runs the test reports.
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	dark, err := tui.LoadBuiltinTheme("dark")
	if err != nil {
		t.Fatal(err)
	}
	foregrounds, backgrounds := dark.ANSIPalette()
	palette, err := json.Marshal(map[string]any{"name": "dark", "foregrounds": foregrounds, "backgrounds": backgrounds, "modifiers": true, "mode": "truecolor"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORCE_COLOR", "3")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("PIG_TEST_THEME_PALETTE", string(palette))
	runPinnedComparison(t, []string{"dist", "modes", "interactive", "theme", "theme.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
const [piTheme, pig] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
const piHints = await import(new URL("../components/keybinding-hints.js", process.argv[1]).href);
const { ThemeShim } = await import(new URL("../runtime.mjs", process.argv[2]).href);
const { setRuntime } = await import(new URL("../state.mjs", process.argv[2]).href);
const shim = new ThemeShim();
shim.setPalette(JSON.parse(process.env.PIG_TEST_THEME_PALETTE));
setRuntime({ ui: { theme: shim } });
piTheme.initTheme("dark");
function scene(m, hints) {
  const select = m.getSelectListTheme();
  const settings = m.getSettingsListTheme();
  const markdown = m.getMarkdownTheme();
  return [
    select.selectedPrefix("→ "), select.selectedText("item"), select.description("desc"), select.scrollInfo("(1/3)"), select.noMatch("none"),
    settings.label("label", true), settings.label("label", false), settings.value("v", true), settings.value("v", false),
    settings.description("d"), settings.cursor, settings.hint("h"),
    markdown.heading("H"), markdown.link("l"), markdown.linkUrl("u"), markdown.code("c"), markdown.codeBlock("cb"),
    markdown.codeBlockBorder("b"), markdown.quote("q"), markdown.quoteBorder("|"), markdown.hr("-"), markdown.listBullet("*"),
    markdown.bold("b"), markdown.italic("i"), markdown.underline("u"), markdown.strikethrough("s"),
    markdown.highlightCode("const x = 1; // note\nfunction f(a) { return \"s\" + a; }", "javascript"),
    markdown.highlightCode("plain text", undefined),
    m.highlightCode("def f(x):\n    return x * 2  # twice", "python"),
    m.highlightCode("no language", "not-a-language"),
    hints.keyText("tui.select.confirm"), hints.keyText("tui.select.cancel"),
    hints.keyHint("tui.select.confirm", "to select"), hints.rawKeyHint("ctrl+x", "to cut"),
  ];
}
function themeScene(t) {
  return [t.fg("accent", "a"), t.bg("selectedBg", "b"), t.bold("c"), t.italic("d"), t.underline("e"), t.inverse("f"), t.strikethrough("g"),
    t.getFgAnsi("borderMuted"), t.getBgAnsi("toolPendingBg"), t.getColorMode(),
    t.getThinkingBorderColor("high")("h"), t.getThinkingBorderColor("unknown")("i"), t.getBashModeBorderColor()("j")];
}
assert.deepEqual(themeScene(shim), themeScene(piTheme.theme));
const got = scene(pig, pig);
if (!got.some((v) => JSON.stringify(v).includes("\\u001b[38;2;"))) throw new Error("theme helpers drew no truecolor text: " + JSON.stringify(got).slice(0, 300));
assert.deepEqual(got, scene(piTheme, piHints));
`)
}

// Pi's extension loader requires the jiti its package depends on; the
// runtime's copy must be that release.
func TestVendoredJitiIsThePinnedDependency(t *testing.T) {
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(readPinned(t, "package.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	var vendored struct{ Version string }
	data, err := os.ReadFile(filepath.Join("runtime-node", "shims", "jiti", "package.json"))
	if err != nil {
		t.Fatalf("%v: run automation/gen/vendor-pi-dist.sh", err)
	}
	if err := json.Unmarshal(data, &vendored); err != nil {
		t.Fatal(err)
	}
	if want := manifest.Dependencies["jiti"]; vendored.Version != want {
		t.Fatalf("vendored jiti %s, pinned Pi depends on jiti %s: run automation/gen/vendor-pi-dist.sh", vendored.Version, want)
	}
}

// The harness entry an extension re-launches is named like Pi's CLI package
// and carries the Pi version PiG ports, as process.argv[1] does in Pi.
func TestHarnessEntryIsNamedLikePinnedPi(t *testing.T) {
	var pinned, harness struct {
		Name    string
		Version string
		Bin     map[string]string
	}
	if err := json.Unmarshal(readPinned(t, "package.json"), &pinned); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("runtime-node", "harness", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &harness); err != nil {
		t.Fatal(err)
	}
	if harness.Name != pinned.Name || harness.Version != coding.UpstreamVersion || pinned.Version != coding.UpstreamVersion {
		t.Fatalf("harness package %s@%s, pinned Pi %s@%s, UpstreamVersion %s", harness.Name, harness.Version, pinned.Name, pinned.Version, coding.UpstreamVersion)
	}
	if _, ok := pinned.Bin["pi"]; !ok || harness.Bin["pi"] != "cli.mjs" {
		t.Fatalf("harness bin %v, pinned bin %v", harness.Bin, pinned.Bin)
	}
	if _, err := os.Stat(filepath.Join("runtime-node", "harness", "cli.mjs")); err != nil {
		t.Fatal(err)
	}
}

// Pi's tool factories run the tool in the extension's own process, and
// extensions build on their definitions at load (gentle-pi registers
// createReadToolDefinition's spread with its own renderers). The runtime's
// factories return the pinned package's shape and metadata, and the
// definitions' execute gives Pi's results at ctx.cwd.
func TestPiToolFactoriesMatchThePinnedPackage(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "core", "tools", "index.js"}, "builtin-tools.mjs", `
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
const [pi, pig] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
const fail = (message) => { console.log(message); process.exitCode = 1; };
const describe = (value) => typeof value === "function" ? "function" : JSON.stringify(value);
for (const tool of ["Read", "Bash", "Edit", "Write", "Grep", "Find", "Ls"]) {
  for (const suffix of ["Tool", "ToolDefinition"]) {
    const name = "create" + tool + suffix;
    const want = pi[name]("/tmp"), got = pig[name]("/tmp");
    if (Object.keys(want).join() !== Object.keys(got).join()) fail(name + " keys: pi " + Object.keys(want) + " pig " + Object.keys(got));
    for (const key of Object.keys(want)) {
      if (describe(want[key]) !== describe(got[key])) fail(name + "." + key + ": pi " + describe(want[key]) + " pig " + describe(got[key]));
    }
  }
}
const run = async (mod, dir) => {
  const other = mkdtempSync(join(tmpdir(), "pig-tools-other-"));
  const ctx = { cwd: dir };
  const results = [];
  const call = async (factory, params) => {
    try {
      const result = await mod[factory](other).execute("id", params, undefined, undefined, ctx);
      results.push(JSON.stringify({ content: result.content, details: result.details }));
    } catch (error) {
      results.push("error: " + error.message);
    }
  };
  await call("createWriteToolDefinition", { path: "notes/a.txt", content: "one\ntwo\nthree\n" });
  await call("createReadToolDefinition", { path: "notes/a.txt" });
  await call("createReadToolDefinition", { path: "notes/a.txt", offset: 2, limit: 1 });
  await call("createEditToolDefinition", { path: "notes/a.txt", edits: [{ oldText: "two", newText: "TWO" }] });
  await call("createReadToolDefinition", { path: "notes/a.txt" });
  await call("createLsToolDefinition", { path: "notes" });
  await call("createReadToolDefinition", { path: "missing.txt" });
  return results;
};
const piDir = mkdtempSync(join(tmpdir(), "pig-tools-pi-")), pigDir = mkdtempSync(join(tmpdir(), "pig-tools-pig-"));
const [want, got] = [await run(pi, piDir), await run(pig, pigDir)];
for (let i = 0; i < want.length; i++) {
  const normalize = (text, dir) => text.split(dir).join("<dir>");
  if (normalize(want[i], piDir) !== normalize(got[i], pigDir)) fail("call " + i + ":\npi:  " + want[i] + "\npig: " + got[i]);
}
`)
}

// createCodemodeExtension from the runtime's Pi SDK runs a script as Pi's
// does: executeCodemode (.upstream/v0.99.1/packages/coding-agent/src/extensions/codemode/execute.ts)
// loads the QuickJS wasm from getQuickJSWasmPath and starts the sandbox on
// getCodemodeWorkerUrl (config.ts:488-490,501-505), so both assets must exist
// in the private copy. A direct call has no session context, so the script
// cannot call tools (execute.ts executeCodemode). The sandbox worker inherits
// Node's execArgv, so the scene runs from a file rather than --eval.
func TestPiCodemodeRunsFromTheVendoredSDK(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	readPinned(t, "dist", "index.js")
	pinned, err := filepath.Abs(filepath.Join(pinnedPiPackages, "dist", "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	shim, err := filepath.Abs(filepath.Join("runtime-node", "shims", "pi-coding-agent.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "codemode.mjs")
	if err := os.WriteFile(script, []byte(`
import assert from "node:assert/strict";
async function scene(url) {
  const m = await import(url);
  const tools = [];
  m.createCodemodeExtension({ mode: "on", models: false })({ registerTool: (tool) => tools.push(tool), appendEntry() {}, getAllTools: () => [], getSettings: () => ({}) });
  assert.deepEqual(tools.map((tool) => [tool.name, tool.defaultActive]), [["codemode", false]]);
  const out = [];
  for (const code of ["console.log('a'); return [1 + 1, typeof store]", "throw new Error('boom')", "return await callTool('read', {})"]) {
    const result = await tools[0].execute("c1", { code }, undefined, undefined, undefined);
    out.push({ ...result, content: result.content.map((item) => ({ ...item, text: item.text?.replace(/Wall time [0-9.]+ seconds/, "Wall time <t> seconds") })), details: undefined });
  }
  return out;
}
const want = await scene(process.argv[2]);
assert.match(JSON.stringify(want), /Script completed/);
assert.deepEqual(await scene(process.argv[3]), want);
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), node, script, "file://"+filepath.ToSlash(pinned), "file://"+filepath.ToSlash(shim))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
