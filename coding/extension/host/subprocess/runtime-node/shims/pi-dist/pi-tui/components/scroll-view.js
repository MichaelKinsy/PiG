import { LAYOUT_NODE } from "../layout-node.js";
import { Container } from "../tui.js";
export class ScrollView extends Container {
    child;
    followEnd;
    primary;
    overscroll;
    scrollbarTrackStyle;
    scrollbarThumbStyle;
    currentScrollbar;
    scrollbarHideDelayMs;
    currentScrollTop = 0;
    contentHeight = 0;
    currentViewportHeight = 0;
    followingEnd;
    followSuppressedAtEnd = false;
    requestRenderCallback;
    transientScrollbarVisible = false;
    scrollbarActive = false;
    scrollbarHideTimer;
    constructor(component, options = {}) {
        super();
        if (options.axis !== undefined && options.axis !== "vertical") {
            throw new Error(`Unsupported ScrollView axis: ${options.axis}`);
        }
        this.child = component;
        this.children.push(component);
        this.followEnd = (options.follow ?? "none") === "end";
        this.followingEnd = this.followEnd;
        this.primary = options.primary ?? false;
        this.overscroll = options.overscroll ?? "chain";
        this.currentScrollbar = options.scrollbar ?? "hidden";
        this.scrollbarTrackStyle = options.scrollbarTrackStyle ?? ((text) => `\x1b[90m${text}\x1b[39m`);
        this.scrollbarThumbStyle = options.scrollbarThumbStyle ?? ((text) => `\x1b[37m${text}\x1b[39m`);
        this.scrollbarHideDelayMs = Math.max(0, Math.floor(options.scrollbarHideDelayMs ?? 1000));
    }
    get scrollTop() {
        return this.currentScrollTop;
    }
    get isFollowingEnd() {
        return this.followingEnd;
    }
    get viewportHeight() {
        return this.currentViewportHeight;
    }
    get scrollbar() {
        return this.currentScrollbar;
    }
    get isScrollbarVisible() {
        if (this.scrollbar === "always")
            return this.currentViewportHeight > 0;
        return (this.scrollbar === "auto" && this.contentHeight > this.currentViewportHeight && this.transientScrollbarVisible);
    }
    get isScrollbarActive() {
        return this.scrollbarActive;
    }
    setScrollbar(scrollbar) {
        if (scrollbar === this.currentScrollbar)
            return;
        this.currentScrollbar = scrollbar;
        if (scrollbar !== "auto")
            this.hideTransientScrollbar();
        else if (this.scrollbarActive)
            this.markScrollbarActivity();
        this.requestRenderCallback?.();
    }
    getContentWidth(width) {
        return this.scrollbar === "always" && width > 1 ? width - 1 : width;
    }
    markScrollbarActivity() {
        if (this.scrollbar !== "auto" || this.contentHeight <= this.currentViewportHeight)
            return;
        this.transientScrollbarVisible = true;
        if (this.scrollbarHideTimer) {
            clearTimeout(this.scrollbarHideTimer);
            this.scrollbarHideTimer = undefined;
        }
        if (this.scrollbarActive)
            return;
        this.scrollbarHideTimer = setTimeout(() => {
            this.scrollbarHideTimer = undefined;
            this.transientScrollbarVisible = false;
            this.requestRenderCallback?.();
        }, this.scrollbarHideDelayMs);
        this.scrollbarHideTimer.unref();
    }
    hideTransientScrollbar() {
        this.transientScrollbarVisible = false;
        if (!this.scrollbarHideTimer)
            return;
        clearTimeout(this.scrollbarHideTimer);
        this.scrollbarHideTimer = undefined;
    }
    setScrollbarActive(active) {
        if (active === this.scrollbarActive)
            return;
        this.scrollbarActive = active;
        this.markScrollbarActivity();
        this.requestRenderCallback?.();
    }
    scrollTo(scrollTop, options = {}) {
        const requested = Number.isFinite(scrollTop) ? Math.trunc(scrollTop) : this.currentScrollTop;
        const maxScrollTop = Math.max(0, this.contentHeight - this.currentViewportHeight);
        const next = Math.max(0, Math.min(maxScrollTop, requested));
        const nextFollowSuppressedAtEnd = options.disableFollow === true && next === maxScrollTop;
        const nextFollowingEnd = !nextFollowSuppressedAtEnd && this.followEnd && next === maxScrollTop;
        if (next === this.currentScrollTop &&
            nextFollowingEnd === this.followingEnd &&
            nextFollowSuppressedAtEnd === this.followSuppressedAtEnd) {
            return;
        }
        const moved = next !== this.currentScrollTop;
        this.currentScrollTop = next;
        this.followingEnd = nextFollowingEnd;
        this.followSuppressedAtEnd = nextFollowSuppressedAtEnd;
        if (moved)
            this.markScrollbarActivity();
        this.requestRenderCallback?.();
    }
    scrollBy(lines) {
        const requested = Number.isFinite(lines) ? Math.trunc(lines) : 0;
        if (requested === 0)
            return 0;
        const maxScrollTop = Math.max(0, this.contentHeight - this.currentViewportHeight);
        const start = this.followingEnd ? maxScrollTop : this.currentScrollTop;
        const next = Math.max(0, Math.min(maxScrollTop, start + requested));
        const moved = next - start;
        const wasFollowingEnd = this.followingEnd;
        this.currentScrollTop = next;
        this.followingEnd = this.followEnd && next === maxScrollTop;
        this.followSuppressedAtEnd = false;
        if (moved !== 0)
            this.markScrollbarActivity();
        if (moved !== 0 || this.followingEnd !== wasFollowingEnd)
            this.requestRenderCallback?.();
        return requested - moved;
    }
    scrollToStart() {
        const changed = this.currentScrollTop !== 0 ||
            this.followingEnd !== (this.followEnd && this.contentHeight <= this.currentViewportHeight);
        this.currentScrollTop = 0;
        this.followingEnd = this.followEnd && this.contentHeight <= this.currentViewportHeight;
        this.followSuppressedAtEnd = false;
        if (changed) {
            this.markScrollbarActivity();
            this.requestRenderCallback?.();
        }
    }
    scrollToEnd() {
        const next = Math.max(0, this.contentHeight - this.currentViewportHeight);
        const changed = this.currentScrollTop !== next || this.followingEnd !== this.followEnd;
        this.currentScrollTop = next;
        this.followingEnd = this.followEnd;
        this.followSuppressedAtEnd = false;
        if (changed) {
            this.markScrollbarActivity();
            this.requestRenderCallback?.();
        }
    }
    updateLayout(contentHeight, viewportHeight, requestRender) {
        this.contentHeight = Math.max(0, Math.floor(contentHeight));
        this.currentViewportHeight = Math.max(0, Math.floor(viewportHeight));
        this.requestRenderCallback = requestRender;
        const maxScrollTop = Math.max(0, this.contentHeight - this.currentViewportHeight);
        if (this.followingEnd)
            this.currentScrollTop = maxScrollTop;
        else
            this.currentScrollTop = Math.max(0, Math.min(this.currentScrollTop, maxScrollTop));
        if (this.currentScrollTop < maxScrollTop)
            this.followSuppressedAtEnd = false;
        if (this.followEnd && this.currentScrollTop === maxScrollTop && !this.followSuppressedAtEnd) {
            this.followingEnd = true;
        }
        if (this.contentHeight <= this.currentViewportHeight)
            this.hideTransientScrollbar();
    }
    addChild(_component) {
        throw new Error("ScrollView has exactly one child");
    }
    removeChild(_component) {
        throw new Error("ScrollView child cannot be removed");
    }
    clear() {
        throw new Error("ScrollView child cannot be cleared");
    }
    render(width) {
        const contentWidth = this.getContentWidth(width);
        const lines = this.child.render(contentWidth);
        return contentWidth === width ? lines : lines.map((line) => `${line} `);
    }
    [LAYOUT_NODE]() {
        return { type: "scroll", component: this.child, state: this };
    }
}
//# sourceMappingURL=scroll-view.js.map