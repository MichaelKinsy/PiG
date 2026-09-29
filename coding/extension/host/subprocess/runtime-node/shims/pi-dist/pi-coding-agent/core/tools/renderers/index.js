/**
 * Built-in tool renderers, without the tools themselves.
 *
 * A presentation displays tool calls and results; it does not execute them and does not need their
 * typebox parameter schemas. Importing this instead of `core/tools/index.ts` keeps ~17 MB of module
 * graph out of a process that only renders.
 */
import { createShellRenderers } from "./bash.js";
import { editRenderers } from "./edit.js";
import { findRenderers } from "./find.js";
import { grepRenderers } from "./grep.js";
import { lsRenderers } from "./ls.js";
import { readRenderers } from "./read.js";
import { writeRenderers } from "./write.js";
export { createShellRenderers, editRenderers, findRenderers, grepRenderers, lsRenderers, readRenderers, writeRenderers, };
/** Renderers for every built-in tool, keyed by tool name. */
export function createAllToolRenderers() {
    return {
        read: readRenderers,
        bash: createShellRenderers("$"),
        powershell: createShellRenderers("PS>"),
        edit: editRenderers,
        write: writeRenderers,
        grep: grepRenderers,
        find: findRenderers,
        ls: lsRenderers,
    };
}
/**
 * Merge built-in renderers into a tool definition that does not supply its own.
 *
 * `ToolExecutionComponent` used to do this lookup itself, which forced every presentation to import
 * the tool implementations. Callers do it now, so a process that renders can import renderers alone.
 */
export function withBuiltInRenderers(toolName, definition) {
    const builtIn = createAllToolRenderers()[toolName];
    if (!definition)
        return builtIn;
    if (!builtIn)
        return definition;
    return {
        ...definition,
        renderCall: definition.renderCall ?? builtIn.renderCall,
        renderResult: definition.renderResult ?? builtIn.renderResult,
    };
}
//# sourceMappingURL=index.js.map