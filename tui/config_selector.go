package tui

// config_selector.go: resource configuration picker.
//
// Ports upstream config-selector.ts (592 LOC).
// Manages package resources (extensions, skills, prompts, themes)
// with enable/disable toggling and search filtering.
//
// The component is a Container of upstream's constructor children; the
// header and the resource list are private children reading its state.
// The data model (groups, subgroups, items) mirrors upstream exactly.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ResourceType identifies the kind of package resource.
type ResourceType string

const (
	ResourceExtensions ResourceType = "extensions"
	ResourceSkills     ResourceType = "skills"
	ResourcePrompts    ResourceType = "prompts"
	ResourceThemes     ResourceType = "themes"
)

var resourceTypeLabels = map[ResourceType]string{
	ResourceExtensions: "Extensions",
	ResourceSkills:     "Skills",
	ResourcePrompts:    "Prompts",
	ResourceThemes:     "Themes",
}

// ResourceItem is a single toggleable resource.
type ResourceItem struct {
	Path         string
	Enabled      bool
	ResourceType ResourceType
	DisplayName  string
	GroupKey     string
	SubgroupKey  string
	Scope        string // "user" or "project"
	Origin       string // "package" or "top-level"
	Source       string
	BaseDir      string // for package-relative pattern generation
	// PackageRoot is the root of the package a resource belongs to (package-manager.ts PathMetadata.packageRoot): set for a local
	// directory or an installed npm or git package, empty for a local file source, whose BaseDir is only the file's directory.
	PackageRoot      string
	Pattern          string // authored Package-relative member path
	Health           string // empty or "missing"
	Override         string // "inherit", "load", or "unload" in project mode
	Inherited        bool
	InheritedEnabled bool
}

// ResourceSubgroup groups items by resource type within a group.
type ResourceSubgroup struct {
	Type  ResourceType
	Label string
	Items []*ResourceItem
}

// ResourceGroup is a top-level grouping (by origin + scope + source).
type ResourceGroup struct {
	Key       string
	Label     string
	Scope     string
	Origin    string
	Source    string
	Subgroups []*ResourceSubgroup
}

// flatEntry is a tagged union for the flat list rendering.
type flatEntry struct {
	entryType string // "group", "subgroup", "item"
	group     *ResourceGroup
	subgroup  *ResourceSubgroup
	item      *ResourceItem
}

// ConfigSelectorComponent manages the resource configuration overlay.
type ConfigSelectorComponent struct {
	Container
	groupsByScope        map[string][]*ResourceGroup
	groups               []*ResourceGroup
	writeScope           string
	projectModeAvailable bool
	flatItems            []flatEntry
	filtered             []flatEntry
	cursor               int
	maxVisible           int
	terminalRows         int
	input                *TextInput
	OnCancel             func()
	OnExit               func()
	OnToggle             func(item *ResourceItem, enabled bool)
	OnOverride           func(item *ResourceItem, state string) error
}

// NewConfigSelector creates a config selector from resolved resource groups.
func NewConfigSelector(groups []*ResourceGroup, terminalRows int) *ConfigSelectorComponent {
	return NewScopedConfigSelector(groups, groups, terminalRows, "global", true)
}

// NewScopedConfigSelector creates the global/project selector used by pig config.
func NewScopedConfigSelector(global, project []*ResourceGroup, terminalRows int, writeScope string, projectModeAvailable bool) *ConfigSelectorComponent {
	if writeScope != "project" {
		writeScope = "global"
	}
	cs := &ConfigSelectorComponent{
		groupsByScope: map[string][]*ResourceGroup{"global": global, "project": project},
		groups:        global, writeScope: writeScope, projectModeAvailable: projectModeAvailable,
		maxVisible: configSelectorMaxVisible(terminalRows), terminalRows: terminalRows,
		input: NewInput(InputOptions{}), // config-selector.ts:243 `new Input()`: callback mode, so Enter without onSubmit leaves it editable
	}
	cs.input.Focused = true
	cs.groups = cs.groupsByScope[writeScope]
	cs.buildFlatList()
	cs.filtered = append([]flatEntry{}, cs.flatItems...)
	// config-selector.ts:910-939 constructor children.
	cs.Add(NewSpacer(1))
	cs.Add(NewDynamicBorder())
	cs.Add(NewSpacer(1))
	cs.Add(configSelectorHeader{cs})
	cs.Add(NewSpacer(1))
	cs.Add(configResourceList{cs})
	cs.Add(NewSpacer(1))
	cs.Add(NewDynamicBorder())
	return cs
}

// query returns the current filter query (current TextInput value).
func (cs *ConfigSelectorComponent) query() string {
	if cs.input == nil {
		return ""
	}
	return cs.input.Text()
}

func (cs *ConfigSelectorComponent) buildFlatList() {
	cs.flatItems = nil
	for _, g := range cs.groups {
		cs.flatItems = append(cs.flatItems, flatEntry{entryType: "group", group: g})
		for _, sg := range g.Subgroups {
			cs.flatItems = append(cs.flatItems, flatEntry{entryType: "subgroup", subgroup: sg, group: g})
			for _, item := range sg.Items {
				cs.flatItems = append(cs.flatItems, flatEntry{entryType: "item", item: item})
			}
		}
	}
	// Mirrors upstream buildFlatList (config-selector.ts): start selection
	// on the first item entry (skipping group/subgroup headers). Earlier
	// pig called selectFirstItem here, which iterated cs.filtered: empty
	// at construction time: so the initial cursor never landed on an
	// item and the overlay opened without a visible `>` pointer.
	cs.cursor = 0
	for i, e := range cs.flatItems {
		if e.entryType == "item" {
			cs.cursor = i
			return
		}
	}
}

func (cs *ConfigSelectorComponent) selectFirstItem() {
	for i, e := range cs.filtered {
		if e.entryType == "item" {
			cs.cursor = i
			return
		}
	}
	cs.cursor = 0
}

// SetTerminalRows updates the selector's view budget to track terminal height.
func (cs *ConfigSelectorComponent) SetTerminalRows(rows int) {
	cs.terminalRows = rows
	cs.maxVisible = configSelectorMaxVisible(rows)
}

func configSelectorMaxVisible(terminalRows int) int {
	if terminalRows <= 0 {
		terminalRows = 24
	}
	const chromeRows = 8
	return max(5, terminalRows-chromeRows)
}

func (cs *ConfigSelectorComponent) findNextItem(from, direction int) int {
	idx := from + direction
	for idx >= 0 && idx < len(cs.filtered) {
		if cs.filtered[idx].entryType == "item" {
			return idx
		}
		idx += direction
	}
	return from
}

func (cs *ConfigSelectorComponent) filterItems() {
	q := strings.TrimSpace(strings.ToLower(cs.query()))
	if q == "" {
		cs.filtered = append([]flatEntry{}, cs.flatItems...)
		cs.selectFirstItem()
		return
	}

	matching := make(map[*ResourceItem]bool)
	matchingSG := make(map[*ResourceSubgroup]bool)
	matchingG := make(map[*ResourceGroup]bool)

	for _, e := range cs.flatItems {
		if e.entryType == "item" {
			item := e.item
			if strings.Contains(strings.ToLower(item.DisplayName), q) ||
				strings.Contains(strings.ToLower(string(item.ResourceType)), q) ||
				strings.Contains(strings.ToLower(item.Path), q) {
				matching[item] = true
			}
		}
	}

	for _, g := range cs.groups {
		for _, sg := range g.Subgroups {
			for _, item := range sg.Items {
				if matching[item] {
					matchingSG[sg] = true
					matchingG[g] = true
				}
			}
		}
	}

	cs.filtered = nil
	for _, e := range cs.flatItems {
		switch e.entryType {
		case "group":
			if matchingG[e.group] {
				cs.filtered = append(cs.filtered, e)
			}
		case "subgroup":
			if matchingSG[e.subgroup] {
				cs.filtered = append(cs.filtered, e)
			}
		case "item":
			if matching[e.item] {
				cs.filtered = append(cs.filtered, e)
			}
		}
	}
	cs.selectFirstItem()
}

// configSelectorHeader is the title, key hints and scope hint rows (config-selector.ts ConfigSelectorHeader). It reads the owner's write scope on every render, so it is not cached by the parent Container.
type configSelectorHeader struct{ cs *ConfigSelectorComponent }

func (configSelectorHeader) Invalidate() {}

func (h configSelectorHeader) Render(width int) []string {
	cs := h.cs
	t := ActiveTheme()
	var lines []string
	// Header mirrors upstream global/project write-scope ownership.
	titleText := "Global Resources"
	scopeHint := "~/.pig/agent/settings.json"
	action := "toggle"
	if cs.writeScope == "project" {
		titleText = "Project Local Resources"
		scopeHint = ".pig/settings.json · inherited global resources are dimmed"
		action = "cycle inherit/+/-"
	}
	title := "\x1b[1m" + titleText + "\x1b[22m"
	sep := t.Muted + " · " + "\x1b[39m"
	hint := RawKeyHint("space", action) + sep + RawKeyHint("esc", "close")
	if cs.projectModeAvailable {
		hint = RawKeyHint("tab", "switch mode") + sep + hint
	}
	titleW := widthx.VisibleWidth(title)
	hintW := widthx.VisibleWidth(hint)
	spacing := max(1, width-titleW-hintW)
	// Upstream ConfigSelectorHeader and ResourceList truncate every row to the
	// width (items with "...", the rest without an ellipsis).
	fit := func(line string) string { return widthx.TruncateToWidth(line, width, "", false) }
	lines = append(lines, fit(title+strings.Repeat(" ", spacing)+hint))
	lines = append(lines, fit(t.Muted+scopeHint+"\x1b[39m"))

	return lines
}

// configResourceList is the search input and the resource rows (config-selector.ts ResourceList). It reads the owner's filter state on every render.
type configResourceList struct{ cs *ConfigSelectorComponent }

func (configResourceList) Invalidate() {}

func (l configResourceList) Render(width int) []string {
	cs := l.cs
	t := ActiveTheme()
	fit := func(line string) string { return widthx.TruncateToWidth(line, width, "", false) }
	var lines []string
	// Search input: mirrors upstream config-selector.ts which renders
	// `this.searchInput.render(width)` (an Input component → `> <value>`
	// surface). Using TextInput directly here keeps the prompt prefix and
	// cursor rendering aligned with upstream's bare Input component.
	lines = append(lines, cs.input.Render(width)...)
	lines = append(lines, "")

	if len(cs.filtered) == 0 {
		lines = append(lines, fit(t.Muted+"  No resources found"+"\x1b[39m"))
	} else {
		// Calculate visible range.
		start := max(0, min(cs.cursor-cs.maxVisible/2, len(cs.filtered)-cs.maxVisible))
		end := min(start+cs.maxVisible, len(cs.filtered))

		for i := start; i < end; i++ {
			e := cs.filtered[i]
			isSelected := i == cs.cursor

			switch e.entryType {
			case "group":
				label := e.group.Label
				color := t.Accent
				if cs.writeScope == "project" && e.group.Scope == "user" {
					label += " · inherited global"
					color = t.Dim
				}
				gl := color + "\x1b[1m" + label + "\x1b[22m\x1b[39m"
				lines = append(lines, fit("  "+gl))
			case "subgroup":
				color := t.Muted
				if cs.writeScope == "project" && e.group.Scope == "user" {
					color = t.Dim
				}
				sgl := color + e.subgroup.Label + "\x1b[39m"
				lines = append(lines, fit("    "+sgl))
			case "item":
				cursor := "  "
				if isSelected {
					cursor = "> "
				}
				dimmed := cs.writeScope == "project" && e.item.Inherited && e.item.Override == "inherit"
				// upstream renderCheckbox: outside the project scope an enabled item is green; in it only an override is coloured and an inherited state is dim, enabled or not.
				checkbox := t.Dim + "[ ]" + "\x1b[39m"
				switch {
				case cs.writeScope == "project" && e.item.Enabled:
					checkbox = t.Dim + "[x]" + "\x1b[39m"
				case cs.writeScope != "project" && e.item.Enabled:
					checkbox = t.Success + "[x]" + "\x1b[39m"
				}
				suffix := ""
				if cs.writeScope == "project" {
					switch e.item.Override {
					case "load":
						checkbox = t.Success + "[+]" + "\x1b[39m"
						suffix = t.Muted + "  project load" + "\x1b[39m"
					case "unload":
						checkbox = t.Warning + "[-]" + "\x1b[39m"
						suffix = t.Muted + "  project unload" + "\x1b[39m"
					case "inherit":
						if e.item.Inherited {
							suffix = t.Dim + "  inherited global" + "\x1b[39m"
						}
					}
				}
				name := e.item.DisplayName
				if isSelected && !dimmed {
					name = "\x1b[1m" + name + "\x1b[22m"
				}
				if dimmed {
					name = t.Dim + name + "\x1b[39m"
				}
				status := ""
				if e.item.Health != "" {
					status = t.Warning + "  " + e.item.Health + "\x1b[39m"
				}
				lines = append(lines, widthx.TruncateToWidth(cursor+"    "+checkbox+" "+name+suffix+status, width, "...", false))
			}
		}

		// Scroll indicator.
		if start > 0 || end < len(cs.filtered) {
			itemCount := 0
			currentItemIndex := 0
			for i, e := range cs.filtered {
				if e.entryType != "item" {
					continue
				}
				itemCount++
				if i <= cs.cursor {
					currentItemIndex = itemCount
				}
			}
			if itemCount > 0 {
				lines = append(lines, fit(t.Dim+fmt.Sprintf("  (%d/%d)", currentItemIndex, itemCount)+"\x1b[39m"))
			}
		}
	}

	return lines
}

// HandleInput processes keyboard input.
func (cs *ConfigSelectorComponent) HandleInput(data string) {
	// Route through the TUI keybinding registry.
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBSelectUp):
		cs.cursor = cs.findNextItem(cs.cursor, -1)
	case kb.Matches(data, KBSelectDown):
		cs.cursor = cs.findNextItem(cs.cursor, 1)
	case kb.Matches(data, KBSelectPageUp):
		target := max(0, cs.cursor-cs.maxVisible)
		for target < len(cs.filtered) && cs.filtered[target].entryType != "item" {
			target++
		}
		if target < len(cs.filtered) {
			cs.cursor = target
		}
	case kb.Matches(data, KBSelectPageDown):
		target := min(len(cs.filtered)-1, cs.cursor+cs.maxVisible)
		for target >= 0 && cs.filtered[target].entryType != "item" {
			target--
		}
		if target >= 0 {
			cs.cursor = target
		}
	case kb.Matches(data, KBSelectCancel): // config-selector.ts:487: tui.select.cancel (Esc, and Ctrl+C by default)
		if cs.OnCancel != nil {
			cs.OnCancel()
		}
	case MatchesKeyID(data, Key.Ctrl("c")): // config-selector.ts:491: Ctrl+C when tui.select.cancel is rebound; CSI-u under the Kitty protocol
		if cs.OnExit != nil {
			cs.OnExit()
		}
	case kb.Matches(data, KBInputTab): // config-selector.ts:495-498: Tab never reaches the search input; onSwitchMode is set only when project mode is available
		if !cs.projectModeAvailable {
			break
		}
		if cs.writeScope == "global" {
			cs.writeScope = "project"
		} else {
			cs.writeScope = "global"
		}
		cs.groups = cs.groupsByScope[cs.writeScope]
		cs.buildFlatList()
		cs.filterItems()
	case data == " " || kb.Matches(data, KBSelectConfirm): // Space or Enter: toggle
		if cs.cursor >= 0 && cs.cursor < len(cs.filtered) {
			e := cs.filtered[cs.cursor]
			if e.entryType == "item" {
				if cs.writeScope == "project" {
					state := nextProjectOverride(e.item.Override, e.item.InheritedEnabled)
					if cs.OnOverride == nil || cs.OnOverride(e.item, state) == nil {
						e.item.Override = state
						if state == "inherit" {
							e.item.Enabled = e.item.InheritedEnabled
						} else {
							e.item.Enabled = state == "load"
						}
					}
				} else {
					e.item.Enabled = !e.item.Enabled
					if cs.OnToggle != nil {
						cs.OnToggle(e.item, e.item.Enabled)
					}
				}
			}
		}
	default:
		// config-selector.ts:518-519: every key that reaches the search Input re-filters, so a key that
		// leaves the query unchanged still resets the selection to the first item.
		cs.input.HandleInput(data)
		cs.filterItems()
	}
	cs.Invalidate()
}

func nextProjectOverride(current string, inheritedEnabled bool) string {
	switch current {
	case "load":
		if inheritedEnabled {
			return "inherit"
		}
		return "unload"
	case "unload":
		if inheritedEnabled {
			return "load"
		}
		return "inherit"
	default:
		if inheritedEnabled {
			return "unload"
		}
		return "load"
	}
}

// BuildResourceGroups constructs groups from resolved resource data.
// This is a helper for callers that have raw path lists.
func BuildResourceGroups(resources []ResourceItem) []*ResourceGroup {
	groupMap := make(map[string]*ResourceGroup)

	for i := range resources {
		res := &resources[i]
		baseDirKey := ""
		if res.BaseDir != "" {
			baseDirKey = filepath.Clean(res.BaseDir)
		}
		key := res.Origin + ":" + res.Scope + ":" + res.Source + ":" + baseDirKey
		res.GroupKey = key
		res.SubgroupKey = key + ":" + string(res.ResourceType)

		g, ok := groupMap[key]
		if !ok {
			g = &ResourceGroup{
				Key:    key,
				Label:  groupLabel(res),
				Scope:  res.Scope,
				Origin: res.Origin,
				Source: res.Source,
			}
			groupMap[key] = g
		}

		var sg *ResourceSubgroup
		for _, s := range g.Subgroups {
			if s.Type == res.ResourceType {
				sg = s
				break
			}
		}
		if sg == nil {
			sg = &ResourceSubgroup{
				Type:  res.ResourceType,
				Label: resourceTypeLabels[res.ResourceType],
			}
			g.Subgroups = append(g.Subgroups, sg)
		}

		// Display name. A built-in extension is named without the `builtin:` prefix
		// (config-selector.ts:138-139).
		if res.DisplayName == "" {
			name := filepath.Base(res.Path)
			parent := filepath.Base(filepath.Dir(res.Path))
			switch {
			case res.Source == "builtin":
				name = strings.TrimPrefix(res.Path, "builtin:")
			case res.ResourceType == ResourceExtensions:
				if parent != "extensions" {
					name = parent + "/" + name
				}
			case res.ResourceType == ResourceSkills:
				if name == "SKILL.md" {
					name = parent
				}
			}
			res.DisplayName = name
		}

		sg.Items = append(sg.Items, res)
	}

	// Collect and sort groups.
	groups := make([]*ResourceGroup, 0, len(groupMap))
	for _, g := range groupMap {
		groups = append(groups, g)
	}
	// Sort: packages before top-level, user before project.
	sortGroups(groups)
	return groups
}

func groupLabel(res *ResourceItem) string {
	if res.Origin == "package" {
		return res.Source + " (" + res.Scope + ")"
	}
	if res.Source == "builtin" {
		if res.Scope == "user" {
			return "Built-in"
		}
		return "Built-in (project override)"
	}
	if res.Source == "auto" {
		if res.BaseDir != "" {
			if res.Scope == "user" {
				return "User (" + formatBaseDir(res.BaseDir) + ")"
			}
			return "Project (" + formatBaseDir(res.BaseDir) + ")"
		}
		if res.Scope == "user" {
			return "User (~/.pig/agent/)"
		}
		return "Project (.pig/)"
	}
	if res.Scope == "user" {
		return "User settings"
	}
	return "Project settings"
}

func formatBaseDir(baseDir string) string {
	homeDir, err := os.UserHomeDir()
	if err == nil && homeDir != "" {
		homeDir = filepath.Clean(homeDir)
		baseDir = filepath.Clean(baseDir)
		slashHome := filepath.ToSlash(homeDir)
		slashBase := filepath.ToSlash(baseDir)
		switch {
		case slashBase == slashHome:
			baseDir = "~"
		case strings.HasPrefix(slashBase, slashHome+"/"):
			baseDir = "~" + slashBase[len(slashHome):]
		default:
			baseDir = slashBase
		}
	} else {
		baseDir = filepath.ToSlash(filepath.Clean(baseDir))
	}
	if !strings.HasSuffix(baseDir, "/") {
		baseDir += "/"
	}
	return baseDir
}

func sortGroups(groups []*ResourceGroup) {
	// Simple insertion sort (small N).
	for i := 1; i < len(groups); i++ {
		for j := i; j > 0 && groupLess(groups[j], groups[j-1]); j-- {
			groups[j], groups[j-1] = groups[j-1], groups[j]
		}
	}
	typeOrder := map[ResourceType]int{
		ResourceExtensions: 0, ResourceSkills: 1,
		ResourcePrompts: 2, ResourceThemes: 3,
	}
	for _, g := range groups {
		// Sort subgroups by type order.
		for i := 1; i < len(g.Subgroups); i++ {
			for j := i; j > 0 && typeOrder[g.Subgroups[j].Type] < typeOrder[g.Subgroups[j-1].Type]; j-- {
				g.Subgroups[j], g.Subgroups[j-1] = g.Subgroups[j-1], g.Subgroups[j]
			}
		}
		// Sort items by name.
		for _, sg := range g.Subgroups {
			for i := 1; i < len(sg.Items); i++ {
				for j := i; j > 0 && sg.Items[j].DisplayName < sg.Items[j-1].DisplayName; j-- {
					sg.Items[j], sg.Items[j-1] = sg.Items[j-1], sg.Items[j]
				}
			}
		}
	}
}

func groupLess(a, b *ResourceGroup) bool {
	if a.Origin != b.Origin {
		return a.Origin == "package"
	}
	if a.Scope != b.Scope {
		return a.Scope == "user"
	}
	return a.Source < b.Source
}
