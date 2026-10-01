// Audited configuration and asset seams for independent Pi SDK objects.
// Run through vendor-pi-dist.sh. No Session, Agent or loader behavior is rewritten.
import { cpSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const [pkg, output] = process.argv.slice(2);
const configPath = join(output, "config.js");
let config = readFileSync(configPath, "utf8");
function replace(before, after) {
  if (!config.includes(before)) throw new Error(`Pi config seam no longer matches: ${before}`);
  config = config.replaceAll(before, after);
}
replace('export const isBundledNode = typeof PI_BUNDLED_NODE !== "undefined" && PI_BUNDLED_NODE;', 'export const isBundledNode = true;');
replace('export const APP_NAME = piConfigName || "pi";', 'export const APP_NAME = "pig"; // pig divergence (D2): host configuration identity.');
replace('export const CONFIG_DIR_NAME = pkg.piConfig?.configDir || ".pi";', 'export { CONFIG_DIR_NAME } from "../../pig-config.mjs"; // pig divergence (D2): selected host configuration tree.');
replace('export const ENV_AGENT_DIR = `${APP_NAME.toUpperCase()}_CODING_AGENT_DIR`;', 'export { ENV_AGENT_DIR } from "../../pig-config.mjs";');
replace(`export function getAgentDir() {
    const envDir = process.env[ENV_AGENT_DIR];
    if (envDir) {
        return expandTildePath(envDir);
    }
    return join(homedir(), CONFIG_DIR_NAME, "agent");
}`, `// pig divergence (D2): independent SDK instances share the host's default config root.
import { CONFIG_DIR_NAME, getAgentDir } from "../../pig-config.mjs";
export { getAgentDir };`);
// The private copy mirrors dist directly under its package root.
replace('const srcOrDist = existsSync(join(packageDir, "src")) ? "src" : "dist";', 'const srcOrDist = ".";');
// Codemode assets: the private quickjs-wasi copy is not under a node_modules directory, so the wasm resolves beside it; isBundledNode names the worker entry that the closure copies from dist/extensions/codemode/worker.js (Pi's Bun layout, config.ts getCodemodeWorkerUrl).
replace('return embeddedQuickJSWasmPath ?? createRequire(import.meta.url).resolve("quickjs-wasi/quickjs.wasm");', 'return embeddedQuickJSWasmPath ?? fileURLToPath(new URL("../../quickjs-wasi/quickjs.wasm", import.meta.url));');
replace('return new URL("./codemode-worker.js", moduleUrl);', 'return new URL("./extensions/codemode/worker.js", moduleUrl);');
writeFileSync(configPath, config);
mkdirSync(join(output, "dist"), { recursive: true });
writeFileSync(join(output, "dist/index.js"), 'export * from "../../../pi-coding-agent.mjs";\n');
for (const file of ["package.json", "README.md", "CHANGELOG.md", "docs", "examples"]) {
  cpSync(join(pkg, file), join(output, file), { recursive: true });
}
for (const file of ["modes/interactive/theme/dark.json", "modes/interactive/theme/light.json", "modes/interactive/assets", "core/export-html/template.html", "core/export-html/template.js", "core/export-html/template.css"]) {
  cpSync(join(pkg, "dist", file), join(output, file), { recursive: true });
}
