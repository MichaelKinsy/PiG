package tui

// Ports packages/coding-agent/src/modes/interactive/components/user-message.ts.

import (
	"slices"

	"github.com/MichaelKinsy/PiG/coding/extension/markdowntransform"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

const (
	userMessageZoneStart = "\x1b]133;A\x07"
	userMessageZoneEnd   = "\x1b]133;B\x07\x1b]133;C\x07"
)

// UserMessageComponent renders user Markdown in a padded background with OSC 133 zone markers. Closing markers prefix the existing bottom padding row and do not add height.
type UserMessageComponent struct {
	Container
	content       string
	inner         *Markdown
	outputPad     int
	markdownTheme *MarkdownTheme
	transform     func(string, int) string
	// images are the message's image blocks, which only a frontend draws,
	// and decoded those images as frontendImages decodes them once.
	images  []ImageBlock
	decoded []frontend.ViewImage
}

// NewUserMessageComponent preserves source list markers and backslash escapes in user text. It is Pi's constructor(text,
// markdownTheme = getMarkdownTheme(), outputPad = 1, markdownTransformers = []) (user-message.ts:18-31): a nil
// markdownTheme is the active theme's, outputPad is the horizontal padding (Pi keeps any value; a negative one cannot be repeated, so it is 0), and markdownTransformers run
// in order on each render as the display-only transform createMarkdownTransform("user", false, markdownTransformers).
func NewUserMessageComponent(text string, markdownTheme *MarkdownTheme, outputPad int, markdownTransformers []markdowntransform.MarkdownTransformer) *UserMessageComponent {
	var transform func(string, int) string
	if len(markdownTransformers) > 0 {
		transform = markdowntransform.CreateMarkdownTransform(markdowntransform.MarkdownMessageUser, false, markdownTransformers)
	}
	block := &UserMessageComponent{content: text, outputPad: max(0, outputPad), markdownTheme: markdownTheme, transform: transform}
	block.rebuild()
	return block
}

// SetImages records the image blocks the message carries. The terminal
// renderer draws none, as Pi's UserMessageComponent; a frontend session
// receives them on the message's MarkdownText (pig additive, D91).
func (u *UserMessageComponent) SetImages(images []ImageBlock) {
	u.images, u.decoded = images, nil
	u.invalidatable.Invalidate()
}

// rebuild applies the output padding. The Markdown pads and colors its own background, so no Box keeps a second full-width copy of every line (user-message.ts:38-59).
func (u *UserMessageComponent) rebuild() {
	if u.inner == nil {
		background := &DefaultTextStyle{BgColor: func(text string) string { return UserMessageBgOpen() + text + BgClose() }}
		u.inner = NewMarkdownWithOptions(u.content, u.outputPad, 1, u.markdownTheme, background, &MarkdownOptions{PreserveOrderedListMarkers: true, PreserveBackslashEscapes: true, Transform: u.transform})
		u.Add(u.inner)
	} else {
		u.inner.paddingX = u.outputPad
		u.inner.cachedLines = nil
		u.inner.Invalidate() // the Container caches the child by its dirty flag
	}
	u.Container.Invalidate()
}

// SetMarkdownTransform installs the display-only constructor transform at the message's available content width.
func (u *UserMessageComponent) SetMarkdownTransform(transform func(string, int) string) {
	u.inner.Transform = transform
	u.Invalidate()
}

// Invalidate also invalidates the child Markdown transform state so display transformers run again.
func (u *UserMessageComponent) Invalidate() { u.Container.Invalidate(); u.inner.Invalidate() }

// SetMarkdownTransformState declares the external state the installed
// transform reads, for the Markdown render cache key.
func (u *UserMessageComponent) SetMarkdownTransformState(fn func() string) {
	u.inner.TransformState = fn
	u.Invalidate()
}

// SetAsyncMarkdownTransform installs an off-loop rewrite that completes before the message content is painted.
func (u *UserMessageComponent) SetAsyncMarkdownTransform(transform *AsyncMarkdownTransform) {
	if transform != nil {
		options := *transform
		completed := options.Invalidate
		options.Invalidate = func() {
			// Completion dirties the rendered frame, not the logical transform revision.
			u.Container.Invalidate()
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
func (u *UserMessageComponent) SetOutputPad(padding int) {
	u.outputPad = max(0, padding)
	u.rebuild()
}

// Dispose releases this message's pending Markdown generation.
func (u *UserMessageComponent) Dispose() { u.inner.Dispose() }

func (u *UserMessageComponent) Render(width int) []string {
	u.inner.SetDefaultColor(ActiveTheme().UserMessageText)
	out := slices.Clone(u.renderBorrowed(width))
	if len(out) == 0 {
		return out
	}
	out[0] = userMessageZoneStart + out[0]
	out[len(out)-1] = userMessageZoneEnd + out[len(out)-1]
	return out
}
