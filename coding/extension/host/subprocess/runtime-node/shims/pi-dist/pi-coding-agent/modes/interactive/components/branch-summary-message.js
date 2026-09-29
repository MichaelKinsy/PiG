import { Box, Container, Markdown, MouseRegion, Spacer, Text } from "../../../../../pi-tui.mjs";
import { getMarkdownTheme, theme } from "../theme/theme.js";
import { keyText } from "./keybinding-hints.js";
/**
 * Component that renders a branch summary message with collapsed/expanded state.
 * Uses same background color as custom messages for visual consistency.
 */
export class BranchSummaryMessageComponent extends Box {
    expanded = false;
    message;
    markdownTheme;
    constructor(message, markdownTheme = getMarkdownTheme()) {
        super(1, 1, (t) => theme.bg("customMessageBg", t));
        this.message = message;
        this.markdownTheme = markdownTheme;
        this.updateDisplay();
    }
    setExpanded(expanded) {
        this.expanded = expanded;
        this.updateDisplay();
    }
    invalidate() {
        super.invalidate();
        this.updateDisplay();
    }
    updateDisplay() {
        this.clear();
        const content = new Container();
        const label = theme.fg("customMessageLabel", `\x1b[1m[branch]\x1b[22m`);
        content.addChild(new Text(label, 0, 0));
        content.addChild(new Spacer(1));
        if (this.expanded) {
            const header = "**Branch Summary**\n\n";
            content.addChild(new Markdown(header + this.message.summary, 0, 0, this.markdownTheme, {
                color: (text) => theme.fg("customMessageText", text),
            }));
        }
        else {
            content.addChild(new Text(theme.fg("customMessageText", "Branch summary (") +
                theme.fg("dim", keyText("app.tools.expand")) +
                theme.fg("customMessageText", " to expand)"), 0, 0));
        }
        this.addChild(new MouseRegion(content, (event) => {
            if (event.type !== "click" || event.button !== "left")
                return undefined;
            this.setExpanded(!this.expanded);
            return { handled: true };
        }));
    }
}
//# sourceMappingURL=branch-summary-message.js.map