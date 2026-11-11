// Schema builders: short functions that return plain JSON Schema objects and carry the static type of the values
// they accept, so `Static<typeof schema>` is the TypeScript type a valid value has. The objects are ordinary data:
// they serialize with JSON.stringify and compile with compile() like any other schema.
/** A string, with optional length bounds (in code points), pattern and format. */
export function str(options = {}) {
    return { type: "string", ...options };
}
/** A number within optional bounds. */
export function num(options = {}) {
    return { type: "number", ...options };
}
/** An integer within optional bounds. */
export function int(options = {}) {
    return { type: "integer", ...options };
}
/** A boolean. */
export function bool(options = {}) {
    return { type: "boolean", ...options };
}
/** `null`. */
export function nul(options = {}) {
    return { type: "null", ...options };
}
/** Any value. */
export function unknown(options = {}) {
    return { ...options };
}
/** Exactly `value` (a string, number, boolean or null). */
export function lit(value, options = {}) {
    return { const: value, ...options };
}
/** One of the given values. */
export function oneOf(values, options = {}) {
    return { enum: values, ...options };
}
/**
 * Any of the given schemas. A union of objects tagged by a `lit()` property (`{ kind: lit("text"), ... }`) is checked
 * by the tag: only the branch it selects runs, and its errors are the ones reported.
 */
export function union(schemas, options = {}) {
    return { anyOf: schemas, ...options };
}
/** `schema` or `null`. */
export function nullable(schema, options = {}) {
    return { anyOf: [schema, { type: "null" }], ...options };
}
/** An array of `items`, with optional length bounds and unique items. */
export function arr(items, options = {}) {
    return { type: "array", items, ...options };
}
/** A fixed-length array whose items match `items` in order. */
export function tuple(items, options = {}) {
    return { type: "array", prefixItems: items, items: false, minItems: items.length, ...options };
}
/** An object with string keys and values of `value` (a record). */
export function map(value, options = {}) {
    return { type: "object", additionalProperties: value, ...options };
}
/** A Uint8Array with optional length bounds (an extension of JSON Schema for values that never pass through JSON). */
export function bytes(options = {}) {
    return { type: "bytes", ...options };
}
/** An optional property of obj(): `{ name: opt(str()) }` may omit `name` or set it to undefined. */
export class Optional {
    schema;
    constructor(schema) {
        this.schema = schema;
    }
}
/** Marks an obj() property as optional. */
export function opt(schema) {
    return new Optional(schema);
}
/**
 * A closed object: the given properties (required unless opt()) and no others. `open: true` allows other
 * properties, as tool schemas from other systems often do.
 */
export function obj(properties, options = {}) {
    const { open, ...annotations } = options;
    const props = {};
    const required = [];
    for (const [name, property] of Object.entries(properties)) {
        if (property instanceof Optional)
            props[name] = property.schema;
        else {
            props[name] = property;
            required.push(name);
        }
    }
    return {
        type: "object",
        properties: props,
        ...(required.length > 0 ? { required } : {}),
        ...(open === true ? {} : { additionalProperties: false }),
        ...annotations,
    };
}
//# sourceMappingURL=builders.js.map