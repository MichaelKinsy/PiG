import { dispatchMouseToChildLines, renderChildLines, } from "../tui.js";
import { flattenLines, visibleWidth } from "../utils.js";
/**
 * Box component - a container that applies padding and background to all children
 */
export class Box {
    children = [];
    paddingX;
    paddingY;
    bgFn;
    // Cache for rendered output
    cache;
    childLines;
    constructor(paddingX = 1, paddingY = 1, bgFn) {
        this.paddingX = paddingX;
        this.paddingY = paddingY;
        this.bgFn = bgFn;
    }
    addChild(component) {
        this.children.push(component);
        this.invalidateCache();
    }
    removeChild(component) {
        const index = this.children.indexOf(component);
        if (index !== -1) {
            this.children.splice(index, 1);
            this.invalidateCache();
        }
    }
    clear() {
        this.children = [];
        this.invalidateCache();
    }
    setBgFn(bgFn) {
        this.bgFn = bgFn;
        // Don't invalidate here - we'll detect bgFn changes by sampling output
    }
    setPaddingX(paddingX) {
        this.paddingX = paddingX;
        this.invalidateCache();
    }
    invalidateCache() {
        this.cache = undefined;
        this.childLines = undefined;
    }
    invalidate() {
        this.invalidateCache();
        for (const child of this.children) {
            child.invalidate?.();
        }
    }
    handleMouse(event) {
        const contentWidth = Math.max(1, event.width - this.paddingX * 2);
        const contentY = event.y - this.paddingY;
        const contentX = event.x - this.paddingX;
        if (contentY < 0 || contentX < 0 || contentX >= contentWidth)
            return undefined;
        const layout = this.childLines?.width === contentWidth
            ? this.childLines
            : renderChildLines(this.children, contentWidth, undefined);
        return dispatchMouseToChildLines(layout, { ...event, x: contentX, y: contentY, width: contentWidth });
    }
    render(width) {
        if (this.children.length === 0) {
            return [];
        }
        const contentWidth = Math.max(1, width - this.paddingX * 2);
        const leftPad = " ".repeat(this.paddingX);
        // Keep the child lines unpadded: unchanged children keep their line array, so the cache check
        // below is one identity comparison. Padding here would create new strings every frame.
        this.childLines = renderChildLines(this.children, contentWidth, this.childLines);
        const childLines = this.childLines.lines;
        if (childLines.length === 0) {
            return [];
        }
        // Check if bgFn output changed by sampling
        const bgSample = this.bgFn ? this.bgFn("test") : undefined;
        // Check cache validity
        const cache = this.cache;
        if (cache?.childLines === childLines && cache.width === width && cache.bgSample === bgSample) {
            return cache.lines;
        }
        // Apply background and padding
        const result = [];
        // Top padding
        for (let i = 0; i < this.paddingY; i++) {
            result.push(this.applyBg("", width));
        }
        // Content
        for (const line of childLines) {
            result.push(this.applyBg(leftPad + line, width));
        }
        // Bottom padding
        for (let i = 0; i < this.paddingY; i++) {
            result.push(this.applyBg("", width));
        }
        // Update cache
        flattenLines(result);
        this.cache = { childLines, width, bgSample, lines: result };
        return result;
    }
    applyBg(line, width) {
        const visLen = visibleWidth(line);
        const padNeeded = Math.max(0, width - visLen);
        const padded = line + " ".repeat(padNeeded);
        // Already padded to width, so apply the background directly instead of measuring the line again.
        return this.bgFn ? this.bgFn(padded) : padded;
    }
}
//# sourceMappingURL=box.js.map