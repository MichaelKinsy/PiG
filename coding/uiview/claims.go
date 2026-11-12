package uiview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
)

// pig additive (D614): client claims and alternatives. A client states facts about itself as claims; a plugin offers
// alternatives of a view, each guarded by a predicate over the claims, and the host sends every client the first
// alternative whose predicate holds for it. Claim names are open, their values and the operators are closed, and nothing
// here names a vocabulary or a version.

// pig additive (D614): the bounds of claims and alternatives.
const (
	maxClaims       = 64  // pig additive (D614): claims of a client, and of one slot
	maxClaimName    = 64  // pig additive (D614): bytes of a claim name
	maxClaimString  = 256 // pig additive (D614): bytes of a string claim value
	maxClaimList    = 32  // pig additive (D614): strings of a list claim value
	maxAlternatives = 8   // pig additive (D614): alternatives of a mount
	maxGroups       = 8   // pig additive (D614): groups of a predicate
	maxClauses      = 8   // pig additive (D614): clauses of a group
	maxSafeInteger  = 1<<53 - 1
)

// Claim is one fact a client states: Name is "<area>.<fact>" and Value a boolean, a non-negative integer, a string, or
// a list of strings.
type Claim struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// SlotClaims replace the same-named claims of a client for the mounts of one slot.
type SlotClaims struct {
	Slot   Slot    `json:"slot"`
	Claims []Claim `json:"claims"`
}

// DrawingAbilities says which drawing profiles a client draws and how much of one.
type DrawingAbilities struct {
	Profiles []string `json:"profiles"`
	Shapes   int      `json:"shapes"`
	Pixels   int      `json:"pixels"`
}

// Clause tests one claim. Op is present (the claim exists), equals (a boolean, integer or string value), contains (the
// claimed list has every listed string) or atLeast (the claimed integer is not below the value).
type Clause struct {
	Claim string          `json:"claim"`
	Op    string          `json:"op"`
	Value json.RawMessage `json:"value,omitempty"`
}

// ClauseGroup holds when all its clauses hold.
type ClauseGroup struct {
	All []Clause `json:"all"`
}

// Predicate holds when any of its groups holds.
type Predicate struct {
	Any []ClauseGroup `json:"any"`
}

// Alternative is a view a client is sent in place of the mount's view when When holds for the client's claims.
type Alternative struct {
	When    Predicate       `json:"when"`
	View    json.RawMessage `json:"view"`
	Drawing *Drawing        `json:"drawing,omitempty"`
}

type claimKind uint8

const (
	claimBool claimKind = iota + 1
	claimInt
	claimString
	claimList
)

// claimValue is a decoded claim or clause value.
type claimValue struct {
	kind claimKind
	b    bool
	n    uint64
	s    string
	list []string
}

func (v claimValue) equal(o claimValue) bool {
	return v.kind == o.kind && v.b == o.b && v.n == o.n && v.s == o.s && slices.Equal(v.list, o.list)
}

func decodeClaimString(raw []byte) (string, bool) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil || len(s) > maxClaimString {
		return "", false
	}
	return s, true
}

// decodeClaimValue reads the closed value subset: true, false, a non-negative integer written without a fraction or an
// exponent, a string, or a list of strings.
func decodeClaimValue(raw json.RawMessage) (claimValue, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return claimValue{}, false
	}
	switch raw[0] {
	case 't', 'f':
		if string(raw) == "true" || string(raw) == "false" {
			return claimValue{kind: claimBool, b: raw[0] == 't'}, true
		}
	case '"':
		if s, ok := decodeClaimString(raw); ok {
			return claimValue{kind: claimString, s: s}, true
		}
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil || len(items) > maxClaimList {
			return claimValue{}, false
		}
		list := make([]string, 0, len(items))
		for _, item := range items {
			s, ok := decodeClaimString(bytes.TrimSpace(item))
			if !ok || slices.Contains(list, s) {
				return claimValue{}, false
			}
			list = append(list, s)
		}
		return claimValue{kind: claimList, list: list}, true
	default:
		if n, err := strconv.ParseUint(string(raw), 10, 64); err == nil && n <= maxSafeInteger && (len(raw) == 1 || raw[0] != '0') {
			return claimValue{kind: claimInt, n: n}, true
		}
	}
	return claimValue{}, false
}

// validClaimName reports whether name is "<area>.<fact>...": a lower-case first segment and at least one more segment,
// each starting with a lower-case letter, in at most 64 bytes.
func validClaimName(name string) bool {
	if len(name) == 0 || len(name) > maxClaimName {
		return false
	}
	segments, start := 0, true
	for i := range len(name) {
		c := name[i]
		switch {
		case start:
			if c < 'a' || c > 'z' {
				return false
			}
			start = false
			segments++
		case c == '.':
			start = true
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c >= 'A' && c <= 'Z':
			if segments == 1 {
				return false
			}
		default:
			return false
		}
	}
	return !start && segments >= 2
}

func checkClaims(claims []Claim) error {
	if len(claims) > maxClaims {
		return fmt.Errorf("%d claims, at most %d", len(claims), maxClaims)
	}
	seen := make(map[string]struct{}, len(claims))
	for _, c := range claims {
		if !validClaimName(c.Name) {
			return fmt.Errorf("claim name %q is not <area>.<fact>", c.Name)
		}
		if _, dup := seen[c.Name]; dup {
			return fmt.Errorf("claim %q stated twice", c.Name)
		}
		seen[c.Name] = struct{}{}
		if _, ok := decodeClaimValue(c.Value); !ok {
			return fmt.Errorf("claim %q has a value outside boolean, integer, string and list of strings", c.Name)
		}
	}
	return nil
}

// check validates the members of the abilities this record adds: the claims, the slot claims and the drawing abilities.
func (a *ClientAbilities) check() *Error {
	if err := checkClaims(a.Claims); err != nil {
		return fail(CodeInvalid, "%v", err)
	}
	seen := map[Slot]struct{}{}
	for _, sc := range a.SlotClaims {
		if !slices.Contains(slots, sc.Slot) {
			return fail(CodeInvalid, "slotClaims names unknown slot %q", sc.Slot)
		}
		if _, dup := seen[sc.Slot]; dup {
			return fail(CodeInvalid, "slotClaims names slot %q twice", sc.Slot)
		}
		seen[sc.Slot] = struct{}{}
		if err := checkClaims(sc.Claims); err != nil {
			return fail(CodeInvalid, "slot %q: %v", sc.Slot, err)
		}
	}
	if d := a.Drawing; d != nil {
		for i, p := range d.Profiles {
			if (p != ProfileScene && p != ProfilePixels) || slices.Contains(d.Profiles[:i], p) {
				return fail(CodeInvalid, "drawing profile %q is unknown or repeated", p)
			}
		}
		if d.Shapes < 0 || d.Shapes > maxShapes || d.Pixels < 0 || d.Pixels > maxPixels {
			return fail(CodeInvalid, "drawing limits are shapes 0..%d and pixels 0..%d", maxShapes, maxPixels)
		}
	}
	return nil
}

// claimsFor returns the claims that hold for a mount in slot: the client's claims with the slot's own in their place.
func (a *ClientAbilities) claimsFor(slot Slot) map[string]claimValue {
	out := make(map[string]claimValue, len(a.Claims))
	add := func(claims []Claim) {
		for _, c := range claims {
			if v, ok := decodeClaimValue(c.Value); ok {
				out[c.Name] = v
			}
		}
	}
	add(a.Claims)
	for _, sc := range a.SlotClaims {
		if sc.Slot == slot {
			add(sc.Claims)
		}
	}
	return out
}

func (p Predicate) check() error {
	if len(p.Any) == 0 || len(p.Any) > maxGroups {
		return fmt.Errorf("when.any has %d groups, 1..%d are allowed", len(p.Any), maxGroups)
	}
	for _, g := range p.Any {
		if len(g.All) == 0 || len(g.All) > maxClauses {
			return fmt.Errorf("a group has %d clauses, 1..%d are allowed", len(g.All), maxClauses)
		}
		for _, c := range g.All {
			if !validClaimName(c.Claim) {
				return fmt.Errorf("clause names claim %q, which is not <area>.<fact>", c.Claim)
			}
			v, ok := decodeClaimValue(c.Value)
			switch c.Op {
			case "present":
				ok = len(bytes.TrimSpace(c.Value)) == 0
			case "equals":
				ok = ok && v.kind != claimList
			case "contains":
				ok = ok && v.kind == claimList && len(v.list) > 0
			case "atLeast":
				ok = ok && v.kind == claimInt
			default:
				return fmt.Errorf("unknown operator %q", c.Op)
			}
			if !ok {
				return fmt.Errorf("operator %q on claim %q has a value it does not take", c.Op, c.Claim)
			}
		}
	}
	return nil
}

// holds reports whether the predicate is true of the claims. It costs at most maxGroups*maxClauses clause tests, each
// over at most maxClaimList strings.
func (p Predicate) holds(claims map[string]claimValue) bool {
	for _, g := range p.Any {
		all := true
		for _, c := range g.All {
			if !c.holds(claims) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func (c Clause) holds(claims map[string]claimValue) bool {
	have, ok := claims[c.Claim]
	if !ok {
		return false
	}
	if c.Op == "present" {
		return true
	}
	want, ok := decodeClaimValue(c.Value)
	if !ok {
		return false
	}
	switch c.Op {
	case "equals":
		return have.equal(want)
	case "contains":
		if have.kind != claimList {
			return false
		}
		for _, s := range want.list {
			if !slices.Contains(have.list, s) {
				return false
			}
		}
		return true
	case "atLeast":
		return have.kind == claimInt && have.n >= want.n
	}
	return false
}

func cloneClaims(in []Claim) []Claim {
	if in == nil {
		return nil
	}
	out := make([]Claim, len(in))
	for i, c := range in {
		out[i] = Claim{Name: c.Name, Value: slices.Clone(c.Value)}
	}
	return out
}

func cloneAbilities(a ClientAbilities) ClientAbilities {
	a.Slots = slices.Clone(a.Slots)
	a.Nodes = slices.Clone(a.Nodes)
	a.ImageFormats = slices.Clone(a.ImageFormats)
	a.Claims = cloneClaims(a.Claims)
	if a.SlotClaims != nil {
		sc := make([]SlotClaims, len(a.SlotClaims))
		for i, s := range a.SlotClaims {
			sc[i] = SlotClaims{Slot: s.Slot, Claims: cloneClaims(s.Claims)}
		}
		a.SlotClaims = sc
	}
	if a.Drawing != nil {
		d := *a.Drawing
		d.Profiles = slices.Clone(d.Profiles)
		a.Drawing = &d
	}
	return a
}

func union[T comparable](dst, src []T) []T {
	for _, v := range src {
		if !slices.Contains(dst, v) {
			dst = append(dst, v)
		}
	}
	return dst
}

// shared keeps the claims of a that b states with the same value.
func shared(a, b []Claim) []Claim {
	out := make([]Claim, 0, len(a))
	for _, c := range a {
		av, _ := decodeClaimValue(c.Value)
		for _, o := range b {
			if bv, ok := decodeClaimValue(o.Value); ok && o.Name == c.Name && av.equal(bv) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// mergeAbilities is what the extensions are told while several clients are attached: a mount is possible when some
// client can draw it, so the slots, node kinds, image formats and drawing profiles are the union, markdown and input the
// or, and every limit the largest; a claim is a fact about every client, so only the claims all of them state with the
// same value remain. One client's abilities are returned as they are.
func mergeAbilities(all []*Attachment) *ClientAbilities {
	if len(all) == 0 {
		return nil
	}
	m := cloneAbilities(all[0].abilities)
	for _, t := range all[1:] {
		a := &t.abilities
		m.Slots = union(m.Slots, a.Slots)
		m.Nodes = union(m.Nodes, a.Nodes)
		m.ImageFormats = union(m.ImageFormats, a.ImageFormats)
		m.Markdown = m.Markdown || a.Markdown
		m.Input = Input{Keyboard: m.Input.Keyboard || a.Input.Keyboard, Pointer: m.Input.Pointer || a.Input.Pointer, Text: m.Input.Text || a.Input.Text}
		m.Limits = Limits{
			Nodes: max(m.Limits.Nodes, a.Limits.Nodes), Depth: max(m.Limits.Depth, a.Limits.Depth),
			SnapshotBytes: max(m.Limits.SnapshotBytes, a.Limits.SnapshotBytes), TextBytes: max(m.Limits.TextBytes, a.Limits.TextBytes),
			Mounts: max(m.Limits.Mounts, a.Limits.Mounts), IntentArgBytes: max(m.Limits.IntentArgBytes, a.Limits.IntentArgBytes),
		}
		m.Claims = shared(m.Claims, a.Claims)
		var sc []SlotClaims
		for _, s := range m.SlotClaims {
			for _, o := range a.SlotClaims {
				if o.Slot == s.Slot {
					sc = append(sc, SlotClaims{Slot: s.Slot, Claims: shared(s.Claims, o.Claims)})
				}
			}
		}
		m.SlotClaims = sc
		if a.Drawing != nil {
			if m.Drawing == nil {
				m.Drawing = &DrawingAbilities{}
			}
			m.Drawing.Profiles = union(m.Drawing.Profiles, a.Drawing.Profiles)
			m.Drawing.Shapes = max(m.Drawing.Shapes, a.Drawing.Shapes)
			m.Drawing.Pixels = max(m.Drawing.Pixels, a.Drawing.Pixels)
		}
	}
	return &m
}
