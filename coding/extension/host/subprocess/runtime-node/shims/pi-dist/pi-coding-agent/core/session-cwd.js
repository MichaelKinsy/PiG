import { existsSync } from "node:fs";
export function getMissingSessionCwdIssue(sessionManager, fallbackCwd) {
    const sessionFile = sessionManager.getSessionFile();
    if (!sessionFile) {
        return undefined;
    }
    const sessionCwd = sessionManager.getCwd();
    if (!sessionCwd || existsSync(sessionCwd)) {
        return undefined;
    }
    return {
        sessionFile,
        sessionCwd,
        fallbackCwd,
    };
}
export function formatMissingSessionCwdError(issue) {
    const sessionFile = issue.sessionFile ? `\nSession file: ${issue.sessionFile}` : "";
    return `Stored session working directory does not exist: ${issue.sessionCwd}${sessionFile}\nCurrent working directory: ${issue.fallbackCwd}`;
}
export function formatMissingSessionCwdPrompt(issue) {
    return `Session cwd does not exist: ${issue.sessionCwd}`;
}
/** Prompt options for a missing session cwd, naming the current cwd; `undefined` cancels. */
export function getMissingSessionCwdOptions(issue) {
    return [
        { label: `Continue in ${issue.fallbackCwd} (asks again next time)`, value: "continue" },
        { label: `Fork into ${issue.fallbackCwd} (new session ID, original kept)`, value: "fork" },
        { label: "Cancel", value: undefined },
    ];
}
export class MissingSessionCwdError extends Error {
    issue;
    constructor(issue) {
        super(formatMissingSessionCwdError(issue));
        this.name = "MissingSessionCwdError";
        this.issue = issue;
    }
}
export function assertSessionCwdExists(sessionManager, fallbackCwd) {
    const issue = getMissingSessionCwdIssue(sessionManager, fallbackCwd);
    if (issue) {
        throw new MissingSessionCwdError(issue);
    }
}
//# sourceMappingURL=session-cwd.js.map