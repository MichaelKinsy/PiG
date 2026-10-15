// Package kit describes an extension surface as Pi's tui components (D107):
// a declarative tree, the view, that PiG's host renders with its Go ports of
// pi-tui, so the terminal shows what Pi's components draw.
//
// Each node mirrors the upstream component of the same name. Constructors take
// the upstream constructor's positional arguments; a style closure upstream
// takes is a theme token name here (Box bgFn and Text customBgFn become Bg,
// DynamicBorder's color, Loader's color functions and Image's fallbackColor
// become foreground tokens). A component that draws its own rows is a [Lines]
// node.
//
// A view reaches the host through sdk.Context.Custom with an
// sdk.ViewComponent, sdk.Context.SetWidget with a [View],
// sdk.Context.SetHeaderView and SetFooterView, sdk.ToolRenderers CallView and
// ResultView, and sdk.Extension MessageViewRenderer and EntryViewRenderer.
// See docs/plan/extension-component-kit.md.
package kit

import (
	"crypto/sha256"
	"encoding/hex"
)

// Node is one component of a view. The set of nodes is closed: every Node is
// one of this package's pointer types.
type Node interface {
	isNode()
}

// StackChild is a child of an [HStack] or a [VStack]: a [Node], or a
// [StackEntry] that carries the child's layout options.
type StackChild interface {
	isStackChild()
}

// View is one surface's component tree with its focus and theme overrides.
type View struct {
	// Root is the component tree; a view without one is invalid.
	Root Node
	// Focus is the ID of the [SelectList] or [SettingsList] that receives the
	// keys it binds (ui.custom only).
	Focus string
	// Theme overrides theme tokens for this surface only: Pi's token name to
	// "#rrggbb". The host validates the tokens and colors.
	Theme map[string]string
}

// Container is upstream Container: its children rendered one after another.
type Container struct {
	Children []Node
}

// NewContainer returns a Container holding children.
func NewContainer(children ...Node) *Container {
	return &Container{Children: children}
}

// AddChild appends child, as upstream addChild does.
func (c *Container) AddChild(child Node) { c.Children = append(c.Children, child) }

// Box is upstream Box(paddingX, paddingY, bgFn): its children inside padding,
// on the background token Bg ("" for none).
type Box struct {
	PaddingX int
	PaddingY int
	Bg       string
	Children []Node
}

// NewBox returns a Box with upstream's positional paddings holding children.
func NewBox(paddingX, paddingY int, children ...Node) *Box {
	return &Box{PaddingX: paddingX, PaddingY: paddingY, Children: children}
}

// AddChild appends child, as upstream addChild does.
func (b *Box) AddChild(child Node) { b.Children = append(b.Children, child) }

// Text is upstream Text(text, paddingX, paddingY, customBgFn): wrapped text
// on the background token Bg ("" for none).
type Text struct {
	Text     string
	PaddingX int
	PaddingY int
	Bg       string
}

// NewText returns a Text with upstream's positional arguments.
func NewText(text string, paddingX, paddingY int) *Text {
	return &Text{Text: text, PaddingX: paddingX, PaddingY: paddingY}
}

// TruncatedText is upstream TruncatedText(text, paddingX, paddingY): one line
// cut to the width.
type TruncatedText struct {
	Text     string
	PaddingX int
	PaddingY int
}

// NewTruncatedText returns a TruncatedText with upstream's positional
// arguments.
func NewTruncatedText(text string, paddingX, paddingY int) *TruncatedText {
	return &TruncatedText{Text: text, PaddingX: paddingX, PaddingY: paddingY}
}

// TextStyle is upstream Markdown DefaultTextStyle with theme tokens for its
// color functions: Color is a foreground token and BgColor a background token.
type TextStyle struct {
	Color         string
	BgColor       string
	Bold          bool
	Italic        bool
	Strikethrough bool
	Underline     bool
}

// Markdown is upstream Markdown(text, paddingX, paddingY, getMarkdownTheme(),
// defaultTextStyle, {renderLatex}).
type Markdown struct {
	Text             string
	PaddingX         int
	PaddingY         int
	DefaultTextStyle *TextStyle
	// RenderLatex is upstream MarkdownOptions.renderLatex; nil is upstream's
	// default (true).
	RenderLatex *bool
}

// NewMarkdown returns a Markdown with upstream's positional arguments.
func NewMarkdown(text string, paddingX, paddingY int) *Markdown {
	return &Markdown{Text: text, PaddingX: paddingX, PaddingY: paddingY}
}

// Spacer is upstream Spacer(lines): empty rows.
type Spacer struct {
	Lines int
}

// NewSpacer returns a Spacer of lines rows.
func NewSpacer(lines int) *Spacer { return &Spacer{Lines: lines} }

// DynamicBorder is coding-agent's DynamicBorder: a full-width rule in the
// foreground token Color ("" for upstream's default "border").
type DynamicBorder struct {
	Color string
}

// NewDynamicBorder returns a DynamicBorder in the foreground token color.
func NewDynamicBorder(color string) *DynamicBorder { return &DynamicBorder{Color: color} }

// SelectItem is upstream SelectItem.
type SelectItem struct {
	Value       string
	Label       string
	Description string
}

// SelectListLayout is upstream SelectListLayoutOptions without
// truncatePrimary; nil fields take upstream's defaults.
type SelectListLayout struct {
	MinPrimaryColumnWidth *int
	MaxPrimaryColumnWidth *int
}

// SelectList is upstream SelectList(items, maxVisible, getSelectListTheme(),
// layout). The host owns its selection and filter; SelectedIndex and Filter
// are upstream's setSelectedIndex and setFilter calls, which the host applies
// when they differ from the values this surface sent last.
type SelectList struct {
	// ID names the list; it is required and unique within the view, and
	// [Event.Node] carries it.
	ID            string
	Items         []SelectItem
	MaxVisible    int
	Layout        SelectListLayout
	SelectedIndex *int
	Filter        *string
}

// NewSelectList returns a SelectList with upstream's positional arguments.
func NewSelectList(id string, items []SelectItem, maxVisible int) *SelectList {
	return &SelectList{ID: id, Items: items, MaxVisible: maxVisible}
}

// SetSelectedIndex is upstream setSelectedIndex.
func (l *SelectList) SetSelectedIndex(index int) { l.SelectedIndex = &index }

// SetFilter is upstream setFilter.
func (l *SelectList) SetFilter(filter string) { l.Filter = &filter }

// SettingItem is upstream SettingItem. Submenu is the component the submenu
// factory returns, or nil for none.
type SettingItem struct {
	ID           string
	Label        string
	Description  string
	CurrentValue string
	Values       []string
	Submenu      Node
}

// SettingsList is upstream SettingsList(items, maxVisible,
// getSettingsListTheme(), onChange, onCancel, {enableSearch}). The host owns
// its selection, search text and values; SelectedIndex and Filter apply as on
// [SelectList].
type SettingsList struct {
	// ID names the list; it is required and unique within the view.
	ID           string
	Items        []SettingItem
	MaxVisible   int
	EnableSearch bool
	// SelectedIndex is an index into the shown items.
	SelectedIndex *int
	// Filter is the search input's value.
	Filter *string
}

// NewSettingsList returns a SettingsList with upstream's positional arguments.
func NewSettingsList(id string, items []SettingItem, maxVisible int) *SettingsList {
	return &SettingsList{ID: id, Items: items, MaxVisible: maxVisible}
}

// SetSelectedIndex sets the selected index into the shown items.
func (l *SettingsList) SetSelectedIndex(index int) { l.SelectedIndex = &index }

// SetFilter sets the search input's value.
func (l *SettingsList) SetFilter(filter string) { l.Filter = &filter }

// Image is upstream Image(base64Data, mimeType, theme, options). The SDK sends
// its bytes the first time a frame on the connection references them, and
// only its [Image.Ref] after that.
type Image struct {
	MimeType       string
	MaxWidthCells  *int
	MaxHeightCells *int
	Filename       string
	// FallbackColor is the foreground token of the text fallback ("" for
	// none).
	FallbackColor string

	data []byte
	ref  string
}

// NewImage returns an Image of the decoded bytes data.
func NewImage(data []byte, mimeType string) *Image {
	return &Image{MimeType: mimeType, data: data, ref: imageRef(data)}
}

// Data returns the image's decoded bytes.
func (i *Image) Data() []byte { return i.data }

// Ref returns the lowercase hex SHA-256 of the image's bytes.
func (i *Image) Ref() string {
	if i.ref != "" {
		return i.ref
	}
	return imageRef(i.data)
}

func imageRef(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// LoaderIndicator is upstream LoaderIndicatorOptions. Nil Frames are the
// default spinner and an empty non-nil list hides the indicator; IntervalMs 0
// is upstream's default interval.
type LoaderIndicator struct {
	Frames     []string
	IntervalMs int
}

// Loader is upstream Loader(tui, spinnerColorFn, messageColorFn, message,
// indicator). The host animates it.
type Loader struct {
	Message string
	// SpinnerColor and MessageColor are foreground tokens ("" for none).
	SpinnerColor string
	MessageColor string
	Indicator    *LoaderIndicator
	// Frame is upstream currentFrame; nil starts at 0. The host's animation
	// continues from it.
	Frame *int
}

// NewLoader returns a Loader showing message.
func NewLoader(message string) *Loader { return &Loader{Message: message} }

// StackEntryOptions is upstream StackEntryOptions without visible. A nil
// Basis is "auto"; nil fields take upstream's defaults.
type StackEntryOptions struct {
	Basis   *int
	Grow    *int
	Shrink  *int
	MinSize *int
	MaxSize *int
}

// StackEntry is upstream StackEntry: a stack child with its layout options.
type StackEntry struct {
	Node    Node
	Options StackEntryOptions
}

// Entry returns node as a stack child with options.
func Entry(node Node, options StackEntryOptions) StackEntry {
	return StackEntry{Node: node, Options: options}
}

// HStack is upstream HStack(children, {gap, align}): children side by side.
type HStack struct {
	Children []StackChild
	Gap      int
	// Align is "stretch", "start", "center" or "end"; "" is upstream's
	// default ("stretch").
	Align string
}

// NewHStack returns an HStack holding children.
func NewHStack(children ...StackChild) *HStack { return &HStack{Children: children} }

// AddChild appends child.
func (s *HStack) AddChild(child StackChild) { s.Children = append(s.Children, child) }

// VStack is upstream VStack(children, {gap, align}): children one below
// another.
type VStack struct {
	Children []StackChild
	Gap      int
	// Align is "stretch", "start", "center" or "end"; "" is upstream's
	// default ("stretch").
	Align string
}

// NewVStack returns a VStack holding children.
func NewVStack(children ...StackChild) *VStack { return &VStack{Children: children} }

// AddChild appends child.
func (s *VStack) AddChild(child StackChild) { s.Children = append(s.Children, child) }

// Lines is rows a component of its own drew, rendered verbatim. Image,
// Progress and List are PiG's frontend-only annotations: the SDK sends them,
// and the image's bytes, only while a frontend draws the session.
type Lines struct {
	Lines []string
	// Image says the rows depict this image.
	Image *LinesImage
	// Progress says the rows show a position in a range.
	Progress *Progress
	// List says the rows are a list, one item per row: it needs exactly as
	// many items as Lines has rows, or the host rejects the view.
	List *List
}

// NewLines returns a Lines node of rows.
func NewLines(lines []string) *Lines { return &Lines{Lines: lines} }

// LinesImage is the image a [Lines] node depicts.
type LinesImage struct {
	Data     []byte
	MimeType string
}

// Progress is a position Value in the range 0 to Max.
type Progress struct {
	Value float64
	Max   float64
}

// List is a list a [Lines] node's rows show. Items[i] is row i. Selected is
// the highlighted item, or -1 for none.
type List struct {
	Items    []ListItem
	Selected int
}

// ListItem is one row of a [List]: its primary text, an optional secondary
// text, and optional further cells of a table row (a track's artist, album
// and length, say).
type ListItem struct {
	Label   string
	Detail  string
	Columns []string
}

func (*Container) isNode()     {}
func (*Box) isNode()           {}
func (*Text) isNode()          {}
func (*TruncatedText) isNode() {}
func (*Markdown) isNode()      {}
func (*Spacer) isNode()        {}
func (*DynamicBorder) isNode() {}
func (*SelectList) isNode()    {}
func (*SettingsList) isNode()  {}
func (*Image) isNode()         {}
func (*Loader) isNode()        {}
func (*HStack) isNode()        {}
func (*VStack) isNode()        {}
func (*Lines) isNode()         {}

func (*Container) isStackChild()     {}
func (*Box) isStackChild()           {}
func (*Text) isStackChild()          {}
func (*TruncatedText) isStackChild() {}
func (*Markdown) isStackChild()      {}
func (*Spacer) isStackChild()        {}
func (*DynamicBorder) isStackChild() {}
func (*SelectList) isStackChild()    {}
func (*SettingsList) isStackChild()  {}
func (*Image) isStackChild()         {}
func (*Loader) isStackChild()        {}
func (*HStack) isStackChild()        {}
func (*VStack) isStackChild()        {}
func (*Lines) isStackChild()         {}
func (StackEntry) isStackChild()     {}
