import { Text } from "../../../../../pi-tui.mjs";
/**
 * Text whose content applies theme colors. Plain `Text` keeps the colors its string was built with, so
 * a theme change, or the system theme receiving the terminal's colors, would leave it stale. This
 * rebuilds the string from `build` after every invalidation, which the UI performs on theme changes.
 *
 * `build` must return the same content each time, apart from colors. Snapshot changing data before
 * creating the component, or call `invalidate()` after changing state that `build` reads.
 */
export class ThemedText extends Text {
    build;
    stale = true;
    constructor(build, paddingX = 1, paddingY = 1) {
        super("", paddingX, paddingY);
        this.build = build;
    }
    invalidate() {
        super.invalidate();
        this.stale = true;
    }
    render(width) {
        if (this.stale) {
            this.stale = false;
            this.setText(this.build());
        }
        return super.render(width);
    }
}
//# sourceMappingURL=themed-text.js.map