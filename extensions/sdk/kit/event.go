package kit

// Event types: the upstream callbacks of SelectList (onSelect, onCancel,
// onSelectionChange) and SettingsList (onChange, onCancel).
const (
	EventSelect          = "select"
	EventCancel          = "cancel"
	EventSelectionChange = "selectionChange"
	EventChange          = "change"
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
}
