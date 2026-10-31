package tui

import (
	"fmt"
	"testing"
)

const (
	testPageUpKey   = "\x1b[5~"
	testPageDownKey = "\x1b[6~"
)

func pageTestItems(count int) []SelectItem {
	items := make([]SelectItem, count)
	for i := range items {
		items[i] = SelectItem{Value: fmt.Sprintf("v%d", i), Label: fmt.Sprintf("Item %d", i)}
	}
	return items
}

// Pi #7629, PR #7680 (packages/tui/src/selection-navigation.ts getSelectPageIndex): the page keys move a selection
// by one visible page and clamp at both ends, without the wraparound of tui.select.up and tui.select.down.
func TestPageSelectionIndex(t *testing.T) {
	kb := GetTUIKeybindings()
	for name, tc := range map[string]struct {
		data               string
		selected, count, n int
		want               int
		ok                 bool
	}{
		"page down":              {testPageDownKey, 2, 30, 5, 7, true},
		"page up":                {testPageUpKey, 7, 30, 5, 2, true},
		"page down clamps":       {testPageDownKey, 28, 30, 5, 29, true},
		"page up clamps":         {testPageUpKey, 3, 30, 5, 0, true},
		"short list":             {testPageDownKey, 0, 3, 10, 2, true},
		"empty list":             {testPageDownKey, 0, 0, 5, 0, true},
		"another key":            {"j", 2, 30, 5, 0, false},
		"up arrow is not a page": {"\x1b[A", 2, 30, 5, 0, false},
	} {
		got, ok := GetSelectPageIndex(kb, tc.data, tc.selected, tc.count, tc.n)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: GetSelectPageIndex = %d, %v; want %d, %v", name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestSelectListPageKeysMoveByAPage(t *testing.T) {
	var changed []string
	list := NewSelectList(pageTestItems(23), 5, SelectListTheme{})
	list.OnSelectionChange = func(item SelectItem) { changed = append(changed, item.Value) }
	list.HandleInput(testPageDownKey)
	if list.SelectedIndex() != 5 {
		t.Fatalf("page down: index = %d, want 5", list.SelectedIndex())
	}
	for range 10 {
		list.HandleInput(testPageDownKey)
	}
	if list.SelectedIndex() != 22 {
		t.Fatalf("page down at the end: index = %d, want 22 (no wraparound)", list.SelectedIndex())
	}
	list.HandleInput(testPageUpKey)
	if list.SelectedIndex() != 17 {
		t.Fatalf("page up: index = %d, want 17", list.SelectedIndex())
	}
	for range 10 {
		list.HandleInput(testPageUpKey)
	}
	if list.SelectedIndex() != 0 {
		t.Fatalf("page up at the start: index = %d, want 0", list.SelectedIndex())
	}
	if len(changed) == 0 || changed[0] != "v5" {
		t.Fatalf("selection changes = %v, want the page jump reported", changed)
	}
	if !CapturesViewportInput(list) {
		t.Fatal("a SelectList must capture the selection page keys")
	}
}

func TestSelectListPageKeysIgnoreAnEmptyList(t *testing.T) {
	list := NewSelectList(nil, 5, SelectListTheme{})
	list.HandleInput(testPageDownKey)
	list.HandleInput(testPageUpKey)
	if list.SelectedIndex() != 0 {
		t.Fatalf("index = %d, want 0", list.SelectedIndex())
	}
}

func TestSettingsListPageKeysMoveByAPage(t *testing.T) {
	items := make([]SettingItem, 12)
	for i := range items {
		items[i] = SettingItem{ID: fmt.Sprintf("s%d", i), Label: fmt.Sprintf("Setting %d", i), CurrentValue: "off", Values: []string{"off", "on"}}
	}
	list := NewSettingsList(items, 4, markedSettingsTheme(), func(string, string) {}, func() {})
	list.HandleInput(testPageDownKey)
	if list.SelectedIndex() != 4 {
		t.Fatalf("page down: index = %d, want 4", list.SelectedIndex())
	}
	list.HandleInput(testPageDownKey)
	list.HandleInput(testPageDownKey)
	if list.SelectedIndex() != 11 {
		t.Fatalf("page down at the end: index = %d, want 11", list.SelectedIndex())
	}
	list.HandleInput(testPageUpKey)
	if list.SelectedIndex() != 7 {
		t.Fatalf("page up: index = %d, want 7", list.SelectedIndex())
	}
	if !CapturesViewportInput(list) {
		t.Fatal("a SettingsList must capture the selection page keys")
	}
}

// editor.ts handleInput (PR #7680): with the autocomplete list open the page keys reach it; with it closed they do not.
func TestEditorAutocompletePageKeys(t *testing.T) {
	commands := make([]SlashCommand, 12)
	for i := range commands {
		commands[i] = SlashCommand{Name: fmt.Sprintf("cmd%02d", i), Description: "command"}
	}
	editor := NewEditor()
	editor.SetAutocomplete(NewSlashOnlyProvider(commands))
	if CapturesViewportInput(editor) {
		t.Fatal("an editor without an open autocomplete list must not capture the page keys")
	}
	editor.HandleInput("/")
	if !CapturesViewportInput(editor) {
		t.Fatal("an editor with an open autocomplete list must capture the page keys")
	}
	page := editor.AutocompleteMaxVisible()
	editor.HandleInput(testPageDownKey)
	if editor.autocompleteCursor != page {
		t.Fatalf("page down: cursor = %d, want %d", editor.autocompleteCursor, page)
	}
	editor.HandleInput(testPageDownKey)
	editor.HandleInput(testPageDownKey)
	if editor.autocompleteCursor != 11 {
		t.Fatalf("page down at the end: cursor = %d, want 11", editor.autocompleteCursor)
	}
	editor.HandleInput(testPageUpKey)
	if editor.autocompleteCursor != 11-page {
		t.Fatalf("page up: cursor = %d, want %d", editor.autocompleteCursor, 11-page)
	}
	if editor.Text() != "/" {
		t.Fatalf("text = %q, want the page keys not to edit", editor.Text())
	}
}

func TestContainerCapturesSelectPageInputFromItsChildren(t *testing.T) {
	container := NewContainer()
	container.AddChild(NewText("static"))
	if CapturesViewportInput(container) {
		t.Fatal("a container of plain children must not capture")
	}
	container.AddChild(NewSelectList(pageTestItems(3), 3, SelectListTheme{}))
	if !CapturesViewportInput(container) {
		t.Fatal("a container with a capturing child must capture")
	}
}

// tui-alt-screen.ts handleViewportInput (PR #7680): tui.select.pageUp and tui.select.pageDown share their default keys
// with tui.altScreen.pageUp and pageDown. A focused selection that captures them keeps them; otherwise the viewport
// scrolls.
func TestAltScreenGivesSelectionPageKeysToACapturingFocus(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 20, 8, TuiAltScreenOptions{})
	transcript := NewScrollView(NewText(numberedLines(40)), ScrollViewOptions{Follow: "end", Primary: true})
	list := NewSelectList(pageTestItems(30), 4, SelectListTheme{})
	h.tui.SetLayoutRoot(transcriptOverDock(transcript, list, new(5)))
	h.tui.SetFocus(list)
	h.start()
	bottom := transcript.ScrollTop()
	if bottom <= 0 {
		t.Fatalf("bottom = %d, want > 0", bottom)
	}
	h.send(testPageUpKey)
	if transcript.ScrollTop() != bottom {
		t.Fatalf("page up scrolled the transcript to %d with a selection focused", transcript.ScrollTop())
	}
	h.send(testPageDownKey)
	if list.SelectedIndex() != 4 || transcript.ScrollTop() != bottom {
		t.Fatalf("page down: selection %d, transcript %d; want 4 and %d", list.SelectedIndex(), transcript.ScrollTop(), bottom)
	}

	plain := &inputRecorder{lines: []string{"editor"}}
	h2 := newAltHarness(t, 20, 8, TuiAltScreenOptions{})
	transcript2 := NewScrollView(NewText(numberedLines(40)), ScrollViewOptions{Follow: "end", Primary: true})
	h2.tui.SetLayoutRoot(transcriptOverDock(transcript2, plain, new(1)))
	h2.tui.SetFocus(plain)
	h2.start()
	bottom2 := transcript2.ScrollTop()
	h2.send(testPageUpKey)
	if transcript2.ScrollTop() >= bottom2 {
		t.Fatalf("page up with a non-capturing focus left the transcript at %d, want it scrolled up from %d", transcript2.ScrollTop(), bottom2)
	}
}

// Each selector of Pi PR #7680 moves its selection by its visible page (getSelectPageIndex), clamped at both ends, and
// captures the selection page keys so fullscreen leaves them to it.
func TestSelectorComponentsMoveByTheirPage(t *testing.T) {
	models := make([]ModelSelectorItem, 25)
	scoped := make([]ModelItem, 25)
	providers := make([]OAuthProvider, 20)
	messages := make([]UserMessageItem, 25)
	options := make([]string, 20)
	labels := make([]string, 30)
	for i := range 25 {
		models[i] = ModelSelectorItem{Provider: "p", ID: fmt.Sprintf("m%02d", i)}
		scoped[i] = ModelItem{FullID: fmt.Sprintf("p/m%02d", i), Name: fmt.Sprintf("m%02d", i), Provider: "p"}
		messages[i] = UserMessageItem{ID: fmt.Sprintf("u%02d", i), Text: fmt.Sprintf("message %02d", i)}
	}
	for i := range 20 {
		providers[i] = OAuthProvider{ID: fmt.Sprintf("prov%02d", i), Name: fmt.Sprintf("Provider %02d", i), AuthType: "oauth"}
		options[i] = fmt.Sprintf("option %02d", i)
	}
	for i := range labels {
		labels[i] = fmt.Sprintf("label %02d", i)
	}
	model := NewStaticModelSelectorComponent("Select model", nil, models, "p/m00")
	model.SetFilter("m")
	oauth := NewOAuthSelectorComponent("login", providers, func(string, string) {}, func() {})
	scopedList := NewScopedModelsList(ScopedModelsConfig{AllModels: scoped})
	userMessages := NewUserMessageSelectorComponent(messages, func(string) {}, func() {}, "u00")
	extension := NewExtensionSelectorComponent("Pick", options, func(string) {}, func() {})
	filterable := NewFilterableList("", labels)
	filterable.EnableSearch = false
	filterable.MaxVisible = 7

	type selector struct {
		name   string
		input  func(string)
		cursor func() int
		page   int
		last   int
		captor Component
	}
	for _, tc := range []selector{
		{"model", model.HandleInput, func() int { return model.cursor }, 10, len(model.filtered) - 1, model},
		{"oauth", oauth.HandleInput, func() int { return oauth.cursor }, 8, 19, oauth},
		{"scoped models", scopedList.HandleInput, func() int { return scopedList.cursor }, 8, 24, scopedList},
		{"user messages", userMessages.HandleInput, func() int { return userMessages.list.selectedIndex }, 10, 24, userMessages},
		{"extension", extension.HandleInput, func() int { return extension.cursor }, 8, 19, extension},
		{"select list without search", filterable.HandleInput, func() int { return filterable.cursor }, 7, 29, filterable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !CapturesViewportInput(tc.captor) {
				t.Fatal("selector does not capture the selection page keys")
			}
			start := tc.cursor()
			tc.input(testPageDownKey)
			if got := tc.cursor(); got != start+tc.page {
				t.Fatalf("page down from %d: cursor = %d, want %d", start, got, start+tc.page)
			}
			for range 10 {
				tc.input(testPageDownKey)
			}
			if got := tc.cursor(); got != tc.last {
				t.Fatalf("page down at the end: cursor = %d, want %d without wrapping", got, tc.last)
			}
			tc.input(testPageUpKey)
			if got := tc.cursor(); got != tc.last-tc.page {
				t.Fatalf("page up: cursor = %d, want %d", got, tc.last-tc.page)
			}
			for range 10 {
				tc.input(testPageUpKey)
			}
			if got := tc.cursor(); got != 0 {
				t.Fatalf("page up at the start: cursor = %d, want 0", got)
			}
		})
	}
}

// select-list.ts setFilter uses `item.value.toLowerCase().startsWith(filter.toLowerCase())`: JavaScript lowercases İ (U+0130)
// to i followed by U+0307, where Go's strings.ToLower gives a plain i.
func TestSelectListFilterLowercasesAsJavaScriptDoes(t *testing.T) {
	list := NewSelectList([]SelectItem{{Value: "İstanbul"}, {Value: "istanbul"}}, 5, SelectListTheme{})
	list.SetFilter("i\u0307s")
	if len(list.filteredItems) != 1 || list.filteredItems[0].Value != "İstanbul" {
		t.Fatalf("filter i+U+0307+s kept %+v, want only İstanbul", list.filteredItems)
	}
}

// Pi 3bae8527 (#7629), packages/tui/test/tui-alt-screen.test.ts "defers viewport keys to a focused list instead of
// scrolling the transcript": a focused list that captures viewport input takes every viewport key, not only the page
// keys. PageDown moves the selection by a page and Ctrl+Home does not scroll the transcript to the top.
func TestAltScreenDefersEveryViewportKeyToACapturingFocus(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 20, 8, TuiAltScreenOptions{})
	transcript := NewScrollView(NewText(numberedLines(40)), ScrollViewOptions{Follow: "end", Primary: true})
	list := NewSelectList(pageTestItems(12), 3, SelectListTheme{})
	h.tui.SetLayoutRoot(transcriptOverDock(transcript, list, new(5)))
	h.tui.SetFocus(list)
	h.start()
	bottom := transcript.ScrollTop()
	if bottom <= 0 {
		t.Fatalf("bottom = %d, want > 0", bottom)
	}
	h.send(testPageDownKey)
	h.send("\x1b[1;5H") // Ctrl+Home
	if list.SelectedIndex() != 3 || transcript.ScrollTop() != bottom {
		t.Fatalf("selection %d, transcript %d; want 3 and %d", list.SelectedIndex(), transcript.ScrollTop(), bottom)
	}
}

// Pi 3bae8527 (#7629), packages/coding-agent/test/settings-selector.test.ts "pages a searchable submenu with selection page
// keys": with the search box present, the page keys still reach the list.
func TestSelectSubmenuPagesASearchableList(t *testing.T) {
	items := make([]SelectItem, 20)
	for i := range items {
		items[i] = SelectItem{Value: fmt.Sprintf("theme-%d", i), Label: fmt.Sprintf("theme-%d", i)}
	}
	submenu := NewSelectSubmenu("Theme", "", items, "theme-0", SelectSubmenuOptions{Searchable: true})
	var changed []string
	submenu.OnSelectionChange = func(value string) { changed = append(changed, value) }
	submenu.HandleInput(testPageDownKey)
	submenu.HandleInput("\r")
	if !submenu.Done() || submenu.SelectedValue() != "theme-10" {
		t.Fatalf("done = %v, selected = %q; want theme-10 after PageDown and Enter", submenu.Done(), submenu.SelectedValue())
	}
	if len(changed) != 1 || changed[0] != "theme-10" {
		t.Fatalf("selection changes = %v, want [theme-10]", changed)
	}
}

// tree-selector.ts capturesViewportInput (Pi 3bae8527): the tree selector takes the viewport keys except while its label
// editor is open, where they belong to the label input.
func TestTreeSelectorCapturesViewportInputExceptWhileEditingALabel(t *testing.T) {
	ts := newNativeTestTree(t)
	if !CapturesViewportInput(ts) {
		t.Fatal("the tree selector does not capture the viewport keys")
	}
	ts.HandleInput("L")
	if ts.labelInput == nil {
		t.Fatal("the label editor did not open")
	}
	if CapturesViewportInput(ts) {
		t.Fatal("the tree selector captures the viewport keys while its label editor is open")
	}
}
