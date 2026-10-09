/**
 * Component for displaying bash command execution with streaming output.
 */
import { Container, Loader, Spacer, Text } from "../../../../../pi-tui.mjs";
import { DEFAULT_MAX_BYTES, DEFAULT_MAX_LINES, truncateTail, } from "../../../core/tools/truncate.js";
import { stripAnsi } from "../../../utils/ansi.js";
import { theme } from "../theme/theme.js";
import { DynamicBorder } from "./dynamic-border.js";
import { keyHint, keyText } from "./keybinding-hints.js";
import { truncateToVisualLines } from "./visual-truncate.js";
// Preview line limit when not expanded (matches tool execution behavior)
const PREVIEW_LINES = 20;
export class BashExecutionComponent extends Container {
    command;
    outputLines = [];
    status = "running";
    exitCode = undefined;
    loader;
    truncationResult;
    fullOutputPath;
    expanded = false;
    contentContainer;
    /** `dim` marks `!!` commands, whose output is excluded from the model context. */
    colorKey;
    outputPad;
    constructor(command, ui, excludeFromContext = false, outputPad = 1) {
        super();
        this.command = command;
        this.colorKey = excludeFromContext ? "dim" : "bashMode";
        this.outputPad = outputPad;
        const borderColor = (str) => theme.fg(this.colorKey, str);
        // Add spacer
        this.addChild(new Spacer(1));
        // Top border
        this.addChild(new DynamicBorder(borderColor));
        // Content container (holds dynamic content between borders)
        this.contentContainer = new Container();
        this.addChild(this.contentContainer);
        this.loader = new Loader(ui, (spinner) => theme.fg(this.colorKey, spinner), (text) => theme.fg("muted", text), `Running... (${keyText("tui.select.cancel")} to cancel)`);
        // Bottom border
        this.addChild(new DynamicBorder(borderColor));
        this.updateDisplay();
    }
    /**
     * Set whether the output is expanded (shows full output) or collapsed (preview only).
     */
    setExpanded(expanded) {
        this.expanded = expanded;
        this.updateDisplay();
    }
    setOutputPad(outputPad) {
        this.outputPad = outputPad;
        this.updateDisplay();
    }
    invalidate() {
        super.invalidate();
        this.updateDisplay();
    }
    appendOutput(chunk) {
        // Strip ANSI codes and normalize line endings
        // Note: binary data is already sanitized in tui-renderer.ts executeBashCommand
        const clean = stripAnsi(chunk).replace(/\r\n/g, "\n").replace(/\r/g, "\n");
        // Append to output lines
        const newLines = clean.split("\n");
        if (this.outputLines.length > 0 && newLines.length > 0) {
            // Append first chunk to last line (incomplete line continuation)
            this.outputLines[this.outputLines.length - 1] += newLines[0];
            this.outputLines.push(...newLines.slice(1));
        }
        else {
            this.outputLines.push(...newLines);
        }
        this.updateDisplay();
    }
    setComplete(exitCode, cancelled, truncationResult, fullOutputPath) {
        this.exitCode = exitCode;
        this.status = cancelled
            ? "cancelled"
            : exitCode !== 0 && exitCode !== undefined && exitCode !== null
                ? "error"
                : "complete";
        this.truncationResult = truncationResult;
        this.fullOutputPath = fullOutputPath;
        // Stop loader
        this.loader.stop();
        this.updateDisplay();
    }
    updateDisplay() {
        // Apply truncation for LLM context limits (same limits as bash tool)
        const fullOutput = this.outputLines.join("\n");
        const contextTruncation = truncateTail(fullOutput, {
            maxLines: DEFAULT_MAX_LINES,
            maxBytes: DEFAULT_MAX_BYTES,
        });
        // Get the lines to potentially display (after context truncation)
        const availableLines = contextTruncation.content ? contextTruncation.content.split("\n") : [];
        // Apply preview truncation based on expanded state
        const previewLogicalLines = availableLines.slice(-PREVIEW_LINES);
        const hiddenLineCount = availableLines.length - previewLogicalLines.length;
        // Rebuild content container
        this.contentContainer.clear();
        // Command header
        const header = new Text(theme.fg(this.colorKey, theme.bold(`$ ${this.command}`)), this.outputPad, 0);
        this.contentContainer.addChild(header);
        // Output
        if (availableLines.length > 0) {
            if (this.expanded) {
                // Show all lines
                const displayText = availableLines.map((line) => theme.fg("muted", line)).join("\n");
                this.contentContainer.addChild(new Text(`\n${displayText}`, this.outputPad, 0));
            }
            else {
                // Use shared visual truncation utility with width-aware caching
                const styledOutput = previewLogicalLines.map((line) => theme.fg("muted", line)).join("\n");
                const styledInput = `\n${styledOutput}`;
                let cachedWidth;
                let cachedLines;
                this.contentContainer.addChild({
                    render: (width) => {
                        if (cachedLines === undefined || cachedWidth !== width) {
                            const result = truncateToVisualLines(styledInput, PREVIEW_LINES, width, this.outputPad);
                            cachedLines = result.visualLines;
                            cachedWidth = width;
                        }
                        return cachedLines ?? [];
                    },
                    invalidate: () => {
                        cachedWidth = undefined;
                        cachedLines = undefined;
                    },
                });
            }
        }
        // Loader or status
        if (this.status === "running") {
            this.contentContainer.addChild(this.loader);
        }
        else {
            const statusParts = [];
            // Show how many lines are hidden (collapsed preview)
            if (hiddenLineCount > 0) {
                if (this.expanded) {
                    statusParts.push(`${theme.fg("muted", "(")}${keyHint("app.tools.expand", "to collapse")}${theme.fg("muted", ")")}`);
                }
                else {
                    statusParts.push(`${theme.fg("muted", `... ${hiddenLineCount} more lines (`)}${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`);
                }
            }
            if (this.status === "cancelled") {
                statusParts.push(theme.fg("warning", "(cancelled)"));
            }
            else if (this.status === "error") {
                statusParts.push(theme.fg("error", `(exit ${this.exitCode})`));
            }
            // Add truncation warning (context truncation, not preview truncation)
            const wasTruncated = this.truncationResult?.truncated || contextTruncation.truncated;
            if (wasTruncated && this.fullOutputPath) {
                statusParts.push(theme.fg("warning", `Output truncated. Full output: ${this.fullOutputPath}`));
            }
            if (statusParts.length > 0) {
                this.contentContainer.addChild(new Text(`\n${statusParts.join("\n")}`, this.outputPad, 0));
            }
        }
    }
    /**
     * Get the raw output for creating BashExecutionMessage.
     */
    getOutput() {
        return this.outputLines.join("\n");
    }
    /**
     * Get the command that was executed.
     */
    getCommand() {
        return this.command;
    }
}
//# sourceMappingURL=bash-execution.js.map