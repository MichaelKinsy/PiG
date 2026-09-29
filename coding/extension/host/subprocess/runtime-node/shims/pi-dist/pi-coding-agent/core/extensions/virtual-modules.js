import * as bundledPiAgentCore from "../../../pi-agent-core/index.js";
import * as bundledPiAiCompat from "../../../pi-ai/sdk-bundle/compat.js";
import * as bundledPiAiOauth from "../../../pi-ai/oauth.js";
import * as bundledPiAiProviders from "../../../pi-ai/sdk-bundle/providers.js";
import * as bundledPiTui from "../../../../pi-tui.mjs";
import * as bundledTypebox from "../../../../typebox.mjs";
import * as bundledTypeboxCompile from "../../../../typebox-compile.mjs";
import * as bundledTypeboxValue from "../../../../typebox-value.mjs";
// This import is safe because loader.ts exports are not re-exported from index.ts.
// Extensions can therefore import from @earendil-works/pi-coding-agent.
import * as bundledPiCodingAgent from "../../../../pi-coding-agent.mjs";
/** Modules available to extensions in source and compiled binary runtimes. */
export const VIRTUAL_MODULES = {
    typebox: bundledTypebox,
    "typebox/compile": bundledTypeboxCompile,
    "typebox/value": bundledTypeboxValue,
    "@sinclair/typebox": bundledTypebox,
    "@sinclair/typebox/compile": bundledTypeboxCompile,
    "@sinclair/typebox/value": bundledTypeboxValue,
    "@earendil-works/pi-agent-core": bundledPiAgentCore,
    "@earendil-works/pi-tui": bundledPiTui,
    // Extensions resolve the pi-ai root to the compat entrypoint (a strict
    // superset of the core entrypoint): existing extensions using the old
    // global API keep working at runtime until compat is removed.
    "@earendil-works/pi-ai": bundledPiAiCompat,
    "@earendil-works/pi-ai/compat": bundledPiAiCompat,
    "@earendil-works/pi-ai/oauth": bundledPiAiOauth,
    "@earendil-works/pi-ai/providers/all": bundledPiAiProviders,
    "@earendil-works/pi-coding-agent": bundledPiCodingAgent,
    "@mariozechner/pi-agent-core": bundledPiAgentCore,
    "@mariozechner/pi-tui": bundledPiTui,
    "@mariozechner/pi-ai": bundledPiAiCompat,
    "@mariozechner/pi-ai/compat": bundledPiAiCompat,
    "@mariozechner/pi-ai/oauth": bundledPiAiOauth,
    "@mariozechner/pi-ai/providers/all": bundledPiAiProviders,
    "@mariozechner/pi-coding-agent": bundledPiCodingAgent,
};
//# sourceMappingURL=virtual-modules.js.map