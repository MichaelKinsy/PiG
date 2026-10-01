/**
 * Codemode source format: JavaScript, optionally preceded by one options line.
 *
 * ```js
 * // @options: {"max_output_tokens": 2000, "timeout_ms": 30000}
 * const text = await tools.read({ path: "package.json" });
 * text(JSON.parse(text).name);
 * ```
 */
export const CODEMODE_OPTIONS_PREFIX = "// @options:";
const SUPPORTED_FIELDS = ["max_output_tokens", "timeout_ms"];
const SUPPORTED_FIELDS_TEXT = "`max_output_tokens` and `timeout_ms`";
/** Largest delay `setTimeout` supports, which bounds `timeout_ms`. */
const MAX_TIMEOUT_MS = 2_147_483_647;
/**
 * Lark grammar for providers with grammar-constrained tool input. It only fixes the shape of the
 * options line; the options JSON and the code are checked by {@link parseCodemodeSource}.
 */
export const CODEMODE_SOURCE_GRAMMAR = String.raw `
start: options_source | plain_source
options_source: OPTIONS_LINE NEWLINE SOURCE
plain_source: SOURCE

OPTIONS_LINE: /[ \t]*\/\/ @options:[^\r\n]*/
NEWLINE: /\r?\n/
SOURCE: /[\s\S]+/
`;
export class CodemodeSourceError extends Error {
    constructor(message) {
        super(message);
        this.name = "CodemodeSourceError";
    }
}
function isSafeInteger(value) {
    return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}
function parseOptions(directive) {
    if (directive === "") {
        throw new CodemodeSourceError(`@options must be a JSON object with supported fields ${SUPPORTED_FIELDS_TEXT}`);
    }
    let value;
    try {
        value = JSON.parse(directive);
    }
    catch (error) {
        throw new CodemodeSourceError(`@options must be valid JSON with supported fields ${SUPPORTED_FIELDS_TEXT}: ${error instanceof Error ? error.message : String(error)}`);
    }
    if (typeof value !== "object" || value === null || Array.isArray(value)) {
        throw new CodemodeSourceError(`@options must be a JSON object with supported fields ${SUPPORTED_FIELDS_TEXT}`);
    }
    const fields = value;
    for (const key of Object.keys(fields)) {
        if (!SUPPORTED_FIELDS.includes(key)) {
            throw new CodemodeSourceError(`@options only supports ${SUPPORTED_FIELDS_TEXT}; got \`${key}\``);
        }
    }
    const options = {};
    const { max_output_tokens, timeout_ms } = fields;
    if (max_output_tokens !== undefined) {
        if (!isSafeInteger(max_output_tokens)) {
            throw new CodemodeSourceError("@options field `max_output_tokens` must be a non-negative safe integer");
        }
        options.maxOutputTokens = max_output_tokens;
    }
    if (timeout_ms !== undefined) {
        if (!isSafeInteger(timeout_ms) || timeout_ms === 0 || timeout_ms > MAX_TIMEOUT_MS) {
            throw new CodemodeSourceError(`@options field \`timeout_ms\` must be a positive integer up to ${MAX_TIMEOUT_MS}`);
        }
        options.timeoutMs = timeout_ms;
    }
    return options;
}
/**
 * Split an optional first-line `// @options: {...}` from the script. Throws
 * {@link CodemodeSourceError} for empty input and invalid options.
 */
export function parseCodemodeSource(input) {
    if (input.trim() === "") {
        throw new CodemodeSourceError('Expected JavaScript source text (non-empty). Provide JS only, optionally with a first line `// @options: {"max_output_tokens": 1000}`.');
    }
    const newline = input.indexOf("\n");
    const firstLine = (newline === -1 ? input : input.slice(0, newline)).replace(/\r$/, "");
    const trimmed = firstLine.trimStart();
    if (!trimmed.startsWith(CODEMODE_OPTIONS_PREFIX))
        return { code: input, options: {} };
    const code = newline === -1 ? "" : input.slice(newline);
    if (code.trim() === "") {
        throw new CodemodeSourceError("The @options line must be followed by JavaScript source on subsequent lines");
    }
    return { code, options: parseOptions(trimmed.slice(CODEMODE_OPTIONS_PREFIX.length).trim()) };
}
//# sourceMappingURL=source.js.map