package pico3

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// SystemSection is a stable key plus a pure renderer. Only keys, payloads,
// and rendered text are stored; never renderers.
type SystemSection struct {
	Key    string
	Render func(value JsonValue) string
}

// DefineSystemSection defines a section.
func DefineSystemSection(key string, render func(value JsonValue) string) *SystemSection {
	return &SystemSection{Key: key, Render: render}
}

// SystemSections are the built-in sections.
var SystemSections = struct {
	Identity    *SystemSection
	Environment *SystemSection
	Skills      *SystemSection
}{
	Identity: DefineSystemSection("identity", func(value JsonValue) string {
		text, _ := value.(string)
		return text
	}),
	Environment: DefineSystemSection("environment", func(value JsonValue) string {
		info, _ := value.(map[string]any)
		return "Working directory: " + str(info, "cwd")
	}),
	Skills: DefineSystemSection("skills", func(value JsonValue) string {
		items, _ := value.([]any)
		lines := make([]string, 0, len(items))
		for _, item := range items {
			skill, _ := item.(map[string]any)
			lines = append(lines, fmt.Sprintf("- %s: %s", str(skill, "name"), str(skill, "description")))
		}
		return strings.Join(lines, "\n")
	}),
}

// SectionSeedOf builds a section seed with a checked value.
func SectionSeedOf(section *SystemSection, value JsonValue) SectionSeed {
	return SectionSeed{Key: section.Key, Value: value}
}

// RemoveSection builds a seed that removes an inherited key.
func RemoveSection(key string) SectionSeed { return SectionSeed{Key: key, Remove: true} }

// sectionState is one canonical section.
type sectionState struct {
	Value    JsonValue
	Rendered string
}

// canonical is an insertion-ordered section map; its order is canonical order.
type canonical struct {
	keys   []string
	values map[string]sectionState
}

func newCanonical() *canonical { return &canonical{values: map[string]sectionState{}} }

func (sections *canonical) set(key string, state sectionState) {
	if _, ok := sections.values[key]; !ok {
		sections.keys = append(sections.keys, key)
	}
	sections.values[key] = state
}

func (sections *canonical) remove(key string) {
	if _, ok := sections.values[key]; !ok {
		return
	}
	delete(sections.values, key)
	sections.keys = slices.DeleteFunc(sections.keys, func(candidate string) bool { return candidate == key })
}

func (sections *canonical) get(key string) (sectionState, bool) {
	state, ok := sections.values[key]
	return state, ok
}

// foldCanonical folds the fork-visible managed entries from the newest
// baseline forward (§12.4). It never depends on heads or edits.
func foldCanonical(tx *Tx, conversationId Id) (*canonical, *Id, *Id, error) {
	var managed []Entry
	var before *Id
	var baseline *Id
scan:
	for {
		page, err := tx.ScanEntries(EntryScan{ConversationId: conversationId, Kind: "pi.system", Limit: 64, Before: before})
		if err != nil {
			return nil, nil, nil, err
		}
		for _, entry := range page {
			managed = append(managed, entry)
			if entry.Data["baseline"] == true {
				id := entry.Id
				baseline = &id
				break scan
			}
		}
		if len(page) < 64 {
			break
		}
		last := page[len(page)-1].Id
		before = &last
	}
	slices.Reverse(managed)
	folded := newCanonical()
	for _, entry := range managed {
		for _, record := range arr(entry.Data, "sections") {
			applySectionRecord(folded, record)
		}
	}
	var newest *Id
	if len(managed) > 0 {
		id := managed[len(managed)-1].Id
		newest = &id
	}
	return folded, newest, baseline, nil
}

func applySectionRecord(folded *canonical, record any) {
	object, _ := record.(map[string]any)
	if str(object, "action") == "remove" {
		folded.remove(str(object, "key"))
		return
	}
	folded.set(str(object, "key"), sectionState{Value: object["value"], Rendered: str(object, "rendered")})
}

// SystemSectionDraft is what systemInstructions handlers edit.
type SystemSectionDraft struct {
	values   *canonical
	wrappers map[string][]func(rendered string) string
	touched  map[string]bool
}

func newDraft(seed *canonical) *SystemSectionDraft {
	draft := &SystemSectionDraft{values: newCanonical(), wrappers: map[string][]func(string) string{}, touched: map[string]bool{}}
	for _, key := range seed.keys {
		draft.values.set(key, sectionState{Value: seed.values[key].Value})
	}
	return draft
}

// Get returns an owned copy of the section's value, or nil when unset.
func (draft *SystemSectionDraft) Get(section *SystemSection) JsonValue {
	state, ok := draft.values.get(section.Key)
	if !ok {
		return nil
	}
	return cloneJSON(state.Value)
}

// Set sets a section; an existing key keeps its position.
func (draft *SystemSectionDraft) Set(section *SystemSection, value JsonValue) {
	draft.values.set(section.Key, sectionState{Value: mustStored(value)})
	draft.touched[section.Key] = true
}

// Delete removes a section.
func (draft *SystemSectionDraft) Delete(key string) {
	draft.values.remove(key)
	delete(draft.wrappers, key)
	draft.touched[key] = true
}

// Wrap transforms a section's rendered text for this preparation.
func (draft *SystemSectionDraft) Wrap(section *SystemSection, transform func(rendered string) string) {
	draft.wrappers[section.Key] = append(draft.wrappers[section.Key], transform)
	draft.touched[section.Key] = true
}

type draftSnapshot struct {
	values   *canonical
	wrappers map[string][]func(string) string
	touched  map[string]bool
}

func (draft *SystemSectionDraft) snapshot() draftSnapshot {
	values := newCanonical()
	for _, key := range draft.values.keys {
		values.set(key, draft.values.values[key])
	}
	wrappers := map[string][]func(string) string{}
	for key, list := range draft.wrappers {
		wrappers[key] = slices.Clone(list)
	}
	touched := map[string]bool{}
	for key := range draft.touched {
		touched[key] = true
	}
	return draftSnapshot{values: values, wrappers: wrappers, touched: touched}
}

func (draft *SystemSectionDraft) restore(snapshot draftSnapshot) {
	draft.values = snapshot.values
	draft.wrappers = snapshot.wrappers
	draft.touched = snapshot.touched
}

// freeze renders touched sections (wrappers after rendering, in registration
// order); untouched sections keep their stored text.
func freeze(draft *SystemSectionDraft, current *canonical, registry map[string]*SystemSection) *canonical {
	desired := newCanonical()
	for _, key := range draft.values.keys {
		value := draft.values.values[key].Value
		if !draft.touched[key] {
			if previous, ok := current.get(key); ok {
				desired.set(key, previous)
			}
			continue
		}
		definition, ok := registry[key]
		if !ok {
			continue
		}
		rendered := definition.Render(value)
		for _, wrap := range draft.wrappers[key] {
			rendered = wrap(rendered)
		}
		desired.set(key, sectionState{Value: value, Rendered: rendered})
	}
	return desired
}

// SectionRegistry is a live view of registered sections.
type SectionRegistry struct {
	Map      func() map[string]*SystemSection
	Revision func() int
}

// ToolRegistry is a live view of registered tools.
type ToolRegistry struct {
	Map      func() map[string]*ToolDeclaration
	Revision func() int
}

// PreparationSettings are the settings a preparation snapshot captures.
type PreparationSettings struct {
	Model         JsonValue `json:"model,omitempty"`
	ThinkingLevel JsonValue `json:"thinkingLevel"`
	SelectedTools []string  `json:"selectedTools"`
	Profile       JsonValue `json:"profile"`
}

// PreparationSnapshot is snapshot S, compared before the prepared commit.
type PreparationSnapshot struct {
	NewestManaged  *Id                 `json:"newestManaged"`
	NewestBaseline *Id                 `json:"newestBaseline"`
	NewestHead     *Id                 `json:"newestHead"`
	Settings       PreparationSettings `json:"settings"`
	SectionsRev    int                 `json:"sectionsRev"`
	ToolsRev       int                 `json:"toolsRev"`
}

func sameSnapshot(left, right PreparationSnapshot) bool {
	return jsonEqual(mustStored(left), mustStored(right))
}

type takenSnapshot struct {
	snapshot  PreparationSnapshot
	canonical *canonical
	seed      []SectionSeed
}

func takeSnapshot(tx *Tx, conversationId Id, sections SectionRegistry, tools ToolRegistry) (takenSnapshot, error) {
	folded, newestManaged, newestBaseline, err := foldCanonical(tx, conversationId)
	if err != nil {
		return takenSnapshot{}, err
	}
	head, err := tx.NewestEntry(conversationId, NewestOptions{WithHead: true})
	if err != nil {
		return takenSnapshot{}, err
	}
	rewindable, err := tx.Snapshot(RewindableDoc(conversationId))
	if err != nil {
		return takenSnapshot{}, err
	}
	conversation, err := tx.Conversation(conversationId)
	if err != nil {
		return takenSnapshot{}, err
	}
	settings := PreparationSettings{
		Model:         rewindable["model"],
		ThinkingLevel: rewindable["thinkingLevel"],
		SelectedTools: stringList(rewindable["selectedTools"]),
		Profile:       rewindable["profile"],
	}
	taken := takenSnapshot{
		snapshot:  PreparationSnapshot{NewestManaged: newestManaged, NewestBaseline: newestBaseline, Settings: settings, SectionsRev: sections.Revision(), ToolsRev: tools.Revision()},
		canonical: folded,
	}
	if head != nil {
		id := head.Id
		taken.snapshot.NewestHead = &id
	}
	if newestManaged == nil && conversation != nil {
		taken.seed = conversation.Sections
	}
	return taken, nil
}

func stringList(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// SystemInstructionsInput is what a systemInstructions handler receives.
type SystemInstructionsInput struct {
	Sections *SystemSectionDraft
	Config   PreparationSettings
	Tools    []*ToolDeclaration
}

// SystemInstructionsResult optionally overrides the tool loadout; the last
// override wins.
type SystemInstructionsResult struct {
	Tools []*ToolDeclaration
}

type preparedDraft struct {
	desired *canonical
	tools   []*ToolDeclaration
}

// prepareDraft seeds the draft, runs handlers off the line, and freezes it.
// A failing handler's edits are rolled back.
func prepareDraft(ctx context.Context, rt *Runtime, current *canonical, seed []SectionSeed, settings PreparationSettings, report func(string)) (preparedDraft, error) {
	draft := newDraft(current)
	for _, item := range seed {
		if item.Remove {
			draft.Delete(item.Key)
			continue
		}
		draft.Set(&SystemSection{Key: item.Key}, item.Value)
	}
	registered := rt.Registries.Tools.Map()
	var defaultTools []*ToolDeclaration
	for _, name := range settings.SelectedTools {
		if tool, ok := registered[name]; ok {
			defaultTools = append(defaultTools, tool)
			continue
		}
		report(fmt.Sprintf("selected tool %s is not registered", name))
	}
	tools := defaultTools
	for _, binding := range rt.Hooks.Handlers() {
		hooks := generationHooksOf(binding.Handlers)
		if hooks == nil || hooks.SystemInstructions == nil {
			continue
		}
		before := draft.snapshot()
		result, err := callSystemInstructions(ctx, hooks, SystemInstructionsInput{Sections: draft, Config: settings, Tools: defaultTools}, binding.Api)
		if err != nil {
			if ctx.Err() != nil {
				return preparedDraft{}, err
			}
			draft.restore(before)
			report(err.Error())
			continue
		}
		if result != nil && result.Tools != nil {
			tools = result.Tools
		}
	}
	return preparedDraft{desired: freeze(draft, current, rt.Registries.Sections.Map()), tools: tools}, nil
}

func callSystemInstructions(ctx context.Context, hooks *GenerationHooks, input SystemInstructionsInput, api HookApi) (result *SystemInstructionsResult, err error) {
	defer recoverInto(&err)
	return hooks.SystemInstructions(ctx, input, api)
}

// managedPlan is the planned pi.system entry.
type managedPlan struct {
	data  JsonObject
	model []JsonObject
	edits []ContextEdit
}

func toolOf(tool *ToolDeclaration) JsonObject {
	return JsonObject{"name": tool.Name, "description": tool.Description, "parameters": cloneJSON(mustStored(tool.Parameters))}
}

// planManagedEntry diffs desired against canonical on the line; it writes a
// baseline when there is no managed history or a head intervened.
func planManagedEntry(tx *Tx, conversationId Id, snapshot PreparationSnapshot, current, desired *canonical, tools []*ToolDeclaration, now float64) (*managedPlan, error) {
	needBaseline := snapshot.NewestManaged == nil ||
		(snapshot.NewestHead != nil && (snapshot.NewestBaseline == nil || *snapshot.NewestHead > *snapshot.NewestBaseline))
	view, err := tx.Context(conversationId, nil)
	if err != nil {
		return nil, err
	}
	previous := effectiveToolMap(view.Messages)
	if needBaseline {
		return baselinePlan(snapshot, desired, tools, view.Entries, now), nil
	}
	changed := changedSections(current, desired)
	var added []*ToolDeclaration
	for _, tool := range tools {
		if _, ok := previous.values[tool.Name]; !ok {
			added = append(added, tool)
		}
	}
	var removed []any
	for _, name := range previous.keys {
		if !slices.ContainsFunc(tools, func(tool *ToolDeclaration) bool { return tool.Name == name }) {
			removed = append(removed, previous.values[name])
		}
	}
	if len(changed) == 0 && len(added) == 0 && len(removed) == 0 {
		return nil, nil
	}
	return deltaPlan(current, changed, added, removed, now), nil
}

func baselinePlan(snapshot PreparationSnapshot, desired *canonical, tools []*ToolDeclaration, entries []Entry, now float64) *managedPlan {
	sections := make([]any, 0, len(desired.keys))
	parts := make([]string, 0, len(desired.keys))
	for _, key := range desired.keys {
		state := desired.values[key]
		sections = append(sections, JsonObject{"key": key, "action": "set", "value": cloneJSON(state.Value), "rendered": state.Rendered})
		parts = append(parts, fmt.Sprintf("## %s\n%s", key, state.Rendered))
	}
	var newestHead Id
	if snapshot.NewestHead != nil {
		newestHead = *snapshot.NewestHead
	}
	var edits []ContextEdit
	for _, entry := range entries {
		if entry.Kind == "pi.system" && entry.Id > newestHead {
			edits = append(edits, ContextEdit{Target: entry.Id, Action: "omit"})
		}
	}
	added := make([]any, len(tools))
	for index, tool := range tools {
		added[index] = toolOf(tool)
	}
	message := JsonObject{"role": "system", "content": strings.Join(parts, "\n\n"), "toolsAdded": added, "timestamp": now}
	return &managedPlan{data: JsonObject{"baseline": true, "sections": sections}, model: []JsonObject{message}, edits: edits}
}

func changedSections(current, desired *canonical) []JsonObject {
	var changed []JsonObject
	for _, key := range desired.keys {
		state := desired.values[key]
		previous, ok := current.get(key)
		if !ok || previous.Rendered != state.Rendered || !jsonEqual(previous.Value, state.Value) {
			changed = append(changed, JsonObject{"key": key, "action": "set", "value": cloneJSON(state.Value), "rendered": state.Rendered})
		}
	}
	for _, key := range current.keys {
		if _, ok := desired.get(key); !ok {
			changed = append(changed, JsonObject{"key": key, "action": "remove"})
		}
	}
	return changed
}

func deltaPlan(current *canonical, changed []JsonObject, added []*ToolDeclaration, removed []any, now float64) *managedPlan {
	records := make([]any, len(changed))
	renderChanged := false
	parts := make([]string, 0, len(changed))
	for index, record := range changed {
		records[index] = record
		key := str(record, "key")
		if str(record, "action") == "remove" {
			renderChanged = true
			parts = append(parts, fmt.Sprintf("The %s section no longer applies.", key))
			continue
		}
		if previous, ok := current.get(key); !ok || previous.Rendered != str(record, "rendered") {
			renderChanged = true
		}
		parts = append(parts, fmt.Sprintf("The %s section now reads:\n%s", key, str(record, "rendered")))
	}
	data := JsonObject{"sections": records}
	if !renderChanged && len(added) == 0 && len(removed) == 0 {
		return &managedPlan{data: data, model: []JsonObject{}}
	}
	message := JsonObject{"role": "system", "content": strings.Join(parts, "\n\n"), "timestamp": now}
	if len(added) > 0 {
		list := make([]any, len(added))
		for index, tool := range added {
			list[index] = toolOf(tool)
		}
		message["toolsAdded"] = list
	}
	if len(removed) > 0 {
		message["toolsRemoved"] = removed
	}
	return &managedPlan{data: data, model: []JsonObject{message}}
}

// orderedTools is an insertion-ordered tool map folded from SystemMessages.
type orderedTools struct {
	keys   []string
	values map[string]any
}

func effectiveToolMap(messages []JsonObject) orderedTools {
	tools := orderedTools{values: map[string]any{}}
	for _, message := range messages {
		if str(message, "role") != "system" {
			continue
		}
		for _, item := range arr(message, "toolsRemoved") {
			name := str(asObject(item), "name")
			if _, ok := tools.values[name]; ok {
				delete(tools.values, name)
				tools.keys = slices.DeleteFunc(tools.keys, func(key string) bool { return key == name })
			}
		}
		for _, item := range arr(message, "toolsAdded") {
			name := str(asObject(item), "name")
			if _, ok := tools.values[name]; !ok {
				tools.keys = append(tools.keys, name)
			}
			tools.values[name] = item
		}
	}
	return tools
}

func asObject(value any) JsonObject {
	object, _ := value.(map[string]any)
	return object
}

// EffectiveTools folds toolsAdded/toolsRemoved across a request's
// SystemMessages.
func EffectiveTools(messages []JsonObject) []JsonObject {
	tools := effectiveToolMap(messages)
	out := make([]JsonObject, 0, len(tools.keys))
	for _, key := range tools.keys {
		out = append(out, asObject(tools.values[key]))
	}
	return out
}
