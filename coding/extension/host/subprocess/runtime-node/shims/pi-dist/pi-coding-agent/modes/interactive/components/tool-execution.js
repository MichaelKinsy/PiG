import { Box, Container, getCapabilities, Image, MouseRegion, Spacer, Text, } from "../../../../../pi-tui.mjs";
import { formatToolCallWithArgs, getTextOutput as getRenderedTextOutput } from "../../../core/tools/render-utils.js";
import { ensurePngTranscoder } from "../../../utils/image-convert.js";
import { theme } from "../theme/theme.js";
import { keyHint } from "./keybinding-hints.js";
const FALLBACK_PREVIEW_LINES = 10;
export class ToolExecutionComponent extends Container {
    contentBox;
    contentText;
    contentTextRegion;
    selfRenderContainer;
    selfRenderHeight = 0;
    callRendererComponent;
    resultRendererComponent;
    rendererState = {};
    imageComponents = [];
    /** Inputs of imageComponents, so updateDisplay can reuse images and keep their converted PNG data. */
    imageSources = [];
    imageSpacers = [];
    toolName;
    toolCallId;
    args;
    expanded = false;
    showImages;
    imageWidthCells;
    isPartial = true;
    toolDefinition;
    ui;
    cwd;
    executionStarted = false;
    argsComplete = false;
    result;
    hideComponent = false;
    constructor(toolName, toolCallId, args, options = {}, toolDefinition, ui, cwd) {
        super();
        this.toolName = toolName;
        this.toolCallId = toolCallId;
        this.args = args;
        this.toolDefinition = toolDefinition;
        this.showImages = options.showImages ?? true;
        this.imageWidthCells = options.imageWidthCells ?? 60;
        this.ui = ui;
        this.cwd = cwd;
        this.addChild(new Spacer(1));
        // Always create all shell variants. contentBox is used for default renderer-based composition.
        // selfRenderContainer is used when the tool renders its own framing.
        // contentText is reserved for generic fallback rendering when no tool definition exists.
        this.contentBox = new Box(1, 1, (text) => theme.bg("toolPendingBg", text));
        this.contentText = new Text("", 1, 1, (text) => theme.bg("toolPendingBg", text));
        this.contentTextRegion = this.createResultRegion(this.contentText);
        this.selfRenderContainer = new Container();
        if (this.hasRendererDefinition()) {
            this.addChild(this.getRenderShell() === "self" ? this.selfRenderContainer : this.contentBox);
        }
        else {
            this.addChild(this.contentTextRegion);
        }
        this.updateDisplay();
    }
    getCallRenderer() {
        return this.toolDefinition?.renderCall;
    }
    getResultRenderer() {
        return this.toolDefinition?.renderResult;
    }
    hasRendererDefinition() {
        return this.toolDefinition !== undefined;
    }
    getRenderShell() {
        return this.toolDefinition?.renderShell ?? "default";
    }
    getRenderContext(lastComponent) {
        return {
            args: this.args,
            toolCallId: this.toolCallId,
            invalidate: () => {
                this.invalidate();
                this.ui.requestRender();
            },
            lastComponent,
            state: this.rendererState,
            cwd: this.cwd,
            executionStarted: this.executionStarted,
            argsComplete: this.argsComplete,
            isPartial: this.isPartial,
            expanded: this.expanded,
            showImages: this.showImages,
            isError: this.result?.isError ?? false,
        };
    }
    createCallFallback() {
        return new Text(formatToolCallWithArgs(this.toolName, this.args, theme, this.expanded), 0, 0);
    }
    createResultFallback() {
        const output = this.getTextOutput();
        if (!output) {
            return undefined;
        }
        const lines = output.split("\n");
        const displayLines = this.expanded ? lines : lines.slice(0, FALLBACK_PREVIEW_LINES);
        const remaining = lines.length - displayLines.length;
        let text = displayLines.map((line) => theme.fg("toolOutput", line)).join("\n");
        if (remaining > 0) {
            text += `${theme.fg("muted", `\n... (${remaining} more lines,`)} ${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`;
        }
        return new Text(text, 0, 0);
    }
    createResultRegion(component) {
        return new MouseRegion(component, (event) => {
            if (!this.result || event.type !== "click" || event.button !== "left")
                return undefined;
            this.setExpanded(!this.expanded);
            return { handled: true };
        });
    }
    updateArgs(args) {
        this.args = args;
        this.updateDisplay();
    }
    markExecutionStarted() {
        this.executionStarted = true;
        this.updateDisplay();
        this.ui.requestRender();
    }
    setArgsComplete() {
        this.argsComplete = true;
        this.updateDisplay();
        this.ui.requestRender();
    }
    updateResult(result, isPartial = false) {
        this.result = result;
        this.isPartial = isPartial;
        this.updateDisplay();
    }
    setExpanded(expanded) {
        this.expanded = expanded;
        this.updateDisplay();
    }
    setShowImages(show) {
        this.showImages = show;
        this.updateDisplay();
    }
    setImageWidthCells(width) {
        this.imageWidthCells = Math.max(1, Math.floor(width));
        this.updateDisplay();
    }
    invalidate() {
        super.invalidate();
        this.updateDisplay();
    }
    render(width) {
        if (this.hideComponent) {
            return [];
        }
        if (this.hasRendererDefinition() && this.getRenderShell() === "self") {
            const contentLines = this.selfRenderContainer.render(width);
            this.selfRenderHeight = contentLines.length;
            if (contentLines.length === 0 && this.imageComponents.length === 0) {
                return [];
            }
            const lines = [];
            if (contentLines.length > 0) {
                lines.push("");
                lines.push(...contentLines);
            }
            for (let i = 0; i < this.imageComponents.length; i++) {
                const spacer = this.imageSpacers[i];
                if (spacer) {
                    lines.push(...spacer.render(width));
                }
                const imageComponent = this.imageComponents[i];
                if (imageComponent) {
                    lines.push(...imageComponent.render(width));
                }
            }
            return lines;
        }
        return super.render(width);
    }
    handleMouse(event) {
        if (!this.hasRendererDefinition() || this.getRenderShell() !== "self")
            return super.handleMouse(event);
        if (event.y <= 0 || event.y > this.selfRenderHeight)
            return undefined;
        return this.selfRenderContainer.handleMouse({
            ...event,
            y: event.y - 1,
            height: this.selfRenderHeight,
        });
    }
    updateDisplay() {
        const bgFn = this.isPartial
            ? (text) => theme.bg("toolPendingBg", text)
            : this.result?.isError
                ? (text) => theme.bg("toolErrorBg", text)
                : (text) => theme.bg("toolSuccessBg", text);
        let hasContent = false;
        this.hideComponent = false;
        if (this.hasRendererDefinition()) {
            const renderContainer = this.getRenderShell() === "self" ? this.selfRenderContainer : this.contentBox;
            if (renderContainer instanceof Box) {
                renderContainer.setBgFn(bgFn);
            }
            renderContainer.clear();
            const callRenderer = this.getCallRenderer();
            if (!callRenderer) {
                renderContainer.addChild(this.createResultRegion(this.createCallFallback()));
                hasContent = true;
            }
            else {
                try {
                    const component = callRenderer(this.args, theme, this.getRenderContext(this.callRendererComponent));
                    this.callRendererComponent = component;
                    renderContainer.addChild(this.createResultRegion(component));
                    hasContent = true;
                }
                catch {
                    this.callRendererComponent = undefined;
                    renderContainer.addChild(this.createResultRegion(this.createCallFallback()));
                    hasContent = true;
                }
            }
            if (this.result) {
                const resultRenderer = this.getResultRenderer();
                if (!resultRenderer) {
                    const component = this.createResultFallback();
                    if (component) {
                        renderContainer.addChild(this.createResultRegion(component));
                        hasContent = true;
                    }
                }
                else {
                    try {
                        const component = resultRenderer({ content: this.result.content, details: this.result.details }, { expanded: this.expanded, isPartial: this.isPartial }, theme, this.getRenderContext(this.resultRendererComponent));
                        this.resultRendererComponent = component;
                        renderContainer.addChild(this.createResultRegion(component));
                        hasContent = true;
                    }
                    catch {
                        this.resultRendererComponent = undefined;
                        const component = this.createResultFallback();
                        if (component) {
                            renderContainer.addChild(this.createResultRegion(component));
                            hasContent = true;
                        }
                    }
                }
            }
        }
        else {
            this.contentText.setCustomBgFn(bgFn);
            this.contentText.setText(this.formatToolExecution());
            hasContent = true;
        }
        const previousImages = this.imageComponents;
        const previousSources = this.imageSources;
        for (const img of this.imageComponents) {
            this.removeChild(img);
        }
        this.imageComponents = [];
        this.imageSources = [];
        for (const spacer of this.imageSpacers) {
            this.removeChild(spacer);
        }
        this.imageSpacers = [];
        if (this.result) {
            const imageBlocks = this.result.content.filter((c) => c.type === "image");
            const caps = getCapabilities();
            for (const img of imageBlocks) {
                if (caps.images && this.showImages && img.data && img.mimeType) {
                    const spacer = new Spacer(1);
                    this.addChild(spacer);
                    this.imageSpacers.push(spacer);
                    const source = { data: img.data, mimeType: img.mimeType, widthCells: this.imageWidthCells };
                    const index = this.imageComponents.length;
                    const previous = previousSources[index];
                    const imageComponent = previous?.data === source.data &&
                        previous.mimeType === source.mimeType &&
                        previous.widthCells === source.widthCells
                        ? previousImages[index]
                        : new Image(source.data, source.mimeType, { fallbackColor: (s) => theme.fg("toolOutput", s) }, { maxWidthCells: source.widthCells });
                    if (source.mimeType !== "image/png") {
                        ensurePngTranscoder(() => {
                            this.invalidate();
                            this.ui.requestRender();
                        });
                    }
                    this.imageComponents.push(imageComponent);
                    this.imageSources.push(source);
                    this.addChild(imageComponent);
                }
            }
        }
        if (this.hasRendererDefinition() && !hasContent && this.imageComponents.length === 0) {
            this.hideComponent = true;
        }
    }
    getTextOutput() {
        return getRenderedTextOutput(this.result, this.showImages);
    }
    formatToolExecution() {
        let text = theme.fg("toolTitle", theme.bold(this.toolName));
        const content = JSON.stringify(this.args, null, 2);
        if (content) {
            text += `\n\n${content}`;
        }
        const output = this.getTextOutput();
        if (output) {
            text += `\n${output}`;
        }
        return text;
    }
}
//# sourceMappingURL=tool-execution.js.map