import {
  classifySystemOne
} from "./chunk-BXWIDDEK.js";
import {
  isRecord
} from "./chunk-THKX7O6U.js";
import "./chunk-KSWS3LLN.js";
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
