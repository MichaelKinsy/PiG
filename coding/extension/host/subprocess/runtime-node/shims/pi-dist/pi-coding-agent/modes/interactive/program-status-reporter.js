import { APP_NAME } from "../../config.js";
function firstLine(text) {
    return text?.split(/\r?\n/, 1)[0]?.trim() || "Error";
}
/**
 * Reports interactive-mode state to the terminal (OSC 7501): `working` during agent runs and
 * compaction, `blocked` while a dialog waits for the user, then `done`, `error`, or `idle` once the run
 * settles. Messages are limited to the session name, dialog titles, and the first line of errors; prompts
 * and assistant output are never reported.
 */
export class ProgramStatusReporter {
    getTerminal;
    getSessionName;
    runActive = false;
    compacting = false;
    /** Outcome of the current run, reported once it settles. */
    runResult = { state: "done" };
    /** Status while no run is active. */
    restingStatus = { state: "idle" };
    /** Open dialogs by source, in the order they opened. The most recent one is reported. */
    blocked = new Map();
    lastReport;
    constructor(getTerminal, getSessionName) {
        this.getTerminal = getTerminal;
        this.getSessionName = getSessionName;
    }
    handleEvent(event) {
        switch (event.type) {
            case "agent_start":
                this.runActive = true;
                this.runResult = { state: "done" };
                break;
            case "message_end":
                // The latest response decides the outcome, so a retried error is replaced by its successful retry.
                if (event.message.role !== "assistant")
                    return;
                this.runResult =
                    event.message.stopReason === "error"
                        ? { state: "error", message: firstLine(event.message.errorMessage) }
                        : { state: "done" };
                break;
            case "compaction_start":
                this.compacting = true;
                break;
            case "compaction_end":
                this.compacting = false;
                if (this.runActive) {
                    // A failed recovery compaction ends the run unless a later response succeeds.
                    if (event.aborted)
                        this.runResult = { state: "idle" };
                    else if (event.errorMessage)
                        this.runResult = { state: "error", message: firstLine(event.errorMessage) };
                }
                else if (event.aborted) {
                    this.restingStatus = { state: "idle" };
                }
                else if (event.reason === "manual") {
                    this.restingStatus = event.errorMessage
                        ? { state: "error", message: firstLine(event.errorMessage) }
                        : { state: "done" };
                }
                break;
            case "agent_settled":
                this.runActive = false;
                this.restingStatus = event.aborted ? { state: "idle" } : this.runResult;
                break;
            case "session_info_changed":
                // The session name is part of working and done reports.
                break;
            default:
                return;
        }
        this.report();
    }
    /** Report `blocked` for a dialog until it is cleared with `undefined`. Reopening a source replaces it. */
    setBlocked(source, status) {
        this.blocked.delete(source);
        if (status)
            this.blocked.set(source, status);
        this.report();
    }
    /** Forget the previous session's run, for example after switching sessions. */
    reset() {
        this.runActive = false;
        this.compacting = false;
        this.runResult = { state: "done" };
        this.restingStatus = { state: "idle" };
        this.report();
    }
    report() {
        const status = { ...this.currentStatus(), app: APP_NAME };
        const key = JSON.stringify(status);
        if (key === this.lastReport)
            return;
        this.lastReport = key;
        this.getTerminal().setProgramStatus(status);
    }
    currentStatus() {
        const blocked = [...this.blocked.values()].at(-1);
        if (blocked)
            return { state: "blocked", ...blocked };
        if (this.compacting)
            return { state: "working", message: "Compacting context" };
        const status = this.runActive ? { state: "working" } : this.restingStatus;
        if (status.state === "working" || status.state === "done")
            return { ...status, message: this.getSessionName() };
        return status;
    }
}
//# sourceMappingURL=program-status-reporter.js.map