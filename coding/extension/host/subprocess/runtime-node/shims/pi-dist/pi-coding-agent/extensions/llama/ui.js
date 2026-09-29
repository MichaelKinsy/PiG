import { Container, fuzzyFilter, Input, SelectList, Spacer, Text, truncateToWidth, visibleWidth, } from "../../../../pi-tui.mjs";
import { DynamicBorder } from "../../modes/interactive/components/dynamic-border.js";
import { keyHint } from "../../modes/interactive/components/keybinding-hints.js";
const DOWNLOAD_VALUE = "\0download";
function contextLabel(model) {
    const context = model.meta?.n_ctx ?? model.meta?.n_ctx_train;
    if (context)
        return context >= 1000 ? `${Math.round(context / 1000)}k` : String(context);
    const args = model.status.args ?? [];
    for (let index = 0; index < args.length - 1; index++) {
        if (args[index] !== "--ctx-size" && args[index] !== "-c" && args[index] !== "-ctx")
            continue;
        const value = Number(args[index + 1]);
        if (Number.isFinite(value) && value > 0)
            return value >= 1000 ? `${Math.round(value / 1000)}k` : String(value);
    }
    return undefined;
}
function modelDescription(model) {
    const details = [];
    const loaded = model.status.value === "loaded" || model.status.value === "sleeping";
    if (loaded)
        details.push("loaded");
    else if (model.status.value !== "unloaded")
        details.push(model.status.value);
    const context = loaded ? contextLabel(model) : undefined;
    if (context)
        details.push(`${context} context`);
    return details.join(" · ");
}
function selectTheme(theme) {
    return {
        selectedPrefix: (text) => theme.fg("accent", text),
        selectedText: (text) => theme.fg("accent", text),
        description: (text) => theme.fg("muted", text),
        scrollInfo: (text) => theme.fg("dim", text),
        noMatch: (text) => theme.fg("warning", text),
    };
}
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
function compactCount(value) {
    if (value >= 1_000_000)
        return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 0 : 1)}M`;
    if (value >= 1_000)
        return `${(value / 1_000).toFixed(value >= 100_000 ? 0 : 1)}k`;
    return String(value);
}
class HuggingFaceSearch extends Container {
    tui;
    theme;
    keybindings;
    search;
    cache;
    onSelectModel;
    input = new Input();
    resultsContainer = new Container();
    results = [];
    filteredResults = [];
    selectedIndex = 0;
    query = "";
    status = "Type at least 2 characters";
    debounce;
    request;
    closed = false;
    _focused = false;
    constructor(tui, theme, keybindings, search, cache, onSelectModel) {
        super();
        this.tui = tui;
        this.theme = theme;
        this.keybindings = keybindings;
        this.search = search;
        this.cache = cache;
        this.onSelectModel = onSelectModel;
        this.addChild(new Text(theme.fg("dim", "Model name or owner/repository[:quant]"), 1, 0));
        this.addChild(this.input);
        this.addChild(new Spacer(1));
        this.addChild(this.resultsContainer);
        this.updateResults();
    }
    get focused() {
        return this._focused;
    }
    set focused(value) {
        this._focused = value;
        this.input.focused = value;
    }
    updateResults() {
        this.resultsContainer.clear();
        const maxVisible = 10;
        const start = Math.max(0, Math.min(this.selectedIndex - Math.floor(maxVisible / 2), this.filteredResults.length - maxVisible));
        const end = Math.min(start + maxVisible, this.filteredResults.length);
        for (let index = start; index < end; index++) {
            const model = this.filteredResults[index];
            if (!model)
                continue;
            const prefix = index === this.selectedIndex ? "→ " : "  ";
            const details = `${compactCount(model.downloads)} downloads`;
            this.resultsContainer.addChild(new Text(index === this.selectedIndex
                ? this.theme.fg("accent", `${prefix}${model.id}  ${details}`)
                : `${prefix}${model.id}${this.theme.fg("muted", `  ${details}`)}`, 0, 0));
        }
        if (start > 0 || end < this.filteredResults.length) {
            this.resultsContainer.addChild(new Text(this.theme.fg("dim", `  (${this.selectedIndex + 1}/${this.filteredResults.length})`), 0, 0));
        }
        if (this.filteredResults.length === 0) {
            this.resultsContainer.addChild(new Text(this.theme.fg("dim", `  ${this.status}`), 0, 0));
        }
        else if (this.status === "Searching Hugging Face…") {
            this.resultsContainer.addChild(new Text(this.theme.fg("dim", `  ${this.status}`), 0, 0));
        }
        this.tui.requestRender();
    }
    filterResults() {
        if (this.query) {
            const matches = new Set(fuzzyFilter(this.results, this.query, (model) => model.id).map((model) => model.id));
            this.filteredResults = this.results.filter((model) => matches.has(model.id));
        }
        else {
            this.filteredResults = this.results;
        }
        this.selectedIndex = Math.min(this.selectedIndex, Math.max(0, this.filteredResults.length - 1));
        this.updateResults();
    }
    scheduleSearch() {
        if (this.debounce)
            clearTimeout(this.debounce);
        this.request?.abort();
        this.request = undefined;
        if (this.query.length < 2) {
            this.status = "Type at least 2 characters";
            this.filterResults();
            return;
        }
        const cached = this.cache.get(this.query.toLowerCase());
        if (cached) {
            this.results = cached;
            this.status = cached.length === 0 ? "No GGUF models found" : "";
            this.filterResults();
            return;
        }
        this.status = "Searching Hugging Face…";
        this.filterResults();
        this.debounce = setTimeout(() => void this.runSearch(this.query), 500);
    }
    async runSearch(query) {
        const request = new AbortController();
        this.request = request;
        try {
            const results = await this.search(query, request.signal);
            this.cache.set(query.toLowerCase(), results);
            if (this.closed || request.signal.aborted || this.query !== query)
                return;
            this.results = results;
            this.selectedIndex = 0;
            this.status = results.length === 0 ? "No GGUF models found" : "";
            this.filterResults();
        }
        catch (error) {
            if (this.closed || request.signal.aborted || this.query !== query)
                return;
            this.results = [];
            this.status = error instanceof Error ? error.message : String(error);
            this.filterResults();
        }
        finally {
            if (this.request === request)
                this.request = undefined;
        }
    }
    close(model) {
        if (this.closed)
            return;
        this.closed = true;
        if (this.debounce)
            clearTimeout(this.debounce);
        this.request?.abort();
        this.onSelectModel(model);
    }
    handleInput(data) {
        if (this.keybindings.matches(data, "tui.select.up")) {
            if (this.filteredResults.length > 0) {
                this.selectedIndex = this.selectedIndex === 0 ? this.filteredResults.length - 1 : this.selectedIndex - 1;
                this.updateResults();
            }
            return;
        }
        if (this.keybindings.matches(data, "tui.select.down")) {
            if (this.filteredResults.length > 0) {
                this.selectedIndex = this.selectedIndex === this.filteredResults.length - 1 ? 0 : this.selectedIndex + 1;
                this.updateResults();
            }
            return;
        }
        if (this.keybindings.matches(data, "tui.select.confirm")) {
            const exact = /^[^/\s]+\/[^:\s]+(?::[^\s:]+)?$/u.test(this.query) ? this.query : undefined;
            const selected = exact ?? this.filteredResults[this.selectedIndex]?.id;
            if (selected)
                this.close(selected);
            return;
        }
        if (this.keybindings.matches(data, "tui.select.cancel")) {
            this.close(undefined);
            return;
        }
        this.input.handleInput(data);
        const query = this.input.getValue().trim();
        if (query === this.query)
            return;
        this.query = query;
        this.scheduleSearch();
    }
}
class LlamaView {
    tui;
    theme;
    keybindings;
    searchCache = new Map();
    content;
    inputHandler;
    inputTarget;
    progressPromise;
    progressResolver;
    showingProgress = false;
    _focused = false;
    constructor(tui, theme, keybindings) {
        this.tui = tui;
        this.theme = theme;
        this.keybindings = keybindings;
        this.content = frame(theme, "llama.cpp models", [new Text(theme.fg("muted", "Loading…"), 1, 1)]);
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
        this.progressPromise = undefined;
        this.progressResolver = undefined;
        this.showingProgress = false;
        this.content = content;
        this.inputHandler = inputHandler;
        this.inputTarget = inputTarget;
        if (this.inputTarget)
            this.inputTarget.focused = this._focused;
        this.tui.requestRender();
    }
    showModels(serverUrl, models) {
        const sorted = [...models].sort((left, right) => {
            const loaded = Number(right.status.value === "loaded") - Number(left.status.value === "loaded");
            return loaded || left.id.localeCompare(right.id);
        });
        const byId = new Map(sorted.map((model) => [model.id, model]));
        const items = [
            ...sorted.map((model) => ({
                value: model.id,
                label: model.id,
                description: modelDescription(model),
            })),
            { value: DOWNLOAD_VALUE, label: "Download model…", description: "Hugging Face owner/repository[:quant]" },
        ];
        return new Promise((resolve) => {
            const list = new SelectList(items, Math.min(items.length, 12), selectTheme(this.theme), {
                minPrimaryColumnWidth: 36,
                maxPrimaryColumnWidth: 56,
            });
            list.onSelect = (item) => {
                if (item.value === DOWNLOAD_VALUE)
                    resolve({ type: "download" });
                else {
                    const model = byId.get(item.value);
                    if (model)
                        resolve({ type: "model", model });
                }
            };
            list.onCancel = () => resolve({ type: "close" });
            this.setContent(frame(this.theme, "llama.cpp models", [new Text(this.theme.fg("dim", serverUrl), 1, 0), new Spacer(1), list], `${keyHint("tui.select.confirm", "load/unload/download")} • ${keyHint("tui.select.cancel", "close")}`), list);
        });
    }
    select(title, options) {
        return new Promise((resolve) => {
            const list = new SelectList(options.map((option) => ({ value: option, label: option })), Math.min(options.length, 12), selectTheme(this.theme));
            list.onSelect = (item) => resolve(item.value);
            list.onCancel = () => resolve(undefined);
            this.setContent(frame(this.theme, title, [new Spacer(1), list], `${keyHint("tui.select.confirm", "select")} • ${keyHint("tui.select.cancel", "cancel")}`), list);
        });
    }
    async confirm(title, message) {
        return (await this.select(`${title}\n${message}`, ["Yes", "No"])) === "Yes";
    }
    async connectionError(serverUrl, message) {
        const choice = await this.select(`llama.cpp unavailable\n${serverUrl}\n\n${message}`, ["Retry", "Close"]);
        return choice === "Retry" ? "retry" : "close";
    }
    searchModels(search) {
        return new Promise((resolve) => {
            const component = new HuggingFaceSearch(this.tui, this.theme, this.keybindings, search, this.searchCache, resolve);
            this.setContent(frame(this.theme, "Download model", [new Spacer(1), component], `${keyHint("tui.select.confirm", "select")} • ${keyHint("tui.select.cancel", "back")}`), component, component);
        });
    }
    showStatus(title, message) {
        this.setContent(frame(this.theme, title, [new Spacer(1), new Text(this.theme.fg("muted", message), 1, 0)]));
    }
    progress(state) {
        if (!this.progressPromise) {
            this.progressPromise = new Promise((resolve) => {
                this.progressResolver = resolve;
            });
        }
        this.showingProgress = true;
        this.updateProgress(state);
        return this.progressPromise;
    }
    updateProgress(state) {
        if (!this.showingProgress)
            return;
        const body = [
            new Text(this.theme.fg("text", state.model), 1, 0),
            new Spacer(1),
            new Text(this.theme.fg("muted", state.message), 1, 0),
        ];
        if (state.ratio !== undefined) {
            const available = 40;
            const filled = Math.round(Math.max(0, Math.min(1, state.ratio)) * available);
            body.push(new Text(this.theme.fg("accent", `${"█".repeat(filled)}${"─".repeat(available - filled)} ${Math.round(state.ratio * 100)}%`), 1, 0));
        }
        if (state.detail)
            body.push(new Text(this.theme.fg("dim", state.detail), 1, 0));
        this.content = frame(this.theme, state.title, body, keyHint("tui.select.cancel", "stop"));
        this.inputHandler = undefined;
        this.tui.requestRender();
    }
    handleInput(data) {
        if (this.progressResolver && this.keybindings.matches(data, "tui.select.cancel")) {
            const resolve = this.progressResolver;
            this.progressPromise = undefined;
            this.progressResolver = undefined;
            resolve();
            return;
        }
        this.inputHandler?.handleInput?.(data);
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
export async function showLlamaUi(ctx, run) {
    await ctx.ui.custom((tui, theme, keybindings, done) => {
        const view = new LlamaView(tui, theme, keybindings);
        void run(view).then(() => done(), (error) => {
            ctx.ui.notify(error instanceof Error ? error.message : String(error), "error");
            done();
        });
        return view;
    });
}
export async function runWithProgress(ui, options) {
    const controller = new AbortController();
    const state = { title: options.title, model: options.model, message: options.initialMessage };
    const settled = options
        .run(controller.signal, (progress) => {
        Object.assign(state, progress);
        ui.updateProgress(state);
    })
        .then((value) => ({ ok: true, value }), (error) => ({ ok: false, error }));
    let completed = false;
    settled.finally(() => {
        completed = true;
    });
    while (!completed) {
        const outcome = await Promise.race([
            settled.then(() => "settled"),
            ui.progress(state).then(() => "stop"),
        ]);
        if (outcome === "settled")
            break;
        const stop = await ui.confirm(options.cancelTitle, options.cancelMessage);
        if (!stop || completed)
            continue;
        try {
            await options.cancel();
        }
        finally {
            controller.abort(new Error("Cancelled"));
        }
        await settled;
        return { cancelled: true };
    }
    const result = await settled;
    if (!result.ok)
        throw result.error;
    return { cancelled: false, value: result.value };
}
//# sourceMappingURL=ui.js.map