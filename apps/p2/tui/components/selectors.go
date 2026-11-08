package components

// Copied from coding-agent interactive/components/{dynamic-border,model-selector,thinking-selector}.ts with the instance theme and key hints
// (section 5.4). The model selector takes data and actions instead of `ModelRuntime` (section 11): the owner's catalog, the scope
// (`enabledModels`), the current and default model. It does not refresh provider catalogs (the server keeps them; `/login` is P6).
//
// pi: apps/p2/src/tui/components/selectors.ts

import (
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// KeyMatcher is key matching over pi's ids (the runtime's manager, installed with setKeybindings).
type KeyMatcher interface {
	Matches(data, id string) bool
}

// SelectorContext is a RenderContext with key matching.
type SelectorContext interface {
	render.RenderContext
	Keys() KeyMatcher
}

// DynamicBorder is a themed horizontal rule.
type DynamicBorder struct {
	ctx   render.RenderContext
	token string
}

// NewDynamicBorder takes the token "border", "borderAccent" or "borderMuted"; "" selects "border".
func NewDynamicBorder(ctx render.RenderContext, token string) *DynamicBorder {
	if token == "" {
		token = "border"
	}
	return &DynamicBorder{ctx: ctx, token: token}
}

// Invalidate has nothing to drop.
func (b *DynamicBorder) Invalidate() {}

// Render is one rule line.
func (b *DynamicBorder) Render(width int) []string {
	return []string{b.ctx.Theme().Current().Fg(b.token, strings.Repeat("─", max(1, width)))}
}

// SelectorModel is a model of the catalog.
type SelectorModel struct {
	Provider string
	ModelID  string
	Name     string
}

// ModelRef names a model.
type ModelRef struct {
	Provider string
	ModelID  string
}

func sameRef(a *ModelRef, model *SelectorModel) bool {
	return a != nil && model != nil && a.Provider == model.Provider && a.ModelID == model.ModelID
}

// searchText is coding-agent model-search.ts `getModelSelectorSearchText`.
func searchText(model *SelectorModel) string {
	return model.Provider + " " + model.Provider + "/" + model.ModelID + " " + model.Provider + " " + model.ModelID + " " + model.Name
}

func boolNumber(value bool) int {
	if value {
		return 1
	}
	return 0
}

func text(content string) *tui.Text { return tui.NewPaddedText(content, 0, 0, nil) }

// ModelSelectorOptions are the options of NewModelSelector.
type ModelSelectorOptions struct {
	Models            []SelectorModel
	Scoped            []SelectorModel
	Current           *ModelRef
	DefaultModel      *ModelRef
	InitialSearch     string
	Title             string
	OnSelect          func(model SelectorModel)
	OnSelectAsDefault func(model SelectorModel)
	OnCancel          func()
}

// ModelSelectorComponent picks a model, filtered by search and scope.
type ModelSelectorComponent struct {
	*tui.Container
	searchInput    *tui.TextInput
	listContainer  *tui.Container
	allModels      []*SelectorModel
	scopedModels   []*SelectorModel
	activeModels   []*SelectorModel
	filteredModels []*SelectorModel
	selectedIndex  int
	scope          string
	scopeText      *tui.Text
	ctx            SelectorContext
	opts           ModelSelectorOptions
}

// NewModelSelector is the ModelSelectorComponent constructor.
func NewModelSelector(ctx SelectorContext, options ModelSelectorOptions) *ModelSelectorComponent {
	c := &ModelSelectorComponent{
		Container: tui.NewContainer(), searchInput: tui.NewInput(tui.InputOptions{}), listContainer: tui.NewContainer(), ctx: ctx, opts: options,
	}
	models := make([]*SelectorModel, len(options.Models))
	for i := range options.Models {
		models[i] = &options.Models[i]
	}
	c.allModels = c.sortModels(models)
	for i := range options.Scoped {
		c.scopedModels = append(c.scopedModels, &options.Scoped[i])
	}
	c.scope = "all"
	if len(c.scopedModels) > 0 {
		c.scope = "scoped"
	}
	c.activeModels = c.allModels
	if c.scope == "scoped" {
		c.activeModels = c.scopedModels
	}
	theme := ctx.Theme().Current()
	c.AddChild(NewDynamicBorder(ctx, ""))
	c.AddChild(tui.NewSpacer(1))
	if options.Title != "" {
		c.AddChild(text(theme.Bold(options.Title)))
	}
	if len(c.scopedModels) > 0 {
		c.scopeText = text(c.getScopeText())
		c.AddChild(c.scopeText)
		c.AddChild(text(render.KeyHint(ctx, "tui.input.tab", "scope") + theme.Fg("muted", " (all/scoped)")))
	} else {
		c.AddChild(text(theme.Fg("warning", "Only showing models from signed-in providers. Use /login to add providers.")))
	}
	c.AddChild(tui.NewSpacer(1))
	if options.InitialSearch != "" {
		c.searchInput.SetValue(options.InitialSearch)
	}
	c.searchInput.OnSubmit = func(string) {
		if model := c.selectedModel(); model != nil {
			options.OnSelect(*model)
		}
	}
	c.AddChild(c.searchInput)
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(c.listContainer)
	c.AddChild(tui.NewSpacer(1))
	save := ""
	if options.OnSelectAsDefault != nil {
		save = " · " + ctx.KeyText("app.models.save") + " to set as default"
	}
	c.AddChild(text(theme.Fg("dim", "  "+ctx.KeyText("tui.select.confirm")+" to select"+save+" · "+ctx.KeyText("tui.select.cancel")+" to cancel")))
	c.AddChild(NewDynamicBorder(ctx, ""))
	c.filterModels(c.searchInput.GetValue())
	for i, model := range c.filteredModels {
		if sameRef(options.Current, model) {
			if options.InitialSearch == "" {
				c.selectedIndex = i
				c.updateList()
			}
			break
		}
	}
	return c
}

// SetFocused is the TypeScript's `focused` setter: the search input follows.
func (c *ModelSelectorComponent) SetFocused(focused bool) { c.searchInput.SetFocused(focused) }

func (c *ModelSelectorComponent) selectedModel() *SelectorModel {
	if c.selectedIndex < 0 || c.selectedIndex >= len(c.filteredModels) {
		return nil
	}
	return c.filteredModels[c.selectedIndex]
}

// sortModels puts the current model first, the default second, then by provider (pi).
func (c *ModelSelectorComponent) sortModels(models []*SelectorModel) []*SelectorModel {
	slices.SortStableFunc(models, func(a, b *SelectorModel) int {
		if d := boolNumber(sameRef(c.opts.Current, b)) - boolNumber(sameRef(c.opts.Current, a)); d != 0 {
			return d
		}
		if d := boolNumber(sameRef(c.opts.DefaultModel, b)) - boolNumber(sameRef(c.opts.DefaultModel, a)); d != 0 {
			return d
		}
		return localecompare.Compare(a.Provider, b.Provider)
	})
	return models
}

func (c *ModelSelectorComponent) getScopeText() string {
	theme := c.ctx.Theme().Current()
	label := func(scope string) string {
		token := "muted"
		if c.scope == scope {
			token = "accent"
		}
		return theme.Fg(token, scope)
	}
	return theme.Fg("muted", "Scope: ") + label("all") + theme.Fg("muted", " | ") + label("scoped")
}

func (c *ModelSelectorComponent) filterModels(query string) {
	if query != "" {
		filtered := tui.FuzzyFilter(c.activeModels, query, func(model *SelectorModel) string {
			if sameRef(c.opts.DefaultModel, model) {
				return searchText(model) + " default"
			}
			return searchText(model)
		})
		normalized := jsstring.ToLower(strings.TrimFunc(query, widthx.IsJSSpace))
		var defaults []*SelectorModel
		if normalized != "" && strings.HasPrefix("default", normalized) {
			for _, model := range c.activeModels {
				if sameRef(c.opts.DefaultModel, model) {
					defaults = append(defaults, model)
				}
			}
		}
		c.filteredModels = append([]*SelectorModel(nil), defaults...)
		for _, model := range filtered {
			if !slices.Contains(defaults, model) {
				c.filteredModels = append(c.filteredModels, model)
			}
		}
		c.selectedIndex = 0
	} else {
		c.filteredModels = c.activeModels
		c.selectedIndex = min(c.selectedIndex, max(0, len(c.filteredModels)-1))
	}
	c.updateList()
}

func (c *ModelSelectorComponent) updateList() {
	theme := c.ctx.Theme().Current()
	c.listContainer.Clear()
	const maxVisible = 10
	startIndex := max(0, min(c.selectedIndex-maxVisible/2, len(c.filteredModels)-maxVisible))
	endIndex := min(startIndex+maxVisible, len(c.filteredModels))
	for i := startIndex; i < endIndex; i++ {
		model := c.filteredModels[i]
		cursor, marker := "  ", "  "
		if i == c.selectedIndex {
			cursor = theme.Fg("accent", "→ ")
		}
		if sameRef(c.opts.Current, model) {
			marker = theme.Fg("accent", "✓ ")
		}
		id := render.LineText(model.ModelID)
		if i == c.selectedIndex {
			id = theme.Fg("accent", id)
		}
		badge := ""
		if sameRef(c.opts.DefaultModel, model) {
			badge = theme.Fg("muted", " · default")
		}
		c.listContainer.AddChild(text(cursor + marker + id + " " + theme.Fg("muted", "["+render.LineText(model.Provider)+"]") + badge))
	}
	if startIndex > 0 || endIndex < len(c.filteredModels) {
		c.listContainer.AddChild(text(theme.Fg("muted", "  ("+strconv.Itoa(c.selectedIndex+1)+"/"+strconv.Itoa(len(c.filteredModels))+")")))
	}
	if len(c.filteredModels) == 0 {
		c.listContainer.AddChild(text(theme.Fg("muted", "  No matching models")))
		return
	}
	name := ""
	if selected := c.selectedModel(); selected != nil {
		name = selected.Name
	}
	c.listContainer.AddChild(tui.NewSpacer(1))
	c.listContainer.AddChild(text(theme.Fg("muted", "  Model Name: "+render.LineText(name))))
}

// HandleInput is the key handling.
func (c *ModelSelectorComponent) HandleInput(data string) {
	keys := c.ctx.Keys()
	switch {
	case keys.Matches(data, "tui.input.tab"):
		if len(c.scopedModels) > 0 {
			if c.scope == "all" {
				c.scope = "scoped"
				c.activeModels = c.scopedModels
			} else {
				c.scope = "all"
				c.activeModels = c.allModels
			}
			c.selectedIndex = 0
			for i, model := range c.activeModels {
				if sameRef(c.opts.Current, model) {
					c.selectedIndex = i
					break
				}
			}
			c.scopeText.SetText(c.getScopeText())
			c.filterModels(c.searchInput.GetValue())
		}
	case keys.Matches(data, "tui.select.up"):
		if len(c.filteredModels) == 0 {
			return
		}
		if c.selectedIndex == 0 {
			c.selectedIndex = len(c.filteredModels) - 1
		} else {
			c.selectedIndex--
		}
		c.updateList()
	case keys.Matches(data, "tui.select.down"):
		if len(c.filteredModels) == 0 {
			return
		}
		if c.selectedIndex == len(c.filteredModels)-1 {
			c.selectedIndex = 0
		} else {
			c.selectedIndex++
		}
		c.updateList()
	case keys.Matches(data, "tui.select.confirm"):
		if model := c.selectedModel(); model != nil {
			c.opts.OnSelect(*model)
		}
	case keys.Matches(data, "tui.select.cancel"):
		c.opts.OnCancel()
	case keys.Matches(data, "app.models.save") && c.opts.OnSelectAsDefault != nil:
		if model := c.selectedModel(); model != nil {
			c.opts.OnSelectAsDefault(*model)
		}
	default:
		c.searchInput.HandleInput(data)
		c.filterModels(c.searchInput.GetValue())
	}
}

var levelDescriptions = map[string]string{
	"off":     "No reasoning",
	"minimal": "Very brief reasoning (~1k tokens)",
	"low":     "Light reasoning (~2k tokens)",
	"medium":  "Moderate reasoning (~8k tokens)",
	"high":    "Deep reasoning (~16k tokens)",
	"xhigh":   "Extra-high reasoning (~32k tokens)",
	"max":     "Maximum reasoning",
}

// ThinkingSelectorOptions are the options of NewThinkingSelector.
type ThinkingSelectorOptions struct {
	Current           string
	Levels            []string
	DefaultLevel      string
	OnSelect          func(level string)
	OnCancel          func()
	OnSelectAsDefault func(level string)
}

// ThinkingSelectorComponent picks a thinking level.
type ThinkingSelectorComponent struct {
	*tui.Container
	searchInput *tui.TextInput
	selectList  *tui.SelectList
	allItems    []tui.SelectItem
	ctx         SelectorContext
	opts        ThinkingSelectorOptions
}

// NewThinkingSelector is the ThinkingSelectorComponent constructor.
func NewThinkingSelector(ctx SelectorContext, options ThinkingSelectorOptions) *ThinkingSelectorComponent {
	c := &ThinkingSelectorComponent{Container: tui.NewContainer(), searchInput: tui.NewInput(tui.InputOptions{}), ctx: ctx, opts: options}
	theme := ctx.Theme().Current()
	for _, level := range options.Levels {
		mark, description := "  ", level
		if level == options.Current {
			mark = "✓ "
		}
		if known, ok := levelDescriptions[level]; ok {
			description = known
		}
		if options.DefaultLevel != "" && level == options.DefaultLevel {
			description += " · default"
		}
		c.allItems = append(c.allItems, tui.SelectItem{Value: level, Label: mark + level, Description: description})
	}
	c.AddChild(NewDynamicBorder(ctx, ""))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(text("Thinking Level"))
	c.AddChild(tui.NewSpacer(1))
	if cycle := ctx.KeyText("app.thinking.cycle"); cycle != "" {
		c.AddChild(text(cycle + " cycles thinking levels in-session"))
	}
	c.AddChild(tui.NewSpacer(1))
	c.searchInput.OnSubmit = func(string) { c.selectList.HandleInput("\r") }
	c.AddChild(c.searchInput)
	c.AddChild(tui.NewSpacer(1))
	current := options.Current
	c.selectList = c.buildSelectList(c.allItems, &current)
	c.AddChild(c.selectList)
	c.AddChild(tui.NewSpacer(1))
	save := ""
	if options.OnSelectAsDefault != nil {
		save = " · " + ctx.KeyText("app.thinking.save") + " to set as default"
	}
	c.AddChild(text(theme.Fg("dim", "  "+ctx.KeyText("tui.select.confirm")+" to select"+save+" · "+ctx.KeyText("tui.select.cancel")+" to cancel")))
	c.AddChild(NewDynamicBorder(ctx, ""))
	return c
}

// SetFocused is the TypeScript's `focused` setter: the search input follows.
func (c *ThinkingSelectorComponent) SetFocused(focused bool) { c.searchInput.SetFocused(focused) }

func (c *ThinkingSelectorComponent) buildSelectList(items []tui.SelectItem, preselect *string) *tui.SelectList {
	list := tui.NewSelectList(items, max(1, len(items)), c.ctx.Theme().SelectList, tui.SelectListLayoutOptions{MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 32})
	if preselect != nil {
		for i, item := range items {
			if item.Value == *preselect {
				list.SetSelectedIndex(i)
				break
			}
		}
	}
	list.OnSelect = func(item tui.SelectItem) { c.opts.OnSelect(item.Value) }
	list.OnCancel = func() { c.opts.OnCancel() }
	return list
}

// HandleInput is the key handling.
func (c *ThinkingSelectorComponent) HandleInput(data string) {
	keys := c.ctx.Keys()
	if keys.Matches(data, "app.thinking.save") && c.opts.OnSelectAsDefault != nil {
		if item, ok := c.selectList.SelectedItem(); ok {
			c.opts.OnSelectAsDefault(item.Value)
		}
		return
	}
	for _, id := range []string{"tui.select.up", "tui.select.down", "tui.select.confirm", "tui.select.cancel"} {
		if keys.Matches(data, id) {
			c.selectList.HandleInput(data)
			return
		}
	}
	c.searchInput.HandleInput(data)
	query := c.searchInput.GetValue()
	filtered := c.allItems
	if query != "" {
		filtered = tui.FuzzyFilter(c.allItems, query, func(item tui.SelectItem) string { return item.Value + " " + item.Description })
	}
	var preselect *string
	if item, ok := c.selectList.SelectedItem(); ok {
		preselect = &item.Value
	}
	list := c.buildSelectList(filtered, preselect)
	c.InsertBefore(c.selectList, list)
	c.Remove(c.selectList)
	c.selectList = list
}

// ScopedModelsSelectorOptions are the options of NewScopedModelsSelector.
type ScopedModelsSelectorOptions struct {
	Models   []SelectorModel
	Enabled  []ModelRef
	OnSave   func(enabled []string)
	OnCancel func()
}

// ScopedModelsSelectorComponent is pi's `/scoped-models` (scoped-models-selector.ts), reduced: every catalog model with a check mark when
// enabled; Enter or Space toggles, Ctrl+S saves the scope as exact `provider/modelId` patterns in catalog order (the owner's settings.json
// `enabledModels`, replacing hand-written globs), Escape cancels.
type ScopedModelsSelectorComponent struct {
	*tui.Container
	searchInput   *tui.TextInput
	listContainer *tui.Container
	ctx           SelectorContext
	models        []*SelectorModel
	// enabled is the set of enabled keys; a Set iterates in insertion order, only its size is shown.
	enabled       map[string]bool
	filtered      []*SelectorModel
	selectedIndex int
	opts          ScopedModelsSelectorOptions
}

func modelKey(model *SelectorModel) string { return model.Provider + "/" + model.ModelID }

// NewScopedModelsSelector is the ScopedModelsSelectorComponent constructor.
func NewScopedModelsSelector(ctx SelectorContext, options ScopedModelsSelectorOptions) *ScopedModelsSelectorComponent {
	c := &ScopedModelsSelectorComponent{
		Container: tui.NewContainer(), searchInput: tui.NewInput(tui.InputOptions{}), listContainer: tui.NewContainer(), ctx: ctx,
		enabled: map[string]bool{}, opts: options,
	}
	for i := range options.Models {
		c.models = append(c.models, &options.Models[i])
	}
	for _, model := range options.Enabled {
		c.enabled[model.Provider+"/"+model.ModelID] = true
	}
	theme := ctx.Theme().Current()
	c.AddChild(NewDynamicBorder(ctx, ""))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(text(theme.Bold("Scoped models")))
	c.AddChild(text(theme.Fg("muted", "Models cycled with the model cycle keys, in this order.")))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(c.searchInput)
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(c.listContainer)
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(text(theme.Fg("dim", "  "+ctx.KeyText("tui.select.confirm")+"/space to toggle · "+ctx.KeyText("app.models.save")+" to save · "+ctx.KeyText("tui.select.cancel")+" to cancel")))
	c.AddChild(NewDynamicBorder(ctx, ""))
	c.filter()
	return c
}

// SetFocused is the TypeScript's `focused` setter: the search input follows.
func (c *ScopedModelsSelectorComponent) SetFocused(focused bool) { c.searchInput.SetFocused(focused) }

func (c *ScopedModelsSelectorComponent) filter() {
	query := c.searchInput.GetValue()
	c.filtered = c.models
	if query != "" {
		c.filtered = tui.FuzzyFilter(c.models, query, searchText)
	}
	c.selectedIndex = min(c.selectedIndex, max(0, len(c.filtered)-1))
	c.update()
}

func (c *ScopedModelsSelectorComponent) update() {
	theme := c.ctx.Theme().Current()
	c.listContainer.Clear()
	const maxVisible = 10
	start := max(0, min(c.selectedIndex-5, len(c.filtered)-maxVisible))
	for offset, model := range c.filtered[start:min(len(c.filtered), start+maxVisible)] {
		cursor, box := "  ", theme.Fg("muted", "[ ] ")
		if start+offset == c.selectedIndex {
			cursor = theme.Fg("accent", "→ ")
		}
		if c.enabled[modelKey(model)] {
			box = theme.Fg("success", "[x] ")
		}
		c.listContainer.AddChild(text(cursor + box + render.LineText(model.ModelID) + " " + theme.Fg("muted", "["+render.LineText(model.Provider)+"]")))
	}
	if len(c.filtered) == 0 {
		c.listContainer.AddChild(text(theme.Fg("muted", "  No matching models")))
	}
	c.listContainer.AddChild(text(theme.Fg("muted", "  "+strconv.Itoa(len(c.enabled))+" enabled")))
}

// HandleInput is the key handling.
func (c *ScopedModelsSelectorComponent) HandleInput(data string) {
	keys := c.ctx.Keys()
	switch {
	case keys.Matches(data, "tui.select.up"):
		c.selectedIndex = max(0, c.selectedIndex-1)
		c.update()
	case keys.Matches(data, "tui.select.down"):
		c.selectedIndex = min(max(0, len(c.filtered)-1), c.selectedIndex+1)
		c.update()
	case keys.Matches(data, "tui.select.confirm") || data == " ":
		if c.selectedIndex >= len(c.filtered) {
			return
		}
		key := modelKey(c.filtered[c.selectedIndex])
		if c.enabled[key] {
			delete(c.enabled, key)
		} else {
			c.enabled[key] = true
		}
		c.update()
	case keys.Matches(data, "app.models.save"):
		var enabled []string
		for _, model := range c.models {
			if key := modelKey(model); c.enabled[key] {
				enabled = append(enabled, key)
			}
		}
		c.opts.OnSave(enabled)
	case keys.Matches(data, "tui.select.cancel"):
		c.opts.OnCancel()
	default:
		c.searchInput.HandleInput(data)
		c.filter()
	}
}
