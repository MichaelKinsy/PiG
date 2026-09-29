// pi-agent-core as Pi serves it to extensions (core/extensions/virtual-modules.ts):
// Pi's own code, copied from the pinned release with the modules it imports
// (automation/gen/vendor-pi-dist.sh).
import { setDefaultStreamFn } from "./pi-dist/pi-agent-core/index.js";
import { streamSimple } from "./pi-ai.mjs";

// Pi installs pi-ai's compat streamSimple as the fallback for an Agent or
// agent loop built without a streamFn (core/sdk.ts), so an extension's Agent
// streams through the model registry. Here that is the runtime's pi-ai, whose
// builtin API implementations run in PiG's host (D74).
setDefaultStreamFn(streamSimple);

export * from "./pi-dist/pi-agent-core/index.js";
