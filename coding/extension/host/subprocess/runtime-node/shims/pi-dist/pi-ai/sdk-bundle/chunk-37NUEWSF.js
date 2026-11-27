import {
  headersToRecord
} from "./chunk-UVW3US7A.js";
import {
  ownedSlice
} from "./chunk-7PEBEP6Y.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

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
  const message = status === void 0 && body === void 0 ? withCauseDetail(error) : error.message;
  return {
    status,
    body,
    message,
    messageCarriesBody
  };
}
__name(normalizeProviderError, "normalizeProviderError");
var MAX_CAUSE_DEPTH = 5;
function withCauseDetail(error) {
  let deepest;
  let current = error.cause;
  for (let depth = 0; depth < MAX_CAUSE_DEPTH && current instanceof Error; depth++) {
    deepest = current;
    current = current.cause;
  }
  if (!deepest)
    return error.message;
  let detail = (deepest.message || deepest.name).trim();
  const code = deepest.code;
  if ((typeof code === "string" || typeof code === "number") && !detail.includes(String(code))) {
    detail = detail ? `${detail} (${code})` : String(code);
  }
  if (!detail || error.message.includes(detail))
    return error.message;
  return error.message ? `${error.message}: ${detail}` : detail;
}
__name(withCauseDetail, "withCauseDetail");
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
  return `${ownedSlice(text, 0, maxChars)}... [truncated ${text.length - maxChars} chars]`;
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
      if (isProviderError(error) && error.status !== void 0) {
        const headers = error.headers ? headersToRecord(error.headers) : {};
        await options.onErrorResponse?.({ status: error.status, headers });
      }
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

export {
  normalizeProviderError,
  formatProviderError,
  retryProviderRequest
};
