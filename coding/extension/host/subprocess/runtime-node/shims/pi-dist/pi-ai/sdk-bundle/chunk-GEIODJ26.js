import {
  retryProviderRequest
} from "./chunk-37NUEWSF.js";
import {
  headersToRecord,
  providerHeadersToRecord
} from "./chunk-UVW3US7A.js";
import {
  calculateCost
} from "./chunk-HK22DBOK.js";
import {
  getIdentityHeaders
} from "./chunk-7PEBEP6Y.js";
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
  return retryProviderRequest(async () => {
    const timeoutSignal = options.timeoutMs !== void 0 ? AbortSignal.timeout(options.timeoutMs) : void 0;
    const signal = options.signal && timeoutSignal ? AbortSignal.any([options.signal, timeoutSignal]) : options.signal ?? timeoutSignal;
    try {
      const next = await requestFetch(url, {
        method: "POST",
        headers: requestHeaders(model, apiKey, options.headers, options.identity),
        body: JSON.stringify(payload),
        signal
      });
      await options.onResponse?.({ status: next.status, headers: headersToRecord(next.headers) }, model);
      if (!next.ok)
        throw httpError(label, next, await next.text());
      return await next.json();
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
}
__name(postClassifierRequest, "postClassifierRequest");
function tokenCount(value) {
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : 0;
}
__name(tokenCount, "tokenCount");
function parseClassifierUsage(value, model) {
  if (!isRecord(value) || value.input_tokens === void 0 && value.output_tokens === void 0)
    return void 0;
  const totalInput = tokenCount(value.input_tokens);
  const output = tokenCount(value.output_tokens);
  const details = isRecord(value.input_tokens_details) ? value.input_tokens_details : void 0;
  const cacheRead = Math.min(tokenCount(details?.cached_tokens), totalInput);
  const cacheWrite = Math.min(tokenCount(details?.cache_write_tokens), totalInput - cacheRead);
  const usage = {
    input: totalInput - cacheRead - cacheWrite,
    output,
    cacheRead,
    cacheWrite,
    totalTokens: totalInput + output,
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
