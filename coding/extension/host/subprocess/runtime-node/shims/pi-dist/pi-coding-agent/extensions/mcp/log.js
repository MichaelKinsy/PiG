/**
 * Log messages MCP servers send with `notifications/message`, appended to `mcp.log` in the agent
 * directory. Several pi processes may write to the same file, so every message is one synchronous
 * append. The file is rotated to `mcp.log.1` once it grows past `MAX_LOG_BYTES`.
 */
import { appendFileSync, mkdirSync, renameSync, statSync } from "node:fs";
import { dirname } from "node:path";
const MAX_LOG_BYTES = 5 * 1024 * 1024;
function isRecord(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}
function formatData(data) {
    if (typeof data === "string")
        return data;
    try {
        return JSON.stringify(data) ?? String(data);
    }
    catch {
        return String(data);
    }
}
/** Format one `notifications/message` from `server` as a log line; continuation lines are indented. */
export function formatMcpLogMessage(server, params, now = new Date()) {
    const message = isRecord(params) ? params : { data: params };
    const level = typeof message.level === "string" ? message.level : "info";
    const logger = typeof message.logger === "string" && message.logger ? ` ${message.logger}:` : "";
    const text = formatData(message.data).replace(/\r?\n/g, "\n    ");
    return `${now.toISOString()} [${server}] ${level}${logger} ${text}\n`;
}
/** Appends server log messages to one file. Write errors are ignored: logging must not break tools. */
export class McpServerLog {
    path;
    size;
    constructor(path) {
        this.path = path;
    }
    write(server, params) {
        const line = formatMcpLogMessage(server, params);
        try {
            if (this.size === undefined) {
                mkdirSync(dirname(this.path), { recursive: true });
                this.size = this.currentSize();
            }
            if (this.size > MAX_LOG_BYTES) {
                // Another process may have rotated it already; check before renaming.
                if (this.currentSize() > MAX_LOG_BYTES)
                    renameSync(this.path, `${this.path}.1`);
                this.size = this.currentSize();
            }
            appendFileSync(this.path, line);
            this.size += Buffer.byteLength(line);
        }
        catch {
            // Ignore: the log is best effort.
        }
    }
    currentSize() {
        try {
            return statSync(this.path).size;
        }
        catch {
            return 0;
        }
    }
}
//# sourceMappingURL=log.js.map