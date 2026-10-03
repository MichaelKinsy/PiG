package tui

// Ports packages/coding-agent/src/modes/interactive/components/user-message.ts.

import "slices"

const (
	userMessageZoneStart = "\x1b]133;A\x07"
	userMessageZoneEnd   = "\x1b]133;B\x07\x1b]133;C\x07"
)

// UserMessageBlock renders user Markdown in a padded background with OSC 133 zone markers. Closing markers prefix the existing bottom padding row and do not add height.
type UserMessageBlock struct {
	invalidatable
	content   string
	inner     *Markdown
	outputPad int
}

// NewUserMessageBlock preserves source list markers and backslash escapes in user text.
func NewUserMessageBlock(text string) *UserMessageBlock {
	block := &UserMessageBlock{content: text, outputPad: 1}
	block.rebuild()
	return block
}

// rebuild applies the output padding. The Markdown pads and colors its own background, so no Box keeps a second full-width copy of every line (user-message.ts:38-59).
func (u *UserMessageBlock) rebuild() {
	if u.inner == nil {
		background := &DefaultTextStyle{BgColor: func(text string) string { return UserMessageBgOpen() + text + BgClose() }}
		u.inner = NewMarkdownWithOptions(u.content, u.outputPad, 1, nil, background, &MarkdownOptions{PreserveOrderedListMarkers: true, PreserveBackslashEscapes: true})
	} else {
		u.inner.paddingX = u.outputPad
		u.inner.cachedLines = nil
	}
	u.invalidatable.Invalidate()
}

// SetMarkdownTransform installs the display-only constructor transform at the message's available content width.
func (u *UserMessageBlock) SetMarkdownTransform(transform func(string, int) string) {
	u.inner.Transform = transform
	u.Invalidate()
}

// Invalidate also invalidates the child Markdown transform state so display transformers run again.
func (u *UserMessageBlock) Invalidate() { u.invalidatable.Invalidate(); u.inner.Invalidate() }

// SetMarkdownTransformState declares the external state the installed
// transform reads, for the Markdown render cache key.
func (u *UserMessageBlock) SetMarkdownTransformState(fn func() string) {
	u.inner.TransformState = fn
	u.Invalidate()
}

// SetAsyncMarkdownTransform installs an off-loop rewrite that completes before the message content is painted.
func (u *UserMessageBlock) SetAsyncMarkdownTransform(transform *AsyncMarkdownTransform) {
	if transform != nil {
		options := *transform
		completed := options.Invalidate
		options.Invalidate = func() {
			// Completion dirties the rendered frame, not the logical transform revision.
			u.invalidatable.Invalidate()
			if completed != nil {
				completed()
			}
		}
		transform = &options
	}
	u.inner.AsyncTransform = transform
	u.Invalidate()
}

// SetOutputPad changes horizontal content padding while retaining the Markdown child and its transform lifetime.
func (u *UserMessageBlock) SetOutputPad(padding int) {
	u.outputPad = max(0, min(1, padding))
	u.rebuild()
}

// Dispose releases this message's pending Markdown generation.
func (u *UserMessageBlock) Dispose() { u.inner.Dispose() }

func (u *UserMessageBlock) Render(width int) []string {
	u.inner.SetDefaultColor(ActiveTheme().UserMessageText)
	out := slices.Clone(u.inner.Render(width))
	if len(out) == 0 {
		return out
	}
	out[0] = userMessageZoneStart + out[0]
	out[len(out)-1] = userMessageZoneEnd + out[len(out)-1]
	return out
}
