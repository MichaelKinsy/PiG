import {
  isRecord,
  parsePiUsage,
  postJsonRequest
} from "./chunk-PWSD6EHG.js";
import {
  formatProviderError,
  normalizeProviderError
} from "./chunk-HFMBG2Z5.js";
import "./chunk-UVW3US7A.js";
import "./chunk-5FR3CSGH.js";
import "./chunk-5ASU6EDP.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/api/pi-image.js
var LABEL = "Pi Image";
function parseOutput(value) {
  if (!Array.isArray(value))
    throw new Error(`${LABEL} returned an unexpected response`);
  return value.map((item, index) => {
    if (!isRecord(item))
      throw new Error(`${LABEL} returned an invalid output block at index ${index}`);
    if (item.type === "text") {
      if (typeof item.text !== "string") {
        throw new Error(`${LABEL} returned an invalid text block at index ${index}`);
      }
      return { type: "text", text: item.text };
    }
    if (item.type === "image") {
      if (typeof item.data !== "string" || !item.data || typeof item.mimeType !== "string" || !item.mimeType) {
        throw new Error(`${LABEL} returned an invalid image block at index ${index}`);
      }
      return { type: "image", data: item.data, mimeType: item.mimeType };
    }
    throw new Error(`${LABEL} returned an unknown output block type at index ${index}`);
  });
}
__name(parseOutput, "parseOutput");
var generateImages = /* @__PURE__ */ __name(async (model, context, options) => {
  const output = {
    api: model.api,
    provider: model.provider,
    model: model.id,
    output: [],
    stopReason: "stop",
    timestamp: Date.now()
  };
  try {
    if (model.api !== "pi-image")
      throw new Error(`Unsupported image API: ${model.api}`);
    const hasInputImages = context.input.some((item) => item.type === "image");
    if (hasInputImages && !model.input.includes("image")) {
      throw new Error(`${LABEL} model ${model.id} does not support image input`);
    }
    const url = new URL("image", `${model.baseUrl.replace(/\/+$/u, "")}/`);
    const wireOptions = {
      ...options?.aspectRatio !== void 0 ? { aspectRatio: options.aspectRatio } : {},
      ...options?.size !== void 0 ? { size: options.size } : {},
      ...options?.sessionId !== void 0 ? { sessionId: options.sessionId } : {},
      ...options?.metadata !== void 0 ? { metadata: options.metadata } : {}
    };
    const requestBody = {
      model: model.id,
      context: { input: context.input },
      ...Object.keys(wireOptions).length > 0 ? { options: wireOptions } : {}
    };
    const body = await postJsonRequest(LABEL, url, model, requestBody, options);
    if (!isRecord(body))
      throw new Error(`${LABEL} returned an unexpected response`);
    if (typeof body.responseId === "string" && body.responseId)
      output.responseId = body.responseId;
    const usage = parsePiUsage(body.usage, model);
    if (usage)
      output.usage = usage;
    output.output = parseOutput(body.output);
    return output;
  } catch (error) {
    output.output = [];
    output.stopReason = options?.signal?.aborted ? "aborted" : "error";
    output.errorMessage = formatProviderError(normalizeProviderError(error), `${LABEL} error`);
    return output;
  }
}, "generateImages");
export {
  generateImages
};
