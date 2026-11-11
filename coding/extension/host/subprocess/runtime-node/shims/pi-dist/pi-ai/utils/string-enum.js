/**
 * Creates a string enum schema compatible with Google's API and other providers
 * that don't support anyOf/const patterns: `{ type: "string", enum: [...] }`.
 *
 * @example
 * const OperationSchema = StringEnum(["add", "subtract", "multiply", "divide"], {
 *   description: "The operation to perform"
 * });
 *
 * type Operation = Static<typeof OperationSchema>; // "add" | "subtract" | "multiply" | "divide" (Static from @earendil-works/pi-schema)
 */
export function StringEnum(values, options) {
    return {
        type: "string",
        enum: values,
        ...(options?.description && { description: options.description }),
        ...(options?.default && { default: options.default }),
    };
}
//# sourceMappingURL=string-enum.js.map