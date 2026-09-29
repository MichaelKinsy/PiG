import { err, ok } from "../../types.js";
import { JSONL_FORMAT_VERSION } from "./types.js";
function isRecord(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}
function isSafeIntegerAtLeast(value, minimum) {
    return Number.isSafeInteger(value) && value >= minimum;
}
export function isLegacyV3SessionHeader(value) {
    return (isRecord(value) &&
        value.type === "session" &&
        value.version === 3 &&
        typeof value.id === "string" &&
        typeof value.cwd === "string" &&
        typeof value.timestamp === "string" &&
        Number.isFinite(Date.parse(value.timestamp)) &&
        (value.parentSession === undefined || typeof value.parentSession === "string"));
}
export function isJsonlStorageHeader(value) {
    return (isRecord(value) &&
        value.kind === "header" &&
        value.v === JSONL_FORMAT_VERSION &&
        typeof value.id === "string" &&
        typeof value.cwd === "string" &&
        isSafeIntegerAtLeast(value.storageVersion, 1) &&
        isSafeIntegerAtLeast(value.createdAt, 0) &&
        (value.nextSeq === undefined || isSafeIntegerAtLeast(value.nextSeq, 1)) &&
        (value.parentSessionId === undefined || typeof value.parentSessionId === "string") &&
        (value.legacyParentSessionPath === undefined || typeof value.legacyParentSessionPath === "string"));
}
export function parseJsonlSessionHeader(line) {
    let value;
    try {
        value = JSON.parse(line);
    }
    catch (error) {
        return err(new Error("Invalid JSONL session header: not valid JSON", { cause: error }));
    }
    if (isJsonlStorageHeader(value))
        return ok({ format: "v4", header: value });
    if (isLegacyV3SessionHeader(value))
        return ok({ format: "v3-legacy", header: value });
    return err(new Error("Unsupported JSONL session header"));
}
//# sourceMappingURL=codec.js.map