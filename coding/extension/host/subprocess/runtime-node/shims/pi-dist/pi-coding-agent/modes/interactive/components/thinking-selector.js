import { Container, fuzzyFilter, getKeybindings, Input, SelectList, Spacer, Text, } from "../../../../../pi-tui.mjs";
import { getSelectListTheme, theme } from "../theme/theme.js";
import { DynamicBorder } from "./dynamic-border.js";
import { keyDisplayText } from "./keybinding-hints.js";
const THINKING_SELECT_LIST_LAYOUT = {
    minPrimaryColumnWidth: 12,
    maxPrimaryColumnWidth: 32,
};
const LEVEL_DESCRIPTIONS = {
    off: "No reasoning",
    minimal: "Very brief reasoning (~1k tokens)",
    low: "Light reasoning (~2k tokens)",
    medium: "Moderate reasoning (~8k tokens)",
    high: "Deep reasoning (~16k tokens)",
    xhigh: "Extra-high reasoning (~32k tokens)",
    max: "Maximum reasoning",
};
/**
 * Component that renders a thinking level selector with borders
 */
export class ThinkingSelectorComponent extends Container {
    searchInput;
    selectList;
    selectListChildIndex;
    allItems;
    onSelect;
    onCancel;
    onSelectAsDefault;
    _focused = false;
    get focused() {
        return this._focused;
    }
    set focused(value) {
        this._focused = value;
        this.searchInput.focused = value;
    }
    constructor(currentLevel, availableLevels, onSelect, onCancel, onSelectAsDefault, defaultThinkingLevel) {
        super();
        this.onSelect = onSelect;
        this.onCancel = onCancel;
        this.onSelectAsDefault = onSelectAsDefault;
        this.allItems = availableLevels.map((level) => ({
            value: level,
            label: `${level === currentLevel ? "✓ " : "  "}${level}`,
            description: level === defaultThinkingLevel ? `${LEVEL_DESCRIPTIONS[level]} · default` : LEVEL_DESCRIPTIONS[level],
        }));
        // Add top border
        this.addChild(new DynamicBorder());
        this.addChild(new Spacer(1));
        this.addChild(new Text("Thinking Level", 0, 0));
        this.addChild(new Spacer(1));
        this.addChild(new Text(`${keyDisplayText("app.thinking.cycle")} cycles thinking levels in-session`, 0, 0));
        this.addChild(new Spacer(1));
        this.searchInput = new Input();
        this.searchInput.onSubmit = () => this.selectList.handleInput("\r");
        this.addChild(this.searchInput);
        this.addChild(new Spacer(1));
        // Create selector
        this.selectList = this.buildSelectList(this.allItems, currentLevel);
        this.selectListChildIndex = this.children.length;
        this.addChild(this.selectList);
        this.addChild(new Spacer(1));
        this.addChild(new Text(theme.fg("dim", `  ${keyDisplayText("tui.select.confirm")} to select · ${keyDisplayText("app.thinking.save")} to set as default · ${keyDisplayText("tui.select.cancel")} to cancel`), 0, 0));
        // Add bottom border
        this.addChild(new DynamicBorder());
    }
    buildSelectList(items, preselect) {
        const list = new SelectList(items, Math.max(1, items.length), getSelectListTheme(), THINKING_SELECT_LIST_LAYOUT);
        const currentIndex = items.findIndex((item) => item.value === preselect);
        if (currentIndex !== -1) {
            list.setSelectedIndex(currentIndex);
        }
        list.onSelect = (item) => this.onSelect(item.value);
        list.onCancel = () => this.onCancel();
        return list;
    }
    applyFilter(query) {
        const filtered = query
            ? fuzzyFilter(this.allItems, query, (item) => `${item.value} ${item.description ?? ""}`)
            : this.allItems;
        const selectedValue = this.selectList.getSelectedItem()?.value;
        const newList = this.buildSelectList(filtered, selectedValue);
        this.children[this.selectListChildIndex] = newList;
        this.selectList = newList;
    }
    handleInput(keyData) {
        const kb = getKeybindings();
        if (kb.matches(keyData, "app.thinking.save") && this.onSelectAsDefault) {
            const item = this.selectList.getSelectedItem();
            if (item)
                this.onSelectAsDefault(item.value);
            return;
        }
        const isNav = kb.matches(keyData, "tui.select.up") ||
            kb.matches(keyData, "tui.select.down") ||
            kb.matches(keyData, "tui.select.confirm") ||
            kb.matches(keyData, "tui.select.cancel");
        if (isNav) {
            this.selectList.handleInput(keyData);
            return;
        }
        this.searchInput.handleInput(keyData);
        this.applyFilter(this.searchInput.getValue());
    }
    getSelectList() {
        return this.selectList;
    }
}
//# sourceMappingURL=thinking-selector.js.map