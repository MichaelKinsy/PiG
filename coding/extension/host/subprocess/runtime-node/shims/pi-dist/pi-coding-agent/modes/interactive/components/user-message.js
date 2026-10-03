import { Container, Markdown } from "../../../../../pi-tui.mjs";
import { getMarkdownTheme, theme } from "../theme/theme.js";
import { createMarkdownTransform } from "./markdown-transform.js";
const OSC133_ZONE_START = "\x1b]133;A\x07";
const OSC133_ZONE_END = "\x1b]133;B\x07";
const OSC133_ZONE_FINAL = "\x1b]133;C\x07";
/**
 * Component that renders a user message
 */
export class UserMessageComponent extends Container {
    text;
    markdownTheme;
    outputPad;
    markdownTransformers;
    constructor(text, markdownTheme = getMarkdownTheme(), outputPad = 1, markdownTransformers = []) {
        super();
        this.text = text;
        this.markdownTheme = markdownTheme;
        this.outputPad = outputPad;
        this.markdownTransformers = markdownTransformers;
        this.rebuild();
    }
    setOutputPad(padding) {
        this.outputPad = padding;
        this.rebuild();
    }
    rebuild() {
        this.clear();
        // The Markdown pads and colors its own background: a Box around it would keep a second full-width copy of every
        // line, with identical output.
        this.addChild(new Markdown(this.text, this.outputPad, 1, this.markdownTheme, {
            color: (content) => theme.fg("userMessageText", content),
            bgColor: (content) => theme.bg("userMessageBg", content),
        }, {
            preserveOrderedListMarkers: true,
            preserveBackslashEscapes: true,
            transform: createMarkdownTransform("user", false, this.markdownTransformers),
        }));
    }
    render(width) {
        const lines = super.render(width);
        if (lines.length === 0) {
            return lines;
        }
        lines[0] = OSC133_ZONE_START + lines[0];
        lines[lines.length - 1] = OSC133_ZONE_END + OSC133_ZONE_FINAL + lines[lines.length - 1];
        return lines;
    }
}
//# sourceMappingURL=user-message.js.map