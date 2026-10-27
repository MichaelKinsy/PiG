import { Container, Markdown, MouseRegion, Spacer, Text, } from "../../../../../pi-tui.mjs";
import { getMarkdownTheme, theme } from "../theme/theme.js";
import { createMarkdownTransform } from "./markdown-transform.js";
const OSC133_ZONE_START = "\x1b]133;A\x07";
const OSC133_ZONE_END = "\x1b]133;B\x07";
const OSC133_ZONE_FINAL = "\x1b]133;C\x07";
/**
 * Component that renders a complete assistant message
 */
export class AssistantMessageComponent extends Container {
    contentContainer;
    hideThinkingBlock;
    markdownTheme;
    hiddenThinkingLabel;
    outputPad;
    markdownTransformers;
    lastMessage;
    hasToolCalls = false;
    isStreaming = false;
    messageId;
    timestamp;
    thinkingVisibilityOverrides = new Map();
    constructor(message, hideThinkingBlock = false, markdownTheme = getMarkdownTheme(), hiddenThinkingLabel = "Thinking...", outputPad = 1, markdownTransformers = [], messageId, timestamp) {
        super();
        this.hideThinkingBlock = hideThinkingBlock;
        this.markdownTheme = markdownTheme;
        this.hiddenThinkingLabel = hiddenThinkingLabel;
        this.outputPad = outputPad;
        this.markdownTransformers = markdownTransformers;
        this.messageId = messageId;
        this.timestamp = timestamp;
        // Container for text/thinking content
        this.contentContainer = new Container();
        this.addChild(this.contentContainer);
        if (message) {
            this.updateContent(message);
        }
    }
    invalidate() {
        super.invalidate();
        if (this.lastMessage) {
            this.updateContent(this.lastMessage);
        }
    }
    setHideThinkingBlock(hide) {
        this.hideThinkingBlock = hide;
        this.thinkingVisibilityOverrides.clear();
        if (this.lastMessage) {
            this.updateContent(this.lastMessage);
        }
    }
    setHiddenThinkingLabel(label) {
        this.hiddenThinkingLabel = label;
        if (this.lastMessage) {
            this.updateContent(this.lastMessage);
        }
    }
    setOutputPad(padding) {
        this.outputPad = padding;
        if (this.lastMessage) {
            this.updateContent(this.lastMessage);
        }
    }
    render(width) {
        const lines = super.render(width);
        if (this.hasToolCalls || lines.length === 0) {
            return lines;
        }
        lines[0] = OSC133_ZONE_START + lines[0];
        lines[lines.length - 1] = OSC133_ZONE_END + OSC133_ZONE_FINAL + lines[lines.length - 1];
        return lines;
    }
    updateContent(message, isStreaming = this.isStreaming) {
        this.lastMessage = message;
        this.isStreaming = isStreaming;
        // Clear content container
        this.contentContainer.clear();
        // Spacers are added lazily, right before the next visible content block. This puts a blank
        // line before the first visible block and after each visible thinking run that is followed
        // by more visible content, while hidden thinking runs without a label contribute nothing.
        let needsSpacer = true;
        const addVisibleChild = (child) => {
            if (needsSpacer) {
                this.contentContainer.addChild(new Spacer(1));
                needsSpacer = false;
            }
            this.contentContainer.addChild(child);
        };
        // Render content in order
        let thinkingRunIndex = 0;
        for (let i = 0; i < message.content.length; i++) {
            const content = message.content[i];
            if (content.type === "text" && content.text.trim()) {
                // Assistant text messages with no background - trim the text
                // Set paddingY=0 to avoid extra spacing before tool executions
                addVisibleChild(new Markdown(content.text.trim(), this.outputPad, 0, this.markdownTheme, undefined, {
                    transform: createMarkdownTransform("assistant", this.isStreaming, this.markdownTransformers, this.messageId, this.timestamp),
                }));
            }
            else if (content.type === "thinking") {
                const thinkingBlocks = [];
                for (; i < message.content.length; i++) {
                    const thinkingContent = message.content[i];
                    if (thinkingContent.type !== "thinking") {
                        break;
                    }
                    const thinking = thinkingContent.thinking.trim();
                    if (thinking) {
                        thinkingBlocks.push(thinking);
                    }
                }
                i--;
                if (thinkingBlocks.length === 0) {
                    continue;
                }
                const runIndex = thinkingRunIndex++;
                const hidden = this.thinkingVisibilityOverrides.get(runIndex) ?? this.hideThinkingBlock;
                // A hidden run with an empty label renders nothing, including no spacing.
                if (hidden && !this.hiddenThinkingLabel.trim()) {
                    continue;
                }
                const thinkingComponent = hidden
                    ? new Text(theme.italic(theme.fg("thinkingText", this.hiddenThinkingLabel)), this.outputPad, 0)
                    : new Markdown(thinkingBlocks.join("\n\n"), this.outputPad, 0, this.markdownTheme, {
                        color: (text) => theme.fg("thinkingText", text),
                        italic: true,
                    }, {
                        transform: createMarkdownTransform("assistant-thinking", this.isStreaming, this.markdownTransformers, this.messageId, this.timestamp),
                    });
                addVisibleChild(new MouseRegion(thinkingComponent, (event) => {
                    if (event.type !== "click" || event.button !== "left")
                        return undefined;
                    this.thinkingVisibilityOverrides.set(runIndex, !hidden);
                    if (this.lastMessage)
                        this.updateContent(this.lastMessage);
                    return { handled: true };
                }));
                // Add spacing only when another visible assistant content block follows.
                // This avoids a superfluous blank line before separately-rendered tool execution blocks.
                needsSpacer = true;
            }
        }
        // Check if incomplete/failed - show after partial content.
        // For aborted/error tool calls, tool execution components show the error.
        // Length stops can happen before a tool call is complete, so surface them here too.
        const hasToolCalls = message.content.some((c) => c.type === "toolCall");
        this.hasToolCalls = hasToolCalls;
        if (message.stopReason === "length") {
            this.contentContainer.addChild(new Spacer(1));
            this.contentContainer.addChild(new Text(theme.fg("error", "Response was truncated before completion."), this.outputPad, 0));
        }
        else if (!hasToolCalls) {
            if (message.stopReason === "aborted") {
                const abortMessage = message.errorMessage && message.errorMessage !== "Request was aborted"
                    ? message.errorMessage
                    : "Operation aborted";
                this.contentContainer.addChild(new Spacer(1));
                this.contentContainer.addChild(new Text(theme.fg("error", abortMessage), this.outputPad, 0));
            }
            else if (message.stopReason === "error") {
                const errorMsg = message.errorMessage || "Unknown error";
                this.contentContainer.addChild(new Spacer(1));
                this.contentContainer.addChild(new Text(theme.fg("error", `Error: ${errorMsg}`), this.outputPad, 0));
            }
        }
    }
}
//# sourceMappingURL=assistant-message.js.map