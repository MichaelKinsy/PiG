/** Prefix of built-in tool and extension paths, such as `builtin:read` or `builtin:mcp`. */
export const BUILTIN_PATH_PREFIX = "builtin:";
/**
 * Source of a path that names no file: `builtin` for `builtin:<name>`, or the prefix of an
 * angle-bracket path such as `inline` for `<inline:name>`. Undefined for file paths.
 */
export function getSyntheticPathSource(path) {
    if (path.startsWith(BUILTIN_PATH_PREFIX))
        return "builtin";
    if (path.startsWith("<") && path.endsWith(">"))
        return path.slice(1, -1).split(":")[0] || "temporary";
    return undefined;
}
export function isSyntheticPath(path) {
    return path.startsWith(BUILTIN_PATH_PREFIX) || path.startsWith("<");
}
export function createSourceInfo(path, metadata) {
    return {
        path,
        source: metadata.source,
        scope: metadata.scope,
        origin: metadata.origin,
        baseDir: metadata.baseDir,
    };
}
export function createSyntheticSourceInfo(path, options) {
    return {
        path,
        source: options.source,
        scope: options.scope ?? "temporary",
        origin: options.origin ?? "top-level",
        baseDir: options.baseDir,
    };
}
//# sourceMappingURL=source-info.js.map