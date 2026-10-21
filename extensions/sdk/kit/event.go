package kit

// Event types: the upstream callbacks of SelectList (onSelect, onCancel,
// onSelectionChange) and SettingsList (onChange, onCancel).
const (
	EventSelect          = "select"
	EventCancel          = "cancel"
	EventSelectionChange = "selectionChange"
	EventChange          = "change"
	// The widget kinds' callbacks: Input's and Editor's onSubmit, Input's
	// onEscape, CancellableLoader's onAbort and the mouse event a
	// MouseRegion took. EventChange also reports the value an Input or an
	// Editor holds after a key.
	EventSubmit = "submit"
	EventEscape = "escape"
	EventAbort  = "abort"
	EventMouse  = "mouse"
)

// Event is a callback an interactive node of a ui.custom view fired on the
// host. The host owns the list's state and reports each callback in firing
// order, after every key it forwarded before it.
type Event struct {
	// Node is the ID of the [SelectList] or [SettingsList] that fired.
	Node string
	// Type is [EventSelect], [EventCancel], [EventSelectionChange] or
	// [EventChange].
	Type string
	// Index is the item's index among the shown (filtered) items for
	// EventSelect and EventSelectionChange.
	Index int
	// Item is the select-list item of EventSelect and EventSelectionChange.
	Item *SelectItem
	// ID and Value are the setting and its new value of EventChange.
	ID    string
	Value string
	// Cursor is the cursor of an input or an editor after EventChange,
	// EventSubmit and EventEscape.
	Cursor *Cursor
	// Mouse is the event of EventMouse.
	Mouse *Mouse
}
