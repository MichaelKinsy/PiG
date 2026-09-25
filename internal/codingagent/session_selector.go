package codingagent

import (
	"cmp"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type sessionScope string

type sessionSortMode string

type sessionNameFilter string

const (
	sessionScopeCurrent sessionScope = "current"
	sessionScopeAll     sessionScope = "all"

	sessionSortThreaded  sessionSortMode = "threaded"
	sessionSortRecent    sessionSortMode = "recent"
	sessionSortRelevance sessionSortMode = "relevance"

	sessionNameAll   sessionNameFilter = "all"
	sessionNameNamed sessionNameFilter = "named"
)

type sessionSelector struct {
	currentLoader func() ([]SessionInfo, error)
	allLoader     func() ([]SessionInfo, error)
	renameSession func(path, name string) error
	deleteSession func(path string) error
	currentPath   string
	keybindings   *KeybindingsManager

	scope          sessionScope
	sortMode       sessionSortMode
	nameFilter     sessionNameFilter
	showPath       bool
	showRenameHint bool

	searchInput  *tui.TextInput
	current      []SessionInfo
	all          []SessionInfo
	filtered     []sessionDisplayNode
	selected     int
	done         bool
	cancelled    bool
	selectedPath string

	confirmDelete string
	status        string
	statusError   bool

	renameMode  bool
	renamePath  string
	renameInput *tui.TextInput
}

type sessionDisplayNode struct {
	Session   SessionInfo
	Depth     int
	IsLast    bool
	Ancestors []bool
}

type parsedSearchQuery struct {
	mode   string // tokens|regex
	tokens []searchToken
	regex  *regexp.Regexp
	error  string
}

type searchToken struct {
	kind  string // fuzzy|phrase
	value string
}

func newSessionSelector(currentLoader, allLoader func() ([]SessionInfo, error), renameSession func(path, name string) error, deleteSession func(path string) error, currentPath string, kb *KeybindingsManager) *sessionSelector {
	if kb == nil {
		kb = DefaultKeybindingsManager()
	}
	s := &sessionSelector{
		currentLoader:  currentLoader,
		allLoader:      allLoader,
		renameSession:  renameSession,
		deleteSession:  deleteSession,
		currentPath:    canonicalSessionPath(currentPath),
		keybindings:    kb,
		scope:          sessionScopeCurrent,
		sortMode:       sessionSortThreaded,
		nameFilter:     sessionNameAll,
		searchInput:    tui.NewInput(tui.InputOptions{}),
		renameInput:    tui.NewTextInput("Rename Session"),
		showRenameHint: true,
	}
	s.loadScope(sessionScopeCurrent)
	return s
}

func (s *sessionSelector) Done() bool      { return s.done }
func (s *sessionSelector) Cancelled() bool { return s.cancelled }
func (s *sessionSelector) SelectedPath() string {
	return s.selectedPath
}

func (s *sessionSelector) Invalidate() {}

// sessionSelectorMaxVisible is upstream SessionList.maxVisible.
const sessionSelectorMaxVisible = 10

// Render mirrors upstream SessionSelectorComponent.buildBaseLayout: spacer, accent border, spacer, header and spacer (list mode only), content, spacer, accent border. The list owns a focused Input that positions the terminal cursor within its search row.
func (s *sessionSelector) Render(width int) []string {
	border := tui.NewDynamicBorder(tui.ActiveTheme().Accent).Render(width)[0]
	lines := []string{"", border, ""}
	if s.renameMode {
		// Upstream enterRenameMode adds the title and hint as Text(..., 1, 0)
		// and hides the header.
		lines = append(lines, tui.NewPaddedText(sessionBold("Rename Session"), 1, 0, nil).Render(width)...)
		lines = append(lines, "")
		lines = append(lines, s.renameInput.Render(width)...)
		lines = append(lines, "")
		hint := sessionFg(tui.ActiveTheme().Muted, sessionKeyText(s, tui.KBSelectConfirm)+" to save · "+sessionKeyText(s, tui.KBSelectCancel)+" to cancel")
		lines = append(lines, tui.NewPaddedText(hint, 1, 0, nil).Render(width)...)
		return append(lines, "", border)
	}
	lines = append(lines, sessionSelectorHeader(s, width)...)
	lines = append(lines, "")
	lines = append(lines, s.renderList(width)...)
	return append(lines, "", border)
}

// renderList mirrors upstream SessionList.render.
func (s *sessionSelector) renderList(width int) []string {
	lines := append(s.searchInput.Render(width), "")
	if len(s.filtered) == 0 {
		var msg string
		switch {
		case s.nameFilter == sessionNameNamed && s.scope == sessionScopeAll:
			msg = "  No named sessions found. Press " + sessionKeyText(s, "app.session.toggleNamedFilter") + " to show all."
		case s.nameFilter == sessionNameNamed:
			msg = "  No named sessions in current folder. Press " + sessionKeyText(s, "app.session.toggleNamedFilter") + " to show all, or Tab to view all."
		case s.scope == sessionScopeAll:
			msg = "  No sessions found"
		default:
			msg = "  No sessions in current folder. Press Tab to view all."
		}
		return append(lines, sessionFg(tui.ActiveTheme().Muted, widthx.TruncateToWidth(msg, width, "…", false)))
	}
	start := max(0, min(s.selected-sessionSelectorMaxVisible/2, len(s.filtered)-sessionSelectorMaxVisible))
	end := min(start+sessionSelectorMaxVisible, len(s.filtered))
	for i := start; i < end; i++ {
		lines = append(lines, s.renderNode(s.filtered[i], i == s.selected, width))
	}
	if start > 0 || end < len(s.filtered) {
		scrollText := fmt.Sprintf("  (%d/%d)", s.selected+1, len(s.filtered))
		lines = append(lines, sessionFg(tui.ActiveTheme().Muted, widthx.TruncateToWidth(scrollText, width, "", false)))
	}
	return lines
}

// sessionSelectorHeader mirrors upstream SessionSelectorHeader.render: a bold
// title with right-aligned scope, name filter and sort, then two hint rows
// that a delete confirmation or status message replaces.
func sessionSelectorHeader(s *sessionSelector, width int) []string {
	th := tui.ActiveTheme()
	title := "Resume Session (All)"
	scopeText := sessionFg(th.Muted, "○ Current Folder | ") + sessionFg(th.Accent, "◉ All")
	if s.scope == sessionScopeCurrent {
		title = "Resume Session (Current Folder)"
		scopeText = sessionFg(th.Accent, "◉ Current Folder") + sessionFg(th.Muted, " | ○ All")
	}
	sortLabel := map[sessionSortMode]string{sessionSortThreaded: "Threaded", sessionSortRecent: "Recent", sessionSortRelevance: "Fuzzy"}[s.sortMode]
	nameLabel := map[sessionNameFilter]string{sessionNameAll: "All", sessionNameNamed: "Named"}[s.nameFilter]
	sortText := sessionFg(th.Muted, "Sort: ") + sessionFg(th.Accent, sortLabel)
	nameText := sessionFg(th.Muted, "Name: ") + sessionFg(th.Accent, nameLabel)
	rightText := widthx.TruncateToWidth(scopeText+"  "+nameText+"  "+sortText, width, "", false)
	left := widthx.TruncateToWidth(sessionBold(title), max(0, width-widthx.VisibleWidth(rightText)-1), "", false)
	spacing := max(0, width-widthx.VisibleWidth(left)-widthx.VisibleWidth(rightText))
	line1 := left + strings.Repeat(" ", spacing) + rightText

	switch {
	case s.confirmDelete != "":
		hint := "Delete session? " + sessionKeyHint(s, tui.KBSelectConfirm, "confirm") + " · " + sessionKeyHint(s, tui.KBSelectCancel, "cancel")
		return []string{line1, sessionFg(th.Error, widthx.TruncateToWidth(hint, width, "…", false)), ""}
	case s.status != "":
		color := th.Accent
		if s.statusError {
			color = th.Error
		}
		return []string{line1, sessionFg(color, widthx.TruncateToWidth(s.status, width, "…", false)), ""}
	}
	sep := sessionFg(th.Muted, " · ")
	hint1 := sessionKeyHint(s, tui.KBInputTab, "scope") + sep + sessionFg(th.Muted, `re:<pattern> regex · "phrase" exact`)
	pathState := "(off)"
	if s.showPath {
		pathState = "(on)"
	}
	parts := []string{
		sessionKeyHint(s, "app.session.toggleSort", "sort"),
		sessionKeyHint(s, "app.session.toggleNamedFilter", "named"),
		sessionKeyHint(s, "app.session.delete", "delete"),
		sessionKeyHint(s, "app.session.togglePath", "path "+pathState),
	}
	if s.showRenameHint {
		parts = append(parts, sessionKeyHint(s, "app.session.rename", "rename"))
	}
	return []string{
		line1,
		widthx.TruncateToWidth(hint1, width, "…", false),
		widthx.TruncateToWidth(strings.Join(parts, sep), width, "…", false),
	}
}

// sessionKeyText mirrors upstream keyText: the bound keys, not capitalized.
func sessionKeyText(s *sessionSelector, action string) string {
	var keys []string
	if strings.HasPrefix(action, "app.") && s.keybindings != nil {
		for _, key := range s.keybindings.Get(action) {
			keys = append(keys, string(key))
		}
	} else {
		keys = tui.GetTUIKeybindings().GetKeys(action)
	}
	return tui.FormatKeyText(strings.Join(keys, "/"), false)
}

// sessionKeyHint mirrors upstream keyHint: dim keys, muted description.
func sessionKeyHint(s *sessionSelector, action, description string) string {
	th := tui.ActiveTheme()
	return sessionFg(th.Dim, sessionKeyText(s, action)) + sessionFg(th.Muted, " "+description)
}

func sessionFg(color, text string) string {
	if color == "" {
		return text
	}
	return color + text + tui.SGRFgReset
}

func sessionBold(text string) string { return "\x1b[1m" + text + tui.SGRBoldDimReset }

func (s *sessionSelector) HandleInput(data string) {
	if s.done {
		return
	}
	if s.renameMode {
		s.renameInput.HandleInput(data)
		if s.renameInput.Done() {
			if s.renameInput.Cancelled() {
				s.exitRenameMode()
				return
			}
			next := strings.TrimSpace(s.renameInput.Text())
			if next != "" && s.renameSession != nil && s.renamePath != "" {
				if err := s.renameSession(s.renamePath, next); err != nil {
					s.status = "Rename failed: " + err.Error()
					s.statusError = true
				} else {
					s.status = "Session renamed"
					s.statusError = false
					s.refreshCurrentScope()
				}
			}
			s.exitRenameMode()
		}
		return
	}
	if s.confirmDelete != "" {
		kb := tui.GetTUIKeybindings()
		switch {
		case kb.Matches(data, tui.KBSelectConfirm):
			if s.deleteSession != nil {
				if err := s.deleteSession(s.confirmDelete); err != nil {
					s.status = "Delete failed: " + err.Error()
					s.statusError = true
				} else {
					s.status = "Session deleted"
					s.statusError = false
					s.refreshCurrentScope()
				}
			}
			s.confirmDelete = ""
			return
		case kb.Matches(data, tui.KBSelectCancel):
			s.confirmDelete = ""
			return
		default:
			return
		}
	}

	switch {
	case tui.GetTUIKeybindings().Matches(data, tui.KBInputTab):
		s.toggleScope()
		return
	case s.keybindings.Matches(data, "app.session.toggleSort"):
		s.toggleSortMode()
		return
	case s.keybindings.Matches(data, "app.session.toggleNamedFilter"):
		s.toggleNameFilter()
		return
	case s.keybindings.Matches(data, "app.session.togglePath"):
		s.showPath = !s.showPath
		return
	case s.keybindings.Matches(data, "app.session.rename"):
		s.enterRenameMode()
		return
	case s.keybindings.Matches(data, "app.session.delete"):
		s.startDeleteConfirmation()
		return
	case s.keybindings.Matches(data, "app.session.deleteNoninvasive"):
		if s.searchInput.Text() != "" {
			s.searchInput.HandleInput(data)
			s.refilter()
		} else {
			s.startDeleteConfirmation()
		}
		return
	case tui.GetTUIKeybindings().Matches(data, tui.KBSelectCancel):
		s.cancelled = true
		s.done = true
		return
	case tui.GetTUIKeybindings().Matches(data, tui.KBSelectConfirm):
		if len(s.filtered) == 0 {
			return
		}
		s.selectedPath = s.filtered[s.selected].Session.Path
		s.done = true
		return
	case tui.GetTUIKeybindings().Matches(data, tui.KBSelectUp):
		s.move(-1)
		return
	case tui.GetTUIKeybindings().Matches(data, tui.KBSelectDown):
		s.move(1)
		return
	case tui.GetTUIKeybindings().Matches(data, tui.KBSelectPageUp):
		s.move(-sessionSelectorMaxVisible)
		return
	case tui.GetTUIKeybindings().Matches(data, tui.KBSelectPageDown):
		s.move(sessionSelectorMaxVisible)
		return
	default:
		s.searchInput.HandleInput(data)
		s.refilter()
	}
}

func (s *sessionSelector) loadScope(scope sessionScope) {
	var infos []SessionInfo
	var err error
	if scope == sessionScopeAll {
		if s.all == nil {
			infos, err = s.allLoader()
			if err != nil {
				s.status = "Failed to load sessions: " + err.Error()
				s.statusError = true
				infos = nil
			}
			s.all = infos
		}
	} else {
		if s.current == nil {
			infos, err = s.currentLoader()
			if err != nil {
				s.status = "Failed to load sessions: " + err.Error()
				s.statusError = true
				infos = nil
			}
			s.current = infos
		}
	}
	s.scope = scope
	s.refilter()
}

func (s *sessionSelector) refreshCurrentScope() {
	if s.scope == sessionScopeCurrent {
		s.current = nil
	} else {
		s.all = nil
		s.current = nil
	}
	s.loadScope(s.scope)
}

func (s *sessionSelector) toggleScope() {
	if s.scope == sessionScopeCurrent {
		s.loadScope(sessionScopeAll)
	} else {
		s.loadScope(sessionScopeCurrent)
	}
}

func (s *sessionSelector) toggleSortMode() {
	switch s.sortMode {
	case sessionSortThreaded:
		s.sortMode = sessionSortRecent
	case sessionSortRecent:
		s.sortMode = sessionSortRelevance
	default:
		s.sortMode = sessionSortThreaded
	}
	s.refilter()
}

func (s *sessionSelector) toggleNameFilter() {
	if s.nameFilter == sessionNameAll {
		s.nameFilter = sessionNameNamed
	} else {
		s.nameFilter = sessionNameAll
	}
	s.refilter()
}

func (s *sessionSelector) startDeleteConfirmation() {
	if len(s.filtered) == 0 {
		return
	}
	selected := s.filtered[s.selected].Session
	if canonicalSessionPath(selected.Path) == s.currentPath {
		s.status = "Cannot delete the currently active session"
		s.statusError = true
		return
	}
	s.confirmDelete = selected.Path
}

func (s *sessionSelector) enterRenameMode() {
	if len(s.filtered) == 0 || s.renameSession == nil {
		return
	}
	selected := s.filtered[s.selected].Session
	s.renameMode = true
	s.renamePath = selected.Path
	s.renameInput = tui.NewTextInput("Rename Session")
	s.renameInput.SetText(selected.Name)
}

// exitRenameMode mirrors upstream SessionSelectorComponent.exitRenameMode
// (session-selector.ts:908): it returns to the existing list without touching
// the search Input, so its text, cursor, undo and kill-ring state survive.
func (s *sessionSelector) exitRenameMode() {
	s.renameMode = false
	s.renamePath = ""
	s.renameInput = tui.NewTextInput("Rename Session")
	s.refilter()
}

func (s *sessionSelector) move(delta int) {
	if len(s.filtered) == 0 {
		s.selected = 0
		return
	}
	s.selected += delta
	if s.selected < 0 {
		s.selected = 0
	}
	if s.selected >= len(s.filtered) {
		s.selected = len(s.filtered) - 1
	}
}

func (s *sessionSelector) refilter() {
	var base []SessionInfo
	if s.scope == sessionScopeAll {
		base = s.all
	} else {
		base = s.current
	}
	if s.currentPath != "" {
		filteredBase := make([]SessionInfo, 0, len(base))
		for _, sess := range base {
			if canonicalSessionPath(sess.Path) == s.currentPath {
				continue
			}
			filteredBase = append(filteredBase, sess)
		}
		base = filteredBase
	}
	if s.nameFilter == sessionNameNamed {
		filtered := make([]SessionInfo, 0, len(base))
		for _, sess := range base {
			if strings.TrimSpace(sess.Name) != "" {
				filtered = append(filtered, sess)
			}
		}
		base = filtered
	}
	trimmed := strings.TrimSpace(s.searchInput.Text())
	if s.sortMode == sessionSortThreaded && trimmed == "" {
		roots := buildSessionTree(base)
		s.filtered = flattenSessionTree(roots)
	} else {
		flat := filterAndSortSessions(base, trimmed, s.sortMode)
		s.filtered = make([]sessionDisplayNode, len(flat))
		for i, sess := range flat {
			s.filtered[i] = sessionDisplayNode{Session: sess}
		}
	}
	if s.selected >= len(s.filtered) {
		s.selected = max(len(s.filtered)-1, 0)
	}
	if s.selected < 0 {
		s.selected = 0
	}
}

func canonicalSessionPath(path string) string {
	return canonicalizePath(path)
}

type sessionTreeNode struct {
	Session  SessionInfo
	Children []*sessionTreeNode
}

func buildSessionTree(sessions []SessionInfo) []*sessionTreeNode {
	byPath := make(map[string]*sessionTreeNode, len(sessions))
	for _, sess := range sessions {
		key := canonicalSessionPath(sess.Path)
		byPath[key] = &sessionTreeNode{Session: sess}
	}
	var roots []*sessionTreeNode
	for _, sess := range sessions {
		node := byPath[canonicalSessionPath(sess.Path)]
		parent := canonicalSessionPath(sess.ParentSession)
		if parent != "" {
			if p, ok := byPath[parent]; ok {
				p.Children = append(p.Children, node)
				continue
			}
		}
		roots = append(roots, node)
	}
	var sortNodes func([]*sessionTreeNode)
	sortNodes = func(nodes []*sessionTreeNode) {
		slices.SortFunc(nodes, func(a, b *sessionTreeNode) int {
			return compareTimeDesc(a.Session.Modified, b.Session.Modified)
		})
		for _, n := range nodes {
			sortNodes(n.Children)
		}
	}
	sortNodes(roots)
	return roots
}

func flattenSessionTree(roots []*sessionTreeNode) []sessionDisplayNode {
	var out []sessionDisplayNode
	var walk func(node *sessionTreeNode, depth int, ancestors []bool, isLast bool)
	walk = func(node *sessionTreeNode, depth int, ancestors []bool, isLast bool) {
		out = append(out, sessionDisplayNode{Session: node.Session, Depth: depth, IsLast: isLast, Ancestors: slices.Clone(ancestors)})
		for i, child := range node.Children {
			cont := false
			if depth > 0 {
				cont = !isLast
			}
			walk(child, depth+1, append(slices.Clone(ancestors), cont), i == len(node.Children)-1)
		}
	}
	for i, root := range roots {
		walk(root, 0, nil, i == len(roots)-1)
	}
	return out
}

func compareTimeDesc(a, b time.Time) int {
	return cmp.Compare(b.UnixNano(), a.UnixNano())
}

func parseSearchQuery(query string) parsedSearchQuery {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return parsedSearchQuery{mode: "tokens"}
	}
	if after, ok := strings.CutPrefix(trimmed, "re:"); ok {
		pattern := strings.TrimSpace(after)
		if pattern == "" {
			return parsedSearchQuery{mode: "regex", error: "empty regex"}
		}
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return parsedSearchQuery{mode: "regex", error: err.Error()}
		}
		return parsedSearchQuery{mode: "regex", regex: re}
	}
	var tokens []searchToken
	var buf strings.Builder
	inQuote := false
	flush := func(kind string) {
		v := strings.TrimSpace(buf.String())
		buf.Reset()
		if v != "" {
			tokens = append(tokens, searchToken{kind: kind, value: v})
		}
	}
	for _, r := range trimmed {
		switch {
		case r == '"':
			if inQuote {
				flush("phrase")
				inQuote = false
			} else {
				flush("fuzzy")
				inQuote = true
			}
		case !inQuote && (r == ' ' || r == '\t' || r == '\n'):
			flush("fuzzy")
		default:
			buf.WriteRune(r)
		}
	}
	if inQuote {
		// fallback: plain tokens on unclosed quote
		parts := strings.Fields(trimmed)
		tokens = tokens[:0]
		for _, p := range parts {
			tokens = append(tokens, searchToken{kind: "fuzzy", value: p})
		}
		return parsedSearchQuery{mode: "tokens", tokens: tokens}
	}
	flush("fuzzy")
	return parsedSearchQuery{mode: "tokens", tokens: tokens}
}

func sessionSearchText(session SessionInfo) string {
	return fmt.Sprintf("%s %s %s %s", session.ID, session.Name, session.AllMessagesText, session.CWD)
}

func filterAndSortSessions(sessions []SessionInfo, query string, sortMode sessionSortMode) []SessionInfo {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		out := slices.Clone(sessions)
		if sortMode != sessionSortThreaded {
			slices.SortFunc(out, func(a, b SessionInfo) int { return compareTimeDesc(a.Modified, b.Modified) })
		}
		return out
	}
	parsed := parseSearchQuery(query)
	if parsed.error != "" {
		return nil
	}
	type scored struct {
		Session SessionInfo
		Score   float64
	}
	var matches []scored
	for _, sess := range sessions {
		ok, score := matchSession(sess, parsed)
		if ok {
			matches = append(matches, scored{Session: sess, Score: score})
		}
	}
	if sortMode == sessionSortRecent {
		out := make([]SessionInfo, len(matches))
		for i, m := range matches {
			out[i] = m.Session
		}
		slices.SortFunc(out, func(a, b SessionInfo) int { return compareTimeDesc(a.Modified, b.Modified) })
		return out
	}
	slices.SortFunc(matches, func(a, b scored) int {
		if a.Score < b.Score {
			return -1
		}
		if a.Score > b.Score {
			return 1
		}
		return compareTimeDesc(a.Session.Modified, b.Session.Modified)
	})
	out := make([]SessionInfo, len(matches))
	for i, m := range matches {
		out[i] = m.Session
	}
	return out
}

func matchSession(session SessionInfo, parsed parsedSearchQuery) (bool, float64) {
	text := sessionSearchText(session)
	lower := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if parsed.mode == "regex" {
		if parsed.regex == nil {
			return false, 0
		}
		idx := parsed.regex.FindStringIndex(text)
		if idx == nil {
			return false, 0
		}
		return true, float64(idx[0]) * 0.1
	}
	if len(parsed.tokens) == 0 {
		return true, 0
	}
	var total float64
	for _, tok := range parsed.tokens {
		needle := strings.ToLower(strings.TrimSpace(tok.value))
		if needle == "" {
			continue
		}
		idx := strings.Index(lower, needle)
		if idx >= 0 {
			total += float64(idx) * 0.1
			continue
		}
		if tok.kind == "fuzzy" && fuzzyContains(lower, needle) {
			total += float64(len(needle))
			continue
		}
		return false, 0
	}
	return true, total
}

func fuzzyContains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	n := []rune(needle)
	j := 0
	for _, r := range haystack {
		if r == n[j] {
			j++
			if j == len(n) {
				return true
			}
		}
	}
	return false
}

// renderNode mirrors one upstream SessionList row: cursor, dim tree prefix,
// the (styled) name or first message, and right-aligned count and age.
func (s *sessionSelector) renderNode(node sessionDisplayNode, selected bool, width int) string {
	th := tui.ActiveTheme()
	session := node.Session
	prefix := s.buildTreePrefix(node)
	hasName := session.Name != ""
	displayText := session.Name
	if !hasName {
		displayText = session.FirstMessage
	}
	message := strings.TrimSpace(sessionControlChars.ReplaceAllString(displayText, " "))

	rightPart := fmt.Sprintf("%d %s", session.MessageCount, sessionAge(session.Modified))
	if s.scope == sessionScopeAll && session.CWD != "" {
		rightPart = shortenSessionPath(session.CWD) + " " + rightPart
	}
	if s.showPath {
		rightPart = shortenSessionPath(session.Path) + " " + rightPart
	}
	cursor := "  "
	if selected {
		cursor = sessionFg(th.Accent, "› ")
	}
	available := width - 2 - widthx.VisibleWidth(prefix) - (widthx.VisibleWidth(rightPart) + 2)
	styled := widthx.TruncateToWidth(message, max(10, available), "…", false)
	isConfirmingDelete := session.Path == s.confirmDelete
	switch {
	case isConfirmingDelete:
		styled = sessionFg(th.Error, styled)
	case canonicalSessionPath(session.Path) == canonicalSessionPath(s.currentPath) && s.currentPath != "":
		styled = sessionFg(th.Accent, styled)
	case hasName:
		styled = sessionFg(th.Warning, styled)
	}
	if selected {
		styled = sessionBold(styled)
	}
	leftPart := cursor + sessionFg(th.Dim, prefix) + styled
	spacing := max(1, width-widthx.VisibleWidth(leftPart)-widthx.VisibleWidth(rightPart))
	rightColor := th.Dim
	if isConfirmingDelete {
		rightColor = th.Error
	}
	line := leftPart + strings.Repeat(" ", spacing) + sessionFg(rightColor, rightPart)
	if selected {
		line = th.SelectedBg + line + th.BgClose
	}
	return widthx.TruncateToWidth(line, width, "…", false)
}

var sessionControlChars = regexp.MustCompile(`[\x00-\x1f\x7f]`)

func (s *sessionSelector) buildTreePrefix(node sessionDisplayNode) string {
	if node.Depth == 0 {
		return ""
	}
	parts := make([]string, 0, len(node.Ancestors)+1)
	for _, cont := range node.Ancestors {
		if cont {
			parts = append(parts, "│  ")
		} else {
			parts = append(parts, "   ")
		}
	}
	if node.IsLast {
		parts = append(parts, "└─ ")
	} else {
		parts = append(parts, "├─ ")
	}
	return strings.Join(parts, "")
}

func shortenSessionPath(path string) string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}
