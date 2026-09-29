import { getOrThrow } from "../types.js";
const UNICODE_SPACES = /[\u00A0\u2000-\u200A\u202F\u205F\u3000]/g;
const NARROW_NO_BREAK_SPACE = "\u202F";
function normalizeToolPath(path) {
    const normalized = path.replace(UNICODE_SPACES, " ");
    return normalized.startsWith("@") ? normalized.slice(1) : normalized;
}
export async function resolveToolPath(env, path, context) {
    return getOrThrow(await env.absolutePath(normalizeToolPath(path), context));
}
export async function resolveReadToolPath(env, path, context) {
    const resolved = await resolveToolPath(env, path, context);
    const variants = [
        resolved,
        resolved.replace(/ (AM|PM)\./gi, `${NARROW_NO_BREAK_SPACE}$1.`),
        resolved.normalize("NFD"),
        resolved.replace(/'/g, "\u2019"),
        resolved.normalize("NFD").replace(/'/g, "\u2019"),
    ];
    for (const variant of new Set(variants)) {
        if (getOrThrow(await env.exists(variant, context)))
            return variant;
    }
    return resolved;
}
//# sourceMappingURL=path-utils.js.map