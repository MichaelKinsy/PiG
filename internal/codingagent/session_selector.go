// Ports packages/coding-agent/src/modes/interactive/components/session-selector.ts.
package codingagent

import (
	"cmp"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
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

type SessionSelectorComponent struct {
	*tui.Container
	currentLoader sessionsLoader
	allLoader     sessionsLoader
	work          sessionSelectorWork
	currentLoad   *sessionLoad
	allLoad       *sessionLoad
	loading       bool
	loadProgress  *[2]int
	renameSession func(path, name string) error
	deleteSession func(path string) sessionDeleteResult
	currentPath   string
	keybindings   *KeybindingsManager

	scope          sessionScope
	sortMode       sessionSortMode
	nameFilter     sessionNameFilter
	showPath       bool
	showRenameHint bool

	// requestRender is session-selector.ts:775 requestRender; nil is a no-op.
	requestRender func()

	searchInput      *tui.TextInput
	current          []SessionInfo
	all              []SessionInfo
	filtered         []sessionDisplayNode
	selected         int
	selectionTouched bool
	done             bool
	cancelled        bool
	selectedPath     string
	operationError   error

	confirmDelete string
	statusState   sessionSelectorStatus

	renameMode  bool
	renamePath  string
	renameInput *tui.TextInput

	list *SessionList // session-selector.ts sessionList: the handle GetSessionList returns
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

func newSessionSelectorWithLoaders(currentLoader, allLoader sessionsLoader, renameSession func(path, name string) error, deleteSession func(path string) sessionDeleteResult, currentPath string, kb *KeybindingsManager) *SessionSelectorComponent {
	if kb == nil {
		kb = DefaultKeybindingsManager()
	}
	s := &SessionSelectorComponent{
		Container:      tui.NewContainer(),
		work:           sessionSelectorWork{updates: make(chan func()), ready: make(chan struct{}, 1)},
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
		renameInput:    tui.NewInput(tui.InputOptions{}),
		showRenameHint: true,
	}
	s.searchInput.Focused = true
	// session-selector.ts: the search Input's own submit (tui.input.submit, or LF when tui.select.confirm is rebound) selects the highlighted session.
	s.searchInput.OnSubmit = func(string) { s.selectHighlighted() }
	s.renameInput.OnSubmit = func(value string) {
		s.operationError = s.confirmRename(value)
	}
	s.renameInput.OnEscape = s.exitRenameMode
	s.buildBaseLayout()
	s.loadScope(sessionScopeCurrent)
	return s
}

func (s *SessionSelectorComponent) Done() bool      { return s.done || s.operationError != nil }
func (s *SessionSelectorComponent) Cancelled() bool { return s.cancelled }
func (s *SessionSelectorComponent) SelectedPath() string {
	return s.selectedPath
}

// sessionSelectorMaxVisible is upstream SessionList.maxVisible.
const sessionSelectorMaxVisible = 10

// IsDirty is always true: loads, status expiry and rename completion change what the selector renders without invalidating it, so a parent Container must render it every frame.
func (s *SessionSelectorComponent) IsDirty() bool { return true }

// buildBaseLayout mirrors upstream SessionSelectorComponent.buildBaseLayout: spacer, accent border, spacer, header and spacer (list mode only), content, spacer, accent border. Entering or leaving rename mode rebuilds it. The list owns a focused Input that positions the terminal cursor within its search row.
func (s *SessionSelectorComponent) buildBaseLayout() {
	s.Clear()
	s.Add(tui.NewSpacer(1))
	s.Add(tui.NewDynamicBorderToken("accent"))
	s.Add(tui.NewSpacer(1))
	if s.renameMode {
		// Upstream enterRenameMode adds the title and hint as Text(..., 1, 0) and hides the header.
		panel := tui.NewContainer()
		panel.Add(tui.NewPaddedText(sessionBold("Rename Session"), 1, 0, nil))
		panel.Add(tui.NewSpacer(1))
		panel.Add(s.renameInput)
		panel.Add(tui.NewSpacer(1))
		hint := sessionFg(tui.ActiveTheme().Muted, sessionKeyText(s, tui.KBSelectConfirm)+" to save · "+sessionKeyText(s, tui.KBSelectCancel)+" to cancel")
		panel.Add(tui.NewPaddedText(hint, 1, 0, nil))
		s.Add(panel)
	} else {
		s.Add(sessionSection(func(width int) []string { return sessionSelectorHeader(s, width) }))
		s.Add(tui.NewSpacer(1))
		s.Add(sessionSection(s.renderList))
	}
	s.Add(tui.NewSpacer(1))
	s.Add(tui.NewDynamicBorderToken("accent"))
}

// sessionSection renders the header or the list (upstream SessionSelectorHeader, SessionList) from the selector's current state on every frame, so the parent Container never serves a stale one.
type sessionSection func(width int) []string

func (f sessionSection) Render(width int) []string { return f(width) }
func (sessionSection) Invalidate()                 {}

// renderList mirrors upstream SessionList.render.
func (s *SessionSelectorComponent) renderList(width int) []string {
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
func sessionSelectorHeader(s *SessionSelectorComponent, width int) []string {
	s.expireStatusMessage()
	th := tui.ActiveTheme()
	title := "Resume Session (All)"
	scopeText := sessionFg(th.Muted, "○ Current Folder | ") + sessionFg(th.Accent, "◉ All")
	if s.scope == sessionScopeCurrent {
		title = "Resume Session (Current Folder)"
		scopeText = sessionFg(th.Accent, "◉ Current Folder") + sessionFg(th.Muted, " | ○ All")
	}
	if s.loading {
		progressText := "..."
		if s.loadProgress != nil {
			progressText = fmt.Sprintf("%d/%d", s.loadProgress[0], s.loadProgress[1])
		}
		scopeText = sessionFg(th.Muted, "○ Current Folder | ") + sessionFg(th.Accent, "Loading "+progressText)
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
	case s.statusState.message != "":
		color := th.Accent
		if s.statusState.error {
			color = th.Error
		}
		return []string{line1, sessionFg(color, widthx.TruncateToWidth(s.statusState.message, width, "…", false)), ""}
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
func sessionKeyText(s *SessionSelectorComponent, action string) string {
	var keys []string
	if strings.HasPrefix(action, "app.") && s.keybindings != nil {
		for _, key := range s.keybindings.GetKeys(action) {
			keys = append(keys, string(key))
		}
	} else {
		keys = tui.GetTUIKeybindings().GetKeys(action)
	}
	return tui.FormatKeyText(strings.Join(keys, "/"), false)
}

// sessionKeyHint mirrors upstream keyHint: dim keys, muted description.
func sessionKeyHint(s *SessionSelectorComponent, action, description string) string {
	th := tui.ActiveTheme()
	return sessionFg(th.Dim, sessionKeyText(s, action)) + sessionFg(th.Muted, " "+description)
}

func sessionFg(color, text string) string {
	if color == "" {
		return text
	}
	return color + text + tui.FgClose(color)
}

func sessionBold(text string) string { return "\x1b[1m" + text + tui.SGRBoldDimReset }

func (s *SessionSelectorComponent) HandleInput(data string) {
	s.drainLoadUpdates()
	if s.done {
		return
	}
	if s.renameMode {
		s.renameInput.HandleInput(data)
		return
	}
	if s.confirmDelete != "" {
		kb := tui.GetTUIKeybindings()
		switch {
		case kb.Matches(data, tui.KBSelectConfirm):
			pathToDelete := s.confirmDelete
			s.setConfirmDelete("")
			if s.deleteSession != nil {
				if result := s.deleteSession(pathToDelete); !result.ok {
					errorMessage := result.error
					if errorMessage == "" {
						errorMessage = "Unknown error"
					}
					s.setStatusMessage("Failed to delete: "+errorMessage, true, sessionSelectorErrorTimeout)
				} else {
					// Pi session-selector.ts:845-859 publishes the deletion before awaiting a refresh, so stale rows cannot be selected while loading.
					deleted := func(session SessionInfo) bool { return session.Path == pathToDelete }
					s.current = slices.DeleteFunc(slices.Clone(s.current), deleted)
					s.all = slices.DeleteFunc(slices.Clone(s.all), deleted)
					s.refilterLoadedSessions()
					msg := "Session deleted"
					if result.method == sessionDeleteTrash {
						msg = "Session moved to trash"
					}
					s.setStatusMessage(msg, false, sessionSelectorInfoTimeout)
					s.refreshCurrentScope()
				}
			}
			return
		case kb.Matches(data, tui.KBSelectCancel):
			s.setConfirmDelete("")
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
	case s.keybindings.Matches(data, "app.session.delete"):
		s.startDeleteConfirmation()
		return
	case s.keybindings.Matches(data, "app.session.rename"):
		s.enterRenameMode()
		return
	case s.keybindings.Matches(data, "app.session.deleteNoninvasive"):
		if s.searchInput.Text() != "" {
			s.searchInput.HandleInput(data)
			s.refilter()
		} else {
			s.startDeleteConfirmation()
		}
		return
	}
	// session-selector.ts handleInput sets selectionTouched before the first list action and checks the list actions in this order.
	s.selectionTouched = true
	switch kb := tui.GetTUIKeybindings(); {
	case kb.Matches(data, tui.KBSelectUp):
		s.move(-1)
	case kb.Matches(data, tui.KBSelectDown):
		s.move(1)
	case kb.Matches(data, tui.KBSelectPageUp):
		s.move(-sessionSelectorMaxVisible)
	case kb.Matches(data, tui.KBSelectPageDown):
		s.move(sessionSelectorMaxVisible)
	case kb.Matches(data, tui.KBSelectConfirm):
		s.selectHighlighted()
	case kb.Matches(data, tui.KBSelectCancel):
		s.clearStatusMessage()
		s.cancelled = true
		s.done = true
		s.cancelLoads()
		if s.list != nil && s.list.OnCancel != nil {
			s.list.OnCancel()
		}
	default:
		s.searchInput.HandleInput(data)
		s.refilter()
	}
}

func (s *SessionSelectorComponent) selectHighlighted() {
	if len(s.filtered) == 0 {
		return
	}
	s.clearStatusMessage()
	s.selectedPath = s.filtered[s.selected].Session.Path
	s.done = true
	s.cancelLoads()
	if s.list != nil && s.list.OnSelect != nil {
		s.list.OnSelect(s.selectedPath)
	}
}

// renderRequested is the `this.requestRender()` call session-selector.ts makes after a status, scope, sort, filter or load change.
func (s *SessionSelectorComponent) renderRequested() {
	if s.requestRender != nil {
		s.requestRender()
	}
}

func (s *SessionSelectorComponent) toggleSortMode() {
	switch s.sortMode {
	case sessionSortThreaded:
		s.sortMode = sessionSortRecent
	case sessionSortRecent:
		s.sortMode = sessionSortRelevance
	default:
		s.sortMode = sessionSortThreaded
	}
	s.refilter()
	s.renderRequested()
}

func (s *SessionSelectorComponent) toggleNameFilter() {
	if s.nameFilter == sessionNameAll {
		s.nameFilter = sessionNameNamed
	} else {
		s.nameFilter = sessionNameAll
	}
	s.refilter()
	s.renderRequested()
}

func (s *SessionSelectorComponent) startDeleteConfirmation() {
	if len(s.filtered) == 0 {
		return
	}
	selected := s.filtered[s.selected].Session
	if canonicalSessionPath(selected.Path) == s.currentPath {
		s.setStatusMessage("Cannot delete the currently active session", true, sessionSelectorErrorTimeout)
		return
	}
	s.setConfirmDelete(selected.Path)
}

func (s *SessionSelectorComponent) enterRenameMode() {
	if s.scopeLoad(s.scope) != nil || len(s.filtered) == 0 || s.renameSession == nil {
		return
	}
	selected := s.filtered[s.selected].Session
	s.renameMode = true
	defer s.buildBaseLayout()
	s.renamePath = selected.Path
	s.renameInput.SetText(selected.Name)
	s.renameInput.Focused = true
}

// confirmRename preserves header status, exits on rejection, and keeps the panel mounted until a successful refresh settles. The owner surfaces input-callback errors.
func (s *SessionSelectorComponent) confirmRename(value string) error {
	next := jsTrim(value)
	if next == "" {
		return nil
	}
	exitImmediately := true
	defer func() {
		if exitImmediately {
			s.exitRenameMode()
		}
	}()
	if s.renameSession == nil || s.renamePath == "" {
		return nil
	}
	if err := s.renameSession(s.renamePath, next); err != nil {
		return err
	}
	load := s.refreshCurrentScope()
	load.complete = s.exitRenameMode
	exitImmediately = false
	return nil
}

// exitRenameMode mirrors upstream SessionSelectorComponent.exitRenameMode
// (session-selector.ts:908): it returns to the existing list without touching
// the search Input, so its text, cursor, undo and kill-ring state survive.
func (s *SessionSelectorComponent) exitRenameMode() {
	s.renameMode = false
	s.renamePath = ""
	s.buildBaseLayout()
	s.refilter()
}

func (s *SessionSelectorComponent) move(delta int) {
	s.selectionTouched = true
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

// refilter applies the scope, name filter, query, and sort without excluding the active session.
func (s *SessionSelectorComponent) refilter() {
	var base []SessionInfo
	if s.scope == sessionScopeAll {
		base = s.all
	} else {
		base = s.current
	}
	if s.nameFilter == sessionNameNamed {
		filtered := make([]SessionInfo, 0, len(base))
		for _, sess := range base {
			if jsTrim(sess.Name) != "" {
				filtered = append(filtered, sess)
			}
		}
		base = filtered
	}
	trimmed := jsTrim(s.searchInput.Text())
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
	Session        SessionInfo
	Children       []*sessionTreeNode
	LatestActivity int64
}

// buildSessionTree groups canonical paths and stably orders each subtree by its most recent millisecond timestamp.
func buildSessionTree(sessions []SessionInfo) []*sessionTreeNode {
	byPath := make(map[string]*sessionTreeNode, len(sessions))
	for _, sess := range sessions {
		key := canonicalSessionPath(sess.Path)
		byPath[key] = &sessionTreeNode{Session: sess}
	}
	var roots []*sessionTreeNode
	for _, sess := range sessions {
		node := byPath[canonicalSessionPath(sess.Path)]
		parent := canonicalSessionPath(sess.ParentSessionPath)
		if parent != "" {
			if p, ok := byPath[parent]; ok {
				p.Children = append(p.Children, node)
				continue
			}
		}
		roots = append(roots, node)
	}
	var updateLatestActivity func(*sessionTreeNode) int64
	updateLatestActivity = func(node *sessionTreeNode) int64 {
		latest := node.Session.Modified.UnixMilli()
		for _, child := range node.Children {
			latest = max(latest, updateLatestActivity(child))
		}
		node.LatestActivity = latest
		return latest
	}
	for _, root := range roots {
		updateLatestActivity(root)
	}
	var sortNodes func([]*sessionTreeNode)
	sortNodes = func(nodes []*sessionTreeNode) {
		slices.SortStableFunc(nodes, func(a, b *sessionTreeNode) int {
			return cmp.Compare(b.LatestActivity, a.LatestActivity)
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

// Ports packages/coding-agent/src/modes/interactive/components/session-selector-search.ts.
func parseSearchQuery(query string) parsedSearchQuery {
	trimmed := jsTrim(query)
	if trimmed == "" {
		return parsedSearchQuery{mode: "tokens"}
	}
	if after, ok := strings.CutPrefix(trimmed, "re:"); ok {
		pattern := jsTrim(after)
		if pattern == "" {
			return parsedSearchQuery{mode: "regex", error: "Empty regex"}
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
		v := jsTrim(buf.String())
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
		case !inQuote && isJSWhitespace(r):
			flush("fuzzy")
		default:
			buf.WriteRune(r)
		}
	}
	if inQuote {
		// fallback: plain tokens on unclosed quote
		parts := strings.FieldsFunc(trimmed, isJSWhitespace)
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

// filterAndSortSessions preserves incoming order for empty queries and Recent mode; otherwise it ranks by Pi's fuzzy/phrase scores, with stable modified-time ties.
func filterAndSortSessions(sessions []SessionInfo, query string, sortMode sessionSortMode) []SessionInfo {
	trimmed := jsTrim(query)
	if trimmed == "" {
		return slices.Clone(sessions)
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
		return out
	}
	slices.SortStableFunc(matches, func(a, b scored) int {
		if a.Score < b.Score {
			return -1
		}
		if a.Score > b.Score {
			return 1
		}
		return cmp.Compare(b.Session.Modified.UnixMilli(), a.Session.Modified.UnixMilli())
	})
	out := make([]SessionInfo, len(matches))
	for i, m := range matches {
		out[i] = m.Session
	}
	return out
}

func matchSession(session SessionInfo, parsed parsedSearchQuery) (bool, float64) {
	text := sessionSearchText(session)
	if parsed.mode == "regex" {
		if parsed.regex == nil {
			return false, 0
		}
		idx := parsed.regex.FindStringIndex(text)
		if idx == nil {
			return false, 0
		}
		return true, float64(jsstring.Length(text[:idx[0]])) * 0.1
	}
	if len(parsed.tokens) == 0 {
		return true, 0
	}
	var total float64
	var normalizedText string
	for _, tok := range parsed.tokens {
		if tok.kind == "phrase" {
			if normalizedText == "" {
				normalizedText = normalizeWhitespaceLower(text)
			}
			phrase := normalizeWhitespaceLower(tok.value)
			if phrase == "" {
				continue
			}
			idx := strings.Index(normalizedText, phrase)
			if idx < 0 {
				return false, 0
			}
			total += float64(jsstring.Length(normalizedText[:idx])) * 0.1
			continue
		}
		match := tui.FuzzyMatchScore(tok.value, text)
		if !match.Matches {
			return false, 0
		}
		total += match.Score
	}
	return true, total
}

func normalizeWhitespaceLower(text string) string {
	return strings.Join(strings.FieldsFunc(cases.Lower(language.Und).String(text), isJSWhitespace), " ")
}

// renderNode mirrors one upstream SessionList row: cursor, dim tree prefix,
// the (styled) name or first message, and right-aligned count and age.
func (s *SessionSelectorComponent) renderNode(node sessionDisplayNode, selected bool, width int) string {
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
	// upstream: session-selector.ts SessionList.render truncateToWidth(line, width) with its default "..." ellipsis
	return widthx.TruncateToWidth(line, width, "...", false)
}

var sessionControlChars = lazyregexp.New(`[\x00-\x1f\x7f]`)

func (s *SessionSelectorComponent) buildTreePrefix(node sessionDisplayNode) string {
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

// SetFocused propagates TUI focus to the session list's search input and to the rename input, which emit the hardware-cursor marker only while focused (upstream `set focused`, session-selector.ts:733-740).
func (s *SessionSelectorComponent) SetFocused(focused bool) {
	s.searchInput.SetFocused(focused)
	s.renameInput.SetFocused(focused)
}

// Focused reports whether the session list holds the TUI focus (upstream `get focused`).
func (s *SessionSelectorComponent) Focused() bool { return s.searchInput.Focused }

// SessionList is upstream's SessionList (session-selector.ts): the session rows of a SessionSelectorComponent, the component the host focuses.
// Its callbacks run when the selector handles the confirm or cancel key and when the delete confirmation changes.
type SessionList struct {
	s *SessionSelectorComponent
	// OnSelect runs with the selected session's path on the confirm key.
	OnSelect func(path string)
	// OnCancel runs on the cancel key.
	OnCancel func()
	// OnExit is the host's exit hook (session-selector.ts:302 onExit); the list never raises it itself.
	OnExit func()
	// OnDeleteConfirmationChange runs with the path awaiting delete confirmation, or "" (upstream null) when the confirmation ends.
	OnDeleteConfirmationChange func(path string)
}

// GetSessionList returns the session list (session-selector.ts getSessionList).
func (s *SessionSelectorComponent) GetSessionList() *SessionList {
	if s.list == nil {
		s.list = &SessionList{s: s}
	}
	return s.list
}

// HandleInput delegates to the selector's key handling (SessionList.handleInput).
func (l *SessionList) HandleInput(data string) { l.s.HandleInput(data) }

// GetSelectedSessionPath returns the path of the highlighted session, or "" when no row is visible (SessionList.getSelectedSessionPath).
func (l *SessionList) GetSelectedSessionPath() string {
	if l.s.selected < 0 || l.s.selected >= len(l.s.filtered) {
		return ""
	}
	return l.s.filtered[l.s.selected].Session.Path
}

// setConfirmDelete starts or ends the delete confirmation and reports the change (SessionList.setConfirmingDeletePath).
func (s *SessionSelectorComponent) setConfirmDelete(path string) {
	s.confirmDelete = path
	if s.list != nil && s.list.OnDeleteConfirmationChange != nil {
		s.list.OnDeleteConfirmationChange(path)
	}
}
