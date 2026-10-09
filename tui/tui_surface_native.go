package tui

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pig additive (D91): a frontend session draws the selectors and dialogs that
// take the input editor's place natively, and drives them with Env.Act, which
// PiG performs as the keys the action stands for.

// NativeComponent is a component that a frontend session draws as a
// [frontend.Selector] or [frontend.Settings] node when it takes the input
// editor's place in the dock.
type NativeComponent interface {
	Component
	// NativeNode returns the component's node, and false while it shows
	// something the node does not model, which then arrives as lines.
	NativeNode() (frontend.Node, bool)
}

// nativeTabKeyer is a native component whose tab key is not tui.input.tab.
type nativeTabKeyer interface {
	nativeTabKey() TUIKeybinding
}

// NativeAction is one frontend action in progress. TuiSurface.NativeKeys
// steps it on the owner loop.
type NativeAction struct {
	action frontend.Action
	phase  nativePhase
	// presses counts the keys pressed, and limit bounds them: the tab or
	// confirm presses a cycle can take, or the keys that turn the query a
	// filter started from into the new one.
	presses, limit int
}

type nativePhase uint8

const (
	nativeMove nativePhase = iota
	nativePress
	nativeDone
)

// NewNativeAction starts action.
func NewNativeAction(action frontend.Action) *NativeAction {
	return &NativeAction{action: action}
}

// NativeKeys returns the keys that take a its next step against the node it
// names as that node is now, or nil once the action is complete or the node
// is no longer shown. Each key is one input sequence.
func (t *TuiSurface) NativeKeys(a *NativeAction) []string {
	if a.action.ViewNode != "" {
		// pig additive (D107): a list of an extension's view, which takes
		// the keys through the node that shows it.
		t.mu.Lock()
		node := t.viewNativeNode(a.action)
		t.mu.Unlock()
		if node == nil || a.phase == nativeDone {
			a.phase = nativeDone
			return nil
		}
		keys := a.step(nil, node)
		if len(keys) == 0 {
			a.phase = nativeDone
		}
		return keys
	}
	t.mu.Lock()
	c, node := t.liveNative(a.action.Node)
	t.mu.Unlock()
	if c == nil || a.phase == nativeDone {
		a.phase = nativeDone
		return nil
	}
	keys := a.step(c, node)
	if len(keys) == 0 {
		a.phase = nativeDone
	}
	return keys
}

func (a *NativeAction) step(c NativeComponent, node frontend.Node) []string {
	ids, selected := nativeSelection(node)
	switch a.action.Kind {
	case frontend.Highlight:
		a.phase = nativeDone
		return nativeMoves(ids, selected, a.action.Item)
	case frontend.Choose:
		target := a.action.Item
		if target == "" {
			target = selected
		}
		if a.phase == nativeMove {
			a.phase = nativePress
			if !slices.Contains(ids, target) {
				return nil
			}
			if moves := nativeMoves(ids, selected, target); len(moves) > 0 {
				return moves
			}
		}
		a.phase = nativeDone
		if selected != target {
			return nil
		}
		return keySequences(KBSelectConfirm)
	case frontend.Dismiss:
		a.phase = nativeDone
		return keySequences(KBSelectCancel)
	case frontend.Filter:
		return nativeFilterKey(node, a.action.Value, &a.presses, &a.limit)
	case frontend.SwitchTab:
		sel, ok := node.(frontend.Selector)
		if !ok || sel.Tab == a.action.Item || a.presses >= len(sel.Tabs) ||
			!slices.ContainsFunc(sel.Tabs, func(tab frontend.SelectorTab) bool { return tab.ID == a.action.Item }) {
			return nil
		}
		a.presses++
		key := KBInputTab
		if keyer, ok := c.(nativeTabKeyer); ok {
			key = keyer.nativeTabKey()
		}
		return keySequences(key)
	case frontend.SetValue:
		settings, ok := node.(frontend.Settings)
		if !ok {
			return nil
		}
		i := slices.IndexFunc(settings.Items, func(s frontend.Setting) bool { return s.ID == a.action.Item })
		if i < 0 || settings.Items[i].Submenu || !slices.Contains(settings.Items[i].Values, a.action.Value) {
			return nil
		}
		if a.phase == nativeMove {
			a.phase = nativePress
			if moves := nativeMoves(ids, selected, a.action.Item); len(moves) > 0 {
				return moves
			}
		}
		if selected != a.action.Item || settings.Items[i].Value == a.action.Value || a.presses >= len(settings.Items[i].Values) {
			return nil
		}
		a.presses++
		return keySequences(KBSelectConfirm)
	}
	return nil
}

// nativeSelection returns the ids of a node's items in key order and the
// selected one.
func nativeSelection(node frontend.Node) (ids []string, selected string) {
	switch n := node.(type) {
	case frontend.Selector:
		ids = make([]string, len(n.Items))
		for i, item := range n.Items {
			ids[i] = item.ID
		}
		return ids, n.Selected
	case frontend.Settings:
		ids = make([]string, len(n.Items))
		for i, item := range n.Items {
			ids[i] = item.ID
		}
		return ids, n.Selected
	}
	return nil, ""
}

// nativeMoves returns the selection keys that move from the selected item to
// target without wrapping around, or nil when either is not shown.
func nativeMoves(ids []string, selected, target string) []string {
	from, to := slices.Index(ids, selected), slices.Index(ids, target)
	if from < 0 || to < 0 || from == to {
		return nil
	}
	key, count := KBSelectDown, to-from
	if count < 0 {
		key, count = KBSelectUp, -count
	}
	seq := keySequence(key)
	if seq == "" {
		return nil
	}
	moves := make([]string, count)
	for i := range moves {
		moves[i] = seq
	}
	return moves
}

// nativeFilterKey returns the one key that brings a searchable node's query
// closer to query: Backspace while the query is not a prefix of it, then
// its next character. Each key is computed against the query it changed,
// whatever unit the component's Backspace deletes. The keys are bounded by
// the bytes to erase and to type from the query the filter started with.
func nativeFilterKey(node frontend.Node, query string, presses, limit *int) []string {
	var searchable bool
	var current string
	switch n := node.(type) {
	case frontend.Selector:
		searchable, current = n.Searchable, n.Query
	case frontend.Settings:
		searchable, current = n.Searchable, n.Query
	}
	if *presses == 0 {
		*limit = len(current) + len(query)
	}
	if !searchable || current == query || *presses >= *limit {
		return nil
	}
	*presses++
	if !strings.HasPrefix(query, current) {
		return keySequences(KBEditorDeleteCharBack)
	}
	r, _ := utf8.DecodeRuneInString(query[len(current):])
	if r < 0x20 || r == 0x7f {
		return nil
	}
	return []string{string(r)}
}

// keySequence returns an input sequence of the first key bound to action
// that the binding matches, or "" when none of its keys has one.
func keySequence(action TUIKeybinding) string {
	kb := GetTUIKeybindings()
	for _, key := range kb.GetKeys(action) {
		for _, seq := range tuiKeyIDInputs[strings.ToLower(key)] {
			if kb.Matches(seq, action) {
				return seq
			}
		}
	}
	return ""
}

func keySequences(action TUIKeybinding) []string {
	if seq := keySequence(action); seq != "" {
		return []string{seq}
	}
	return nil
}

// dockSlot returns the native component that takes the editor's place in
// root, and its node, or nil when there is none or it shows something its
// node does not model.
func dockSlot(root Component) (NativeComponent, frontend.Node) {
	switch c := root.(type) {
	case *Container:
		for _, child := range c.Children() {
			if native, node := dockSlot(child); native != nil {
				return native, node
			}
		}
	case NativeComponent:
		if node, ok := c.NativeNode(); ok {
			return c, node
		}
	}
	return nil, nil
}

// nativeID returns the id of the dock's native component c, a new one for a
// component the last frame did not show.
func (t *TuiSurface) nativeID(c NativeComponent) string {
	if c != t.nativeComp || t.nativeIDText == "" {
		t.nativeSeq++
		t.nativeComp = c
		t.nativeIDText = "selector." + strconv.Itoa(t.nativeSeq)
	}
	return t.nativeIDText
}

// liveNative returns the native component the last frame reported as id and
// its node now, or nil when it no longer takes the editor's place.
func (t *TuiSurface) liveNative(id string) (NativeComponent, frontend.Node) {
	if t.dock == nil || t.nativeComp == nil || id != t.nativeIDText {
		return nil, nil
	}
	c, node := dockSlot(t.dock)
	if c == nil || c != t.nativeComp {
		return nil, nil
	}
	return c, node
}

// plainText removes styling from text a native node carries.
func plainText(text string) string {
	if strings.IndexByte(text, 0x1b) < 0 {
		return text
	}
	return widthx.StripAnsi(text)
}

// NativeNode reports the list as a selector; item ids are indexes into
// Labels.
func (f *FilterableList) NativeNode() (frontend.Node, bool) {
	node := frontend.Selector{
		Title:      plainText(f.Title),
		Searchable: f.EnableSearch,
		Query:      f.filter,
		Items:      make([]frontend.SelectorItem, len(f.filtered)),
	}
	for i, index := range f.filtered {
		item := frontend.SelectorItem{ID: strconv.Itoa(index), Label: plainText(f.Labels[index])}
		if index < len(f.Descriptions) {
			item.Detail = plainText(f.Descriptions[index])
		}
		node.Items[i] = item
	}
	if f.cursor >= 0 && f.cursor < len(f.filtered) {
		node.Selected = node.Items[f.cursor].ID
	}
	return node, true
}

// NativeNode reports the list as a selector whose item ids are the items'
// values.
func (l *SelectList) NativeNode() (frontend.Node, bool) {
	node := frontend.Selector{Items: make([]frontend.SelectorItem, len(l.filteredItems))}
	for i, item := range l.filteredItems {
		label := item.Label
		if label == "" {
			label = item.Value
		}
		node.Items[i] = frontend.SelectorItem{ID: item.Value, Label: plainText(label), Detail: plainText(item.Description)}
	}
	if l.selectedIndex >= 0 && l.selectedIndex < len(l.filteredItems) {
		node.Selected = l.filteredItems[l.selectedIndex].Value
	}
	return node, true
}

// NativeNode reports the submenu as a selector whose item ids are the items'
// values.
func (s *SelectSubmenuComponent) NativeNode() (frontend.Node, bool) {
	node := frontend.Selector{
		Title:       plainText(s.listTitle),
		Description: plainText(s.description),
		Searchable:  s.searchInput != nil,
		Items:       make([]frontend.SelectorItem, 0, len(s.list.filtered)),
	}
	if s.searchInput != nil {
		node.Query = s.searchInput.Text()
	}
	for i, index := range s.list.filtered {
		if index >= len(s.items) {
			continue
		}
		item := s.items[index]
		node.Items = append(node.Items, frontend.SelectorItem{ID: item.Value, Label: plainText(item.Label), Detail: plainText(item.Description)})
		if i == s.list.cursor {
			node.Selected = item.Value
		}
	}
	return node, true
}

// NativeNode reports the dialog as a selector. A title's first line is the
// heading and the rest, such as a confirm dialog's message, comes before the
// description. Item ids are option indexes.
func (e *ExtensionSelectorComponent) NativeNode() (frontend.Node, bool) {
	title, rest, _ := strings.Cut(e.title, "\n")
	description := rest
	if e.description != "" {
		if description != "" {
			description += "\n"
		}
		description += e.description
	}
	node := frontend.Selector{
		Title:       plainText(title),
		Description: plainText(description),
		Items:       make([]frontend.SelectorItem, len(e.options)),
	}
	for i, option := range e.options {
		node.Items[i] = frontend.SelectorItem{ID: strconv.Itoa(i), Label: plainText(option)}
	}
	if e.cursor >= 0 && e.cursor < len(e.options) {
		node.Selected = strconv.Itoa(e.cursor)
	}
	return node, true
}

// NativeNode reports the list as settings, or, while a submenu is open, the
// submenu's node.
func (s *SettingsList) NativeNode() (frontend.Node, bool) {
	if s.submenu != nil {
		if native, ok := s.submenu.(NativeComponent); ok {
			return native.NativeNode()
		}
		return nil, false
	}
	display := s.displayItems()
	node := frontend.Settings{
		Searchable: s.searchEnabled,
		Query:      s.query(),
		Items:      make([]frontend.Setting, len(display)),
	}
	for i, index := range display {
		item := s.items[index]
		node.Items[i] = frontend.Setting{
			ID:          item.ID,
			Label:       plainText(item.Label),
			Description: plainText(item.Description),
			Value:       plainText(item.CurrentValue),
			Values:      item.Values,
			Submenu:     item.Submenu != nil,
		}
	}
	if s.cursor >= 0 && s.cursor < len(display) {
		node.Selected = node.Items[s.cursor].ID
	}
	return node, true
}

// selectorsEqual, settingsEqual and overlaysEqual compare nodes by value
// without reflection, which allocates on every call.
func selectorsEqual(a, b frontend.Selector) bool {
	return a.Title == b.Title && a.Description == b.Description && a.Searchable == b.Searchable &&
		a.Query == b.Query && a.Selected == b.Selected && a.Tab == b.Tab && a.Status == b.Status &&
		a.Loading == b.Loading && a.Confirm == b.Confirm && slices.Equal(a.Items, b.Items) && slices.Equal(a.Tabs, b.Tabs)
}

func settingsEqual(a, b frontend.Settings) bool {
	return a.Searchable == b.Searchable && a.Query == b.Query && a.Selected == b.Selected &&
		slices.EqualFunc(a.Items, b.Items, func(x, y frontend.Setting) bool {
			return x.ID == y.ID && x.Label == y.Label && x.Description == y.Description && x.Value == y.Value &&
				x.Submenu == y.Submenu && slices.Equal(x.Values, y.Values)
		})
}

func overlaysEqual(a, b frontend.Overlay) bool {
	return a.Width == b.Width && a.Anchor == b.Anchor && a.NonCapturing == b.NonCapturing && a.Fullscreen == b.Fullscreen &&
		areasEqual(a.Click, b.Click) && slices.Equal(a.Lines, b.Lines) && viewsEqual(a.View, b.View)
}
