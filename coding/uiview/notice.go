package uiview

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// pig additive (D614): accessibility, focus and diagnostics. The a11y and focus records sit beside the view and name
// its nodes by id, so the D107 node records are not touched. A notice tells a plugin what the host or a client did
// with its mount; it never carries content.

// pig additive (D614): the bounds of the a11y, focus and notice records.
const (
	maxA11y          = 256  // pig additive (D614): a11y entries, and focus entries, of a mount
	maxA11yName      = 256  // pig additive (D614): bytes of an accessible name
	maxA11yText      = 1024 // pig additive (D614): bytes of an accessible description
	maxRelations     = 16   // pig additive (D614): relations of one a11y entry
	maxNotices       = 32   // pig additive (D614): notices kept for a mount
	maxNoticeMessage = 256  // pig additive (D614): bytes of a notice message
)

// roles is the closed set of accessible roles.
var roles = []string{"alert", "button", "checkbox", "dialog", "group", "heading", "image", "link", "list", "listitem", "menu", "menuitem",
	"progressbar", "radio", "region", "status", "switch", "tab", "tablist", "tabpanel", "textbox", "toolbar", "tree", "treeitem"}

// Relation ties a node to another node of the same view.
type Relation struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// A11y describes one node of the view to assistive technology.
type A11y struct {
	Node        string     `json:"node"`
	Name        string     `json:"name,omitempty"`
	Description string     `json:"description,omitempty"`
	Role        string     `json:"role,omitempty"`
	Selected    *bool      `json:"selected,omitempty"`
	Checked     *bool      `json:"checked,omitempty"`
	Expanded    *bool      `json:"expanded,omitempty"`
	Busy        *bool      `json:"busy,omitempty"`
	Disabled    *bool      `json:"disabled,omitempty"`
	ReadOnly    *bool      `json:"readOnly,omitempty"`
	Required    *bool      `json:"required,omitempty"`
	Invalid     *bool      `json:"invalid,omitempty"`
	Relations   []Relation `json:"relations,omitempty"`
}

// Focus makes one node focusable. The order of the entries is the focus order. Request "mount" asks for the focus when
// the mount is first drawn; Restore says where the focus goes when the mount ends: "previous" (where it was before) or
// "self" (the client keeps it on the slot).
type Focus struct {
	Node    string `json:"node"`
	Request string `json:"request,omitempty"`
	Restore string `json:"restore,omitempty"`
}

func checkA11y(entries []A11y) error {
	if len(entries) > maxA11y {
		return fmt.Errorf("%d a11y entries, at most %d", len(entries), maxA11y)
	}
	seen := make(map[string]struct{}, len(entries))
	for i := range entries {
		e := &entries[i]
		if _, dup := seen[e.Node]; dup || e.Node == "" {
			return fmt.Errorf("a11y node %q is empty or repeated", e.Node)
		}
		seen[e.Node] = struct{}{}
		if len(e.Name) > maxA11yName || len(e.Description) > maxA11yText || (e.Role != "" && !slices.Contains(roles, e.Role)) {
			return fmt.Errorf("a11y of node %q has a name, description or role outside the record", e.Node)
		}
		if len(e.Relations) > maxRelations {
			return fmt.Errorf("a11y of node %q has %d relations, at most %d", e.Node, len(e.Relations), maxRelations)
		}
		for j, rel := range e.Relations {
			if !slices.Contains([]string{"labelledBy", "describedBy", "controls", "owns"}, rel.Kind) || rel.Target == "" || rel.Target == e.Node || slices.Contains(e.Relations[:j], rel) {
				return fmt.Errorf("a11y of node %q has an unknown, self-referring or repeated relation", e.Node)
			}
		}
	}
	return nil
}

func checkFocus(entries []Focus) error {
	if len(entries) > maxA11y {
		return fmt.Errorf("%d focus entries, at most %d", len(entries), maxA11y)
	}
	seen := make(map[string]struct{}, len(entries))
	requests := 0
	for _, e := range entries {
		if _, dup := seen[e.Node]; dup || e.Node == "" {
			return fmt.Errorf("focus node %q is empty or repeated", e.Node)
		}
		seen[e.Node] = struct{}{}
		if e.Request == "mount" {
			requests++
		} else if e.Request != "" {
			return fmt.Errorf("focus request is %q; only mount is known", e.Request)
		}
		if e.Restore != "" && e.Restore != "previous" && e.Restore != "self" {
			return fmt.Errorf("focus restore is %q; previous and self are known", e.Restore)
		}
	}
	if requests > 1 {
		return fmt.Errorf("%d focus entries request the focus, at most one may", requests)
	}
	return nil
}

// Severity grades a notice.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// NoticeUndrawn is the code of a client's report that it could not draw part of a mount.
const NoticeUndrawn = "undrawn"

// Notice is one diagnostic of a mount. Code is an operation error code (invalid, unsupported, limit, stale, terminal),
// an intent outcome the host decided (refused, failed, timeout, stale) or undrawn. Count is how often it happened.
type Notice struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message,omitempty"`
	Path     string   `json:"path,omitempty"`
	Claim    string   `json:"claim,omitempty"`
	Intent   string   `json:"intent,omitempty"`
	Measured *int64   `json:"measured,omitempty"`
	Limit    *int64   `json:"limit,omitempty"`
	Epoch    uint64   `json:"epoch"`
	Rev      uint64   `json:"rev,omitempty"`
	Count    int      `json:"count"`
}

// Diagnostics is the reply of ui.view.diagnostics.
type Diagnostics struct {
	Notices []Notice `json:"notices"`
}

// redact bounds a message to maxNoticeMessage bytes on a rune boundary and removes control characters, so a notice
// cannot smuggle an escape sequence to whoever prints it.
func redact(s string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
	if len(clean) > maxNoticeMessage {
		clean = clean[:maxNoticeMessage]
		for len(clean) > 0 && !utf8.ValidString(clean) {
			clean = clean[:len(clean)-1]
		}
	}
	return clean
}

// note records n on the mount's current incarnation. A repeat of the same code, severity, path, claim and intent raises
// the count and keeps the latest message and numbers; a 33rd distinct notice drops the oldest. The caller holds the lock.
func (m *mount) note(n Notice) {
	n.Message, n.Path, n.Claim, n.Intent = redact(n.Message), redact(n.Path), redact(n.Claim), redact(n.Intent)
	n.Epoch, n.Rev, n.Count = m.snap.Mount.Epoch, m.snap.Mount.Rev, 1
	for i := range m.notices {
		o := &m.notices[i]
		if o.Code == n.Code && o.Severity == n.Severity && o.Path == n.Path && o.Claim == n.Claim && o.Intent == n.Intent {
			n.Count = o.Count + 1
			*o = n
			return
		}
	}
	if len(m.notices) == maxNotices {
		m.notices = slices.Delete(m.notices, 0, 1)
	}
	m.notices = append(m.notices, n)
}

// Rejected records that an operation on a mount was refused before it reached the registry, because its record is
// outside the closed shape. raw is the refused record; only its mount ref is read. A record that names no current
// incarnation of a mount leaves no notice.
func (r *Registry) Rejected(plugin string, op string, raw []byte, err *Error) {
	var head struct {
		Mount Ref `json:"mount"`
	}
	if json.Unmarshal(raw, &head) != nil || head.Mount.Key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if m := r.mounts[ownerKey{plugin, head.Mount.Key}]; m != nil && m.snap.Mount.Epoch == head.Mount.Epoch {
		m.rejected(op, err)
	}
}

// Diagnostics returns the notices of the mount of plugin under key, oldest first.
func (r *Registry) Diagnostics(plugin string, a StateArgs) (Diagnostics, *Error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.mounts[ownerKey{plugin, a.Key}]
	if m == nil {
		return Diagnostics{}, fail(CodeNotFound, "no mount under key %q", a.Key)
	}
	out := make([]Notice, len(m.notices))
	copy(out, m.notices)
	return Diagnostics{Notices: out}, nil
}
