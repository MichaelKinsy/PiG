package codingagent

// Ports packages/coding-agent/src/modes/interactive/components/trust-selector.ts.

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

type TrustSelection struct {
	Trusted bool
	Updates []ProjectTrustUpdate
}

type TrustSelectorOptions struct {
	Cwd            string
	SavedDecision  *ProjectTrustStoreEntry
	ProjectTrusted bool
	OnSelect       func(TrustSelection)
	OnCancel       func()
}

type TrustSelectorComponent struct {
	*tui.Container
	savedDecision    *ProjectTrustStoreEntry
	trustOptions     []ProjectTrustOption
	selectedIndex    int
	listContainer    *tui.Container
	onSelectCallback func(TrustSelection)
	onCancelCallback func()
}

func NewTrustSelectorComponent(options TrustSelectorOptions) *TrustSelectorComponent {
	selector := &TrustSelectorComponent{Container: tui.NewContainer(), savedDecision: options.SavedDecision, onSelectCallback: options.OnSelect, onCancelCallback: options.OnCancel, trustOptions: GetProjectTrustOptions(options.Cwd, false), listContainer: tui.NewContainer()}
	for i, option := range selector.trustOptions {
		if selector.isSavedOption(option) {
			selector.selectedIndex = i
			break
		}
	}
	theme := tui.ActiveTheme()
	text := func(value string) *tui.Text { return tui.NewPaddedText(value, 1, 0, nil) }
	saved := "none"
	if options.SavedDecision != nil {
		label := "untrusted"
		if options.SavedDecision.Decision {
			label = "trusted"
		}
		if len(selector.trustOptions) > 0 && options.SavedDecision.Path != selector.trustOptions[0].SavedPath {
			saved = fmt.Sprintf("%s (inherited from %s)", label, options.SavedDecision.Path)
		} else {
			saved = fmt.Sprintf("%s (%s)", label, options.SavedDecision.Path)
		}
	}
	current := "untrusted"
	if options.ProjectTrusted {
		current = "trusted"
	}
	selector.Add(tui.NewDynamicBorder())
	selector.Add(tui.NewSpacer(1))
	selector.Add(text(theme.Fg("accent", "\x1b[1mProject trust\x1b[22m")))
	selector.Add(text(theme.Fg("muted", options.Cwd)))
	selector.Add(tui.NewSpacer(1))
	selector.Add(text(theme.Fg("muted", "Saved decision: "+saved)))
	selector.Add(text(theme.Fg("muted", "Current session: "+current)))
	selector.Add(tui.NewSpacer(1))
	selector.Add(selector.listContainer)
	selector.Add(tui.NewSpacer(1))
	keyHint := func(action, description string) string {
		return tui.RawKeyHint(strings.Join(tui.GetKeybindings().GetKeys(action), "/"), description)
	}
	selector.Add(text(tui.RawKeyHint("↑↓", "navigate") + "  " + keyHint(tui.KBSelectConfirm, "save") + "  " + keyHint(tui.KBSelectCancel, "cancel")))
	selector.Add(tui.NewSpacer(1))
	selector.Add(tui.NewDynamicBorder())
	selector.updateList()
	return selector
}

func (s *TrustSelectorComponent) isSavedOption(option ProjectTrustOption) bool {
	return option.SavedPath != "" && s.savedDecision != nil && s.savedDecision.Decision == option.Trusted && s.savedDecision.Path == option.SavedPath
}

func (s *TrustSelectorComponent) updateList() {
	s.listContainer.Clear()
	theme := tui.ActiveTheme()
	for i, option := range s.trustOptions {
		prefix, marker := "  ", "  "
		if i == s.selectedIndex {
			prefix = theme.Fg("accent", "→ ")
		}
		if s.isSavedOption(option) {
			marker = theme.Fg("accent", "✓ ")
		}
		color := "text"
		if i == s.selectedIndex {
			color = "accent"
		}
		s.listContainer.Add(tui.NewPaddedText(prefix+marker+theme.Fg(color, option.Label), 1, 0, nil))
	}
	s.Invalidate()
}

func (s *TrustSelectorComponent) HandleInput(data string) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(data, tui.KBSelectUp) || data == "k":
		s.selectedIndex = max(0, s.selectedIndex-1)
		s.updateList()
	case kb.Matches(data, tui.KBSelectDown) || data == "j":
		s.selectedIndex = min(len(s.trustOptions)-1, s.selectedIndex+1)
		s.updateList()
	case kb.Matches(data, tui.KBSelectConfirm) || data == "\n":
		option := s.trustOptions[s.selectedIndex]
		if s.onSelectCallback != nil {
			s.onSelectCallback(TrustSelection{Trusted: option.Trusted, Updates: option.Updates})
		}
	case kb.Matches(data, tui.KBSelectCancel):
		if s.onCancelCallback != nil {
			s.onCancelCallback()
		}
	}
}

func (m *InteractiveMode) runTrustSelector(options TrustSelectorOptions) (TrustSelection, bool) {
	var selection TrustSelection
	done, cancelled := false, false
	onSelect, onCancel := options.OnSelect, options.OnCancel
	options.OnSelect = func(value TrustSelection) {
		selection = value
		if onSelect != nil {
			onSelect(value)
		}
		done = true
	}
	options.OnCancel = func() {
		cancelled = true
		if onCancel != nil {
			onCancel()
		}
		done = true
	}
	selector := NewTrustSelectorComponent(options)
	previousFocus := m.tuiInst.GetFocusedComponent()
	m.editorContainer.SetChildren(selector)
	m.tuiInst.SetFocus(selector)
	m.tuiInst.Render()
	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.SetFocus(previousFocus)
		m.tuiInst.RequestRender()
	}()
	input, release := m.acquireModalInputChannel()
	defer release()
	for !done {
		data, ok := m.readModalInput(input)
		if !ok {
			return TrustSelection{}, false
		}
		m.dispatchModalInput(selector, []string{string(data)}, selector.HandleInput, func() bool { return done })
		if !done {
			m.tuiInst.Render()
		}
	}
	return selection, !cancelled
}
