/**
 * The `@earendil-works/pi-ai` module extensions import: pi-ai's compat entry point plus TypeBox's `Type`, `Static` and
 * `TSchema`, which pi-ai's main entry no longer re-exports, and a `StringEnum` built with TypeBox (`TUnsafe<T>`), so
 * extensions that use it inside `Type.Object` type-check and infer as before. The TypeBox is the coding agent's own,
 * the same one extensions get for `typebox`.
 */
import { Type } from "../../../../typebox.mjs";
export * from "../../../pi-ai/sdk-bundle/compat.js";
export { Type };
/**
 * Creates a string enum schema compatible with Google's API and other providers that don't support anyOf/const
 * patterns: `{ type: "string", enum: [...] }`, typed for TypeBox's `Static`.
 */
export function StringEnum(values, options) {
    return Type.Unsafe({
        type: "string",
        enum: values,
        ...(options?.description && { description: options.description }),
        ...(options?.default && { default: options.default }),
    });
}
//# sourceMappingURL=pi-ai-extension-view.js.map