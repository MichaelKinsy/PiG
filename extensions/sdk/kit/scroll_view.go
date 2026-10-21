package kit

// ScrollView is pi-tui's ScrollView(child, {follow, primary, overscroll,
// scrollbar, scrollbarTrackStyle, scrollbarThumbStyle, scrollbarHideDelayMs}):
// one child that scrolls. A view is laid out without a viewport height, as
// Pi's layout engine does for a scroll node given none: the host draws the
// child's rows, padded by a column when Scrollbar is "always", and a frontend
// scrolls from the state the component reports. A node with an ID keeps its
// host component (and its scroll state) across frames.
type ScrollView struct {
	// ID names the node; it is required for the host to keep the scroll
	// state across frames.
	ID    string
	Child Node
	// Follow is "none" (the default, also when empty) or "end".
	Follow string
	// Primary makes it the scroll view the wheel scrolls first.
	Primary bool
	// Overscroll is "chain" (the default, also when empty) or "contain".
	Overscroll string
	// Scrollbar is "hidden" (the default, also when empty), "auto" or
	// "always".
	Scrollbar string
	// ScrollbarTrackColor and ScrollbarThumbColor are theme foreground
	// tokens for the scrollbar's styles; empty keeps upstream's.
	ScrollbarTrackColor string
	ScrollbarThumbColor string
	// ScrollbarHideDelayMs is the options' delay; nil takes upstream's 1000.
	ScrollbarHideDelayMs *int
	// ScrollTop is scrollTo's argument, applied when it differs from the one
	// the previous frame sent.
	ScrollTop *int
}

// NewScrollView returns a ScrollView named id over child.
func NewScrollView(id string, child Node) *ScrollView { return &ScrollView{ID: id, Child: child} }

func (*ScrollView) isNode() {}

type scrollViewFields struct {
	Follow               string `json:"follow,omitempty"`
	Primary              bool   `json:"primary,omitempty"`
	Overscroll           string `json:"overscroll,omitempty"`
	Scrollbar            string `json:"scrollbar,omitempty"`
	ScrollbarTrackColor  string `json:"scrollbarTrackColor,omitempty"`
	ScrollbarThumbColor  string `json:"scrollbarThumbColor,omitempty"`
	ScrollbarHideDelayMs *int   `json:"scrollbarHideDelayMs,omitempty"`
	ScrollTop            *int   `json:"scrollTop,omitempty"`
}

// WireNode implements [Wired].
func (s *ScrollView) WireNode() WireForm {
	return WireForm{Kind: "scroll-view", ID: s.ID, Children: []Node{s.Child}, Fields: scrollViewFields{
		Follow: s.Follow, Primary: s.Primary, Overscroll: s.Overscroll, Scrollbar: s.Scrollbar,
		ScrollbarTrackColor: s.ScrollbarTrackColor, ScrollbarThumbColor: s.ScrollbarThumbColor,
		ScrollbarHideDelayMs: s.ScrollbarHideDelayMs, ScrollTop: s.ScrollTop,
	}}
}
