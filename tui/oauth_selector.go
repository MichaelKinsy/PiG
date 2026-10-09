package tui

// oauth_selector.go: auth provider picker overlay.
//
// Faithful port of upstream oauth-selector.ts: bordered searchable selector with
// title "Select provider to configure:" (or "logout"), cursor "→ " prefix
// on selected row, and auth status indicators for each provider.
//
// Keys: Up/Down navigate (clamped), Enter confirms, Esc cancels, and every
// other key edits the fuzzy search input.

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// AuthMethod is an auth method of a provider option: an ai.APIKeyAuth or ai.OAuthAuth.
type AuthMethod interface {
	AuthMethodName() string
}

// OAuthProvider is one entry in the auth provider selector.
type OAuthProvider struct {
	ID       string // e.g. "github-copilot"
	Name     string // display name
	AuthType string // "oauth" or "api_key"
	// Method is the provider's auth method (Pi's `method?: ApiKeyAuth | OAuthAuth`); the selector searches its name.
	Method AuthMethod
	// Subscription reports whether the provider's OAuth sign-in is backed by a subscription. False labels it as an account; nil keeps the "subscription" label (oauth-selector.ts:20-24).
	Subscription *bool

	// Status is the provider's configured auth, or nil when it has none (Pi's `status?: AuthCheck`).
	Status AuthCheck
}

// AuthCheck reports that a provider's auth is configured: its type ("oauth" or "api_key") and, when known, where it comes
// from (an ai.AuthCheck).
type AuthCheck interface {
	AuthCheckType() string
	AuthCheckSource() string
}

// OAuthSelectorComponent renders the bordered provider picker (port of
// OAuthSelectorComponent in oauth-selector.ts).
type OAuthSelectorComponent struct {
	Container
	listContainer *Container
	mode          string // "login" or "logout"
	onSelect      func(providerID, authType string)
	onCancel      func()
	providers     []OAuthProvider
	filtered      []OAuthProvider
	cursor        int
	done          bool
	cancelled     bool
	search        *TextInput
	// showAuthTypeLabels mirrors upstream: label rows with their auth type
	// only when the list mixes subscription and API-key entries.
	showAuthTypeLabels bool
}

// authSelectorMaxVisible is upstream oauth-selector.ts updateList maxVisible.
// upstream: coding-agent/src/modes/interactive/components/oauth-selector.ts:maxVisible
const authSelectorMaxVisible = 8

// FormatAuthSelectorProviderType mirrors upstream formatAuthSelectorProviderType(authType, subscription?)
// (oauth-selector.ts:27-33): an OAuth sign-in is a "subscription" unless subscription is false, which makes it an
// "account". Omitting subscription is Pi's undefined.
func FormatAuthSelectorProviderType(authType string, subscription ...bool) string {
	if authType == "api_key" {
		return "API key"
	}
	if len(subscription) > 0 && !subscription[0] {
		return "account"
	}
	return "subscription"
}

// subscriptionArg passes an option's subscription flag as Pi's optional argument: nil is undefined.
func subscriptionArg(subscription *bool) []bool {
	if subscription == nil {
		return nil
	}
	return []bool{*subscription}
}

// SetFocused records TUI focus and propagates it to the search input for IME cursor positioning (upstream `set focused`).
func (s *OAuthSelectorComponent) SetFocused(focused bool) {
	s.mu.Lock() // a render holds the container's read lock while it draws the search input
	defer s.mu.Unlock()
	s.search.SetFocused(focused)
}

// NewOAuthSelectorComponent constructs the picker. onSelect receives the chosen provider's id and auth type and onCancel runs on cancel (upstream onSelect/onCancel); nil callbacks are skipped.
func NewOAuthSelectorComponent(mode string, providers []OAuthProvider, onSelect func(providerID, authType string), onCancel func(), initialSearch ...string) *OAuthSelectorComponent {
	sel := &OAuthSelectorComponent{
		onSelect:  onSelect,
		onCancel:  onCancel,
		mode:      mode,
		providers: append([]OAuthProvider(nil), providers...),
		filtered:  append([]OAuthProvider(nil), providers...),
		search:    NewInput(InputOptions{}),

		listContainer: NewContainer(),
	}
	sel.search.SetFocused(false)
	sel.search.OnSubmit = func(string) { sel.confirm() }
	authTypes := map[string]bool{}
	for _, p := range providers {
		authTypes[p.AuthType] = true
	}
	sel.showAuthTypeLabels = len(authTypes) > 1
	if len(initialSearch) > 0 {
		sel.search.SetText(initialSearch[0])
		sel.applyFilter()
	}
	// upstream: oauth-selector.ts constructor children.
	title := "Select provider to configure:"
	if mode == "logout" {
		title = "Select provider to logout:"
	}
	t := ActiveTheme()
	sel.Add(NewDynamicBorder())
	sel.Add(NewSpacer(1))
	sel.Add(NewTruncatedText(t.Fg("accent", t.Bold(title)), 1, 0))
	sel.Add(NewSpacer(1))
	sel.Add(sel.search)
	sel.Add(NewSpacer(1))
	sel.Add(sel.listContainer)
	sel.Add(NewSpacer(1))
	sel.Add(NewDynamicBorder())
	sel.updateList()
	return sel
}

// Done reports whether the user selected or cancelled.
func (s *OAuthSelectorComponent) Done() bool { return s.done }

// Cancelled reports whether Esc was pressed.
func (s *OAuthSelectorComponent) Cancelled() bool { return s.cancelled }

// SelectedID returns the selected provider ID, or "" on cancel.
func (s *OAuthSelectorComponent) SelectedID() string {
	if s.cancelled || s.cursor < 0 || s.cursor >= len(s.filtered) {
		return ""
	}
	return s.filtered[s.cursor].ID
}

func (s *OAuthSelectorComponent) applyFilter() {
	query := ""
	if s.search != nil {
		query = s.search.Text()
	}
	s.filtered = FuzzyFilter(s.providers, query, func(p OAuthProvider) string {
		methodName := ""
		if p.Method != nil {
			methodName = p.Method.AuthMethodName()
		}
		return p.Name + " " + p.ID + " " + p.AuthType + " " + methodName
	})
	if s.cursor >= len(s.filtered) {
		s.cursor = len(s.filtered) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// authSelectorEnvVarSource matches a status source that lists environment variable names (oauth-selector.ts:49).
var authSelectorEnvVarSource = lazyregexp.New(`^[A-Z][A-Z0-9_]*(?:, [A-Z][A-Z0-9_]*)*$`)

// FormatAuthSelectorProviderStatus is upstream formatAuthSelectorProviderStatus (oauth-selector.ts:36-53): the themed suffix
// describing whether and how a login option is configured, for example " ✓ configured".
func FormatAuthSelectorProviderStatus(p OAuthProvider) string {
	th := ActiveTheme()
	if p.Status == nil {
		return th.Fg("muted", " • not configured")
	}
	statusType, source := p.Status.AuthCheckType(), p.Status.AuthCheckSource()
	if statusType != p.AuthType {
		label := FormatAuthSelectorProviderType(statusType, subscriptionArg(p.Subscription)...) + " configured"
		return th.Fg("muted", " • ") + th.Fg("warning", label)
	}
	if source == "" || source == "OAuth" || source == "stored credential" {
		return th.Fg("success", " ✓ configured")
	}
	if authSelectorEnvVarSource.MatchString(source) {
		source = "env: " + source
	}
	return th.Fg("success", " ✓ "+source)
}

// updateList rebuilds the provider rows with the current theme (oauth-selector.ts updateList).
func (s *OAuthSelectorComponent) updateList() {
	t := ActiveTheme()
	s.listContainer.Clear()
	// Upstream shows a window of maxVisible rows centered on the selection,
	// followed by a "(n/total)" row when the list is clipped.
	startIndex := max(0, min(s.cursor-authSelectorMaxVisible/2, len(s.filtered)-authSelectorMaxVisible))
	endIndex := min(startIndex+authSelectorMaxVisible, len(s.filtered))
	for i := startIndex; i < endIndex; i++ {
		p := s.filtered[i]
		authTypeLabel := ""
		if s.showAuthTypeLabels {
			authTypeLabel = t.Fg("muted", " ["+FormatAuthSelectorProviderType(p.AuthType, subscriptionArg(p.Subscription)...)+"]")
		}
		var line string
		if i == s.cursor {
			line = t.Fg("accent", "→ ") + t.Fg("accent", p.Name)
		} else {
			line = "  " + t.Fg("text", p.Name)
		}
		s.listContainer.Add(NewTruncatedText(line+authTypeLabel+FormatAuthSelectorProviderStatus(p), 1, 0))
	}
	if startIndex > 0 || endIndex < len(s.filtered) {
		s.listContainer.Add(NewTruncatedText(t.Fg("muted", fmt.Sprintf("  (%d/%d)", s.cursor+1, len(s.filtered))), 1, 0))
	}

	if len(s.filtered) == 0 {
		msg := "No matching providers"
		if len(s.providers) == 0 {
			if s.mode == "login" {
				msg = "No providers available"
			} else {
				msg = "No providers logged in. Use /login first."
			}
		}
		s.listContainer.Add(NewTruncatedText(t.Fg("muted", "  "+msg), 1, 0))
	}
}

// confirm selects the highlighted provider; an empty filtered list selects nothing (the Input's onSubmit and tui.select.confirm).
func (s *OAuthSelectorComponent) confirm() {
	if len(s.filtered) > 0 {
		s.done = true
		if s.onSelect != nil && s.cursor >= 0 && s.cursor < len(s.filtered) {
			provider := s.filtered[s.cursor]
			s.onSelect(provider.ID, provider.AuthType)
		}
	}
}

// HandleInput processes navigation, select, and cancel keys.
func (s *OAuthSelectorComponent) HandleInput(data string) {
	if s.done {
		return
	}
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBSelectUp):
		if s.cursor > 0 {
			s.cursor--
		}
	case kb.Matches(data, KBSelectDown):
		if s.cursor < len(s.filtered)-1 {
			s.cursor++
		}
	case kb.Matches(data, KBSelectConfirm):
		s.confirm()
	case kb.Matches(data, KBSelectCancel):
		s.done = true
		s.cancelled = true
		if s.onCancel != nil {
			s.onCancel()
		}
	default:
		if s.search != nil {
			s.search.HandleInput(data)
			s.applyFilter()
		}
	}
	s.updateList()
	s.Invalidate()
}

// NativeNode reports the picker as a selector whose item ids are the
// providers' ids, followed by ":" and the auth type in a list that mixes
// auth types, where one provider can appear once per auth type. pig additive
// (D91): each row's auth type label, in a mixed list, and its status
// indicator are its detail, and an empty list reports the empty message as
// the status.
func (s *OAuthSelectorComponent) NativeNode() (frontend.Node, bool) {
	node := frontend.Selector{
		Title:      "Select provider to configure:",
		Searchable: s.search != nil,
		Items:      make([]frontend.SelectorItem, len(s.filtered)),
	}
	if s.mode == "logout" {
		node.Title = "Select provider to logout:"
	}
	if s.search != nil {
		node.Query = s.search.Text()
	}
	for i, p := range s.filtered {
		id, detail := p.ID, plainText(FormatAuthSelectorProviderStatus(p))
		if s.showAuthTypeLabels {
			id += ":" + p.AuthType
			detail = " [" + FormatAuthSelectorProviderType(p.AuthType, subscriptionArg(p.Subscription)...) + "]" + detail
		}
		node.Items[i] = frontend.SelectorItem{ID: id, Label: p.Name, Detail: strings.TrimSpace(detail)}
	}
	if s.cursor >= 0 && s.cursor < len(node.Items) {
		node.Selected = node.Items[s.cursor].ID
	}
	if len(s.filtered) == 0 {
		switch {
		case len(s.providers) > 0:
			node.Status = "No matching providers"
		case s.mode == "login":
			node.Status = "No providers available"
		default:
			node.Status = "No providers logged in. Use /login first."
		}
	}
	return node, true
}

// Compile-time check.
var _ Component = (*OAuthSelectorComponent)(nil)

// Focused reports whether the search input holds the TUI focus (upstream `get focused`).
func (s *OAuthSelectorComponent) Focused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.search.Focused
}
