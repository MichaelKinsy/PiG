// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import "github.com/MichaelKinsy/PiG/durable/core/jv"

// System prompt preparation of src/harness/prompt.ts: replay the sections and tools the context's system messages show,
// and plan the pi.system entries that make them equal the desired sections and tools in values and order.

// Sections is an insertion-ordered string map (a JavaScript Map<string, string>): setting an existing key keeps its
// place, deleting then setting again appends.
type Sections struct {
	keys []string
	vals map[string]string
}

// Set stores a value; an existing key keeps its position.
func (s *Sections) Set(key, value string) {
	if s.vals == nil {
		s.vals = map[string]string{}
	}
	if _, ok := s.vals[key]; !ok {
		s.keys = append(s.keys, key)
	}
	s.vals[key] = value
}

// Delete removes a key.
func (s *Sections) Delete(key string) {
	if _, ok := s.vals[key]; !ok {
		return
	}
	delete(s.vals, key)
	for i, k := range s.keys {
		if k == key {
			s.keys = append(s.keys[:i], s.keys[i+1:]...)
			break
		}
	}
}

// Get returns a value.
func (s *Sections) Get(key string) (string, bool) { v, ok := s.vals[key]; return v, ok }

// Has reports whether key is present.
func (s *Sections) Has(key string) bool { _, ok := s.vals[key]; return ok }

// Keys returns the keys in order. The slice is the map's own; do not modify it.
func (s *Sections) Keys() []string { return s.keys }

// ReplaySections is replaySections: the sections in effect after replaying system messages in order. A message's
// `sections` object is read in ECMAScript property order; a null value deletes.
func ReplaySections(messages []*jv.Object) *Sections {
	shown := &Sections{}
	for _, message := range messages {
		if role, _ := message.Get("role"); role != "system" {
			continue
		}
		patch, ok := getObject(message, "sections")
		if !ok {
			continue
		}
		patch.Entries(func(key string, value any) bool {
			switch v := value.(type) {
			case nil:
				shown.Delete(key)
			case string:
				shown.Set(key, v)
			}
			return true
		})
	}
	return shown
}

// PromptEntry is an entry of the context view that planning inspects.
type PromptEntry struct {
	ID   int64
	Kind string
}

// PromptView is the part of a ContextView that planSystemEntries reads: the head marker, the entries and the messages.
type PromptView struct {
	HasHead  bool
	HeadID   int64
	Entries  []PromptEntry
	Messages []*jv.Object
}

// KindSystem is the kind of the system entry.
const KindSystem = "pi.system"

// PlanSystemEntries is planSystemEntries. It returns entry drafts (`{model: [message]}`, with `edits` on a baseline that
// omits retained system entries). desired are the rendered sections in order and tools the registered tool schemas.
//
//   - A head marker with no later system entry in context: one complete baseline that omits every retained earlier
//     system entry, written even when it restates the replayed values.
//   - Otherwise, when a minimal section patch would leave a different order: remove every shown section, then re-add
//     every desired section in order.
//   - Otherwise the minimal patch of changed values and null removals, or nothing.
//
// Tool changes ride on the last planned entry, or on one entry of their own.
func PlanSystemEntries(view PromptView, desired *Sections, tools []*jv.Object, timestamp float64) []*jv.Object {
	if view.HasHead {
		later := false
		for _, e := range view.Entries {
			if e.Kind == KindSystem && e.ID > view.HeadID {
				later = true
				break
			}
		}
		if !later {
			var edits []any
			for _, e := range view.Entries {
				if e.Kind == KindSystem {
					edit := jv.NewObject()
					edit.Set("target", float64(e.ID))
					edit.Set("action", "omit")
					edits = append(edits, edit)
				}
			}
			baseline := systemEntry(sectionsPatch(desired), &toolChanges{removed: []any{}, added: declarations(tools)}, timestamp)
			if len(edits) != 0 {
				baseline.Set("edits", edits)
			}
			return []*jv.Object{baseline}
		}
	}
	patches := planSections(ReplaySections(view.Messages), desired)
	changes := planTools(CurrentTools(view.Messages), tools)
	if len(changes.removed) == 0 && len(changes.added) == 0 {
		out := make([]*jv.Object, 0, len(patches))
		for _, patch := range patches {
			out = append(out, systemEntry(patch, nil, timestamp))
		}
		return out
	}
	if len(patches) == 0 {
		return []*jv.Object{systemEntry(nil, changes, timestamp)}
	}
	out := make([]*jv.Object, 0, len(patches))
	for i, patch := range patches {
		var c *toolChanges
		if i == len(patches)-1 {
			c = changes
		}
		out = append(out, systemEntry(patch, c, timestamp))
	}
	return out
}

type toolChanges struct{ removed, added []any }

// CurrentTools is getCurrentTools: the tools available after applying every system message's toolsRemoved and toolsAdded
// in order, in first-declaration order (a re-added tool keeps its place).
func CurrentTools(messages []*jv.Object) []*jv.Object {
	var names []string
	byName := map[string]*jv.Object{}
	for _, message := range messages {
		if role, _ := message.Get("role"); role != "system" {
			continue
		}
		for _, ref := range arrayOf(message, "toolsRemoved") {
			if name := nameOf(ref); name != "" {
				if _, ok := byName[name]; ok {
					delete(byName, name)
					names = removeString(names, name)
				}
			}
		}
		for _, tool := range arrayOf(message, "toolsAdded") {
			t, ok := tool.(*jv.Object)
			if !ok {
				continue
			}
			name := nameOf(t)
			if _, ok := byName[name]; !ok {
				names = append(names, name)
			}
			byName[name] = t
		}
	}
	out := make([]*jv.Object, 0, len(names))
	for _, n := range names {
		out = append(out, byName[n])
	}
	return out
}

// ToolDeclaration is toToolDeclaration: name, description, the parameters as a JSON round trip and constrainedSampling
// when defined.
func ToolDeclaration(tool *jv.Object) *jv.Object {
	d := jv.NewObject()
	for _, key := range [...]string{"name", "description"} {
		if v, ok := tool.Get(key); ok {
			d.Set(key, v)
		}
	}
	if v, ok := tool.Get("parameters"); ok {
		d.Set("parameters", jv.Clone(v))
	}
	if v, ok := tool.Get("constrainedSampling"); ok {
		d.Set("constrainedSampling", v)
	}
	return d
}

// DeclarationsEqual is declarationsEqual: both tools go through ToolDeclaration, then their JSON text is compared.
func DeclarationsEqual(left, right *jv.Object) bool {
	return jv.Stringify(ToolDeclaration(left)) == jv.Stringify(ToolDeclaration(right))
}

func declarations(tools []*jv.Object) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, ToolDeclaration(t))
	}
	return out
}

// planTools returns the tool changes from offered to desired. A changed declaration is removed and re-added. Replay keeps
// retained tools in place and appends additions; when that would not yield the desired order, every offered tool is
// removed and every desired tool re-added in order.
func planTools(offered, desired []*jv.Object) *toolChanges {
	wanted := map[string]*jv.Object{}
	for _, t := range desired {
		wanted[nameOf(t)] = t // a repeated name keeps the last, as Map construction does
	}
	keptNames := map[string]bool{}
	var kept []*jv.Object
	for _, t := range offered {
		if next, ok := wanted[nameOf(t)]; ok && DeclarationsEqual(t, next) {
			kept = append(kept, t)
			keptNames[nameOf(t)] = true
		}
	}
	var added []*jv.Object
	for _, t := range desired {
		if !keptNames[nameOf(t)] {
			added = append(added, t)
		}
	}
	replayed := append(append([]*jv.Object{}, kept...), added...)
	for i, t := range replayed {
		if i >= len(desired) || nameOf(t) != nameOf(desired[i]) {
			return &toolChanges{removed: references(offered), added: declarations(desired)}
		}
	}
	var removed []*jv.Object
	for _, t := range offered {
		if !keptNames[nameOf(t)] {
			removed = append(removed, t)
		}
	}
	return &toolChanges{removed: references(removed), added: declarations(added)}
}

func references(tools []*jv.Object) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		ref := jv.NewObject()
		ref.Set("name", nameOf(t))
		out = append(out, ref)
	}
	return out
}

// planSections returns the section patches: none, the minimal patch, or a remove-all/re-add-all pair when the order
// would differ. A patch maps a key to its text, or to null.
func planSections(shown, desired *Sections) []*jv.Object {
	var patched []string
	for _, k := range shown.Keys() {
		if desired.Has(k) {
			patched = append(patched, k)
		}
	}
	for _, k := range desired.Keys() {
		if !shown.Has(k) {
			patched = append(patched, k)
		}
	}
	for i, k := range patched {
		if k != desired.Keys()[i] {
			remove := jv.NewObject()
			for _, key := range shown.Keys() {
				remove.Set(key, nil)
			}
			return []*jv.Object{remove, sectionsPatch(desired)}
		}
	}
	patch := jv.NewObject()
	for _, k := range shown.Keys() {
		value, _ := shown.Get(k)
		if next, ok := desired.Get(k); !ok {
			patch.Set(k, nil)
		} else if next != value {
			patch.Set(k, next)
		}
	}
	for _, k := range desired.Keys() {
		if !shown.Has(k) {
			next, _ := desired.Get(k)
			patch.Set(k, next)
		}
	}
	if patch.Len() == 0 {
		return nil
	}
	return []*jv.Object{patch}
}

// sectionsPatch is Object.fromEntries(sections).
func sectionsPatch(sections *Sections) *jv.Object {
	o := jv.NewObject()
	for _, k := range sections.Keys() {
		v, _ := sections.Get(k)
		o.Set(k, v)
	}
	return o
}

// systemEntry is the draft of one pi.system entry.
func systemEntry(sections *jv.Object, tools *toolChanges, timestamp float64) *jv.Object {
	message := jv.NewObject()
	message.Set("role", "system")
	message.Set("content", "")
	if sections != nil {
		message.Set("sections", sections)
	}
	if tools != nil && len(tools.removed) != 0 {
		message.Set("toolsRemoved", tools.removed)
	}
	if tools != nil && len(tools.added) != 0 {
		message.Set("toolsAdded", tools.added)
	}
	message.Set("timestamp", timestamp)
	draft := jv.NewObject()
	draft.Set("model", []any{message})
	return draft
}

func getObject(o *jv.Object, key string) (*jv.Object, bool) {
	v, ok := o.Get(key)
	if !ok {
		return nil, false
	}
	obj, ok := v.(*jv.Object)
	return obj, ok
}

func arrayOf(o *jv.Object, key string) []any {
	v, _ := o.Get(key)
	a, _ := v.([]any)
	return a
}

func nameOf(v any) string {
	o, ok := v.(*jv.Object)
	if !ok {
		return ""
	}
	n, _ := o.Get("name")
	s, _ := n.(string)
	return s
}

func removeString(list []string, s string) []string {
	for i, v := range list {
		if v == s {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}
