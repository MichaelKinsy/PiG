package codingagent

import (
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pig additive (D614): the bindings of a mount in the terminal.
//
// A key binding is in force only while its mount holds the keyboard focus, and only for a key the focused node of the
// view declined: viewMount.HandleInput gives such a key to viewClient.unhandledKey, which is the one place a binding
// fires. No binding is global, and none is in force while the editor, an overlay, another mount or the tab row holds the
// focus; a hidden entry of the tab row has none. A mount that binds a key is focusable as a whole (viewMount.takesFocus),
// so the focus key reaches it even when its view has no node that takes keys, and the mount itself, not its tab entry,
// holds the focus then: a key has one path to the bindings.
//
// On that path the reserved actions come first (viewReservedActions): a key the user's keybindings give to one of them
// reaches no binding.
//
// The terminal reports no gesture, and a mount of a slot that takes no focus is never given a key. The plugin is told
// each such binding once, as an unsupported notice with the path bindings[i].
//
// While the mount holds the focus, one row under its view lists the bound keys that have a name. The row is one of the
// slot's rows. A mount with no named key binding draws no such row.

// viewHintGap separates the entries of the hint row.
const viewHintGap = "  "

// viewReservedActions are the actions whose keys no binding is given: the four app.* rows of Pi's KEYBINDINGS
// (core/keybindings.ts, appKeybindingRows here) whose default keys are rows of uiview.ReservedKeys: app.interrupt
// (escape), app.clear (ctrl+c), app.exit (ctrl+d) and app.suspend (ctrl+z). The host refuses a binding of those defaults
// when it is mounted. The user's keybindings file gives an action other keys and /reload reads it again, so the
// terminal asks the keybindings when a key is pressed (viewClient.unhandledKey, viewMount.HandleInput).
var viewReservedActions = [...]AppKeybinding{AppInterrupt, AppClear, AppExit, AppSuspend}

// viewReservedAction returns the reserved action the keybindings give data to, or "".
func viewReservedAction(km *KeybindingsManager, data string) string {
	for _, action := range viewReservedActions {
		if km.Matches(data, string(action)) {
			return string(action)
		}
	}
	return ""
}

// bind takes the bindings of a snapshot: it decides whether the mount offers a key, builds the text of the hint row and
// tells the plugin which bindings the terminal cannot honour. It runs once for a snapshot, never for a frame.
func (m *viewMount) bind(bindings []uiview.Binding) {
	m.bindings, m.keys = bindings, false
	var hint strings.Builder
	for i := range bindings {
		b := &bindings[i]
		switch {
		case b.Key == "":
			m.reportUnbound(i, b.Gesture, "the terminal reports no gesture, so "+b.Gesture+" raises nothing")
		case !m.slot.focus:
			m.reportUnbound(i, b.Key, "a mount of slot "+string(m.slot.slot)+" never holds the keyboard focus in the terminal, so key "+b.Key+" raises nothing")
		default:
			m.keys = true
			if name := strings.TrimSpace(b.Name); name != "" {
				if hint.Len() > 0 {
					hint.WriteString(viewHintGap)
				}
				hint.WriteString(b.Key)
				hint.WriteString(" ")
				hint.WriteString(name)
			}
		}
	}
	m.hint = hint.String()
}

// binds reports whether a key the terminal offers raises intent.
func (m *viewMount) binds(intent string) bool {
	return m.keys && slices.ContainsFunc(m.bindings, func(b uiview.Binding) bool { return b.Key != "" && b.Intent == intent })
}

// reportUnbound tells the plugin, once for a binding's place and trigger, that the terminal cannot honour it.
func (m *viewMount) reportUnbound(index int, trigger, message string) {
	path := "bindings[" + strconv.Itoa(index) + "]"
	if m.unbound[path+trigger] {
		return
	}
	if m.unbound == nil {
		m.unbound = map[string]bool{}
	}
	m.unbound[path+trigger] = true
	m.c.report(m, uiview.Notice{Code: string(uiview.CodeUnsupported), Severity: uiview.SeverityInfo, Path: path, Message: message})
}

// viewHintRow is the hint row at width: the text cut by cells, never by bytes, in the theme's muted colour.
func viewHintRow(hint string, width int, theme *tui.Theme) string {
	return " " + theme.Fg("muted", widthx.TruncateToWidth(hint, max(width-1, 0), viewTabEllipsis, false))
}
