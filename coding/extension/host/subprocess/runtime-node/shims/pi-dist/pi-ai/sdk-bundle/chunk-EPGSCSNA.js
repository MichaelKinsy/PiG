import {
  retryProviderRequest
} from "./chunk-QURJPWWX.js";
import {
  headersToRecord,
  providerHeadersToRecord
} from "./chunk-UVW3US7A.js";
import {
  calculateCost
} from "./chunk-XXMZSYZB.js";
import {
  getIdentityHeaders
} from "./chunk-D4UU5BNJ.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/api/classifier-shared.js
function httpError(label, response, body) {
  const error = new Error(`${label} returned ${response.status}`);
  error.status = response.status;
  error.headers = response.headers;
  error.body = body;
  return error;
}
__name(httpError, "httpError");
function timeoutError(timeoutMs) {
  const error = new Error(`Request timed out after ${timeoutMs}ms`);
  error.name = "TimeoutError";
  error.status = void 0;
  error.headers = void 0;
  error.body = "";
  return error;
}
__name(timeoutError, "timeoutError");
function isRecord(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
__name(isRecord, "isRecord");
function requiredNumber(label, value, field) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new Error(`${label} returned an invalid ${field}`);
  }
  return value;
}
__name(requiredNumber, "requiredNumber");
function requestHeaders(model, apiKey, optionsHeaders, identity) {
  return providerHeadersToRecord(getIdentityHeaders(model, identity), { authorization: `Bearer ${apiKey}`, "content-type": "application/json" }, model.headers, optionsHeaders) ?? {};
}
__name(requestHeaders, "requestHeaders");
async function postClassifierRequest(label, url, model, body, options, noRetryStatuses) {
  if (!options?.apiKey)
    throw new Error(`No API key for provider: ${model.provider}`);
  const apiKey = options.apiKey;
  let payload = body;
  const transformed = await options.onPayload?.(payload, model);
  if (transformed !== void 0)
    payload = transformed;
  const requestFetch = options.fetch ?? globalThis.fetch;
  const { response, json } = await retryProviderRequest(async () => {
    const timeoutSignal = options.timeoutMs !== void 0 ? AbortSignal.timeout(options.timeoutMs) : void 0;
    const signal = options.signal && timeoutSignal ? AbortSignal.any([options.signal, timeoutSignal]) : options.signal ?? timeoutSignal;
    try {
      const next = await requestFetch(url, {
        method: "POST",
        headers: requestHeaders(model, apiKey, options.headers, options.identity),
        body: JSON.stringify(payload),
        signal
      });
      if (!next.ok)
        throw httpError(label, next, await next.text());
      return { response: next, json: await next.json() };
    } catch (error) {
      if (timeoutSignal?.aborted && !options.signal?.aborted)
        throw timeoutError(options.timeoutMs);
      throw error;
    }
  }, {
    maxRetries: options.maxRetries ?? 2,
    maxRetryDelayMs: options.maxRetryDelayMs,
    signal: options.signal,
    noRetryStatuses
  });
  await options.onResponse?.({ status: response.status, headers: headersToRecord(response.headers) }, model);
  return json;
}
__name(postClassifierRequest, "postClassifierRequest");
function tokenCount(value) {
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : 0;
}
__name(tokenCount, "tokenCount");
function parseClassifierUsage(value, model) {
  if (!isRecord(value) || value.input_tokens === void 0 && value.output_tokens === void 0)
    return void 0;
  const input = tokenCount(value.input_tokens);
  const output = tokenCount(value.output_tokens);
  const usage = {
    input,
    output,
    cacheRead: 0,
    cacheWrite: 0,
    totalTokens: input + output,
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 }
  };
  calculateCost(model, usage);
  return usage;
}
__name(parseClassifierUsage, "parseClassifierUsage");

export {
  isRecord,
  requiredNumber,
  postClassifierRequest,
  parseClassifierUsage
};
