package subprocess_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// Configuration paths, the published-loader branch, SDK ownership imports, and private theme import edges change; every Session, Agent, resource, tool, and theme algorithm remains Pi's code.
func checkCodingAgentVendor(t *testing.T, root, file string, got []byte) {
	t.Helper()
	if strings.HasPrefix(file, "sdk-bundle/") {
		return // TestNodeSDKBundleRegeneratesExactly checks the compiler outputs.
	}
	if file == "dist/index.js" {
		if string(got) != "export * from \"../../../pi-coding-agent.mjs\";\n" {
			t.Fatalf("absolute SDK entry = %s", got)
		}
		return
	}
	parts := append([]string{"dist"}, strings.Split(file, "/")...)
	if file == "package.json" || file == "README.md" || file == "CHANGELOG.md" || strings.HasPrefix(file, "docs/") || strings.HasPrefix(file, "examples/") {
		parts = strings.Split(file, "/")
	}
	want := readPinned(t, parts...)
	var replacements [][2]string
	switch file {
	case "index.js":
		replacements = append(replacements, [2]string{`from "./core/sdk.js";`, `from "../../independent-session.mjs";`})
	case "core/agent-session-services.js":
		replacements = append(replacements, [2]string{`from "./sdk.js";`, `from "../../../independent-session.mjs";`})
	case "core/extensions/virtual-modules.js":
		replacements = append(replacements, [2]string{`from "../../index.js";`, `from "../../../../pi-coding-agent.mjs";`})
	case "modes/interactive/theme/theme.js":
		replacements = append(replacements,
			// .upstream/v0.99.1/packages/coding-agent/src/modes/interactive/theme/theme.ts:4-24 imports its colour helpers and getTerminalColorMode from the pi-tui barrel.
			[2]string{`import { backgroundAnsi, colorToHex, colorToOklch, foregroundAnsi, getTerminalColorMode, indexedColor, mixColors, parseColor, rgbColor, styleTextWithAnsi, } from "@earendil-works/pi-tui";`, `import { backgroundAnsi, colorToHex, colorToOklch, foregroundAnsi, indexedColor, mixColors, parseColor, rgbColor, styleTextWithAnsi, } from "../../../../pi-tui/colors.js";
import { getTerminalColorMode, } from "../../../../pi-tui/terminal-image.js";`},
			[2]string{`import { highlight, supportsLanguage } from "../../../utils/syntax-highlight.js";`, `import { highlight, supportsLanguage } from "../../../../../syntax-highlight.mjs";`},
		)
	case "modes/interactive/theme/system-theme.js":
		replacements = append(replacements,
			// .upstream/v0.99.1/packages/coding-agent/src/modes/interactive/theme/system-theme.ts:21-29 imports its colour helpers from the pi-tui barrel.
			[2]string{`import { colorToOkhsl, colorToOklch, colorToRgb, okhslColor, oklabToOkhslLightness, oklchColor, rgbColor, } from "@earendil-works/pi-tui";`, `import { colorToOkhsl, colorToOklch, colorToRgb, okhslColor, oklchColor, rgbColor, } from "../../../../pi-tui/colors.js";
import { oklabToOkhslLightness, } from "../../../../pi-tui/oklab.js";`},
		)
	case "config.js":
		replacements = append(replacements,
			[2]string{`export const isBundledNode = typeof PI_BUNDLED_NODE !== "undefined" && PI_BUNDLED_NODE;`, `export const isBundledNode = true;`},
			[2]string{`export const APP_NAME = piConfigName || "pi";`, `export const APP_NAME = "pig"; // pig divergence (D2): host configuration identity.`},
			[2]string{`export const CONFIG_DIR_NAME = pkg.piConfig?.configDir || ".pi";`, `export { CONFIG_DIR_NAME } from "../../pig-config.mjs"; // pig divergence (D2): selected host configuration tree.`},
			[2]string{"export const ENV_AGENT_DIR = `${APP_NAME.toUpperCase()}_CODING_AGENT_DIR`;", `export { ENV_AGENT_DIR } from "../../pig-config.mjs";`},
			[2]string{`const srcOrDist = existsSync(join(packageDir, "src")) ? "src" : "dist";`, `const srcOrDist = ".";`},
			// .upstream/v1.0.3/packages/coding-agent/src/config.ts:490-496,507-549 (getQuickJSWasmPath, resolveCodemodeWorkerSpecifier, getCodemodeWorkerSpecifier): the private quickjs-wasi copy is not under node_modules, and the bundled worker entry is the copied dist/extensions/codemode/worker.js. Pi 1.0.3 spawns that worker from a data: URL so an update cannot remove it; the private copy lives in PiG's content-addressed runtime directory, and the vendored worker keeps its relative imports, which need a file location.
			[2]string{`quickJSWasmPath ??= createRequire(import.meta.url).resolve("quickjs-wasi/quickjs.wasm");`, `quickJSWasmPath ??= fileURLToPath(new URL("../../quickjs-wasi/quickjs.wasm", import.meta.url));`},
			[2]string{`return new URL("./codemode-worker.js", moduleUrl);`, `return new URL("./extensions/codemode/worker.js", moduleUrl);`},
			[2]string{"    // Spawn workers from an in-memory copy. An update replaces or deletes the file while this\n    // process keeps running (#10439). The bundle build keeps the worker free of relative imports\n    // and import.meta, so it runs from a data: URL.\n    codemodeWorkerDataUrl ??= new URL(`data:text/javascript;base64,${readFileSync(specifier).toString(\"base64\")}`);\n    return codemodeWorkerDataUrl;", "    return specifier;"},
			[2]string{`export function getAgentDir() {
    const envDir = process.env[ENV_AGENT_DIR];
    if (envDir) {
        return expandTildePath(envDir);
    }
    return join(homedir(), CONFIG_DIR_NAME, "agent");
}`, `// pig divergence (D2): independent SDK instances share the host's default config root.
import { CONFIG_DIR_NAME, getAgentDir } from "../../pig-config.mjs";
export { getAgentDir };`},
		)
	}
	for _, replacement := range replacements {
		if !bytes.Contains(want, []byte(replacement[0])) {
			t.Fatalf("pinned %s no longer contains the declared seam %q", file, replacement[0])
		}
		want = bytes.ReplaceAll(want, []byte(replacement[0]), []byte(replacement[1]))
	}
	want = applyVendoredIdentityPatches(t, "pi-coding-agent/"+file, want)
	path := filepath.Join(root, "pi-coding-agent", filepath.FromSlash(file))
	if !sameExceptBareImports(t, path, got, want) {
		t.Errorf("%s differs from Pi beyond the declared module/configuration seams", path)
	}
}
