// The pi-ai module extensions import, as Pi's extension view serves it (core/extensions/pi-ai-extension-view.ts): pi-ai's compat entry, which
// serves the root and /compat, plus TypeBox's Type, which pi-ai's main entry no longer re-exports, and a StringEnum built with TypeBox so
// that extensions using it inside Type.Object keep their types. The TypeBox is the one extensions get for `typebox` (shims/typebox.mjs).
// Built-in API implementations use PiG's provider bridge (D74).
import { Type } from "./typebox.mjs";

export * from "./pi-dist/pi-ai/sdk-bundle/compat.js";
export { Type };

/**
 * Creates a string enum schema compatible with Google's API and other providers that don't support anyOf/const patterns:
 * `{ type: "string", enum: [...] }`, a TypeBox `TUnsafe`.
 */
export function StringEnum(values, options) {
  return Type.Unsafe({
    type: "string",
    enum: values,
    ...(options?.description && { description: options.description }),
    ...(options?.default && { default: options.default }),
  });
}
