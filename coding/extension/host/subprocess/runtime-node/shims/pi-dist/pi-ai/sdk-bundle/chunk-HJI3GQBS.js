import {
  classifySystemOne
} from "./chunk-NJSSU3XQ.js";
import "./chunk-IXIM6JAH.js";
import {
  isRecord
} from "./chunk-PWSD6EHG.js";
import {
  isOpenRouterModel
} from "./chunk-EENKJTF4.js";
import "./chunk-HFMBG2Z5.js";
import "./chunk-UVW3US7A.js";
import "./chunk-5FR3CSGH.js";
import "./chunk-5ASU6EDP.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/api/typesafe-system-one.js
var transport = {
  api: "typesafe-system-one",
  label: "System One API",
  detectCompat: /* @__PURE__ */ __name((model) => isOpenRouterModel(model) ? { imageInput: "state-image-urls", sendImageOptInHeader: false } : { imageInput: "assets", sendImageOptInHeader: true }, "detectCompat"),
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
