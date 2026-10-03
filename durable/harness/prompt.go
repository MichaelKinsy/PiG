// Ports packages/durable/src/harness/prompt.ts.

package harness

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// SystemDraft is a planned pi.system entry.
type SystemDraft = durable.TypedEntryDraft[durable.Never]

// ReplaySections returns the sections in effect after replaying system messages in order: a value sets in place, a nil value deletes, and a re-added key appends.
func ReplaySections(messages []ai.Message) *OrderedMap[string] {
	shown := NewOrderedMap[string]()
	for _, message := range messages {
		system, ok := message.(ai.SystemMessage)
		if !ok || system.Sections == nil {
			continue
		}
		for _, section := range system.Sections {
			if section.Value == nil {
				shown.Delete(section.Name)
			} else {
				shown.Set(section.Name, *section.Value)
			}
		}
	}
	return shown
}

// RenderSections renders the agent's sections in order. A nil text omits a section; tagged text is wrapped as `<key>\n...\n</key>`. A section that fails keeps its shown text, if any, and is reported; errors after ctx is cancelled propagate.
func RenderSections(ctx context.Context, sections []*durable.PromptSection, input durable.PromptInput, shown *OrderedMap[string], report func(error)) (*OrderedMap[string], error) {
	desired := NewOrderedMap[string]()
	for _, section := range sections {
		text, err := section.Render(ctx, input)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			report(err)
			if kept, ok := shown.Get(section.Key); ok {
				desired.Set(section.Key, kept)
			}
			continue
		}
		if text == nil {
			continue
		}
		if section.Tag != nil && !*section.Tag {
			desired.Set(section.Key, *text)
		} else {
			desired.Set(section.Key, "<"+section.Key+">\n"+*text+"\n</"+section.Key+">")
		}
	}
	return desired, nil
}

type toolChanges struct {
	toolsRemoved []ai.ToolReference
	toolsAdded   []ai.ToolSchema
}

// PlanSystemEntries plans the pi.system entries that make the replayed sections and tools of view equal desired and tools in values and order.
//
//   - A head marker with no later pi.system entry in context: one complete baseline that omits every retained earlier pi.system entry, written even when it restates the replayed values.
//   - Otherwise, when a minimal section patch would leave a different order: remove every shown section, then re-add every desired section in order.
//   - Otherwise the minimal patch of changed values and removals, or nothing.
//
// Tool changes ride on the last planned entry, or on one entry of their own.
func PlanSystemEntries(view durable.ContextView, desired *OrderedMap[string], tools []ai.ToolSchema, timestamp int64) []SystemDraft {
	head := view.Head
	if head != nil && !hasLaterSystemEntry(view.Entries, head.Id) {
		var edits []durable.ContextEdit
		for i := range view.Entries {
			if durable.SystemEntry.Is(&view.Entries[i]) {
				edits = append(edits, durable.ContextEdit{Target: view.Entries[i].Id, Action: durable.EditOmit})
			}
		}
		declarations := make([]ai.ToolSchema, 0, len(tools))
		for _, tool := range tools {
			declarations = append(declarations, ai.ToToolDeclaration(tool))
		}
		baseline := systemEntry(sectionsOf(desired), &toolChanges{toolsAdded: declarations}, timestamp)
		baseline.Edits = edits
		return []SystemDraft{baseline}
	}
	sections := planSections(ReplaySections(view.Messages), desired)
	changes := planTools(ai.GetCurrentTools(view.Messages), tools)
	if len(changes.toolsRemoved) == 0 && len(changes.toolsAdded) == 0 {
		drafts := make([]SystemDraft, 0, len(sections))
		for _, patch := range sections {
			drafts = append(drafts, systemEntry(patch, nil, timestamp))
		}
		return drafts
	}
	if len(sections) == 0 {
		return []SystemDraft{systemEntry(nil, &changes, timestamp)}
	}
	drafts := make([]SystemDraft, 0, len(sections))
	for index, patch := range sections {
		var rides *toolChanges
		if index == len(sections)-1 {
			rides = &changes
		}
		drafts = append(drafts, systemEntry(patch, rides, timestamp))
	}
	return drafts
}

func hasLaterSystemEntry(entries []durable.EntryRecord, headId durable.EntryId) bool {
	for i := range entries {
		if durable.SystemEntry.Is(&entries[i]) && entries[i].Id > headId {
			return true
		}
	}
	return false
}

func sectionsOf(values *OrderedMap[string]) ai.OrderedSections {
	sections := ai.OrderedSections{}
	for _, key := range values.Keys() {
		value, _ := values.Get(key)
		sections = append(sections, ai.PromptSection{Name: key, Value: new(value)})
	}
	return sections
}

// planTools returns the tool changes from offered to desired. A changed declaration is removed and re-added. Replay keeps retained tools in place and appends additions; when that would not yield the desired order, every offered tool is removed and every desired tool re-added in order.
func planTools(offered []ai.ToolSchema, desired []ai.ToolSchema) toolChanges {
	wanted := map[string]ai.ToolSchema{}
	for _, tool := range desired {
		wanted[tool.Name] = tool
	}
	var kept []ai.ToolSchema
	keptNames := map[string]bool{}
	for _, tool := range offered {
		next, ok := wanted[tool.Name]
		if ok && ai.DeclarationsEqual(tool, next) {
			kept = append(kept, tool)
			keptNames[tool.Name] = true
		}
	}
	var added []ai.ToolSchema
	for _, tool := range desired {
		if !keptNames[tool.Name] {
			added = append(added, tool)
		}
	}
	replayed := append(append([]ai.ToolSchema{}, kept...), added...)
	reordered := false
	for index, tool := range replayed {
		if tool.Name != desired[index].Name {
			reordered = true
			break
		}
	}
	var changes toolChanges
	if reordered {
		for _, tool := range offered {
			changes.toolsRemoved = append(changes.toolsRemoved, ai.ToolReference{Name: tool.Name})
		}
		for _, tool := range desired {
			changes.toolsAdded = append(changes.toolsAdded, ai.ToToolDeclaration(tool))
		}
		return changes
	}
	for _, tool := range offered {
		if !keptNames[tool.Name] {
			changes.toolsRemoved = append(changes.toolsRemoved, ai.ToolReference{Name: tool.Name})
		}
	}
	for _, tool := range added {
		changes.toolsAdded = append(changes.toolsAdded, ai.ToToolDeclaration(tool))
	}
	return changes
}

// planSections returns the section patches: none, the minimal patch, or a remove-all/re-add-all pair when the order would differ.
func planSections(shown *OrderedMap[string], desired *OrderedMap[string]) []ai.OrderedSections {
	var patchedOrder []string
	for _, key := range shown.Keys() {
		if desired.Has(key) {
			patchedOrder = append(patchedOrder, key)
		}
	}
	for _, key := range desired.Keys() {
		if !shown.Has(key) {
			patchedOrder = append(patchedOrder, key)
		}
	}
	desiredOrder := desired.Keys()
	for index, key := range patchedOrder {
		if key != desiredOrder[index] {
			removeAll := ai.OrderedSections{}
			for _, shownKey := range shown.Keys() {
				removeAll = append(removeAll, ai.PromptSection{Name: shownKey})
			}
			return []ai.OrderedSections{removeAll, sectionsOf(desired)}
		}
	}

	patch := ai.OrderedSections{}
	for _, key := range shown.Keys() {
		value, _ := shown.Get(key)
		next, ok := desired.Get(key)
		if !ok {
			patch = append(patch, ai.PromptSection{Name: key})
		} else if next != value {
			patch = append(patch, ai.PromptSection{Name: key, Value: new(next)})
		}
	}
	for _, key := range desired.Keys() {
		if !shown.Has(key) {
			value, _ := desired.Get(key)
			patch = append(patch, ai.PromptSection{Name: key, Value: new(value)})
		}
	}
	if len(patch) == 0 {
		return nil
	}
	return []ai.OrderedSections{patch}
}

func systemEntry(sections ai.OrderedSections, tools *toolChanges, timestamp int64) SystemDraft {
	message := ai.SystemMessage{Content: ai.SystemText(""), Sections: sections, Timestamp: timestamp}
	if tools != nil && len(tools.toolsRemoved) > 0 {
		message.ToolsRemoved = tools.toolsRemoved
	}
	if tools != nil && len(tools.toolsAdded) > 0 {
		message.ToolsAdded = tools.toolsAdded
	}
	return SystemDraft{Model: []ai.Message{message}}
}
