// Builders for pi-ai's own configuration schemas (model metadata, compat settings): pi-schema's builders, shaped like the
// TypeBox schemas they replace, so models.json and the catalog validate exactly as before.
import { obj, } from "../../pi-schema/index.js";
/** An object that allows other properties; `additionalProperties: true` says so explicitly. */
export function openObject(properties, options = {}) {
    const { additionalProperties, ...annotations } = options;
    const schema = obj(properties, { ...annotations, open: true });
    return additionalProperties ? { ...schema, additionalProperties } : schema;
}
/** One string: `{ type: "string", const: value }`. */
export function stringLiteral(value, options = {}) {
    return { type: "string", const: value, ...options };
}
/** An object with any string keys and values of `value`: `{ type: "object", patternProperties: { "^.*$": value } }`. */
export function stringRecord(value, options = {}) {
    return { type: "object", patternProperties: { "^.*$": value }, ...options };
}
//# sourceMappingURL=schema-builders.js.map