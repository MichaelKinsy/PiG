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
)

// OAuthProvider is one entry in the auth provider selector.
type OAuthProvider struct {
	ID       string // e.g. "github-copilot"
	Name     string // display name
	AuthType string // "oauth" or "api_key"

	// Stored indicates auth.json contains a stored credential for this provider.
	Stored bool
	// StoredType is the type of the stored credential when Stored is true.
	StoredType string // "oauth" or "api_key"
	// AuthStatusSource/Label mirror ai.AuthStatus for API-key providers.
	AuthStatusSource string // "", "stored", "environment", "runtime", "fallback", "models_json_key", "models_json_command"
	AuthStatusLabel  string
}

// OAuthSelector renders the bordered provider picker (port of
// OAuthSelectorComponent in oauth-selector.ts).
type OAuthSelector struct {
	invalidatable
	mode      string // "login" or "logout"
	providers []OAuthProvider
	filtered  []OAuthProvider
	cursor    int
	done      bool
	cancelled bool
	search    *TextInput
	// showAuthTypeLabels mirrors upstream: label rows with their auth type
	// only when the list mixes subscription and API-key entries.
	showAuthTypeLabels bool
}

// authSelectorMaxVisible is upstream oauth-selector.ts updateList maxVisible.
// upstream: coding-agent/src/modes/interactive/components/oauth-selector.ts:maxVisible
const authSelectorMaxVisible = 8

// FormatAuthSelectorProviderType mirrors upstream formatAuthSelectorProviderType.
func FormatAuthSelectorProviderType(authType string) string {
	if authType == "oauth" {
		return "subscription"
	}
	return "API key"
}

// NewOAuthSelector constructs the picker.
func NewOAuthSelector(mode string, providers []OAuthProvider) *OAuthSelector {
	sel := &OAuthSelector{
		mode:      mode,
		providers: append([]OAuthProvider(nil), providers...),
		filtered:  append([]OAuthProvider(nil), providers...),
		search:    NewTextInput(""),
	}
	sel.search.Focused = true
	authTypes := map[string]bool{}
	for _, p := range providers {
		authTypes[p.AuthType] = true
	}
	sel.showAuthTypeLabels = len(authTypes) > 1
	return sel
}

// Done reports whether the user selected or cancelled.
func (s *OAuthSelector) Done() bool { return s.done }

// Cancelled reports whether Esc was pressed.
func (s *OAuthSelector) Cancelled() bool { return s.cancelled }

// SelectedID returns the selected provider ID, or "" on cancel.
func (s *OAuthSelector) SelectedID() string {
	if s.cancelled || s.cursor < 0 || s.cursor >= len(s.filtered) {
		return ""
	}
	return s.filtered[s.cursor].ID
}

func (s *OAuthSelector) applyFilter() {
	query := ""
	if s.search != nil {
		query = s.search.Text()
	}
	s.filtered = FuzzyFilter(s.providers, query, func(p OAuthProvider) string {
		return strings.TrimSpace(p.Name + " " + p.ID + " " + p.AuthType)
	})
	if s.cursor >= len(s.filtered) {
		s.cursor = len(s.filtered) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func authSelectorIndicator(p OAuthProvider) string {
	th := ActiveTheme()
	configuredOtherLabel := func(kind string) string {
		if kind == "oauth" {
			return "subscription configured"
		}
		return "API key configured"
	}
	if p.Stored {
		if p.StoredType == p.AuthType {
			switch p.AuthStatusSource {
			case "", "OAuth", "stored credential":
				return th.Success + " ✓ configured" + th.Reset
			default:
				return th.Success + " ✓ " + p.AuthStatusSource + th.Reset
			}
		}
		return th.Muted + " • " + th.Reset + th.Warning + configuredOtherLabel(p.StoredType) + th.Reset
	}
	if p.AuthType != "api_key" {
		return th.Muted + " • unconfigured" + th.Reset
	}
	switch p.AuthStatusSource {
	case "environment":
		label := p.AuthStatusLabel
		if label == "" {
			label = "API key"
		}
		return th.Success + " ✓ env: " + label + th.Reset
	case "runtime":
		return th.Success + " ✓ runtime API key" + th.Reset
	case "fallback":
		return th.Success + " ✓ custom API key" + th.Reset
	case "models_json_key":
		return th.Success + " ✓ key in models.json" + th.Reset
	case "models_json_command":
		return th.Success + " ✓ command in models.json" + th.Reset
	default:
		return th.Muted + " • unconfigured" + th.Reset
	}
}

// Render returns ANSI lines for the bordered overlay.
func (s *OAuthSelector) Render(width int) []string {
	t := ActiveTheme()
	border := NewDynamicBorder("")
	// Upstream renders the title and every list row as TruncatedText(..., 1, 0):
	// one cell of padding, truncated to the width, padded to full width.
	truncated := func(content string) string { return NewPaddedTruncatedText(content, 1, 0).Render(width)[0] }

	var lines []string
	lines = append(lines, border.Render(width)...)
	lines = append(lines, "")

	title := "Select provider to configure:"
	if s.mode == "logout" {
		title = "Select provider to logout:"
	}
	lines = append(lines, truncated(t.Accent+"\x1b[1m"+title+SGRBoldDimReset+t.Reset))
	lines = append(lines, "")
	if s.search != nil {
		lines = append(lines, s.search.Render(width)...)
		lines = append(lines, "")
	}

	// Upstream shows a window of maxVisible rows centered on the selection,
	// followed by a "(n/total)" row when the list is clipped.
	startIndex := max(0, min(s.cursor-authSelectorMaxVisible/2, len(s.filtered)-authSelectorMaxVisible))
	endIndex := min(startIndex+authSelectorMaxVisible, len(s.filtered))
	for i := startIndex; i < endIndex; i++ {
		p := s.filtered[i]
		authTypeLabel := ""
		if s.showAuthTypeLabels {
			authTypeLabel = t.Muted + " [" + FormatAuthSelectorProviderType(p.AuthType) + "]" + t.Reset
		}
		var line string
		if i == s.cursor {
			line = t.Accent + "→ " + t.Reset + t.Accent + p.Name + t.Reset
		} else {
			line = "  " + t.Text + p.Name + t.Reset
		}
		lines = append(lines, truncated(line+authTypeLabel+authSelectorIndicator(p)))
	}
	if startIndex > 0 || endIndex < len(s.filtered) {
		lines = append(lines, truncated(t.Muted+fmt.Sprintf("  (%d/%d)", s.cursor+1, len(s.filtered))+t.Reset))
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
		lines = append(lines, truncated(t.Muted+"  "+msg+t.Reset))
	}

	lines = append(lines, "")
	lines = append(lines, border.Render(width)...)
	return lines
}

// HandleInput processes navigation, select, and cancel keys.
func (s *OAuthSelector) HandleInput(data string) {
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
	case kb.Matches(data, KBSelectConfirm) || data == "\n":
		if len(s.filtered) > 0 {
			s.done = true
		}
	case kb.Matches(data, KBSelectCancel):
		s.done = true
		s.cancelled = true
	default:
		if s.search != nil {
			s.search.HandleInput(data)
			s.applyFilter()
		}
	}
	s.Invalidate()
}

// Compile-time check.
var _ Component = (*OAuthSelector)(nil)
