import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { resolvePath } from "../utils/paths.js";
import { CURRENT_SESSION_VERSION } from "./session-manager.js";
/** Serialize the current branch and optional export-only entries as JSONL. */
export function serializeSessionBranch(sessionManager, createTrailingEntries) {
    const timestamp = new Date().toISOString();
    const header = {
        type: "session",
        version: CURRENT_SESSION_VERSION,
        id: sessionManager.getSessionId(),
        timestamp,
        cwd: sessionManager.getCwd(),
    };
    const entries = [header];
    let parentId = null;
    for (const entry of sessionManager.getBranch()) {
        entries.push({ ...entry, parentId });
        parentId = entry.id;
    }
    entries.push(...(createTrailingEntries?.(parentId, timestamp) ?? []));
    return `${entries.map((entry) => JSON.stringify(entry)).join("\n")}\n`;
}
/** Write the current session branch and optional export-only entries as JSONL. */
export function exportSessionToJsonl(sessionManager, outputPath, createTrailingEntries) {
    const filePath = resolvePath(outputPath ?? `session-${new Date().toISOString().replace(/[:.]/g, "-")}.jsonl`, process.cwd());
    const dir = dirname(filePath);
    if (!existsSync(dir))
        mkdirSync(dir, { recursive: true });
    writeFileSync(filePath, serializeSessionBranch(sessionManager, createTrailingEntries));
    return filePath;
}
//# sourceMappingURL=session-export.js.map