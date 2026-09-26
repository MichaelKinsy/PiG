import { LAYOUT_NODE } from "../layout-node.js";
import { Container } from "../tui.js";
function isStackEntry(child) {
    return !("render" in child);
}
function normalizeSize(value, fallback) {
    return value === undefined || !Number.isFinite(value) ? fallback : Math.max(0, Math.floor(value));
}
export class Stack extends Container {
    entries = [];
    gap;
    align;
    constructor(children = [], options = {}) {
        super();
        this.gap = normalizeSize(options.gap, 0);
        this.align = options.align ?? "stretch";
        for (const child of children) {
            if (isStackEntry(child))
                this.addChild(child.component, child);
            else
                this.addChild(child);
        }
    }
    addChild(component, options = {}) {
        super.addChild(component);
        this.entries.push({
            component,
            ...(options.basis === undefined ? {} : { basis: options.basis }),
            ...(options.grow === undefined ? {} : { grow: normalizeSize(options.grow, 0) }),
            ...(options.shrink === undefined ? {} : { shrink: normalizeSize(options.shrink, 1) }),
            ...(options.minSize === undefined ? {} : { minSize: normalizeSize(options.minSize, 0) }),
            ...(options.maxSize === undefined ? {} : { maxSize: normalizeSize(options.maxSize, Number.MAX_SAFE_INTEGER) }),
            ...(options.visible === undefined ? {} : { visible: options.visible }),
        });
    }
    removeChild(component) {
        super.removeChild(component);
        const index = this.entries.findIndex((entry) => entry.component === component);
        if (index !== -1)
            this.entries.splice(index, 1);
    }
    clear() {
        super.clear();
        this.entries.length = 0;
    }
    [LAYOUT_NODE]() {
        return {
            type: this.layoutType,
            entries: this.entries,
            gap: this.gap,
            align: this.align,
        };
    }
}
export function visibleStackEntries(entries, viewport) {
    return entries.filter((entry) => entry.visible?.(viewport) ?? true);
}
function clampSize(size, entry) {
    const min = Math.max(0, Math.floor(entry.minSize ?? 0));
    const max = Math.max(min, Math.floor(entry.maxSize ?? Number.MAX_SAFE_INTEGER));
    return Math.max(min, Math.min(max, Math.max(0, Math.floor(size))));
}
function distribute(sizes, entries, amount, mode) {
    let remaining = amount;
    while (remaining > 0) {
        const candidates = entries
            .map((entry, index) => ({ entry, index }))
            .filter(({ entry, index }) => {
            if (mode === "grow") {
                return (entry.grow ?? 0) > 0 && sizes[index] < (entry.maxSize ?? Number.MAX_SAFE_INTEGER);
            }
            return (entry.shrink ?? 1) > 0 && sizes[index] > (entry.minSize ?? 0);
        });
        if (candidates.length === 0)
            return;
        const totalWeight = candidates.reduce((sum, { entry, index }) => {
            return sum + (mode === "grow" ? (entry.grow ?? 0) : (entry.shrink ?? 1) * Math.max(1, sizes[index]));
        }, 0);
        let distributed = 0;
        for (const { entry, index } of candidates) {
            if (remaining <= 0)
                break;
            const weight = mode === "grow" ? (entry.grow ?? 0) : (entry.shrink ?? 1) * Math.max(1, sizes[index]);
            const proposed = Math.max(1, Math.floor((remaining * weight) / totalWeight));
            const capacity = mode === "grow"
                ? (entry.maxSize ?? Number.MAX_SAFE_INTEGER) - sizes[index]
                : sizes[index] - (entry.minSize ?? 0);
            const delta = Math.min(remaining, proposed, capacity);
            if (delta <= 0)
                continue;
            sizes[index] = sizes[index] + (mode === "grow" ? delta : -delta);
            remaining -= delta;
            distributed += delta;
        }
        if (distributed === 0)
            return;
    }
}
export function allocateStackSizes(entries, intrinsicSizes, availableSize, gap) {
    const sizes = entries.map((entry, index) => clampSize(entry.basis === undefined || entry.basis === "auto" ? (intrinsicSizes[index] ?? 0) : entry.basis, entry));
    if (availableSize === undefined)
        return sizes;
    const contentSize = Math.max(0, Math.floor(availableSize) - Math.max(0, entries.length - 1) * gap);
    const total = sizes.reduce((sum, size) => sum + size, 0);
    if (total < contentSize)
        distribute(sizes, entries, contentSize - total, "grow");
    else if (total > contentSize)
        distribute(sizes, entries, total - contentSize, "shrink");
    return sizes;
}
//# sourceMappingURL=stack.js.map