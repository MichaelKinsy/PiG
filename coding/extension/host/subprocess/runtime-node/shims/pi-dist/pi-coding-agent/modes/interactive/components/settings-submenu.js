import { Container, fuzzyFilter, getKeybindings, Input, SelectList, Spacer, Text, } from "../../../../../pi-tui.mjs";
import { getSelectListTheme, theme } from "../theme/theme.js";
const SUBMENU_SELECT_LIST_LAYOUT = {
    minPrimaryColumnWidth: 12,
    maxPrimaryColumnWidth: 32,
};
/**
 * Single-step submenu that shows a titled select list.
 * With `searchable: true`, typing filters the list using fuzzy matching.
 */
export class SelectSubmenu extends Container {
    selectList;
    listChildIndex;
    allOptions;
    listLayout;
    searchInput;
    onSelectCb;
    onCancelCb;
    onSelectionChangeCb;
    constructor(title, description, options, currentValue, onSelect, onCancel, onSelectionChange, submenuOptions) {
        super();
        this.allOptions = options;
        this.listLayout = submenuOptions?.layout ?? SUBMENU_SELECT_LIST_LAYOUT;
        this.onSelectCb = onSelect;
        this.onCancelCb = onCancel;
        this.onSelectionChangeCb = onSelectionChange;
        // Title
        this.addChild(new Text(theme.bold(theme.fg("accent", title)), 0, 0));
        // Description
        if (description) {
            this.addChild(new Spacer(1));
            this.addChild(new Text(theme.fg("muted", description), 0, 0));
        }
        // Search input
        if (submenuOptions?.searchable) {
            this.addChild(new Spacer(1));
            this.searchInput = new Input();
            this.searchInput.onSubmit = () => {
                this.selectList.handleInput("\r");
            };
            this.addChild(this.searchInput);
        }
        // Spacer
        this.addChild(new Spacer(1));
        // Select list
        this.selectList = this.buildSelectList(options, currentValue);
        this.listChildIndex = this.children.length;
        this.addChild(this.selectList);
        // Hint
        this.addChild(new Spacer(1));
        const hint = submenuOptions?.searchable
            ? "  Type to filter \u00b7 Enter to select \u00b7 Esc to go back"
            : "  Enter to select \u00b7 Esc to go back";
        this.addChild(new Text(theme.fg("dim", hint), 0, 0));
    }
    buildSelectList(options, preselect) {
        const list = new SelectList(options, Math.min(options.length, 10), getSelectListTheme(), this.listLayout);
        const idx = options.findIndex((o) => o.value === preselect);
        if (idx !== -1)
            list.setSelectedIndex(idx);
        list.onSelect = (item) => this.onSelectCb(item.value);
        list.onCancel = this.onCancelCb;
        if (this.onSelectionChangeCb) {
            const cb = this.onSelectionChangeCb;
            list.onSelectionChange = (item) => cb(item.value);
        }
        return list;
    }
    applyFilter(query) {
        const filtered = query
            ? fuzzyFilter(this.allOptions, query, (item) => `${item.label} ${item.description ?? ""}`)
            : this.allOptions;
        const newList = this.buildSelectList(filtered, "");
        this.children[this.listChildIndex] = newList;
        this.selectList = newList;
    }
    handleInput(data) {
        if (this.searchInput) {
            const kb = getKeybindings();
            const isNav = kb.matches(data, "tui.select.up") ||
                kb.matches(data, "tui.select.down") ||
                kb.matches(data, "tui.select.confirm") ||
                kb.matches(data, "tui.select.cancel");
            if (isNav) {
                this.selectList.handleInput(data);
            }
            else {
                this.searchInput.handleInput(data);
                this.applyFilter(this.searchInput.getValue());
            }
        }
        else {
            this.selectList.handleInput(data);
        }
    }
}
/**
 * Generic N-step submenu built on top of {@link SelectSubmenu}.
 *
 * Each step's options can depend on prior selections via the shared context.
 * Esc goes back one step; Esc at step 0 cancels.
 * With `loop: true`, completing the final step invokes `onComplete` then returns to step 0.
 */
export class SteppedSubmenu extends Container {
    steps;
    onComplete;
    onCancel;
    opts;
    activeComponent;
    context;
    constructor(steps, onComplete, onCancel, opts = {}) {
        super();
        this.steps = steps;
        this.onComplete = onComplete;
        this.onCancel = onCancel;
        this.opts = opts;
        this.context = { ...(opts.initialContext ?? {}) };
        this.activeComponent = this.buildStep(opts.startAtStep ?? 0);
    }
    buildStep(stepIndex) {
        const step = this.steps[stepIndex];
        const total = this.steps.length;
        const stepLabel = total > 1 ? `Step ${stepIndex + 1}/${total} \u00b7 ` : "";
        const title = typeof step.title === "function" ? step.title(this.context) : step.title;
        const desc = typeof step.description === "function" ? step.description(this.context) : step.description;
        const items = step.options(this.context);
        const preselect = step.preselect?.(this.context) ?? "";
        return new SelectSubmenu(title, `${stepLabel}${desc}`, items, preselect, (value) => {
            this.context[step.key] = value;
            if (stepIndex < total - 1) {
                // Advance to next step
                this.activeComponent = this.buildStep(stepIndex + 1);
            }
            else {
                // Final step \u2014 deliver result
                this.onComplete({ ...this.context });
                if (this.opts.loop) {
                    this.context = {};
                    this.activeComponent = this.buildStep(0);
                }
                else {
                    this.onCancel();
                }
            }
        }, () => {
            if (stepIndex > 0) {
                delete this.context[step.key];
                this.activeComponent = this.buildStep(stepIndex - 1);
            }
            else {
                this.onCancel();
            }
        }, undefined, step.searchable || step.layout ? { searchable: step.searchable, layout: step.layout } : undefined);
    }
    render(width) {
        return this.activeComponent.render(width);
    }
    handleInput(data) {
        this.activeComponent.handleInput?.(data);
    }
    invalidate() {
        this.activeComponent.invalidate?.();
    }
}
//# sourceMappingURL=settings-submenu.js.map