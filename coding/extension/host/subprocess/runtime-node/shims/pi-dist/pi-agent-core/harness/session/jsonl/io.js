import { parseJsonlSessionHeader } from "./codec.js";
export function fileValue(result, action) {
    if (!result.ok)
        throw new Error(`${action}: ${result.error.message}`, { cause: result.error });
    return result.value;
}
export async function readJsonlHeader(reader, path, context) {
    const line = fileValue(await reader.readLine(context), `Failed to read JSONL storage ${path}`);
    if (line === undefined || !line.terminated || line.text === "") {
        throw new Error(`Invalid JSONL storage ${path}: missing header`);
    }
    const parsed = parseJsonlSessionHeader(line.text);
    if (!parsed.ok) {
        throw new Error(`Invalid JSONL storage ${path}: invalid header`, { cause: parsed.error });
    }
    return parsed.value;
}
function isRecord(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}
function requireSafeInteger(value, field, minimum) {
    if (!Number.isSafeInteger(value) || value < minimum)
        throw new Error(`Invalid JSONL ${field}`);
}
function parseCommittedWrite(value) {
    if (!isRecord(value))
        throw new Error("Invalid JSONL transaction write");
    requireSafeInteger(value.seq, "write seq", 1);
    switch (value.kind) {
        case "entry":
            requireSafeInteger(value.timestamp, "entry timestamp", 0);
            return value;
        case "usage":
            return value;
        case "value":
            if (value.op === "set")
                return value;
            if (value.op === "delete")
                return value;
            throw new Error(`Invalid JSONL value operation: ${String(value.op)}`);
        case "list":
            if (value.op === "append")
                return value;
            if (value.op === "delete")
                return value;
            throw new Error(`Invalid JSONL list operation: ${String(value.op)}`);
        default:
            throw new Error(`Invalid JSONL write kind: ${String(value.kind)}`);
    }
}
export function parseJsonlTransaction(line) {
    let value;
    try {
        value = JSON.parse(line);
    }
    catch (error) {
        throw new Error("Invalid JSONL transaction: not valid JSON", { cause: error });
    }
    return (Array.isArray(value) ? value : [value]).map(parseCommittedWrite);
}
export function serializeJsonlTransaction(writes) {
    return JSON.stringify(writes.length === 1 ? writes[0] : writes);
}
/** Publish only after the callback succeeds; it must await each append before returning. */
export async function publishFileAtomically(fileSystem, destinationPath, context, writeContent) {
    const tempPath = `${destinationPath}.tmp`;
    try {
        fileValue(await fileSystem.writeFile(tempPath, "", context), `Failed to stage JSONL storage ${destinationPath}`);
        await writeContent(async (content) => {
            fileValue(await fileSystem.appendFile(tempPath, content, context), `Failed to append JSONL storage ${destinationPath}`);
        });
        fileValue(await fileSystem.renameFile(tempPath, destinationPath, context), `Failed to publish JSONL storage ${destinationPath}`);
    }
    catch (error) {
        await fileSystem.remove(tempPath, { force: true }, context);
        throw error;
    }
}
/** Stream a header and complete transactions through the shared atomic publisher. */
export async function publishJsonl(fileSystem, destinationPath, header, context, writeTransactions) {
    await publishFileAtomically(fileSystem, destinationPath, context, async (append) => {
        await append(`${JSON.stringify(header)}\n`);
        await writeTransactions((writes) => append(`${serializeJsonlTransaction(writes)}\n`));
    });
}
//# sourceMappingURL=io.js.map