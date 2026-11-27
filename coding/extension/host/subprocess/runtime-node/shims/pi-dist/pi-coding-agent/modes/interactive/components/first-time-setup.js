import { Container, getKeybindings, Spacer, Text } from "../../../../../pi-tui.mjs";
import { APP_NAME } from "../../../config.js";
import { theme } from "../theme/theme.js";
import { DynamicBorder } from "./dynamic-border.js";
import { keyHint, rawKeyHint } from "./keybinding-hints.js";
import { piLogoLines, piWordmark, supportsPiLogo } from "./pi-logo.js";
/** The question the dialog asks. */
export const FIRST_TIME_SETUP_QUESTION = "Opt-in to anonymous usage data sharing?";
const ANALYTICS_OPTIONS = [
    { value: true, label: "Share anonymous usage data" },
    { value: false, label: "Don't share" },
];
/** First-time setup dialog: analytics opt-in. */
export class FirstTimeSetupComponent extends Container {
    analyticsIndex = 0;
    options;
    constructor(options) {
        super();
        this.options = options;
        this.update();
    }
    /** Rebuild on theme changes, e.g. when the system theme receives the terminal's colors. */
    invalidate() {
        this.update();
        super.invalidate();
    }
    update() {
        this.clear();
        this.addChild(new DynamicBorder());
        this.addChild(new Spacer(1));
        const logo = supportsPiLogo() ? piLogoLines().join("\n") : piWordmark();
        this.addChild(new Text(logo, 1, 0));
        this.addChild(new Spacer(1));
        this.addChild(new Text(theme.fg("accent", theme.bold(`Welcome to ${APP_NAME}, the minimal coding agent.`)), 1, 0));
        this.addChild(new Spacer(1));
        this.addChild(new Text(theme.fg("text", FIRST_TIME_SETUP_QUESTION), 1, 0));
        this.addChild(new Text(theme.fg("muted", "Opting in stores a tracking identifier in settings.json and enables anonymous\nusage analytics. This helps us to better debug, reproduce, and resolve issues\nand bugs within Pi. You can observe what is shared using /privacy and make\nchanges anytime in settings.json."), 1, 0));
        this.addChild(new Spacer(1));
        this.addOptionList(ANALYTICS_OPTIONS.map((option) => option.label), this.analyticsIndex);
        this.addChild(new Spacer(1));
        this.addChild(new Text(rawKeyHint("↑↓", "navigate") +
            "  " +
            keyHint("tui.select.confirm", "finish") +
            "  " +
            keyHint("tui.select.cancel", "skip setup"), 1, 0));
        this.addChild(new Spacer(1));
        this.addChild(new DynamicBorder());
    }
    addOptionList(labels, selectedIndex) {
        for (let i = 0; i < labels.length; i++) {
            const isSelected = i === selectedIndex;
            const prefix = isSelected ? theme.fg("accent", "→ ") : "  ";
            const label = isSelected ? theme.fg("accent", labels[i]) : theme.fg("text", labels[i]);
            this.addChild(new Text(`${prefix}${label}`, 1, 0));
        }
    }
    moveSelection(delta) {
        this.analyticsIndex = Math.max(0, Math.min(ANALYTICS_OPTIONS.length - 1, this.analyticsIndex + delta));
        this.update();
    }
    handleInput(keyData) {
        const kb = getKeybindings();
        if (kb.matches(keyData, "tui.select.up") || keyData === "k") {
            this.moveSelection(-1);
        }
        else if (kb.matches(keyData, "tui.select.down") || keyData === "j") {
            this.moveSelection(1);
        }
        else if (kb.matches(keyData, "tui.select.confirm") || keyData === "\n") {
            this.options.onSubmit({
                shareAnalytics: ANALYTICS_OPTIONS[this.analyticsIndex].value,
            });
        }
        else if (kb.matches(keyData, "tui.select.cancel")) {
            this.options.onCancel();
        }
    }
}
//# sourceMappingURL=first-time-setup.js.map