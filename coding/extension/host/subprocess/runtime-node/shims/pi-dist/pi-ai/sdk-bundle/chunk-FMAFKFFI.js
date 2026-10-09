import {
  calculateCost
} from "./chunk-7YNYAWTY.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/utils/headers.js
function headersToRecord(headers) {
  const result = {};
  for (const [key, value] of headers.entries()) {
    result[key] = value;
  }
  return result;
}
__name(headersToRecord, "headersToRecord");
function providerHeadersToRecord(...headerSources) {
  const merged = /* @__PURE__ */ new Map();
  for (const source of headerSources) {
    for (const [name, value] of Object.entries(source ?? {})) {
      const normalizedName = name.toLowerCase();
      merged.delete(normalizedName);
      if (value !== null)
        merged.set(normalizedName, [name, value]);
    }
  }
  return merged.size > 0 ? Object.fromEntries(merged.values()) : void 0;
}
__name(providerHeadersToRecord, "providerHeadersToRecord");

// pi-dist/pi-ai/utils/provider-retry.js
var DEFAULT_MAX_RETRY_DELAY_MS = 6e4;
function isProviderError(error) {
  if (!(error instanceof Error) || !("status" in error) || !("headers" in error))
    return false;
  return (error.status === void 0 || typeof error.status === "number") && (error.headers === void 0 || error.headers instanceof Headers);
}
__name(isProviderError, "isProviderError");
function isRetryableProviderError(error) {
  const shouldRetry = error.headers?.get("x-should-retry");
  if (shouldRetry === "true")
    return true;
  if (shouldRetry === "false")
    return false;
  if (error.status === void 0)
    return true;
  return error.status === 408 || error.status === 409 || error.status === 429 || typeof error.status === "number" && error.status >= 500;
}
__name(isRetryableProviderError, "isRetryableProviderError");
function validateServerRetryDelayMs(delayMs, maxRetryDelayMs, providerErrorMessage) {
  const maxDelayMs = maxRetryDelayMs ?? DEFAULT_MAX_RETRY_DELAY_MS;
  if (maxDelayMs > 0 && delayMs > maxDelayMs) {
    throw new Error(`Server requested ${Math.ceil(delayMs / 1e3)}s retry delay (max: ${Math.ceil(maxDelayMs / 1e3)}s). ${providerErrorMessage}`);
  }
  return delayMs;
}
__name(validateServerRetryDelayMs, "validateServerRetryDelayMs");
function getRetryDelayMs(error, retryIndex, maxRetryDelayMs) {
  const retryAfterMs = error.headers?.get("retry-after-ms");
  if (retryAfterMs) {
    const value = Number.parseFloat(retryAfterMs);
    if (Number.isFinite(value))
      return validateServerRetryDelayMs(value, maxRetryDelayMs, error.message);
  }
  const retryAfter = error.headers?.get("retry-after");
  if (retryAfter) {
    const seconds = Number.parseFloat(retryAfter);
    const delayMs = Number.isNaN(seconds) ? Date.parse(retryAfter) - Date.now() : seconds * 1e3;
    if (Number.isFinite(delayMs))
      return validateServerRetryDelayMs(delayMs, maxRetryDelayMs, error.message);
  }
  const exponentialDelay = Math.min(0.5 * 2 ** retryIndex, 8) * 1e3;
  return exponentialDelay * (1 - Math.random() * 0.25);
}
__name(getRetryDelayMs, "getRetryDelayMs");
function createAbortError() {
  const error = new Error("Request aborted");
  error.name = "AbortError";
  return error;
}
__name(createAbortError, "createAbortError");
function abortableSleep(ms, signal) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(createAbortError());
      return;
    }
    const onAbort = /* @__PURE__ */ __name(() => {
      clearTimeout(timeout);
      reject(createAbortError());
    }, "onAbort");
    const timeout = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, Math.max(0, ms));
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}
__name(abortableSleep, "abortableSleep");
async function retryProviderRequest(request, options = {}) {
  const maxRetries = options.maxRetries ?? 0;
  let retriesRemaining = maxRetries;
  for (; ; ) {
    try {
      return await request();
    } catch (error) {
      if (options.signal?.aborted)
        throw createAbortError();
      if (retriesRemaining <= 0 || !isProviderError(error) || !isRetryableProviderError(error))
        throw error;
      if (error.status !== void 0 && options.noRetryStatuses?.includes(error.status))
        throw error;
      const retryIndex = maxRetries - retriesRemaining;
      retriesRemaining--;
      await abortableSleep(getRetryDelayMs(error, retryIndex, options.maxRetryDelayMs), options.signal);
    }
  }
}
__name(retryProviderRequest, "retryProviderRequest");

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
function requestHeaders(model, apiKey, optionsHeaders) {
  return providerHeadersToRecord({ authorization: `Bearer ${apiKey}`, "content-type": "application/json" }, model.headers, optionsHeaders) ?? {};
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
        headers: requestHeaders(model, apiKey, options.headers),
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

// pi-dist/pi-ai/utils/error-body.js
var MAX_PROVIDER_ERROR_BODY_CHARS = 4e3;
function normalizeProviderError(error) {
  if (!(error instanceof Error)) {
    return { message: safeJsonStringify(error), messageCarriesBody: false };
  }
  const sdkError = error;
  const status = extractStatus(sdkError);
  const body = extractBody(sdkError);
  const messageCarriesBody = body === void 0 || error.message.includes(body);
  return {
    status,
    body,
    message: error.message,
    messageCarriesBody
  };
}
__name(normalizeProviderError, "normalizeProviderError");
function extractStatus(error) {
  if (typeof error.statusCode === "number")
    return error.statusCode;
  if (typeof error.status === "number")
    return error.status;
  if (typeof error.$metadata?.httpStatusCode === "number")
    return error.$metadata.httpStatusCode;
  if (typeof error.$response?.statusCode === "number")
    return error.$response.statusCode;
  return void 0;
}
__name(extractStatus, "extractStatus");
function extractBody(error) {
  const bodyText = pickBodyText(error);
  if (bodyText === void 0)
    return void 0;
  const trimmed = bodyText.trim();
  if (trimmed.length === 0)
    return void 0;
  return truncateErrorText(trimmed, MAX_PROVIDER_ERROR_BODY_CHARS);
}
__name(extractBody, "extractBody");
function pickBodyText(error) {
  if (typeof error.body === "string")
    return error.body;
  if (isPlainNonEmptyObject(error.error))
    return safeJsonStringify(error.error);
  const responseBody = error.$response?.body;
  if (typeof responseBody === "string")
    return responseBody;
  if (isReadableStreamLike(responseBody))
    return void 0;
  if (isPlainNonEmptyObject(responseBody))
    return safeJsonStringify(responseBody);
  return void 0;
}
__name(pickBodyText, "pickBodyText");
function isReadableStreamLike(value) {
  return typeof value === "object" && value !== null && "pipe" in value && typeof value.pipe === "function";
}
__name(isReadableStreamLike, "isReadableStreamLike");
function isPlainNonEmptyObject(value) {
  if (typeof value !== "object" || value === null)
    return false;
  const proto = Object.getPrototypeOf(value);
  if (proto !== Object.prototype && proto !== null)
    return false;
  return Object.keys(value).length > 0;
}
__name(isPlainNonEmptyObject, "isPlainNonEmptyObject");
function formatProviderError(norm, prefix) {
  if (norm.messageCarriesBody || norm.status === void 0 || norm.body === void 0) {
    return prefix !== void 0 && norm.status !== void 0 ? `${prefix} (${norm.status}): ${norm.message}` : norm.message;
  }
  return prefix !== void 0 ? `${prefix} (${norm.status}): ${norm.body}` : `${norm.status}: ${norm.body}`;
}
__name(formatProviderError, "formatProviderError");
function truncateErrorText(text, maxChars) {
  if (text.length <= maxChars)
    return text;
  return `${text.slice(0, maxChars)}... [truncated ${text.length - maxChars} chars]`;
}
__name(truncateErrorText, "truncateErrorText");
function safeJsonStringify(value) {
  try {
    const serialized = JSON.stringify(value);
    return serialized === void 0 ? String(value) : serialized;
  } catch {
    return String(value);
  }
}
__name(safeJsonStringify, "safeJsonStringify");

export {
  isRecord,
  requiredNumber,
  postClassifierRequest,
  parseClassifierUsage,
  normalizeProviderError,
  formatProviderError
};
