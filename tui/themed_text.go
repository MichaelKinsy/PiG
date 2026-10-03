package tui

// Ports packages/coding-agent/src/modes/interactive/components/themed-text.ts

// ThemedText is Text whose content applies theme colors: the string is rebuilt from build after every invalidation.
type ThemedText struct {
	*Text
	build func() string
	stale bool
}

// NewThemedText builds lazily; build must return the same content each time, apart from colors.
func NewThemedText(build func() string, paddingX, paddingY int) *ThemedText {
	return &ThemedText{Text: NewPaddedText("", paddingX, paddingY, nil), build: build, stale: true}
}

// Invalidate marks the text stale.
func (t *ThemedText) Invalidate() {
	t.Text.Invalidate()
	t.stale = true
}

// Render rebuilds a stale text before rendering it.
func (t *ThemedText) Render(width int) []string {
	if t.stale {
		t.stale = false
		t.SetText(t.build())
	}
	return t.Text.Render(width)
}
