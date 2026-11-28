import {
  isRecord,
  postJsonRequest
} from "./chunk-PWSD6EHG.js";
import {
  calculateCost
} from "./chunk-5FR3CSGH.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/api/classifier-shared.js
function requiredNumber(label, value, field) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new Error(`${label} returned an invalid ${field}`);
  }
  return value;
}
__name(requiredNumber, "requiredNumber");
function postClassifierRequest(label, url, model, body, options, noRetryStatuses) {
  return postJsonRequest(label, url, model, body, options, noRetryStatuses);
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
  requiredNumber,
  postClassifierRequest,
  parseClassifierUsage
};
