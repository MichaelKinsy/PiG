import {
  classifySystemOne
} from "./chunk-NU2YA4WH.js";
import {
  isRecord
} from "./chunk-IBDYQWBE.js";
import "./chunk-YUIIRL6G.js";
import "./chunk-UVW3US7A.js";
import "./chunk-35BKLGWE.js";
import "./chunk-TA77CKFD.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/api/typesafe-system-one.js
var transport = {
  api: "typesafe-system-one",
  label: "System One API",
  // OpenRouter by provider ID, which survives baseUrl overrides, or by host, which covers regional endpoints.
  detectCompat: /* @__PURE__ */ __name((model) => model.provider === "openrouter" || model.baseUrl.includes("openrouter.ai") ? { imageInput: "state-image-urls", sendImageOptInHeader: false } : { imageInput: "assets", sendImageOptInHeader: true }, "detectCompat"),
  url: /* @__PURE__ */ __name((model) => new URL("systemone", `${model.baseUrl.replace(/\/+$/u, "")}/`), "url"),
  payload: /* @__PURE__ */ __name((model, request) => ({ model: model.id, ...request }), "payload"),
  output: /* @__PURE__ */ __name((body) => {
    if (!isRecord(body))
      throw new Error("System One API returned an unexpected response");
    return body;
  }, "output")
};
var classify = /* @__PURE__ */ __name((model, context, options) => classifySystemOne(transport, model, context, options), "classify");
export {
  classify
};
