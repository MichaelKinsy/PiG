import {
  retryProviderRequest
} from "./chunk-HFMBG2Z5.js";
import {
  headersToRecord,
  providerHeadersToRecord
} from "./chunk-UVW3US7A.js";
import {
  calculateCost
} from "./chunk-5FR3CSGH.js";
import {
  getIdentityHeaders
} from "./chunk-5ASU6EDP.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/api/json-request.js
function isRecord(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
__name(isRecord, "isRecord");
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
function finiteNonNegative(value) {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : 0;
}
__name(finiteNonNegative, "finiteNonNegative");
async function postJsonRequest(label, url, model, body, options, noRetryStatuses) {
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
        headers: providerHeadersToRecord(getIdentityHeaders(model, options.identity), { authorization: `Bearer ${apiKey}`, "content-type": "application/json" }, model.headers, options.headers) ?? {},
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
__name(postJsonRequest, "postJsonRequest");
function parsePiUsage(value, model) {
  if (!isRecord(value))
    return void 0;
  const hasTokens = typeof value.input === "number" || typeof value.output === "number";
  const rawCost = isRecord(value.cost) ? value.cost : void 0;
  const hasProviderCost = rawCost !== void 0 && typeof rawCost.total === "number" && Number.isFinite(rawCost.total);
  if (!hasTokens && !hasProviderCost)
    return void 0;
  const input = finiteNonNegative(value.input);
  const output = finiteNonNegative(value.output);
  const cacheRead = finiteNonNegative(value.cacheRead);
  const cacheWrite = finiteNonNegative(value.cacheWrite);
  const totalTokens = typeof value.totalTokens === "number" && Number.isFinite(value.totalTokens) && value.totalTokens >= 0 ? value.totalTokens : input + output + cacheRead + cacheWrite;
  const usage = {
    input,
    output,
    cacheRead,
    cacheWrite,
    // Optional splits stay undefined when the backend does not report them.
    ...typeof value.reasoning === "number" && Number.isFinite(value.reasoning) && value.reasoning >= 0 ? { reasoning: value.reasoning } : {},
    ...typeof value.imageOutput === "number" && Number.isFinite(value.imageOutput) && value.imageOutput >= 0 ? { imageOutput: value.imageOutput } : {},
    totalTokens,
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 }
  };
  if (hasProviderCost) {
    usage.cost = {
      input: finiteNonNegative(rawCost.input),
      output: finiteNonNegative(rawCost.output),
      cacheRead: finiteNonNegative(rawCost.cacheRead),
      cacheWrite: finiteNonNegative(rawCost.cacheWrite),
      total: finiteNonNegative(rawCost.total),
      ...typeof rawCost.totalFromProvider === "boolean" ? { totalFromProvider: rawCost.totalFromProvider } : {}
    };
  } else {
    calculateCost(model, usage);
  }
  return usage;
}
__name(parsePiUsage, "parsePiUsage");

export {
  isRecord,
  postJsonRequest,
  parsePiUsage
};
