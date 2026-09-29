import { modelsAreEqual } from "../../../../pi-ai/sdk-bundle/index.js";
import { Container, fuzzyFilter, getKeybindings, Input, Spacer, Text, } from "../../../../../pi-tui.mjs";
import { refreshModelCatalogs } from "../model-catalog-refresh.js";
import { getModelSelectorSearchText } from "../model-search.js";
import { theme } from "../theme/theme.js";
import { DynamicBorder } from "./dynamic-border.js";
import { keyDisplayText, keyHint } from "./keybinding-hints.js";
/**
 * Component that renders a model selector with search
 */
export class ModelSelectorComponent extends Container {
    searchInput;
    // Focusable implementation - propagate to searchInput for IME cursor positioning
    _focused = false;
    get focused() {
        return this._focused;
    }
    set focused(value) {
        this._focused = value;
        this.searchInput.focused = value;
    }
    listContainer;
    allModels = [];
    scopedModelItems = [];
    activeModels = [];
    filteredModels = [];
    selectedIndex = 0;
    currentModel;
    modelRuntime;
    onSelectCallback;
    onSelectAsDefaultCallback;
    onCancelCallback;
    errorMessage;
    refreshStatusMessage = "Refreshing model catalogs…";
    refreshStatusSuccess = false;
    tui;
    scopedModels;
    defaultModel;
    scope = "all";
    scopeText;
    scopeHintText;
    refreshAbortController = new AbortController();
    refreshTimeout;
    closed = false;
    constructor(tui, currentModel, modelRuntime, scopedModels, onSelect, onCancel, initialSearchInput, onSelectAsDefault, defaultModel) {
        super();
        this.tui = tui;
        this.currentModel = currentModel;
        this.modelRuntime = modelRuntime;
        this.scopedModels = scopedModels;
        this.defaultModel = defaultModel;
        this.scope = scopedModels.length > 0 ? "scoped" : "all";
        this.onSelectCallback = onSelect;
        this.onSelectAsDefaultCallback = onSelectAsDefault;
        this.onCancelCallback = onCancel;
        // Add top border
        this.addChild(new DynamicBorder());
        this.addChild(new Spacer(1));
        // Add hint about model filtering
        if (scopedModels.length > 0) {
            this.scopeText = new Text(this.getScopeText(), 0, 0);
            this.addChild(this.scopeText);
            this.scopeHintText = new Text(this.getScopeHintText(), 0, 0);
            this.addChild(this.scopeHintText);
        }
        else {
            const hintText = "Only showing models from configured providers. Use /login to add providers.";
            this.addChild(new Text(theme.fg("warning", hintText), 0, 0));
        }
        this.addChild(new Spacer(1));
        // Create search input
        this.searchInput = new Input();
        if (initialSearchInput) {
            this.searchInput.setValue(initialSearchInput);
        }
        this.searchInput.onSubmit = () => {
            // Enter on search input selects the first filtered item
            if (this.filteredModels[this.selectedIndex]) {
                this.handleSelect(this.filteredModels[this.selectedIndex].model);
            }
        };
        this.addChild(this.searchInput);
        this.addChild(new Spacer(1));
        // Create list container
        this.listContainer = new Container();
        this.addChild(this.listContainer);
        this.addChild(new Spacer(1));
        // Hint
        if (this.onSelectAsDefaultCallback) {
            this.addChild(new Text(theme.fg("dim", `  ${keyDisplayText("tui.select.confirm")} to select · ${keyDisplayText("app.models.save")} to set as default · ${keyDisplayText("tui.select.cancel")} to cancel`), 0, 0));
        }
        // Add bottom border
        this.addChild(new DynamicBorder());
        // Render the current snapshot immediately, then refresh in the background.
        this.loadModelsFromSnapshot();
        if (initialSearchInput)
            this.filterModels(initialSearchInput);
        else
            this.updateList();
        this.tui.requestRender();
        void this.refreshModels();
    }
    loadModelsFromSnapshot() {
        const models = this.modelRuntime.getAvailableSnapshot().map((model) => ({
            provider: model.provider,
            id: model.id,
            model,
        }));
        this.allModels = this.sortModels(models);
        this.scopedModels = this.scopedModels.map((scoped) => {
            const refreshed = this.modelRuntime.getModel(scoped.model.provider, scoped.model.id);
            return refreshed ? { ...scoped, model: refreshed } : scoped;
        });
        this.scopedModelItems = this.scopedModels.map((scoped) => ({
            provider: scoped.model.provider,
            id: scoped.model.id,
            model: scoped.model,
        }));
        this.activeModels = this.scope === "scoped" ? this.scopedModelItems : this.allModels;
        this.filteredModels = this.activeModels;
        const currentIndex = this.filteredModels.findIndex((item) => modelsAreEqual(this.currentModel, item.model));
        this.selectedIndex =
            currentIndex >= 0 ? currentIndex : Math.min(this.selectedIndex, Math.max(0, this.filteredModels.length - 1));
    }
    async refreshModels() {
        const timeoutMs = 15_000;
        let timedOut = false;
        this.refreshTimeout = setTimeout(() => {
            timedOut = true;
            this.refreshAbortController.abort();
        }, timeoutMs);
        try {
            const result = await refreshModelCatalogs(this.modelRuntime, this.refreshAbortController.signal);
            if (this.closed)
                return;
            this.refreshStatusMessage = "";
            if (result.aborted && timedOut) {
                this.errorMessage = "Model refresh timed out; showing cached models.";
            }
            else if (result.errors.size === 1) {
                this.errorMessage = `Could not refresh ${result.errors.keys().next().value}; showing cached models.`;
            }
            else if (result.errors.size > 1) {
                this.errorMessage = `Could not refresh ${result.errors.size} model catalogs (${[...result.errors.keys()].join(", ")}); showing cached models.`;
            }
            else {
                this.errorMessage = this.modelRuntime.getError();
                if (!this.errorMessage) {
                    this.refreshStatusMessage = "Model catalogs refreshed.";
                    this.refreshStatusSuccess = true;
                }
            }
            this.loadModelsFromSnapshot();
            this.filterModels(this.searchInput.getValue());
            this.tui.requestRender();
        }
        catch (error) {
            if (this.closed)
                return;
            this.refreshStatusMessage = "";
            this.errorMessage = timedOut
                ? "Model refresh timed out; showing cached models."
                : `Could not refresh model catalogs: ${error instanceof Error ? error.message : String(error)}`;
            this.updateList();
            this.tui.requestRender();
        }
        finally {
            if (this.refreshTimeout)
                clearTimeout(this.refreshTimeout);
        }
    }
    dispose() {
        if (this.closed)
            return;
        this.closed = true;
        if (this.refreshTimeout)
            clearTimeout(this.refreshTimeout);
        this.refreshAbortController.abort();
    }
    sortModels(models) {
        const sorted = [...models];
        // Sort: current model first, default model second, then by provider.
        sorted.sort((a, b) => {
            const aIsCurrent = modelsAreEqual(this.currentModel, a.model);
            const bIsCurrent = modelsAreEqual(this.currentModel, b.model);
            if (aIsCurrent && !bIsCurrent)
                return -1;
            if (!aIsCurrent && bIsCurrent)
                return 1;
            const aIsDefault = this.isDefaultModel(a.model);
            const bIsDefault = this.isDefaultModel(b.model);
            if (aIsDefault && !bIsDefault)
                return -1;
            if (!aIsDefault && bIsDefault)
                return 1;
            return a.provider.localeCompare(b.provider);
        });
        return sorted;
    }
    getScopeText() {
        const allText = this.scope === "all" ? theme.fg("accent", "all") : theme.fg("muted", "all");
        const scopedText = this.scope === "scoped" ? theme.fg("accent", "scoped") : theme.fg("muted", "scoped");
        return `${theme.fg("muted", "Scope: ")}${allText}${theme.fg("muted", " | ")}${scopedText}`;
    }
    getScopeHintText() {
        return keyHint("tui.input.tab", "scope") + theme.fg("muted", " (all/scoped)");
    }
    isDefaultModel(model) {
        return this.defaultModel?.provider === model.provider && this.defaultModel.id === model.id;
    }
    isDefaultSearch(query) {
        const normalized = query.trim().toLowerCase();
        return normalized.length > 0 && "default".startsWith(normalized);
    }
    setScope(scope) {
        if (this.scope === scope)
            return;
        this.scope = scope;
        this.activeModels = this.scope === "scoped" ? this.scopedModelItems : this.allModels;
        const currentIndex = this.activeModels.findIndex((item) => modelsAreEqual(this.currentModel, item.model));
        this.selectedIndex = currentIndex >= 0 ? currentIndex : 0;
        this.filterModels(this.searchInput.getValue());
        if (this.scopeText) {
            this.scopeText.setText(this.getScopeText());
        }
    }
    filterModels(query) {
        if (query) {
            const filtered = fuzzyFilter(this.activeModels, query, (item) => {
                const defaultText = this.isDefaultModel(item.model) ? " default" : "";
                return `${getModelSelectorSearchText({ id: item.id, provider: item.provider, name: item.model.name })}${defaultText}`;
            });
            if (this.isDefaultSearch(query)) {
                const defaultItems = this.activeModels.filter((item) => this.isDefaultModel(item.model));
                const defaultKeys = new Set(defaultItems.map((item) => `${item.provider}\0${item.id}`));
                this.filteredModels = [
                    ...defaultItems,
                    ...filtered.filter((item) => !defaultKeys.has(`${item.provider}\0${item.id}`)),
                ];
            }
            else {
                this.filteredModels = filtered;
            }
        }
        else {
            this.filteredModels = this.activeModels;
        }
        // When filtering by a query, move the selector to the top row so the best
        // match is highlighted. When the query is cleared, keep the current position
        // clamped to the (restored) list length.
        this.selectedIndex = query ? 0 : Math.min(this.selectedIndex, Math.max(0, this.filteredModels.length - 1));
        this.updateList();
    }
    updateList() {
        this.listContainer.clear();
        const maxVisible = 10;
        const startIndex = Math.max(0, Math.min(this.selectedIndex - Math.floor(maxVisible / 2), this.filteredModels.length - maxVisible));
        const endIndex = Math.min(startIndex + maxVisible, this.filteredModels.length);
        // Show visible slice of filtered models
        for (let i = startIndex; i < endIndex; i++) {
            const item = this.filteredModels[i];
            if (!item)
                continue;
            const isSelected = i === this.selectedIndex;
            const isCurrent = modelsAreEqual(this.currentModel, item.model);
            const isDefault = this.isDefaultModel(item.model);
            const defaultBadge = isDefault ? theme.fg("muted", " · default") : "";
            const cursor = isSelected ? theme.fg("accent", "→ ") : "  ";
            const currentMarker = isCurrent ? theme.fg("accent", "✓ ") : "  ";
            const modelText = isSelected ? theme.fg("accent", item.id) : item.id;
            const providerBadge = theme.fg("muted", `[${item.provider}]`);
            const line = `${cursor}${currentMarker}${modelText} ${providerBadge}${defaultBadge}`;
            this.listContainer.addChild(new Text(line, 0, 0));
        }
        // Add scroll indicator if needed
        if (startIndex > 0 || endIndex < this.filteredModels.length) {
            const scrollInfo = theme.fg("muted", `  (${this.selectedIndex + 1}/${this.filteredModels.length})`);
            this.listContainer.addChild(new Text(scrollInfo, 0, 0));
        }
        // Show error message or "no results" if empty
        if (this.errorMessage) {
            // Show error in red
            const errorLines = this.errorMessage.split("\n");
            for (const line of errorLines) {
                this.listContainer.addChild(new Text(theme.fg("error", line), 0, 0));
            }
        }
        else if (this.filteredModels.length === 0) {
            this.listContainer.addChild(new Text(theme.fg("muted", "  No matching models"), 0, 0));
        }
        else {
            const selected = this.filteredModels[this.selectedIndex];
            this.listContainer.addChild(new Spacer(1));
            this.listContainer.addChild(new Text(theme.fg("muted", `  Model Name: ${selected.model.name}`), 0, 0));
        }
        if (this.refreshStatusMessage) {
            this.listContainer.addChild(new Spacer(1));
            this.listContainer.addChild(new Text(theme.fg(this.refreshStatusSuccess ? "success" : "muted", `  ${this.refreshStatusMessage}`), 0, 0));
        }
    }
    handleInput(keyData) {
        const kb = getKeybindings();
        if (kb.matches(keyData, "tui.input.tab")) {
            if (this.scopedModelItems.length > 0) {
                const nextScope = this.scope === "all" ? "scoped" : "all";
                this.setScope(nextScope);
                if (this.scopeHintText) {
                    this.scopeHintText.setText(this.getScopeHintText());
                }
            }
            return;
        }
        // Up arrow - wrap to bottom when at top
        if (kb.matches(keyData, "tui.select.up")) {
            if (this.filteredModels.length === 0)
                return;
            this.selectedIndex = this.selectedIndex === 0 ? this.filteredModels.length - 1 : this.selectedIndex - 1;
            this.updateList();
        }
        // Down arrow - wrap to top when at bottom
        else if (kb.matches(keyData, "tui.select.down")) {
            if (this.filteredModels.length === 0)
                return;
            this.selectedIndex = this.selectedIndex === this.filteredModels.length - 1 ? 0 : this.selectedIndex + 1;
            this.updateList();
        }
        // Enter
        else if (kb.matches(keyData, "tui.select.confirm")) {
            const selectedModel = this.filteredModels[this.selectedIndex];
            if (selectedModel) {
                this.handleSelect(selectedModel.model);
            }
        }
        // Escape or Ctrl+C
        else if (kb.matches(keyData, "tui.select.cancel")) {
            this.dispose();
            this.onCancelCallback();
        }
        // Select and save as default
        else if (kb.matches(keyData, "app.models.save") && this.onSelectAsDefaultCallback) {
            const selectedModel = this.filteredModels[this.selectedIndex];
            if (selectedModel) {
                this.dispose();
                this.onSelectAsDefaultCallback(selectedModel.model);
            }
        }
        // Pass everything else to search input
        else {
            this.searchInput.handleInput(keyData);
            this.filterModels(this.searchInput.getValue());
        }
    }
    handleSelect(model) {
        this.dispose();
        this.onSelectCallback(model);
    }
    getSearchInput() {
        return this.searchInput;
    }
}
//# sourceMappingURL=model-selector.js.map