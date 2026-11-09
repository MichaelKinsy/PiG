/**
 * The `/login` menu with the animated "Sign in with Radius" option. Internal to the interactive mode.
 */
import { Text } from "../../../../../pi-tui.mjs";
import { theme } from "../theme/theme.js";
import { ExtensionSelectorComponent } from "./extension-selector.js";
import { radiusShimmer, startBrandAnimation } from "./provider-brand.js";
/**
 * Swaps the line `ExtensionSelectorComponent` draws for the selected Radius option with the animated one. When the
 * selector's row style changes or the label wraps, the line no longer matches and the option renders normally.
 */
class RadiusLoginMenuComponent extends ExtensionSelectorComponent {
    radiusOption;
    animationStart = performance.now();
    stopAnimationTimer;
    animating = false;
    constructor(tui, title, options, radiusOption, onSelect, onCancel) {
        super(title, options, (option) => {
            this.stopAnimation();
            onSelect(option);
        }, () => {
            this.stopAnimation();
            onCancel();
        });
        this.radiusOption = radiusOption;
        this.stopAnimationTimer = startBrandAnimation(tui, () => this.animating);
    }
    render(width) {
        const lines = super.render(width);
        const { label, text } = this.radiusOption;
        const selectedLine = new Text(theme.fg("accent", "→ ") + theme.fg("accent", label), 1, 0).render(width)[0];
        const index = selectedLine === undefined ? -1 : lines.indexOf(selectedLine);
        this.animating = index >= 0;
        if (this.animating) {
            const shimmer = radiusShimmer(text, performance.now() - this.animationStart);
            const animatedLine = theme.fg("accent", "→ ") + shimmer + label.slice(text.length);
            lines[index] = new Text(animatedLine, 1, 0).render(width)[0] ?? "";
        }
        return lines;
    }
    stopAnimation() {
        this.stopAnimationTimer();
        this.animating = false;
    }
    dispose() {
        this.stopAnimation();
        super.dispose();
    }
}
/** Top-level `/login` selector whose Radius option shimmers in the Radius logo colors while it is selected. */
export function createLoginMenuSelector(tui, title, options, radiusOption, onSelect, onCancel) {
    return new RadiusLoginMenuComponent(tui, title, options, radiusOption, onSelect, onCancel);
}
//# sourceMappingURL=radius-login-selector.js.map