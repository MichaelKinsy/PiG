import { Container, hyperlink, Text } from "../../../../../pi-tui.mjs";
import { copyToClipboard } from "../../../utils/clipboard.js";
import { theme } from "../theme/theme.js";
import { keyHint } from "./keybinding-hints.js";
/**
 * A sign-in URL with a click hint and a copy hint. Hosts call `copy()` when `app.message.copy` is
 * pressed, since a long URL wraps and often cannot be selected or clicked as a whole (SSH, tmux).
 */
export class AuthUrlComponent extends Container {
    url;
    tui;
    hint;
    constructor(tui, url) {
        super();
        this.tui = tui;
        this.url = url;
        this.addChild(new Text(theme.fg("accent", hyperlink(url, url)), 1, 0));
        this.hint = new Text("", 1, 0);
        this.addChild(this.hint);
        this.setHint(keyHint("app.message.copy", "to copy"));
    }
    setHint(suffix) {
        const clickHint = process.platform === "darwin" ? "Cmd+click to open" : "Ctrl+click to open";
        this.hint.setText(`${theme.fg("dim", hyperlink(clickHint, this.url))} ${theme.fg("dim", "•")} ${suffix}`);
        this.tui.requestRender();
    }
    async copy() {
        try {
            await copyToClipboard(this.url);
            this.setHint(theme.fg("success", "Copied URL to clipboard"));
        }
        catch (error) {
            this.setHint(theme.fg("error", error instanceof Error ? error.message : String(error)));
        }
    }
}
//# sourceMappingURL=auth-url.js.map