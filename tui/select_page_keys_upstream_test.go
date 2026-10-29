package tui

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

// Ports of the selection page key tests of Pi #7629 (packages/tui/test and the selector cases of packages/coding-agent/test).
// Every remapped case moves the selection page keys, so the keybinding lookup, not the PageUp and PageDown defaults, decides
// the behavior.

func useRemappedPageKeys(t *testing.T, overrides map[string][]string) {
	t.Helper()
	previous := globalTUIKeybindings.Load()
	SetKeybindings(NewTUIKeybindingsManager(overrides))
	t.Cleanup(func() { globalTUIKeybindings.Store(previous) })
}

// commandProvider is the upstream tests' provider: twelve "/command-N" items for the "/" prefix, applied by replacing the prefix.
type commandProvider struct{}

func (commandProvider) GetSuggestions(context.Context, []string, int, int, AutocompleteSuggestionOptions) *AutocompleteSuggestions {
	items := make([]AutocompleteItem, 12)
	for i := range items {
		items[i] = AutocompleteItem{Value: fmt.Sprintf("/command-%d", i), Label: fmt.Sprintf("command-%d", i)}
	}
	return &AutocompleteSuggestions{Items: items, Prefix: "/"}
}

func (commandProvider) ApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) ([]string, int, int) {
	next := slices.Clone(lines)
	next[cursorLine] = lines[cursorLine][:cursorCol-len(prefix)] + item.Value
	return next, cursorLine, len(item.Value)
}

// select-list.test.ts "moves by one visible page using remapped selection bindings".
func TestUpstreamSelectListMovesByAPageWithRemappedBindings(t *testing.T) {
	useRemappedPageKeys(t, map[string][]string{"tui.select.pageUp": {"alt+v"}, "tui.select.pageDown": {"ctrl+v"}})
	items := make([]SelectItem, 12)
	for i := range items {
		items[i] = SelectItem{Value: fmt.Sprintf("item-%d", i), Label: fmt.Sprintf("Item %d", i)}
	}
	list := NewSelectList(items, 5, SelectListTheme{})
	var selections []string
	list.OnSelectionChange = func(item SelectItem) { selections = append(selections, item.Value) }
	for _, key := range []string{"\x16", "\x16", "\x16", "\x1bv"} {
		list.HandleInput(key)
	}
	if want := []string{"item-5", "item-10", "item-11", "item-6"}; !slices.Equal(selections, want) {
		t.Fatalf("selections = %v, want %v", selections, want)
	}
}

// settings-list.test.ts "moves by one visible page using remapped selection bindings".
func TestUpstreamSettingsListMovesByAPageWithRemappedBindings(t *testing.T) {
	useRemappedPageKeys(t, map[string][]string{"tui.select.pageUp": {"alt+v"}, "tui.select.pageDown": {"ctrl+v"}})
	items := make([]SettingItem, 12)
	for i := range items {
		items[i] = SettingItem{ID: fmt.Sprintf("setting-%d", i), Label: fmt.Sprintf("Setting %d", i), CurrentValue: fmt.Sprint(i)}
	}
	list := NewSettingsList(items, 5, markedSettingsTheme(), func(string, string) {}, func() {})
	for _, step := range []struct{ key, want string }{{"\x16", "Setting 5"}, {"\x16", "Setting 10"}, {"\x1bv", "Setting 5"}} {
		list.HandleInput(step.key)
		if got := list.Items()[list.SelectedIndex()].Label; got != step.want {
			t.Fatalf("after %q selected %q, want %q", step.key, got, step.want)
		}
	}
}

// editor.test.ts "forwards remapped selection page keys to the autocomplete menu": the key is also tui.editor.pageDown,
// and the open autocomplete takes it.
func TestUpstreamEditorForwardsRemappedPageKeysToTheAutocompleteMenu(t *testing.T) {
	useRemappedPageKeys(t, map[string][]string{"tui.editor.pageDown": {"ctrl+v"}, "tui.select.pageDown": {"ctrl+v"}})
	editor := NewEditor()
	editor.SetAutocomplete(commandProvider{})
	var submitted string
	editor.OnSubmit = func(text string) { submitted = text }
	editor.HandleInput("/")
	if !editor.AutocompleteOpen() {
		t.Fatal("autocomplete did not open")
	}
	editor.HandleInput("\x16")
	editor.HandleInput("\r")
	if submitted != "/command-5" {
		t.Fatalf("submitted %q, want /command-5", submitted)
	}
}

// tui-alt-screen.test.ts "routes an overlapping remapped page key to an open editor autocomplete menu": the key is bound
// to tui.editor.pageDown, tui.select.pageDown and tui.altScreen.pageDown; the open autocomplete keeps it and the
// transcript does not scroll.
func TestUpstreamAltScreenRoutesAnOverlappingRemappedPageKeyToTheAutocomplete(t *testing.T) {
	useAltScreenBindings(t, map[string][]string{"tui.editor.pageDown": {"ctrl+v"}, "tui.select.pageDown": {"ctrl+v"}, "tui.altScreen.pageDown": {"ctrl+v"}})
	h := newAltHarness(t, 80, 12, TuiAltScreenOptions{})
	transcript := NewScrollView(NewText(numberedLines(20)), ScrollViewOptions{Follow: "end", Primary: true})
	editor := NewEditor()
	editor.SetAutocomplete(commandProvider{})
	var submitted string
	editor.OnSubmit = func(text string) { submitted = text }
	h.tui.SetLayoutRoot(transcriptOverDock(transcript, editor, nil))
	h.tui.SetFocus(editor)
	h.start()
	h.send("/")
	if !editor.AutocompleteOpen() {
		t.Fatal("autocomplete did not open")
	}
	scrollTop := transcript.ScrollTop()
	h.send("\x16")
	if transcript.ScrollTop() != scrollTop {
		t.Fatalf("transcript scrolled to %d, want %d", transcript.ScrollTop(), scrollTop)
	}
	h.send("\r")
	if submitted != "/command-5" {
		t.Fatalf("submitted %q, want /command-5", submitted)
	}
}

// containerForwarder is a Container whose HandleInput forwards to a list, the wrapper shape tui.ts says must expose the
// list's capturesViewportInput itself.
type containerForwarder struct {
	*Container
	target InputHandler
}

func (w containerForwarder) HandleInput(data string) { w.target.HandleInput(data) }

// tui.ts capturesViewportInput: only the focused component is checked, so a focused wrapper that forwards input to a list
// does not keep the page keys from the transcript.
func TestUpstreamAltScreenScrollsTheTranscriptForAFocusedContainerWrapper(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 80, 12, TuiAltScreenOptions{})
	transcript := NewScrollView(NewText(numberedLines(20)), ScrollViewOptions{Follow: "end", Primary: true})
	items := make([]SelectItem, 12)
	for i := range items {
		items[i] = SelectItem{Value: fmt.Sprintf("item-%d", i), Label: fmt.Sprintf("Item %d", i)}
	}
	list := NewSelectList(items, 5, SelectListTheme{})
	wrapper := containerForwarder{Container: NewContainer(), target: list}
	wrapper.AddChild(list)
	h.tui.SetLayoutRoot(transcriptOverDock(transcript, wrapper, nil))
	h.tui.SetFocus(wrapper)
	h.start()
	scrollTop := transcript.ScrollTop()
	h.send(testPageUpKey)
	if transcript.ScrollTop() >= scrollTop {
		t.Fatalf("transcript at %d, want it scrolled up from %d", transcript.ScrollTop(), scrollTop)
	}
	if list.SelectedIndex() != 0 {
		t.Fatalf("selection = %d, want the wrapper not to forward the viewport key", list.SelectedIndex())
	}
}

// tui-alt-screen.test.ts "defers viewport keys to a focused list instead of scrolling the transcript (#7629)": PageDown
// pages the list and Ctrl+Home, a tui.altScreen binding, does not move the viewport.
func TestUpstreamAltScreenDefersViewportKeysToAFocusedList(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 20, 8, TuiAltScreenOptions{})
	transcript := NewScrollView(NewText(numberedLines(40)), ScrollViewOptions{Follow: "end", Primary: true})
	list := NewSelectList(pageTestItems(12), 3, SelectListTheme{})
	h.tui.SetLayoutRoot(transcriptOverDock(transcript, list, new(5)))
	h.tui.SetFocus(list)
	h.start()
	top := transcript.ScrollTop()
	h.send(testPageDownKey)
	h.send("\x1b[1;5H")
	if got := list.SelectedIndex(); got != 3 {
		t.Fatalf("selection = %d, want 3", got)
	}
	if transcript.ScrollTop() != top {
		t.Fatalf("transcript scrolled from %d to %d", top, transcript.ScrollTop())
	}
}

// tui-alt-screen.test.ts "gives viewport keys back to the transcript when editor autocomplete closes (#7629)".
func TestUpstreamAltScreenGivesViewportKeysBackWhenEditorAutocompleteCloses(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 40, 8, TuiAltScreenOptions{})
	transcript := NewScrollView(NewText(numberedLines(30)), ScrollViewOptions{Follow: "end", Primary: true})
	editor := NewEditor()
	editor.SetAutocomplete(commandProvider{})
	var submitted string
	editor.OnSubmit = func(text string) { submitted = text }
	h.tui.SetLayoutRoot(transcriptOverDock(transcript, editor, nil))
	h.tui.SetFocus(editor)
	h.start()
	h.send("/")
	if !editor.AutocompleteOpen() {
		t.Fatal("autocomplete did not open")
	}
	topWithMenu := transcript.ScrollTop()
	h.send(testPageDownKey)
	if transcript.ScrollTop() != topWithMenu {
		t.Fatalf("PageDown moved the transcript from %d to %d while the menu was open", topWithMenu, transcript.ScrollTop())
	}
	h.send("\r")
	if submitted != "/command-5" || editor.AutocompleteOpen() {
		t.Fatalf("submitted %q, autocomplete open %v; want /command-5 and closed", submitted, editor.AutocompleteOpen())
	}
	topWithoutMenu := transcript.ScrollTop()
	h.send(testPageUpKey)
	if transcript.ScrollTop() >= topWithoutMenu {
		t.Fatalf("PageUp left the transcript at %d, want it scrolled up from %d", transcript.ScrollTop(), topWithoutMenu)
	}
}

// editor.test.ts "moves the autocomplete selection with selection page keys (#7629)".
func TestUpstreamEditorMovesTheAutocompleteSelectionWithSelectionPageKeys(t *testing.T) {
	editor := NewEditor()
	editor.SetAutocomplete(commandProvider{})
	var submitted string
	editor.OnSubmit = func(text string) { submitted = text }
	if CapturesViewportInput(editor) {
		t.Fatal("an editor without autocomplete must not capture viewport input")
	}
	editor.HandleInput("/")
	if !CapturesViewportInput(editor) {
		t.Fatal("an editor with open autocomplete must capture viewport input")
	}
	for _, key := range []string{testPageDownKey, testPageDownKey, testPageUpKey, "\r"} {
		editor.HandleInput(key)
	}
	if submitted != "/command-5" {
		t.Fatalf("submitted %q, want /command-5", submitted)
	}
}

// select-list.test.ts "moves by one visible page with selection page keys (#7629)".
func TestUpstreamSelectListMovesByOneVisiblePageWithSelectionPageKeys(t *testing.T) {
	items := make([]SelectItem, 12)
	for i := range items {
		items[i] = SelectItem{Value: fmt.Sprintf("item-%d", i), Label: fmt.Sprintf("Item %d", i)}
	}
	list := NewSelectList(items, 5, SelectListTheme{})
	var selections []string
	list.OnSelectionChange = func(item SelectItem) { selections = append(selections, item.Value) }
	for _, key := range []string{testPageDownKey, testPageDownKey, testPageDownKey, testPageDownKey, testPageUpKey} {
		list.HandleInput(key)
	}
	want := []string{"item-5", "item-10", "item-11", "item-6"}
	if !slices.Equal(selections, want) {
		t.Fatalf("selections = %v, want %v", selections, want)
	}
	list.SetFilter("no match")
	list.HandleInput(testPageDownKey)
	if !slices.Equal(selections, want) {
		t.Fatalf("a page key on an empty list notified: %v", selections)
	}
}

// settings-list.test.ts "moves by one visible page with selection page keys (#7629)".
func TestUpstreamSettingsListMovesByOneVisiblePageWithSelectionPageKeys(t *testing.T) {
	items := make([]SettingItem, 12)
	for i := range items {
		items[i] = SettingItem{ID: fmt.Sprintf("setting-%d", i), Label: fmt.Sprintf("Setting %d", i), CurrentValue: fmt.Sprint(i)}
	}
	list := NewSettingsList(items, 5, markedSettingsTheme(), func(string, string) {}, func() {})
	for _, step := range []struct{ key, want string }{{testPageDownKey, "Setting 5"}, {testPageDownKey, "Setting 10"}, {testPageDownKey, "Setting 11"}, {testPageUpKey, "Setting 6"}} {
		list.HandleInput(step.key)
		if got := list.Items()[list.SelectedIndex()].Label; got != step.want {
			t.Fatalf("after %q selected %q, want %q", step.key, got, step.want)
		}
	}
}

// model-selector.test.ts "moves by one page with remapped selection page keys (#7629)": twelve models, Ctrl+V twice and Alt+V once.
func TestUpstreamModelSelectorMovesByOnePageWithRemappedSelectionPageKeys(t *testing.T) {
	useRemappedPageKeys(t, map[string][]string{"tui.select.pageUp": {"alt+v"}, "tui.select.pageDown": {"ctrl+v"}})
	models := make([]ModelSelectorItem, 12)
	for i := range models {
		models[i] = ModelSelectorItem{Provider: "faux", ID: fmt.Sprintf("model-%02d", i)}
	}
	model := NewStaticModelSelectorComponent("Select model", nil, models, "")
	for _, key := range []string{"\x16", "\x16", "\x1bv", "\r"} {
		model.HandleInput(key)
	}
	if model.SelectedFQ() != "faux/model-01" {
		t.Fatalf("selected %q, want faux/model-01", model.SelectedFQ())
	}
}

// settings-selector.test.ts "pages a searchable submenu with selection page keys (#7629)".
func TestUpstreamSelectSubmenuPagesWithSelectionPageKeys(t *testing.T) {
	items := make([]SelectItem, 20)
	for i := range items {
		items[i] = SelectItem{Value: fmt.Sprintf("theme-%d", i), Label: fmt.Sprintf("theme-%d", i)}
	}
	submenu := NewSelectSubmenu("Theme", "", items, "theme-0", SelectSubmenuOptions{Searchable: true})
	var changed []string
	submenu.OnSelectionChange = func(value string) { changed = append(changed, value) }
	submenu.HandleInput(testPageDownKey)
	submenu.HandleInput("\r")
	if got := submenu.SelectedValue(); got != "theme-10" {
		t.Fatalf("selected %q, want theme-10", got)
	}
	if !slices.Equal(changed, []string{"theme-10"}) {
		t.Fatalf("selection changes = %v, want [theme-10]", changed)
	}
}

// Remapped selection page keys on the selectors of this package: the model, scoped models, OAuth and user-message
// selectors and the SelectList-backed show-images and theme selectors follow Pi; the extension selector is a PiG pager (D155).
func TestUpstreamSelectorsPageWithRemappedKeys(t *testing.T) {
	useRemappedPageKeys(t, map[string][]string{"tui.editor.pageUp": {"alt+v"}, "tui.editor.pageDown": {"ctrl+v"}, "tui.select.pageUp": {"alt+v"}, "tui.select.pageDown": {"ctrl+v"}})

	models := make([]ModelSelectorItem, 12)
	scoped := make([]ModelItem, 12)
	for i := range models {
		models[i] = ModelSelectorItem{Provider: "faux", ID: fmt.Sprintf("model-%d", i)}
		scoped[i] = ModelItem{FullID: fmt.Sprintf("faux/model-%d", i), Name: fmt.Sprintf("Model %d", i), Provider: "faux"}
	}
	model := NewStaticModelSelectorComponent("Select model", nil, models, "")
	model.HandleInput("\x16")
	model.HandleInput("\r")
	if !model.Done() || model.SelectedFQ() != "faux/model-10" {
		t.Errorf("model selector selected %q, want faux/model-10", model.SelectedFQ())
	}
	scopedList := NewScopedModelsList(ScopedModelsConfig{AllModels: scoped})
	scopedList.HandleInput("\x16")
	scopedList.HandleInput("\r")
	// The toggle from "all enabled" disables the toggled model (scoped-models-selector.ts:30-31), so the page key must have
	// moved to model-8 and toggled only it.
	want := make([]string, 0, 11)
	for i := range 12 {
		if i != 8 {
			want = append(want, fmt.Sprintf("faux/model-%d", i))
		}
	}
	if got := scopedList.EnabledIDs(); !slices.Equal(got, want) {
		t.Errorf("scoped models enabled %v, want every model but faux/model-8", got)
	}

	providers := make([]OAuthProvider, 10)
	options := make([]string, 10)
	for i := range providers {
		providers[i] = OAuthProvider{ID: fmt.Sprintf("provider-%d", i), Name: fmt.Sprintf("Provider %d", i), AuthType: "api_key"}
		options[i] = fmt.Sprintf("option-%d", i)
	}
	var oauthID, oauthType string
	oauth := NewOAuthSelectorComponent("login", providers, func(id, authType string) { oauthID, oauthType = id, authType }, func() {})
	oauth.HandleInput("\x16")
	oauth.HandleInput("\r")
	if oauthID != "provider-8" || oauthType != "api_key" {
		t.Errorf("oauth selector selected %q %q, want provider-8 api_key", oauthID, oauthType)
	}
	var option string
	extension := NewExtensionSelectorComponent("Select", options, func(o string) { option = o }, func() {})
	extension.HandleInput("\x16")
	extension.HandleInput("\r")
	if option != "option-8" {
		t.Errorf("extension selector selected %q, want option-8", option)
	}
	messages := make([]UserMessageItem, 12)
	for i := range messages {
		messages[i] = UserMessageItem{ID: fmt.Sprintf("message-%d", i), Text: fmt.Sprintf("Message %d", i)}
	}
	var message string
	userMessages := NewUserMessageSelectorComponent(messages, func(id string) { message = id }, func() {}, "")
	userMessages.HandleInput("\x1bv")
	userMessages.HandleInput("\r")
	if message != "message-1" {
		t.Errorf("user message selector selected %q, want message-1", message)
	}

	var show *bool
	showImages := NewShowImagesSelectorComponent(true, func(v bool) { show = &v }, func() {})
	showImages.GetSelectList().HandleInput("\x16")
	showImages.GetSelectList().HandleInput("\r")
	if show == nil || *show {
		t.Errorf("show-images selector selected %v, want false", show)
	}
	themeSelected := 0
	themeSelector := NewThemeSelectorComponent("dark", func(string) { themeSelected++ }, func() {}, func(string) {})
	themeSelector.GetSelectList().HandleInput("\x16")
	themeSelector.GetSelectList().HandleInput("\r")
	if themeSelected != 1 {
		t.Errorf("theme selector selected %d times, want once", themeSelected)
	}
}
