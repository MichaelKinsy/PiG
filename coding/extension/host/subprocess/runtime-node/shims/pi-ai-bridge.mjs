// pig divergence (D74): the provider implementations behind pi-ai's builtin
// APIs (api/<api>.js in the pinned release) run in PiG's host instead of the
// extension process. Pi's own lazy wrappers, api registry, compat dispatch and
// env-key injection load these bridges in place of the SDK-backed modules
// (automation/gen/vendor-pi-dist.sh writes the one-line stubs), so an extension
// sees Pi's dispatch, credential order and result shapes, and the request runs
// on PiG's port of the same provider.
import { getRuntime } from "../state.mjs";
import { AssistantMessageEventStream } from "./pi-dist/pi-ai/utils/event-stream.js";

// Headers that stand in for an API key, per upstream API implementation
// (assertRequestAuth / getClientApiKey). An API not listed needs options.apiKey.
const headerCredentials = {
  "anthropic-messages": ["authorization", "x-api-key", "cf-aig-authorization"],
  "openai-completions": ["authorization", "cf-aig-authorization"],
  "openai-responses": ["authorization", "cf-aig-authorization"],
};

// APIs whose upstream implementation authenticates from the environment
// (Vertex application-default credentials, the AWS credential chain) and so
// never requires options.apiKey.
const ambientCredentials = new Set(["google-vertex", "bedrock-converse-stream"]);

function hasHeader(headers, name) {
  if (!headers) return false;
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() === name && typeof value === "string" && value.trim().length > 0) return true;
  }
  return false;
}

function missingKeyMessage(api, provider) {
  return api === "pi-messages" ? `No API key provided for provider "${provider}"` : `No API key for provider: ${provider}`;
}

// failedStream ends a stream the way an upstream provider ends a request that
// failed before it was sent.
function failedStream(model, message, options) {
  const reason = options?.signal?.aborted ? "aborted" : "error";
  const output = {
    role: "assistant",
    content: [],
    api: model.api,
    provider: model.provider,
    model: model.id,
    usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
    stopReason: reason,
    errorMessage: message,
    timestamp: Date.now(),
  };
  const stream = new AssistantMessageEventStream();
  stream.push({ type: "error", reason, error: output });
  stream.end();
  return stream;
}

function bridged(api, simple) {
  return (model, context, options) => {
    if (!ambientCredentials.has(api) && !options?.apiKey && !(headerCredentials[api] ?? []).some((name) => hasHeader(options?.headers, name))) {
      return failedStream(model, missingKeyMessage(api, model.provider), options);
    }
    // A signal that fired before the request ends it without sending, as
    // upstream's request wrapper does (utils/provider-retry.ts).
    if (options?.signal?.aborted) return failedStream(model, "Request aborted", options);
    const runtime = getRuntime();
    if (!runtime) return failedStream(model, "pi-ai: the extension runtime is not initialized", options);
    // streamSimple carries the thinking level as options.reasoning; stream()
    // takes provider-specific options instead, so it runs without reasoning
    // unless the caller names a level. Neither inherits the session's level.
    const thinking = (simple ? options?.reasoning : undefined) ?? "off";
    return runtime.startModelStream(model, context, { ...options, thinking });
  };
}

// bridgeApi returns the stream functions for one builtin pi-ai API.
export function bridgeApi(api) {
  return { stream: bridged(api, false), streamSimple: bridged(api, true) };
}

// bridgeImages returns openrouter-images' generateImages. Image generation has
// no host provider in PiG, so it ends the way upstream ends a request whose
// provider cannot load.
export function bridgeImages(api) {
  return async (model) => ({
    api: model?.api ?? api,
    provider: model?.provider,
    model: model?.id,
    output: [],
    stopReason: "error",
    errorMessage: "Image generation is not available to extensions running in PiG (see DIVERGENCES.md D74)",
    timestamp: Date.now(),
  });
}
