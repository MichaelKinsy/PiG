import {
  classifySystemOne,
  isRecord
} from "./chunk-XA6S5H4Z.js";
import "./chunk-JUG7GWVD.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/api/cloudflare-workers-ai-system-one.js
var LABEL = "Cloudflare Workers AI";
function cloudflareErrorMessage(errors) {
  if (Array.isArray(errors)) {
    const messages = errors.map((error) => isRecord(error) && typeof error.message === "string" ? error.message : void 0).filter((message) => message !== void 0);
    if (messages.length > 0)
      return `${LABEL} error: ${messages.join("; ")}`;
  }
  return `${LABEL} request failed`;
}
__name(cloudflareErrorMessage, "cloudflareErrorMessage");
var transport = {
  api: "cloudflare-workers-ai-system-one",
  label: LABEL,
  url: /* @__PURE__ */ __name((model) => new URL("run", `${model.baseUrl.replace(/\/+$/u, "")}/`), "url"),
  payload: /* @__PURE__ */ __name((model, request) => ({ model: model.id, input: request }), "payload"),
  output: /* @__PURE__ */ __name((body) => {
    if (!isRecord(body))
      throw new Error(`${LABEL} returned an unexpected response`);
    if (body.success === false)
      throw new Error(cloudflareErrorMessage(body.errors));
    const run = body.result;
    if (!isRecord(run))
      throw new Error(`${LABEL} returned an unexpected response`);
    if (run.state !== "Completed") {
      throw new Error(`${LABEL} run did not complete (state: ${String(run.state)})`);
    }
    if (!isRecord(run.result))
      throw new Error(`${LABEL} returned an unexpected response`);
    return run.result;
  }, "output")
};
var classify = /* @__PURE__ */ __name((model, context, options) => classifySystemOne(transport, model, context, options), "classify");
export {
  classify
};
