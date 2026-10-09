package codingagent

// pig additive (D91): a frontend session draws the codingagent selectors that
// take the input editor's place natively. Each NativeNode reports what the
// ANSI rendering shows, without key hints and scroll indicators, with items
// in the order the selection keys step through them. Pi has no frontend
// member.

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// sessionSelectorTabs are the scopes the tab key toggles, in its order.
var sessionSelectorTabs = []frontend.SelectorTab{
	{ID: string(sessionScopeCurrent), Label: "Current Folder"},
	{ID: string(sessionScopeAll), Label: "All"},
}

// NativeNode reports the session list as a selector whose item ids are
// session paths. Rename mode's text field is not a list, so the dock keeps
// drawing it as lines.
func (s *SessionSelectorComponent) NativeNode() (frontend.Node, bool) {
	if s.renameMode {
		return nil, false
	}
	title := "Resume Session (All)"
	if s.scope == sessionScopeCurrent {
		title = "Resume Session (Current Folder)"
	}
	node := frontend.Selector{
		Title:      title,
		Searchable: true,
		Query:      s.searchInput.Text(),
		Items:      make([]frontend.SelectorItem, len(s.filtered)),
		Tabs:       sessionSelectorTabs,
		Tab:        string(s.scope),
		Status:     s.nativeStatus(),
		Loading:    s.loading,
	}
	if s.confirmDelete != "" {
		node.Confirm = "Delete session?"
	}
	for i, display := range s.filtered {
		session := display.Session
		label := session.Name
		if label == "" {
			label = session.FirstMessage
		}
		node.Items[i] = frontend.SelectorItem{
			ID:      session.Path,
			Label:   strings.TrimSpace(sessionControlChars.ReplaceAllString(label, " ")),
			Detail:  s.nativeDetail(session),
			Checked: s.isCurrentSession(session.Path),
			Depth:   display.Depth,
		}
	}
	if s.selected >= 0 && s.selected < len(s.filtered) {
		node.Selected = s.filtered[s.selected].Session.Path
	}
	return node, true
}

// nativeStatus is the header's load progress, name filter and sort, then the
// status message that replaces its hints, as renderNode and
// sessionSelectorHeader draw them.
func (s *SessionSelectorComponent) nativeStatus() string {
	var b strings.Builder
	if s.loading {
		b.WriteString("Loading ")
		if s.loadProgress != nil {
			b.WriteString(strconv.Itoa(s.loadProgress[0]) + "/" + strconv.Itoa(s.loadProgress[1]))
		} else {
			b.WriteString("...")
		}
		b.WriteString("  ")
	}
	b.WriteString("Name: ")
	if s.nameFilter == sessionNameNamed {
		b.WriteString("Named")
	} else {
		b.WriteString("All")
	}
	b.WriteString("  Sort: ")
	switch s.sortMode {
	case sessionSortRecent:
		b.WriteString("Recent")
	case sessionSortRelevance:
		b.WriteString("Fuzzy")
	default:
		b.WriteString("Threaded")
	}
	if s.statusState.message != "" {
		b.WriteString("\n")
		b.WriteString(s.statusState.message)
	}
	return b.String()
}

// nativeDetail is the right part of a session row: the path while paths
// show, the working directory in the all scope, then the message count and
// age.
func (s *SessionSelectorComponent) nativeDetail(session SessionInfo) string {
	detail := strconv.Itoa(session.MessageCount) + " " + sessionAge(session.Modified)
	if s.scope == sessionScopeAll && session.CWD != "" {
		detail = shortenSessionPath(session.CWD) + " " + detail
	}
	if s.showPath {
		detail = shortenSessionPath(session.Path) + " " + detail
	}
	return detail
}

// isCurrentSession reports the active session, which renderNode marks in the
// accent color. Only a path with the active file's name is canonicalized, so
// a long list does not resolve every path on each frame.
func (s *SessionSelectorComponent) isCurrentSession(path string) bool {
	if s.currentPath == "" {
		return false
	}
	if path == s.currentPath {
		return true
	}
	return filepath.Base(path) == filepath.Base(s.currentPath) && canonicalSessionPath(path) == s.currentPath
}

// NativeNode reports the levels as a selector whose item ids are the levels;
// the current level's check column becomes Checked.
func (s *ThinkingSelectorComponent) NativeNode() (frontend.Node, bool) {
	listNode, ok := s.selectList.NativeNode()
	list, isSelector := listNode.(frontend.Selector)
	if !ok || !isSelector {
		return nil, false
	}
	node := frontend.Selector{
		Title:       "Thinking Level",
		Description: widthx.StripAnsi(tui.ActionKeyDisplayText("app.thinking.cycle") + " cycles thinking levels in-session"),
		Searchable:  true,
		Query:       s.searchInput.Text(),
		Items:       list.Items,
	}
	node.Selected = list.Selected
	nativeCheckColumn(node.Items)
	return node, true
}

// NativeNode reports the trust choices as a selector whose item ids are
// option indexes; the saved decision is Checked. The heading, the folder and
// the saved and current decisions are the texts above the list.
func (s *TrustSelectorComponent) NativeNode() (frontend.Node, bool) {
	var texts []string
	for _, child := range s.Children() {
		if text, ok := child.(*tui.Text); ok {
			texts = append(texts, widthx.StripAnsi(text.Content))
		}
	}
	// The texts are the heading, the folder, the saved decision, the current
	// session's decision and the key hints.
	if len(texts) < 4 {
		return nil, false
	}
	node := frontend.Selector{
		Title:       texts[0],
		Description: strings.Join(texts[1:4], "\n"),
		Items:       make([]frontend.SelectorItem, len(s.trustOptions)),
	}
	for i, option := range s.trustOptions {
		node.Items[i] = frontend.SelectorItem{ID: strconv.Itoa(i), Label: option.Label, Checked: s.isSavedOption(option)}
	}
	if s.selectedIndex >= 0 && s.selectedIndex < len(s.trustOptions) {
		node.Selected = strconv.Itoa(s.selectedIndex)
	}
	return node, true
}

// NativeNode reports the selector's settings list, or the submenu it has
// open.
func (s *SettingsSelectorComponent) NativeNode() (frontend.Node, bool) {
	return s.settingsList.NativeNode()
}

// NativeNode reports the theme menu it shows: the single theme selector or
// the automatic theme settings.
func (m *themeSubmenu) NativeNode() (frontend.Node, bool) {
	if native, ok := m.content.(tui.NativeComponent); ok {
		return native.NativeNode()
	}
	return nil, false
}

// NativeNode reports the automatic theme settings, or, while a theme choice
// is open, its selector.
func (m *automaticThemeMenu) NativeNode() (frontend.Node, bool) {
	return m.list.NativeNode()
}

// NativeNode reports the submenu; a theme selector's check column, the
// current theme, becomes Checked.
func (s *selectSubmenuStep) NativeNode() (frontend.Node, bool) {
	return nativeCheckedSubmenu(s.submenu)
}

// NativeNode reports the warnings as settings.
func (m *warningSettingsSubmenu) NativeNode() (frontend.Node, bool) {
	return m.list.NativeNode()
}

// NativeNode reports the active step's submenu; a step whose labels carry a
// check column, such as the per-model thinking levels, reports it as Checked.
func (s *SteppedSubmenu) NativeNode() (frontend.Node, bool) {
	return nativeCheckedSubmenu(s.activeComponent)
}

// nativeCheckedSubmenu is sel's node with its labels' check column moved to
// Checked.
func nativeCheckedSubmenu(sel *tui.SelectSubmenuComponent) (frontend.Node, bool) {
	node, ok := sel.NativeNode()
	if selector, isSelector := node.(frontend.Selector); ok && isSelector {
		nativeCheckColumn(selector.Items)
	}
	return node, ok
}

// nativeCheckColumn moves a "✓ " check column at the start of every label,
// blank ("  ") for the unchecked items, to Checked. Labels without the
// column on every item are left as they are.
func nativeCheckColumn(items []frontend.SelectorItem) {
	for _, item := range items {
		if !strings.HasPrefix(item.Label, "✓ ") && !strings.HasPrefix(item.Label, "  ") {
			return
		}
	}
	for i := range items {
		label, checked := strings.CutPrefix(items[i].Label, "✓ ")
		if !checked {
			label = items[i].Label[2:]
		}
		items[i].Label, items[i].Checked = label, checked
	}
}
