// pig divergence (D74): write pi-ai's builtin API stand-ins and the Pi modules the stand-ins re-export from, into the vendored pi-ai package.
//
// The ten builtin API implementations import vendor SDKs the extension runtime does not ship, so api/<api>.js is a stand-in that loads PiG's bridge to the
// host provider. Pi's package exports each implementation's other values through the same subpath (`@earendil-works/pi-ai/api/<api>`), which a helper process
// resolves from the harness package's pi-ai peer, so the stand-ins keep those names by re-exporting them from Pi's own module:
//   - api/pi-messages.impl.js: Pi's api/pi-messages.js byte for byte (it imports no SDK). The stand-in re-exports PiMessagesResponseError.
//   - api/openai-completions.impl.js: Pi's api/openai-completions.js with its one SDK import (`openai`) pointed at shims/openai-sdk-absent.mjs, whose class throws
//     when constructed; only the request code constructs it. The stand-in re-exports convertMessages.
//   - api/google-shared.js: Pi's module (the google-generative-ai and google-vertex implementations' shared converters) with its one SDK import (`@google/genai`)
//     pointed at shims/google-genai-enums.mjs, the three enum objects it uses copied byte for byte from the SDK the oracle carries.
// Each replaced line must occur exactly once in Pi's module, so a Pi release that changes it fails the vendoring step. The manifest records the hash of each source.
//
// usage: node vendor-pi-ai-apis.mjs <shims> <pi-ai package root> <@google/genai package root>
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

export const bridgedApis = [
  "anthropic-messages", "azure-openai-responses", "bedrock-converse-stream", "google-generative-ai", "google-vertex",
  "mistral-conversations", "openai-codex-responses", "openai-completions", "openai-responses", "pi-messages",
];

export const googleEnums = ["FinishReason", "FunctionCallingConfigMode", "ThinkingLevel"];
const googleImport = 'import { FinishReason, FunctionCallingConfigMode, ThinkingLevel as GoogleSdkThinkingLevel, } from "@google/genai";';

// Pi modules vendored beside the stand-ins: the file under pi-ai/ (its source is Pi's dist/<source>), what replaces which line, and the values the stand-in re-exports from it.
export const apiModules = {
  "api/pi-messages.impl.js": { source: "api/pi-messages.js", replace: [], stub: "pi-messages", reexport: ["PiMessagesResponseError"] },
  "api/openai-completions.impl.js": {
    source: "api/openai-completions.js",
    replace: [['import OpenAI from "openai";', 'import OpenAI from "../../../openai-sdk-absent.mjs";']],
    stub: "openai-completions",
    reexport: ["convertMessages"],
  },
  "api/google-shared.js": {
    source: "api/google-shared.js",
    replace: [[googleImport, googleImport.replace('"@google/genai"', '"../../../google-genai-enums.mjs"')]],
  },
};

// Values a stand-in exports beside stream and streamSimple that Pi's module keeps in process-local state, so the stand-in cannot copy Pi's module: the bridge supplies them.
export const bridgedHelpers = { "openai-codex-responses": ["closeOpenAICodexWebSocketSessions", "getOpenAICodexWebSocketDebugStats", "resetOpenAICodexWebSocketDebugStats"] };

export function stubSource(api) {
  const impl = Object.entries(apiModules).find(([, module]) => module.stub === api);
  const reexport = impl ? `export { ${impl[1].reexport.join(", ")} } from "./${impl[0].slice("api/".length)}";\n` : "";
  const helpers = bridgedHelpers[api];
  const helperImport = helpers ? ", bridgeCodexWebSocketHelpers" : "";
  const helperExport = helpers ? `export const { ${helpers.join(", ")} } = bridgeCodexWebSocketHelpers();\n` : "";
  return `// PiG: the ${api} implementation runs in PiG's host (D74); see automation/gen/vendor-pi-dist.sh.\nimport { bridgeApi${helperImport} } from "../../../pi-ai-bridge.mjs";\n${reexport}export const { stream, streamSimple } = bridgeApi("${api}");\n${helperExport}`;
}

export const imagesStubSource = '// PiG: image generation has no host provider (D74); see automation/gen/vendor-pi-dist.sh.\nimport { bridgeImages } from "../../../pi-ai-bridge.mjs";\nexport const generateImages = bridgeImages("openrouter-images");\n';

// replaceOnce returns text with each [from, to] applied, failing unless from occurs exactly once.
export function replaceOnce(name, text, replacements) {
  for (const [from, to] of replacements) {
    const parts = text.split(from);
    if (parts.length !== 2) throw new Error(`vendor-pi-ai-apis: the pinned ${name} holds ${JSON.stringify(from)} ${parts.length - 1} times, want exactly once: update automation/gen/vendor-pi-ai-apis.mjs`);
    text = parts.join(to);
  }
  return text;
}

// googleEnumsSource is shims/google-genai-enums.mjs: the SDK's licence header and each enum's declaration copied byte for byte from dist/node/index.mjs.
export function googleEnumsSource(sdk, version) {
  const license = /^\/\*\*\n \* @license\n[\s\S]*?\*\/\n/m.exec(sdk);
  if (!license) throw new Error("vendor-pi-ai-apis: @google/genai has no licence header");
  const blocks = googleEnums.map((name) => {
    const block = new RegExp(`^var ${name};\\n\\(function \\(${name}\\) \\{\\n[\\s\\S]*?^\\}\\)\\(${name} \\|\\| \\(${name} = \\{\\}\\)\\);\\n`, "m").exec(sdk);
    if (!block) throw new Error(`vendor-pi-ai-apis: @google/genai ${version} declares no enum ${name} in the expected form`);
    return block[0];
  });
  return `// PiG (D74): the enums api/google-shared.js imports from @google/genai ${version}, copied byte for byte from its dist/node/index.mjs by automation/gen/vendor-pi-ai-apis.mjs.\n// The rest of the SDK is not shipped to extension processes.\n${license[0]}\n${blocks.join("\n")}\nexport { ${googleEnums.join(", ")} };\n`;
}

if (process.argv[1] === fileURLToPath(import.meta.url) && process.argv[2] === "--names") {
  // The api/<name> modules this script writes in place of Pi's verbatim copy: vendor-pi-dist.sh skips them.
  const sources = Object.values(apiModules).map((module) => module.source.slice("api/".length, -".js".length));
  console.log([...bridgedApis, ...sources].join(" "));
} else if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [shims, ai, genai] = process.argv.slice(2);
  const dist = join(shims, "pi-dist", "pi-ai");
  mkdirSync(join(dist, "api"), { recursive: true });
  for (const api of bridgedApis) writeFileSync(join(dist, "api", `${api}.js`), stubSource(api));
  writeFileSync(join(dist, "api", "openrouter-images.js"), imagesStubSource);
  for (const [file, module] of Object.entries(apiModules)) {
    const source = join(ai, "dist", module.source);
    writeFileSync(join(dist, file), replaceOnce(module.source, readFileSync(source, "utf8"), module.replace));
  }
  const version = JSON.parse(readFileSync(join(genai, "package.json"), "utf8")).version;
  writeFileSync(join(shims, "google-genai-enums.mjs"), googleEnumsSource(readFileSync(join(genai, "dist", "node", "index.mjs"), "utf8"), version));
  if (!existsSync(join(shims, "openai-sdk-absent.mjs"))) throw new Error("vendor-pi-ai-apis: shims/openai-sdk-absent.mjs is missing");
}
