/**
 * The `codemode` tool as an extension. The CLI loads it as a built-in extension; SDK users
 * add `createCodemodeExtension()` to their extension factories.
 *
 * `codemode` is registered inactive. Activate it with `--tools`, the `defaultTools` setting, or
 * `setActiveTools()`; the MCP extension activates it when MCP tools are only reachable from scripts.
 */
import { createCodemodeToolDefinition } from "./tool.js";
function readMode(pi) {
    return pi.getSettings().codemode?.mode === "only" ? "only" : "on";
}
function readInlineBudget(pi) {
    const budget = pi.getSettings().codemode?.inlineBudget;
    return typeof budget === "number" && Number.isFinite(budget) && budget >= 0 ? budget : undefined;
}
export function createCodemodeExtension(options = {}) {
    return (pi) => {
        pi.registerTool({
            ...createCodemodeToolDefinition({
                appendEntry: (customType, data) => pi.appendEntry(customType, data),
                models: options.models ?? true,
                getToolNamespace: (name) => pi.getAllTools().find((tool) => tool.name === name)?.namespace,
                getMode: () => options.mode ?? readMode(pi),
                getInlineBudget: () => options.inlineBudget ?? readInlineBudget(pi),
            }),
            defaultActive: false,
        });
    };
}
export default createCodemodeExtension();
//# sourceMappingURL=index.js.map