import { dispatchMouseEvent, } from "../tui.js";
/** Adds mouse handling to an existing component without changing its rendering. */
export class MouseRegion {
    child;
    onMouse;
    constructor(child, onMouse) {
        this.child = child;
        this.onMouse = onMouse;
    }
    render(width) {
        return this.child.render(width);
    }
    handleMouse(event) {
        const childResult = dispatchMouseEvent(this.child, event);
        return childResult ?? this.onMouse(event);
    }
    invalidate() {
        this.child.invalidate();
    }
}
//# sourceMappingURL=mouse-region.js.map