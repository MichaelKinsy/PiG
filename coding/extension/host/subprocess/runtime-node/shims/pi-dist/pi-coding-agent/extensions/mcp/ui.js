/**
 * The `/mcp` manager view: menus that rebuild while servers connect, a read-only status screen, and
 * the sign-in screen that accepts a pasted redirect URL.
 */
import { Container, hyperlink, Input, SelectList, Spacer, Text, truncateToWidth, visibleWidth, } from "../../../../pi-tui.mjs";
import { DynamicBorder } from "../../modes/interactive/components/dynamic-border.js";
import { keyHint } from "../../modes/interactive/components/keybinding-hints.js";
import { getSelectListTheme } from "../../modes/interactive/theme/theme.js";
function frame(theme, title, body, footer) {
    const container = new Container();
    container.addChild(new DynamicBorder((text) => theme.fg("accent", text)));
    container.addChild(new Text(theme.fg("accent", theme.bold(title)), 1, 0));
    for (const child of body)
        container.addChild(child);
    if (footer) {
        container.addChild(new Spacer(1));
        container.addChild(new Text(theme.fg("dim", footer), 1, 0));
    }
    container.addChild(new DynamicBorder((text) => theme.fg("accent", text)));
    return container;
}
const MAX_VISIBLE_ITEMS = 12;
export class McpManagerView {
    tui;
    theme;
    keybindings;
    content;
    inputHandler;
    inputTarget;
    _focused = false;
    constructor(tui, theme, keybindings) {
        this.tui = tui;
        this.theme = theme;
        this.keybindings = keybindings;
        this.content = frame(theme, "MCP servers", [new Text(theme.fg("muted", "Loading…"), 1, 1)]);
    }
    get focused() {
        return this._focused;
    }
    set focused(value) {
        this._focused = value;
        if (this.inputTarget)
            this.inputTarget.focused = value;
    }
    setContent(content, inputHandler, inputTarget) {
        if (this.inputTarget)
            this.inputTarget.focused = false;
        this.content = content;
        this.inputHandler = inputHandler;
        this.inputTarget = inputTarget;
        if (this.inputTarget)
            this.inputTarget.focused = this._focused;
        this.tui.requestRender();
    }
    menu(build, subscribe) {
        return new Promise((resolve) => {
            let unsubscribe;
            let settled = false;
            const finish = (value) => {
                if (settled)
                    return;
                settled = true;
                unsubscribe?.();
                resolve(value);
            };
            let selected;
            const render = () => {
                const menu = build();
                const wanted = selected ?? menu.selected;
                const body = [];
                if (menu.details)
                    body.push(new Text(this.theme.fg("muted", menu.details), 1, 0));
                if (menu.error)
                    body.push(new Text(this.theme.fg("error", menu.error), 1, 0));
                body.push(new Spacer(1));
                const footer = `${keyHint("tui.select.confirm", menu.confirmLabel)} • ${keyHint("tui.select.cancel", menu.cancelLabel)}`;
                if (menu.items.length === 0) {
                    body.push(new Text(this.theme.fg("muted", menu.empty ?? "Nothing to show."), 1, 0));
                    this.setContent(frame(this.theme, menu.title, body, keyHint("tui.select.cancel", menu.cancelLabel)), (data) => {
                        if (this.keybindings.matches(data, "tui.select.cancel"))
                            finish(undefined);
                    });
                    return;
                }
                const list = new SelectList(menu.items, Math.min(menu.items.length, MAX_VISIBLE_ITEMS), getSelectListTheme());
                const index = menu.items.findIndex((item) => item.value === wanted);
                if (index !== -1)
                    list.setSelectedIndex(index);
                selected = list.getSelectedItem()?.value;
                list.onSelectionChange = (item) => {
                    selected = item.value;
                };
                list.onSelect = (item) => finish(item.value);
                list.onCancel = () => finish(undefined);
                body.push(list);
                this.setContent(frame(this.theme, menu.title, body, footer), (data) => list.handleInput(data));
            };
            render();
            unsubscribe = subscribe?.(() => {
                if (!settled)
                    render();
            });
        });
    }
    status(title, message) {
        this.setContent(frame(this.theme, title, [new Spacer(1), new Text(this.theme.fg("muted", message), 1, 0)]));
    }
    redirectUrl(title, authorizationUrl, signal) {
        return new Promise((resolve) => {
            let settled = false;
            const finish = (value) => {
                if (settled)
                    return;
                settled = true;
                signal.removeEventListener("abort", onAbort);
                resolve(value);
            };
            const onAbort = () => finish(undefined);
            if (signal.aborted) {
                finish(undefined);
                return;
            }
            signal.addEventListener("abort", onAbort, { once: true });
            const input = new Input();
            const clickHint = process.platform === "darwin" ? "Cmd+click to open" : "Ctrl+click to open";
            const body = [
                new Spacer(1),
                new Text(this.theme.fg("muted", "Approve access in your browser. If it did not open, visit:"), 1, 0),
                new Text(this.theme.fg("accent", hyperlink(authorizationUrl, authorizationUrl)), 1, 0),
                new Text(this.theme.fg("dim", hyperlink(clickHint, authorizationUrl)), 1, 0),
                new Spacer(1),
                new Text(this.theme.fg("muted", "If the browser runs on another machine, paste the URL it was redirected to:"), 1, 0),
                input,
            ];
            this.setContent(frame(this.theme, title, body, `${keyHint("tui.select.confirm", "submit")} • ${keyHint("tui.select.cancel", "cancel")}`), (data) => {
                if (this.keybindings.matches(data, "tui.select.confirm")) {
                    const value = input.getValue().trim();
                    if (value)
                        finish(value);
                    return;
                }
                if (this.keybindings.matches(data, "tui.select.cancel")) {
                    finish(undefined);
                    return;
                }
                input.handleInput(data);
            }, input);
        });
    }
    handleInput(data) {
        this.inputHandler?.(data);
        this.tui.requestRender();
    }
    render(width) {
        return this.content
            .render(width)
            .map((line) => (visibleWidth(line) > width ? truncateToWidth(line, width, "") : line));
    }
    invalidate() {
        this.content.invalidate();
    }
}
/** Run `manage` in the manager view until it returns. */
export async function showMcpManager(ctx, manage) {
    await ctx.ui.custom((tui, theme, keybindings, done) => {
        const view = new McpManagerView(tui, theme, keybindings);
        void manage(view).then(() => done(), (error) => {
            ctx.ui.notify(error instanceof Error ? error.message : String(error), "error");
            done();
        });
        return view;
    });
}
//# sourceMappingURL=ui.js.map