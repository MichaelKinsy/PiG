package subprocess

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/tui"
)

// pig additive (D614): the rules of a grid are the registry's to enforce at mount and update as well as the client's at
// draw. The registry cannot call them (coding/uiview imports nothing of the view kit and subprocess imports it), so the
// bridge, the one door an extension's ui.view.mount and ui.view.update come through, runs checkGrid on every grid node of
// the content before the registry sees it.

// viewNodeProbe is the part of a node that finds the grids below it: its kind, children and a settings-list's submenus.
type viewNodeProbe struct {
	Kind     string            `json:"kind"`
	Children []json.RawMessage `json:"children"`
	Items    []struct {
		Submenu json.RawMessage `json:"submenu"`
	} `json:"items"`
}

// checkMountGrids rejects a grid of content that does not follow the record: a member it does not have, a bound passed, a
// value that does not agree. Content with no grid passes untouched, and nothing but grid nodes is checked.
func checkMountGrids(c *uiview.Content) error {
	check := &gridMountCheck{theme: tui.ActiveTheme()}
	check.view(c.View)
	for i := range c.Alternatives {
		check.view(c.Alternatives[i].View)
	}
	check.node(c.Fallback)
	return check.err
}

type gridMountCheck struct {
	theme *tui.Theme
	err   error
}

func (g *gridMountCheck) view(raw json.RawMessage) {
	var v struct {
		Root json.RawMessage `json:"root"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &v) == nil {
		g.node(v.Root)
	}
}

func (g *gridMountCheck) node(raw json.RawMessage) {
	if g.err != nil || len(raw) == 0 {
		return
	}
	var probe viewNodeProbe
	if json.Unmarshal(raw, &probe) != nil {
		return
	}
	if probe.Kind == ViewKindGrid {
		var n ViewNode
		if err := json.Unmarshal(raw, &n); err != nil {
			g.err = err
			return
		}
		c := viewChecker{ids: map[string]string{}, theme: g.theme}
		c.checkWidget(&n)
		g.err = c.err
		return
	}
	for _, child := range probe.Children {
		g.node(child)
	}
	for _, item := range probe.Items {
		g.node(item.Submenu)
	}
}

// gridCheckError is the error a rejected grid is answered with.
func gridCheckError(err error) *uiview.Error {
	return &uiview.Error{Code: uiview.CodeInvalid, Message: err.Error()}
}
