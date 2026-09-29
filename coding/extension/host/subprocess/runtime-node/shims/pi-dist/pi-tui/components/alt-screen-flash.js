import { truncateToWidth } from "../utils.js";
const DEFAULT_DURATION_MS = 1000;
/** Transient messages composited by the alternate-screen renderer. */
export class AltScreenFlashContainer {
    entries = [];
    nextId = 0;
    requestRender;
    constructor(requestRender) {
        this.requestRender = requestRender;
    }
    flash(message, durationMs = DEFAULT_DURATION_MS) {
        const id = this.nextId++;
        const timer = setTimeout(() => {
            const index = this.entries.findIndex((entry) => entry.id === id);
            if (index === -1)
                return;
            this.entries.splice(index, 1);
            this.requestRender();
        }, Math.max(0, durationMs));
        timer.unref();
        this.entries.push({ id, message, timer });
        this.requestRender();
    }
    dispose() {
        for (const entry of this.entries)
            clearTimeout(entry.timer);
        this.entries.length = 0;
    }
    invalidate() { }
    render(width) {
        return this.entries.map((entry) => {
            const message = truncateToWidth(` ${entry.message} `, width, "");
            return `\x1b[7m${message}\x1b[27m`;
        });
    }
}
//# sourceMappingURL=alt-screen-flash.js.map