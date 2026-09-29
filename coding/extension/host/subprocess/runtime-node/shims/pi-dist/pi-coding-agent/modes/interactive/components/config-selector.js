/**
 * TUI component for managing package resources (enable/disable)
 */
import { homedir } from "node:os";
import { basename, dirname, join, relative } from "node:path";
import { Container, getKeybindings, Input, matchesKey, Spacer, truncateToWidth, visibleWidth, } from "../../../../../pi-tui.mjs";
import { CONFIG_DIR_NAME } from "../../../config.js";
import { canonicalizePath, isLocalPath, resolvePath } from "../../../utils/paths.js";
import { theme } from "../theme/theme.js";
import { DynamicBorder } from "./dynamic-border.js";
import { keyHint, rawKeyHint } from "./keybinding-hints.js";
const RESOURCE_TYPES = ["extensions", "skills", "prompts", "themes"];
const RESOURCE_TYPE_LABELS = {
    extensions: "Extensions",
    skills: "Skills",
    prompts: "Prompts",
    themes: "Themes",
};
function formatBaseDir(baseDir) {
    const homeDir = homedir();
    let displayPath;
    if (baseDir === homeDir) {
        displayPath = "~";
    }
    else if (baseDir.startsWith(homeDir)) {
        // Replace home prefix with ~, normalize separators for display
        const rest = baseDir.slice(homeDir.length);
        displayPath = `~${rest.replace(/\\/g, "/")}`;
    }
    else {
        displayPath = baseDir.replace(/\\/g, "/");
    }
    return displayPath.endsWith("/") ? displayPath : `${displayPath}/`;
}
function getGroupLabel(metadata, agentDir) {
    if (metadata.origin === "package") {
        return `${metadata.source} (${metadata.scope})`;
    }
    // Top-level resources
    if (metadata.source === "auto") {
        if (metadata.baseDir) {
            return metadata.scope === "user"
                ? `User (${formatBaseDir(metadata.baseDir)})`
                : `Project (${formatBaseDir(metadata.baseDir)})`;
        }
        return metadata.scope === "user" ? `User (${formatBaseDir(agentDir)})` : `Project (${CONFIG_DIR_NAME}/)`;
    }
    return metadata.scope === "user" ? "User settings" : "Project settings";
}
function buildGroups(resolved, agentDir) {
    const groupMap = new Map();
    const addToGroup = (resources, resourceType) => {
        for (const res of resources) {
            const { path, enabled, metadata } = res;
            const groupKey = `${metadata.origin}:${metadata.scope}:${metadata.source}:${metadata.baseDir ?? ""}`;
            if (!groupMap.has(groupKey)) {
                groupMap.set(groupKey, {
                    key: groupKey,
                    label: getGroupLabel(metadata, agentDir),
                    scope: metadata.scope,
                    origin: metadata.origin,
                    source: metadata.source,
                    subgroups: [],
                });
            }
            const group = groupMap.get(groupKey);
            const subgroupKey = `${groupKey}:${resourceType}`;
            let subgroup = group.subgroups.find((sg) => sg.type === resourceType);
            if (!subgroup) {
                subgroup = {
                    type: resourceType,
                    label: RESOURCE_TYPE_LABELS[resourceType],
                    items: [],
                };
                group.subgroups.push(subgroup);
            }
            const fileName = basename(path);
            const parentFolder = basename(dirname(path));
            let displayName;
            if (resourceType === "extensions" && parentFolder !== "extensions") {
                displayName = `${parentFolder}/${fileName}`;
            }
            else if (resourceType === "skills" && fileName === "SKILL.md") {
                displayName = parentFolder;
            }
            else {
                displayName = fileName;
            }
            subgroup.items.push({
                path,
                enabled,
                metadata,
                resourceType,
                displayName,
                groupKey,
                subgroupKey,
            });
        }
    };
    addToGroup(resolved.extensions, "extensions");
    addToGroup(resolved.skills, "skills");
    addToGroup(resolved.prompts, "prompts");
    addToGroup(resolved.themes, "themes");
    // Sort groups: packages first, then top-level; user before project
    const groups = Array.from(groupMap.values());
    groups.sort((a, b) => {
        if (a.origin !== b.origin) {
            return a.origin === "package" ? -1 : 1;
        }
        if (a.scope !== b.scope) {
            return a.scope === "user" ? -1 : 1;
        }
        return a.source.localeCompare(b.source);
    });
    // Sort subgroups within each group by type order, and items by name
    const typeOrder = { extensions: 0, skills: 1, prompts: 2, themes: 3 };
    for (const group of groups) {
        group.subgroups.sort((a, b) => typeOrder[a.type] - typeOrder[b.type]);
        for (const subgroup of group.subgroups) {
            subgroup.items.sort((a, b) => a.displayName.localeCompare(b.displayName));
        }
    }
    return groups;
}
class ConfigSelectorHeader {
    writeScope;
    projectModeAvailable;
    constructor(writeScope, projectModeAvailable) {
        this.writeScope = writeScope;
        this.projectModeAvailable = projectModeAvailable;
    }
    setWriteScope(writeScope) {
        this.writeScope = writeScope;
    }
    invalidate() { }
    render(width) {
        const title = theme.bold(this.writeScope === "project" ? "Project Local Resources" : "Global Resources");
        const sep = theme.fg("muted", " · ");
        const switchHint = this.projectModeAvailable ? keyHint("tui.input.tab", "switch mode") + sep : "";
        const actionHint = this.writeScope === "project" ? rawKeyHint("space", "cycle inherit/+/-") : rawKeyHint("space", "toggle");
        const hint = switchHint + actionHint + sep + rawKeyHint("esc", "close");
        const spacing = Math.max(1, width - visibleWidth(title) - visibleWidth(hint));
        const scopeHint = this.writeScope === "project"
            ? theme.fg("muted", `${CONFIG_DIR_NAME}/settings.json · inherited global resources are dimmed`)
            : theme.fg("muted", `~/${CONFIG_DIR_NAME}/agent/settings.json`);
        return [
            truncateToWidth(`${title}${" ".repeat(spacing)}${hint}`, width, ""),
            truncateToWidth(scopeHint, width, ""),
        ];
    }
}
class ResourceList {
    groupsByScope;
    flatItems = [];
    filteredItems = [];
    selectedIndex = 0;
    searchInput;
    maxVisible;
    settingsManager;
    cwd;
    agentDir;
    writeScope;
    inheritedEnabledByKey;
    onCancel;
    onExit;
    onToggle;
    onSwitchMode;
    _focused = false;
    get focused() {
        return this._focused;
    }
    set focused(value) {
        this._focused = value;
        this.searchInput.focused = value;
    }
    constructor(groupsByScope, settingsManager, cwd, agentDir, terminalHeight, writeScope = "global") {
        this.groupsByScope = groupsByScope;
        this.settingsManager = settingsManager;
        this.cwd = cwd;
        this.agentDir = agentDir;
        this.writeScope = writeScope;
        this.inheritedEnabledByKey = this.buildInheritedEnabledMap(groupsByScope.global);
        this.searchInput = new Input();
        // 8 lines of chrome: top spacer + top border + spacer + header (2 lines) + spacer + bottom spacer + bottom border
        const chrome = 8;
        this.maxVisible = Math.max(5, (terminalHeight ?? 24) - chrome);
        this.buildFlatList();
        this.filteredItems = [...this.flatItems];
    }
    setWriteScope(writeScope) {
        this.writeScope = writeScope;
        this.buildFlatList();
        this.filterItems(this.searchInput.getValue());
    }
    get groups() {
        return this.groupsByScope[this.writeScope];
    }
    buildInheritedEnabledMap(groups) {
        const result = new Map();
        for (const group of groups) {
            for (const subgroup of group.subgroups) {
                for (const item of subgroup.items) {
                    result.set(this.getResourceItemKey(item), item.enabled);
                }
            }
        }
        return result;
    }
    buildFlatList() {
        this.flatItems = [];
        for (const group of this.groups) {
            this.flatItems.push({ type: "group", group });
            for (const subgroup of group.subgroups) {
                this.flatItems.push({ type: "subgroup", subgroup, group });
                for (const item of subgroup.items) {
                    this.flatItems.push({ type: "item", item });
                }
            }
        }
        // Start selection on first item (not header)
        this.selectedIndex = this.flatItems.findIndex((e) => e.type === "item");
        if (this.selectedIndex < 0)
            this.selectedIndex = 0;
    }
    findNextItem(fromIndex, direction) {
        let idx = fromIndex + direction;
        while (idx >= 0 && idx < this.filteredItems.length) {
            if (this.filteredItems[idx].type === "item") {
                return idx;
            }
            idx += direction;
        }
        return fromIndex; // Stay at current if no item found
    }
    filterItems(query) {
        if (!query.trim()) {
            this.filteredItems = [...this.flatItems];
            this.selectFirstItem();
            return;
        }
        const lowerQuery = query.toLowerCase();
        const matchingItems = new Set();
        const matchingSubgroups = new Set();
        const matchingGroups = new Set();
        for (const entry of this.flatItems) {
            if (entry.type === "item") {
                const item = entry.item;
                if (item.displayName.toLowerCase().includes(lowerQuery) ||
                    item.resourceType.toLowerCase().includes(lowerQuery) ||
                    item.path.toLowerCase().includes(lowerQuery)) {
                    matchingItems.add(item);
                }
            }
        }
        // Find which subgroups and groups contain matching items
        for (const group of this.groups) {
            for (const subgroup of group.subgroups) {
                for (const item of subgroup.items) {
                    if (matchingItems.has(item)) {
                        matchingSubgroups.add(subgroup);
                        matchingGroups.add(group);
                    }
                }
            }
        }
        this.filteredItems = [];
        for (const entry of this.flatItems) {
            if (entry.type === "group" && matchingGroups.has(entry.group)) {
                this.filteredItems.push(entry);
            }
            else if (entry.type === "subgroup" && matchingSubgroups.has(entry.subgroup)) {
                this.filteredItems.push(entry);
            }
            else if (entry.type === "item" && matchingItems.has(entry.item)) {
                this.filteredItems.push(entry);
            }
        }
        this.selectFirstItem();
    }
    selectFirstItem() {
        const firstItemIndex = this.filteredItems.findIndex((e) => e.type === "item");
        this.selectedIndex = firstItemIndex >= 0 ? firstItemIndex : 0;
    }
    updateItem(item, enabled) {
        item.enabled = enabled;
        // Update in groups too
        for (const group of this.groups) {
            for (const subgroup of group.subgroups) {
                const found = subgroup.items.find((i) => i.path === item.path && i.resourceType === item.resourceType);
                if (found) {
                    found.enabled = enabled;
                    return;
                }
            }
        }
    }
    invalidate() { }
    render(width) {
        const lines = [];
        // Search input
        lines.push(...this.searchInput.render(width));
        lines.push("");
        if (this.filteredItems.length === 0) {
            lines.push(theme.fg("muted", "  No resources found"));
            return lines;
        }
        // Calculate visible range
        const startIndex = Math.max(0, Math.min(this.selectedIndex - Math.floor(this.maxVisible / 2), this.filteredItems.length - this.maxVisible));
        const endIndex = Math.min(startIndex + this.maxVisible, this.filteredItems.length);
        for (let i = startIndex; i < endIndex; i++) {
            const entry = this.filteredItems[i];
            const isSelected = i === this.selectedIndex;
            if (entry.type === "group") {
                // Main group header (no cursor)
                const inherited = this.writeScope === "project" && entry.group.scope === "user";
                const label = theme.bold(`${entry.group.label}${inherited ? " · inherited global" : ""}`);
                const groupLine = theme.fg(inherited ? "dim" : "accent", label);
                lines.push(truncateToWidth(`  ${groupLine}`, width, ""));
            }
            else if (entry.type === "subgroup") {
                // Subgroup header (indented, no cursor)
                const color = this.writeScope === "project" && entry.group.scope === "user" ? "dim" : "muted";
                const subgroupLine = theme.fg(color, entry.subgroup.label);
                lines.push(truncateToWidth(`    ${subgroupLine}`, width, ""));
            }
            else {
                // Resource item (cursor only on items)
                const item = entry.item;
                const cursor = isSelected ? "> " : "  ";
                const dimmed = this.isDimmedItem(item);
                const nameText = isSelected && !dimmed ? theme.bold(item.displayName) : item.displayName;
                const name = dimmed ? theme.fg("dim", nameText) : nameText;
                lines.push(truncateToWidth(`${cursor}    ${this.renderCheckbox(item)} ${name}${this.getItemSuffix(item)}`, width, "..."));
            }
        }
        // Scroll indicator
        if (startIndex > 0 || endIndex < this.filteredItems.length) {
            const itemCount = this.filteredItems.filter((e) => e.type === "item").length;
            const currentItemIndex = this.filteredItems.slice(0, this.selectedIndex).filter((e) => e.type === "item").length + 1;
            lines.push(theme.fg("dim", `  (${currentItemIndex}/${itemCount})`));
        }
        return lines;
    }
    handleInput(data) {
        const kb = getKeybindings();
        if (kb.matches(data, "tui.select.up")) {
            this.selectedIndex = this.findNextItem(this.selectedIndex, -1);
            return;
        }
        if (kb.matches(data, "tui.select.down")) {
            this.selectedIndex = this.findNextItem(this.selectedIndex, 1);
            return;
        }
        if (kb.matches(data, "tui.select.pageUp")) {
            // Jump up by maxVisible, then find nearest item
            let target = Math.max(0, this.selectedIndex - this.maxVisible);
            while (target < this.filteredItems.length && this.filteredItems[target].type !== "item") {
                target++;
            }
            if (target < this.filteredItems.length) {
                this.selectedIndex = target;
            }
            return;
        }
        if (kb.matches(data, "tui.select.pageDown")) {
            // Jump down by maxVisible, then find nearest item
            let target = Math.min(this.filteredItems.length - 1, this.selectedIndex + this.maxVisible);
            while (target >= 0 && this.filteredItems[target].type !== "item") {
                target--;
            }
            if (target >= 0) {
                this.selectedIndex = target;
            }
            return;
        }
        if (kb.matches(data, "tui.select.cancel")) {
            this.onCancel?.();
            return;
        }
        if (matchesKey(data, "ctrl+c")) {
            this.onExit?.();
            return;
        }
        if (kb.matches(data, "tui.input.tab")) {
            this.onSwitchMode?.();
            return;
        }
        if (data === " " || kb.matches(data, "tui.select.confirm")) {
            const entry = this.filteredItems[this.selectedIndex];
            if (entry?.type === "item" && (this.writeScope === "project" || this.getItemScope(entry.item) === "user")) {
                const newEnabled = this.toggleResource(entry.item);
                if (newEnabled !== undefined) {
                    this.updateItem(entry.item, newEnabled);
                    this.onToggle?.(entry.item, newEnabled);
                }
            }
            return;
        }
        // Pass to search input
        this.searchInput.handleInput(data);
        this.filterItems(this.searchInput.getValue());
    }
    toggleResource(item) {
        if (this.writeScope === "project") {
            const state = this.getNextOverrideState(item);
            if (!this.setProjectResourceOverride(item, state))
                return undefined;
            return state === "inherit" ? this.getInheritedEnabled(item) : state === "load";
        }
        const enabled = !item.enabled;
        if (item.metadata.origin === "top-level") {
            this.toggleTopLevelResource(item, enabled);
        }
        else {
            this.togglePackageResource(item, enabled);
        }
        return enabled;
    }
    toggleTopLevelResource(item, enabled) {
        const scope = item.metadata.scope;
        const settings = scope === "project" ? this.settingsManager.getProjectSettings() : this.settingsManager.getGlobalSettings();
        const arrayKey = item.resourceType;
        const current = (settings[arrayKey] ?? []);
        // Generate pattern for this resource
        const pattern = this.getResourcePattern(item);
        const disablePattern = `-${pattern}`;
        const enablePattern = `+${pattern}`;
        // Filter out existing patterns for this resource
        const updated = current.filter((p) => {
            const stripped = p.startsWith("!") || p.startsWith("+") || p.startsWith("-") ? p.slice(1) : p;
            return stripped !== pattern;
        });
        if (enabled) {
            updated.push(enablePattern);
        }
        else {
            updated.push(disablePattern);
        }
        if (scope === "project") {
            if (arrayKey === "extensions") {
                this.settingsManager.setProjectExtensionPaths(updated);
            }
            else if (arrayKey === "skills") {
                this.settingsManager.setProjectSkillPaths(updated);
            }
            else if (arrayKey === "prompts") {
                this.settingsManager.setProjectPromptTemplatePaths(updated);
            }
            else if (arrayKey === "themes") {
                this.settingsManager.setProjectThemePaths(updated);
            }
        }
        else {
            if (arrayKey === "extensions") {
                this.settingsManager.setExtensionPaths(updated);
            }
            else if (arrayKey === "skills") {
                this.settingsManager.setSkillPaths(updated);
            }
            else if (arrayKey === "prompts") {
                this.settingsManager.setPromptTemplatePaths(updated);
            }
            else if (arrayKey === "themes") {
                this.settingsManager.setThemePaths(updated);
            }
        }
    }
    togglePackageResource(item, enabled) {
        const scope = item.metadata.scope;
        const settings = scope === "project" ? this.settingsManager.getProjectSettings() : this.settingsManager.getGlobalSettings();
        const packages = [...(settings.packages ?? [])];
        const pkgIndex = packages.findIndex((pkg) => {
            const source = typeof pkg === "string" ? pkg : pkg.source;
            return source === item.metadata.source;
        });
        if (pkgIndex === -1)
            return;
        let pkg = packages[pkgIndex];
        // Convert string to object form if needed
        if (typeof pkg === "string") {
            pkg = { source: pkg };
            packages[pkgIndex] = pkg;
        }
        // Get the resource array for this type
        const arrayKey = item.resourceType;
        const current = (pkg[arrayKey] ?? []);
        // Generate pattern relative to package root
        const pattern = this.getPackageResourcePattern(item);
        const disablePattern = `-${pattern}`;
        const enablePattern = `+${pattern}`;
        // Filter out existing patterns for this resource
        const updated = current.filter((p) => {
            const stripped = p.startsWith("!") || p.startsWith("+") || p.startsWith("-") ? p.slice(1) : p;
            return stripped !== pattern;
        });
        if (enabled) {
            updated.push(enablePattern);
        }
        else {
            updated.push(disablePattern);
        }
        pkg[arrayKey] = updated.length > 0 ? updated : undefined;
        // Clean up empty filter object
        const hasFilters = ["extensions", "skills", "prompts", "themes"].some((k) => pkg[k] !== undefined);
        if (!hasFilters) {
            packages[pkgIndex] = pkg.source;
        }
        if (scope === "project") {
            this.settingsManager.setProjectPackages(packages);
        }
        else {
            this.settingsManager.setPackages(packages);
        }
    }
    renderCheckbox(item) {
        if (this.writeScope === "project") {
            const state = this.getProjectOverrideState(item);
            if (state === "load")
                return theme.fg("success", "[+]");
            if (state === "unload")
                return theme.fg("warning", "[-]");
            return theme.fg("dim", item.enabled ? "[x]" : "[ ]");
        }
        return item.enabled ? theme.fg("success", "[x]") : theme.fg("dim", "[ ]");
    }
    getItemSuffix(item) {
        if (this.writeScope !== "project")
            return "";
        const state = this.getProjectOverrideState(item);
        if (state === "load")
            return theme.fg("muted", "  project load");
        if (state === "unload")
            return theme.fg("muted", "  project unload");
        return this.isInheritedGlobalItem(item) ? theme.fg("dim", "  inherited global") : "";
    }
    isDimmedItem(item) {
        return (this.writeScope === "project" &&
            this.isInheritedGlobalItem(item) &&
            this.getProjectOverrideState(item) === "inherit");
    }
    setProjectResourceOverride(item, state) {
        return item.metadata.origin === "top-level"
            ? this.setProjectTopLevelOverride(item, state)
            : this.setProjectPackageOverride(item, state);
    }
    setProjectTopLevelOverride(item, state) {
        const current = (this.settingsManager.getProjectSettings()[item.resourceType] ?? []);
        const pattern = this.isInheritedGlobalItem(item) ? item.path : this.getResourcePatternForScope(item, "project");
        const patterns = this.getTopLevelOverridePatterns(item, "project");
        const updated = current.filter((entry) => {
            const target = this.getPatternEntryTarget(entry);
            if ((entry.startsWith("!") || entry.startsWith("+") || entry.startsWith("-")) && patterns.has(target))
                return false;
            return !(state === "inherit" && this.isInheritedGlobalItem(item) && target === pattern);
        });
        if (state !== "inherit") {
            if (this.isInheritedGlobalItem(item) && !updated.includes(pattern))
                updated.push(pattern);
            updated.push(`${state === "load" ? "+" : "-"}${pattern}`);
        }
        this.setProjectTopLevelPaths(item.resourceType, updated);
        return true;
    }
    setProjectTopLevelPaths(key, paths) {
        if (key === "extensions")
            this.settingsManager.setProjectExtensionPaths(paths);
        else if (key === "skills")
            this.settingsManager.setProjectSkillPaths(paths);
        else if (key === "prompts")
            this.settingsManager.setProjectPromptTemplatePaths(paths);
        else
            this.settingsManager.setProjectThemePaths(paths);
    }
    setProjectPackageOverride(item, state) {
        const packages = [...(this.settingsManager.getProjectSettings().packages ?? [])];
        let pkgIndex = packages.findIndex((pkg) => this.packageSourceStringMatches(item.metadata.source, this.getItemScope(item), typeof pkg === "string" ? pkg : pkg.source, "project"));
        if (pkgIndex === -1) {
            if (state === "inherit")
                return false;
            packages.push(this.createPackageOverrideSource(item));
            pkgIndex = packages.length - 1;
        }
        let pkg = packages[pkgIndex];
        if (pkg === undefined)
            return false;
        if (typeof pkg === "string") {
            pkg = { source: pkg };
            packages[pkgIndex] = pkg;
        }
        const pattern = this.getPackageResourcePattern(item);
        const updated = (pkg[item.resourceType] ?? []).filter((entry) => this.getPatternEntryTarget(entry) !== pattern);
        if (state !== "inherit")
            updated.push(`${state === "load" ? "+" : "-"}${pattern}`);
        pkg[item.resourceType] = updated.length > 0 ? updated : undefined;
        if (!RESOURCE_TYPES.some((key) => pkg[key] !== undefined)) {
            if (pkg.autoload === false)
                packages.splice(pkgIndex, 1);
            else
                packages[pkgIndex] = pkg.source;
        }
        this.settingsManager.setProjectPackages(packages);
        return true;
    }
    getNextOverrideState(item) {
        const state = this.getProjectOverrideState(item);
        const inheritedEnabled = this.getInheritedEnabled(item);
        if (state === "inherit")
            return inheritedEnabled ? "unload" : "load";
        if (state === "unload")
            return inheritedEnabled ? "load" : "inherit";
        return inheritedEnabled ? "inherit" : "unload";
    }
    getProjectOverrideState(item) {
        if (this.writeScope !== "project")
            return "inherit";
        if (item.metadata.origin === "top-level") {
            return this.getOverrideStateFromEntries((this.settingsManager.getProjectSettings()[item.resourceType] ?? []), this.getTopLevelOverridePatterns(item, "project"), false);
        }
        const pkg = this.findMatchingPackageSource(item, "project");
        if (typeof pkg !== "object")
            return "inherit";
        const entries = pkg[item.resourceType];
        if (entries === undefined)
            return "inherit";
        return this.getOverrideStateFromEntries(entries, new Set([this.getPackageResourcePattern(item)]), pkg.autoload !== false);
    }
    getOverrideStateFromEntries(entries, patterns, emptyArrayIsUnload) {
        if (entries.length === 0 && emptyArrayIsUnload)
            return "unload";
        let state = "inherit";
        for (const entry of entries) {
            if (!patterns.has(this.getPatternEntryTarget(entry)))
                continue;
            if (entry.startsWith("!") || entry.startsWith("-"))
                state = "unload";
            else
                state = "load";
        }
        return state;
    }
    getInheritedEnabled(item) {
        return (this.inheritedEnabledByKey.get(this.getResourceItemKey(item)) ??
            (this.getItemScope(item) === "user" ? item.enabled : true));
    }
    isInheritedGlobalItem(item) {
        return this.getItemScope(item) === "user" || this.inheritedEnabledByKey.has(this.getResourceItemKey(item));
    }
    getTopLevelOverridePatterns(item, scope) {
        const baseDir = this.getTopLevelBaseDir(scope);
        const patterns = new Set([
            this.getResourcePatternForScope(item, scope),
            item.path,
            relative(baseDir, item.path),
        ]);
        if (item.metadata.baseDir)
            patterns.add(relative(item.metadata.baseDir, item.path));
        return patterns;
    }
    getResourcePatternForScope(item, scope) {
        const sourceScope = this.getItemScope(item);
        if (scope !== sourceScope)
            return item.path;
        const baseDir = item.metadata.baseDir ?? this.getTopLevelBaseDir(sourceScope);
        return relative(baseDir, item.path);
    }
    createPackageOverrideSource(item) {
        const source = item.metadata.source;
        if (!isLocalPath(source))
            return { source, autoload: false };
        const sourcePath = resolvePath(source, this.getTopLevelBaseDir(this.getItemScope(item)), { trim: true });
        return { source: relative(this.getTopLevelBaseDir("project"), sourcePath) || ".", autoload: false };
    }
    packageSourceStringMatches(leftSource, leftScope, rightSource, rightScope) {
        if (leftSource === rightSource)
            return true;
        if (!isLocalPath(leftSource) || !isLocalPath(rightSource))
            return false;
        const left = resolvePath(leftSource, this.getTopLevelBaseDir(leftScope), { trim: true });
        const right = resolvePath(rightSource, this.getTopLevelBaseDir(rightScope), { trim: true });
        return left === right;
    }
    findMatchingPackageSource(item, targetScope) {
        const settings = targetScope === "project"
            ? this.settingsManager.getProjectSettings()
            : this.settingsManager.getGlobalSettings();
        return (settings.packages ?? []).find((pkg) => this.packageSourceStringMatches(item.metadata.source, this.getItemScope(item), typeof pkg === "string" ? pkg : pkg.source, targetScope));
    }
    getPatternEntryTarget(entry) {
        return entry.startsWith("!") || entry.startsWith("+") || entry.startsWith("-") ? entry.slice(1) : entry;
    }
    getResourceItemKey(item) {
        return `${item.resourceType}:${canonicalizePath(item.path)}`;
    }
    getItemScope(item) {
        return item.metadata.scope === "project" ? "project" : "user";
    }
    getTopLevelBaseDir(scope) {
        return scope === "project" ? join(this.cwd, CONFIG_DIR_NAME) : this.agentDir;
    }
    getResourcePattern(item) {
        const scope = item.metadata.scope;
        const baseDir = item.metadata.baseDir ?? this.getTopLevelBaseDir(scope);
        return relative(baseDir, item.path);
    }
    getPackageResourcePattern(item) {
        const baseDir = item.metadata.baseDir ?? dirname(item.path);
        return relative(baseDir, item.path);
    }
}
export class ConfigSelectorComponent extends Container {
    header;
    resourceList;
    writeScope;
    _focused = false;
    get focused() {
        return this._focused;
    }
    set focused(value) {
        this._focused = value;
        this.resourceList.focused = value;
    }
    constructor(resolvedPaths, settingsManager, cwd, agentDir, onClose, onExit, requestRender, terminalHeight, writeScope = "global", projectModeAvailable = true) {
        super();
        this.writeScope = writeScope;
        const groupsByScope = {
            global: buildGroups(resolvedPaths.global, agentDir),
            project: buildGroups(resolvedPaths.project, agentDir),
        };
        // Add header
        this.addChild(new Spacer(1));
        this.addChild(new DynamicBorder());
        this.addChild(new Spacer(1));
        this.header = new ConfigSelectorHeader(this.writeScope, projectModeAvailable);
        this.addChild(this.header);
        this.addChild(new Spacer(1));
        // Resource list
        this.resourceList = new ResourceList(groupsByScope, settingsManager, cwd, agentDir, terminalHeight, this.writeScope);
        this.resourceList.onCancel = onClose;
        this.resourceList.onExit = onExit;
        this.resourceList.onToggle = () => requestRender();
        if (projectModeAvailable) {
            this.resourceList.onSwitchMode = () => {
                this.switchWriteScope();
                requestRender();
            };
        }
        this.addChild(this.resourceList);
        // Bottom border
        this.addChild(new Spacer(1));
        this.addChild(new DynamicBorder());
    }
    switchWriteScope() {
        this.writeScope = this.writeScope === "global" ? "project" : "global";
        this.header.setWriteScope(this.writeScope);
        this.resourceList.setWriteScope(this.writeScope);
    }
    getResourceList() {
        return this.resourceList;
    }
}
//# sourceMappingURL=config-selector.js.map