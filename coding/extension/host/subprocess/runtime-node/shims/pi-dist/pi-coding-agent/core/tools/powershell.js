import { getPowerShellConfig } from "../../utils/shell.js";
import { createLocalShellOperations, createShellToolDefinition, } from "./bash.js";
import { wrapToolDefinition } from "./tool-definition-wrapper.js";
const UTF8_OUTPUT_PREFIX = "try { [Console]::OutputEncoding=[System.Text.Encoding]::UTF8 } catch {}\n";
export const powershellToolSystemPromptContribution = {
    snippet: "Execute PowerShell commands",
    guidelines: ["You can inspect PI_* environment variables for current model and session details."],
};
export function createLocalPowerShellOperations() {
    const operations = createLocalShellOperations("PowerShell", getPowerShellConfig);
    return {
        exec: (command, cwd, options) => operations.exec(`${UTF8_OUTPUT_PREFIX}${command}`, cwd, options),
    };
}
const powershellToolConfig = {
    name: "powershell",
    label: "powershell",
    shellName: "PowerShell",
    prompt: "PS>",
    promptSnippet: powershellToolSystemPromptContribution.snippet,
    promptGuidelines: powershellToolSystemPromptContribution.guidelines,
    tempFilePrefix: "pi-powershell",
};
export function createPowerShellToolDefinition(cwd, options) {
    return createShellToolDefinition(cwd, powershellToolConfig, {
        ...options,
        operations: options?.operations ?? createLocalPowerShellOperations(),
    });
}
export function createPowerShellTool(cwd, options) {
    const definition = createPowerShellToolDefinition(cwd, options);
    const tool = wrapToolDefinition(definition);
    Object.assign(tool, {
        promptSnippet: definition.promptSnippet,
        promptGuidelines: definition.promptGuidelines,
    });
    return tool;
}
//# sourceMappingURL=powershell.js.map