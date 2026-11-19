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

export {
  headersToRecord,
  providerHeadersToRecord
};
