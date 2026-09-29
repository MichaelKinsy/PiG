import { Box, Container, Spacer, Text } from "../../../../../pi-tui.mjs";
import { theme } from "../theme/theme.js";
/**
 * Component that renders a custom session entry from extensions.
 * The host owns transcript spacing; renderer output should provide only its content.
 */
export class CustomEntryComponent extends Container {
    entry;
    renderer;
    customComponent;
    _expanded = false;
    constructor(entry, renderer) {
        super();
        this.entry = entry;
        this.renderer = renderer;
        this.rebuild();
    }
    hasContent() {
        return this.customComponent !== undefined;
    }
    setExpanded(expanded) {
        if (this._expanded !== expanded) {
            this._expanded = expanded;
            this.rebuild();
        }
    }
    invalidate() {
        super.invalidate();
        this.rebuild();
    }
    rebuild() {
        this.clear();
        this.customComponent = undefined;
        let component;
        try {
            component = this.renderer(this.entry, { expanded: this._expanded }, theme);
        }
        catch (error) {
            const message = error instanceof Error ? error.message : String(error);
            const box = new Box(1, 1, (text) => theme.bg("customMessageBg", text));
            box.addChild(new Text(theme.fg("error", `[${this.entry.customType}] renderer failed: ${message}`), 0, 0));
            component = box;
        }
        if (!component) {
            return;
        }
        this.customComponent = component;
        this.addChild(new Spacer(1));
        this.addChild(component);
    }
}
//# sourceMappingURL=custom-entry.js.map