import {
  classifySystemOne,
  isRecord
} from "./chunk-3B4HGLD2.js";
import "./chunk-Y5ITVTK2.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/api/typesafe-system-one.js
var transport = {
  api: "typesafe-system-one",
  label: "System One API",
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
