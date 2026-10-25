// Ports packages/durable/src/harness/agent.ts.

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// DefaultRetryPolicy is the built-in durable generation retry policy (agent.ts:18-23).
var DefaultRetryPolicy = durable.ConversationRetryPolicy{
	Enabled:         true,
	MaxRetries:      3,
	BaseDelayMs:     2000,
	MaxAgentDelayMs: new(60000),
}

// DefaultCompactionPolicy is the built-in compaction policy (agent.ts:25-30).
var DefaultCompactionPolicy = durable.CompactionPolicy{
	Enabled:          true,
	ReserveTokens:    16384,
	KeepRecentTokens: 20000,
	BackgroundTokens: 32768,
}

// DefaultProgressPolicy is the built-in progress policy.
var DefaultProgressPolicy = durable.ProgressPolicy{
	PartialIntervalMs: 100,
	OutputIntervalMs:  100,
}

// InstructionsKey is the reserved section key of the agent's instructions.
const InstructionsKey = "instructions"

// ExtensionSelection is the stored extensions field of AgentState: an exact list (Exact), or an edit of the host default selection with optional Add and Remove lists. A nil Add or Remove is absent.
type ExtensionSelection struct {
	Exact  bool
	Names  []string
	Add    []string
	Remove []string
}

// MarshalJSON writes an exact selection as an array and an edit as {add?, remove?}.
func (selection ExtensionSelection) MarshalJSON() ([]byte, error) {
	if selection.Exact {
		return json.Marshal(nonNilStrings(selection.Names))
	}
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	if selection.Add != nil {
		if err := writeMember(&buffer, "add", selection.Add, false); err != nil {
			return nil, err
		}
	}
	if selection.Remove != nil {
		if err := writeMember(&buffer, "remove", selection.Remove, selection.Add != nil); err != nil {
			return nil, err
		}
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// UnmarshalJSON reads an array as an exact selection and an object as an edit.
func (selection *ExtensionSelection) UnmarshalJSON(data []byte) error {
	*selection = ExtensionSelection{}
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '[' {
		selection.Exact = true
		if err := json.Unmarshal(trimmed, &selection.Names); err != nil {
			return err
		}
		selection.Names = nonNilStrings(selection.Names)
		return nil
	}
	var edit struct {
		Add    *[]string `json:"add"`
		Remove *[]string `json:"remove"`
	}
	if err := json.Unmarshal(data, &edit); err != nil {
		return err
	}
	if edit.Add != nil {
		selection.Add = nonNilStrings(*edit.Add)
	}
	if edit.Remove != nil {
		selection.Remove = nonNilStrings(*edit.Remove)
	}
	return nil
}

// ToolSelection is the stored tools field of AgentState: an exact list (Exact), or a filter with an optional Remove list
// and, for modelTools, an optional Add list of the deferred tools loaded so far. A nil Remove or Add is absent.
type ToolSelection struct {
	Exact  bool
	Names  []string
	Remove []string
	Add    []string
}

// MarshalJSON writes an exact selection as an array and a filter as {remove?, add?}.
func (selection ToolSelection) MarshalJSON() ([]byte, error) {
	if selection.Exact {
		return json.Marshal(nonNilStrings(selection.Names))
	}
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	if selection.Remove != nil {
		if err := writeMember(&buffer, "remove", selection.Remove, false); err != nil {
			return nil, err
		}
	}
	if selection.Add != nil {
		if err := writeMember(&buffer, "add", selection.Add, selection.Remove != nil); err != nil {
			return nil, err
		}
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// UnmarshalJSON reads an array as an exact selection and an object as {remove?, add?}.
func (selection *ToolSelection) UnmarshalJSON(data []byte) error {
	*selection = ToolSelection{}
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '[' {
		selection.Exact = true
		if err := json.Unmarshal(trimmed, &selection.Names); err != nil {
			return err
		}
		selection.Names = nonNilStrings(selection.Names)
		return nil
	}
	var filter struct {
		Remove *[]string `json:"remove"`
		Add    *[]string `json:"add"`
	}
	if err := json.Unmarshal(data, &filter); err != nil {
		return err
	}
	if filter.Remove != nil {
		selection.Remove = nonNilStrings(*filter.Remove)
	}
	if filter.Add != nil {
		selection.Add = nonNilStrings(*filter.Add)
	}
	return nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func writeMember(buffer *bytes.Buffer, name string, value any, comma bool) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if comma {
		buffer.WriteByte(',')
	}
	buffer.WriteString(`"` + name + `":`)
	buffer.Write(encoded)
	return nil
}

// AgentState holds the stored choices of one conversation; names, not objects. Unset fields follow the host.
type AgentState struct {
	Model         *durable.ModelRef     `json:"model,omitempty"`
	ThinkingLevel ai.ModelThinkingLevel `json:"thinkingLevel,omitempty"`
	// Extensions: an exact list selects exactly these extensions, in order; an edit changes the host default selection.
	Extensions *ExtensionSelection `json:"extensions,omitempty"`
	// Tools filters the selected extensions' tools. An exact list offers exactly these, in order.
	Tools *ToolSelection `json:"tools,omitempty"`
	// ModelTools filters the enabled tools the model may call (Callers includes the model): the tools offered to it. It never
	// widens: other tools can still call the ones it leaves out.
	//   - Unset: every such tool except deferred ones.
	//   - An exact list: exactly these, in order, deferred ones too.
	//   - A filter: every such tool except deferred ones and those in Remove, in registry order, followed by the deferred tools
	//     in Add that are not in Remove, in Add order. Names of tools that are not deferred or not registered have no effect in Add.
	ModelTools *ToolSelection `json:"modelTools,omitempty"`
	// Instructions render after every extension section, as the section instructions.
	Instructions *string `json:"instructions,omitempty"`
	// Cwd is a directory within the environment's file system, passed to HarnessOptions.Env.
	Cwd *string `json:"cwd,omitempty"`
}

// AgentDoc is the built-in agent document; rewindable so forks start from the agent at their fork entry.
var AgentDoc = durable.DefineDoc(durable.DocDefinition[AgentState]{
	CommonDocDefinition: durable.CommonDocDefinition[AgentState]{
		Kind:              "pi.agent",
		Version:           1,
		CheckpointWhenRaw: func(durable.JsonObject, []durable.Op, durable.CheckpointInfo) bool { return true },

		Initial: func() AgentState { return AgentState{} },
	},
	DocumentSemantics: durable.NewRewindableConversationSemantics(durable.ForkAsOf).DocumentSemantics,
})

// Change is one field of AgentChange: unset leaves the stored field, Cleared removes it, and SetTo replaces it.
type Change[T any] struct {
	state changeState
	value T
}

type changeState uint8

const (
	changeKeep changeState = iota
	changeClear
	changeSet
)

// SetTo returns a change that replaces the stored field with value.
func SetTo[T any](value T) Change[T] { return Change[T]{state: changeSet, value: value} }

// Cleared returns a change that removes the stored field (upstream null).
func Cleared[T any]() Change[T] { return Change[T]{state: changeClear} }

// ExtensionChange is Pi's `Extension[] | { add?, remove? }`: List is the array form, a non-nil list (empty included) that replaces the selection, and Edit is the object form that edits the host default selection. Go has no sum of a list and an object, so exactly one of the two fields is set; neither set is the empty object `{}`.
type ExtensionChange struct {
	List []*durable.Extension
	Edit *ExtensionEdit
}

// ExtensionEdit is the `{ add?, remove? }` form of an extension change; a nil Add or Remove is absent.
type ExtensionEdit struct {
	Add    []*durable.Extension
	Remove []*durable.Extension
}

// ToolChange is Pi's `ToolRegistration[] | { remove, add }`: List is the array form, a non-nil list (empty included) that replaces the filter, and Edit is the `{ remove, add }` form; neither set is `{ remove: [] }`.
type ToolChange struct {
	List []*durable.ToolRegistration
	Edit *ToolEdit
}

// ToolEdit is the `{ remove, add }` form of a tool change: a nil list is absent, and the tools filter takes Remove only.
type ToolEdit struct {
	Remove []*durable.ToolRegistration
	Add    []*durable.ToolRegistration
}

// AgentChange is a change to pi.agent: a set field replaces the stored one, a cleared field removes it, and an unset field changes nothing.
type AgentChange struct {
	Model         Change[durable.ModelRef]
	ThinkingLevel Change[ai.ModelThinkingLevel]
	Extensions    Change[ExtensionChange]
	Tools         Change[ToolChange]
	ModelTools    Change[ToolChange]
	Instructions  Change[string]
	Cwd           Change[string]
}

// HarnessSettings is the Harness-wide run policy. It is read at every resolution and never copied.
type HarnessSettings struct {
	// Extensions is the default extension selection; nil selects every installed extension, in install order.
	Extensions []*durable.Extension
	Stream     *durable.ConversationStreamOptions
	Retry      *RetryPolicyPatch
	Compaction *CompactionPolicyPatch
	Progress   *ProgressPolicyPatch
	// ToolExecution, SteeringMode, and FollowUpMode are empty when unset.
	ToolExecution durable.ToolExecutionMode
	SteeringMode  durable.QueueMode
	FollowUpMode  durable.QueueMode
	// ContextRetentionMs is how long an idle conversation keeps its last context read in memory, so its next run reads
	// only newer entries. Busy conversations always keep it; 0 drops it once the conversation is idle. Nil selects the
	// default, ten minutes.
	ContextRetentionMs *float64
	// DocumentRetentionMs is how long a loaded document that no watch observes stays in memory after
	// its last read or write; the next access loads it from Storage. Observed documents always stay. Nil selects the
	// default, ten minutes; 0 drops one right after use; +Inf keeps them. Anything else that is not a number of
	// milliseconds, 0 or more, means the default.
	DocumentRetentionMs *float64
	// AfterFailedRun is what a run that fails (model_error, no_model) does with the inputs queued behind it: keep leaves
	// them queued until the next input or queued write, continue starts the next run with them, as an answer would, and
	// withdraw settles them unanswered with reason run_failed. Empty selects keep.
	AfterFailedRun durable.AfterFailedRun
	// RestartLimit is how many times in a row a task may be cut off by a crash, eviction, or reset (a close does not
	// count) without changing its state before it is parked as blocked (restart_limit) instead of run again;
	// Harness.RetryTask runs it again. Nil selects the default, 5; +Inf never parks.
	RestartLimit *float64
}

// defaultContextRetentionMs is the default of HarnessSettings.ContextRetentionMs: ten minutes.
const defaultContextRetentionMs = 600_000

// RetryPolicyPatch is Partial<ConversationRetryPolicy>; nil fields keep the defaults.
type RetryPolicyPatch struct {
	Enabled         *bool
	MaxRetries      *int
	BaseDelayMs     *int
	MaxAgentDelayMs *int
}

// CompactionPolicyPatch is CompactionSettings: Partial<CompactionPolicy>, nil fields keep the defaults, plus ModelOverrides.
type CompactionPolicyPatch struct {
	Enabled          *bool
	ReserveTokens    *int
	KeepRecentTokens *int
	BackgroundTokens *int
	// ModelOverrides is keyed by exact "provider/modelId"; each field an entry sets replaces the Harness-wide one for that
	// model, so small- and large-window models can share a Harness (types.ts CompactionSettings, Pi c4cf7957).
	ModelOverrides map[string]durable.CompactionPolicyOverride
}

// ProgressPolicyPatch is Partial<ProgressPolicy>; nil fields keep the defaults.
type ProgressPolicyPatch struct {
	PartialIntervalMs *float64
	OutputIntervalMs  *float64
}

// ResolveSettings resolves host settings: every field over its built-in default, object fields merged.
func ResolveSettings(settings *HarnessSettings) durable.Settings {
	retry := DefaultRetryPolicy
	// Pi spreads the default, so a resolved policy never shares storage with it.
	retry.MaxAgentDelayMs = new(*DefaultRetryPolicy.MaxAgentDelayMs)
	resolved := durable.Settings{
		Retry:         retry,
		Compaction:    DefaultCompactionPolicy,
		Progress:      DefaultProgressPolicy,
		ToolExecution: durable.ToolExecutionParallel,
		SteeringMode:  durable.QueueOneAtATime,
		FollowUpMode:  durable.QueueOneAtATime,

		ContextRetentionMs: defaultContextRetentionMs,
	}
	resolved.DocumentRetentionMs = session.DocumentRetention(nil)
	resolved.AfterFailedRun = durable.AfterFailedRunKeep
	resolved.RestartLimit = durable.DefaultRestartLimit
	// The spread copies the default's values; the pointer field must not alias the shared default.
	resolved.Retry.MaxAgentDelayMs = new(*DefaultRetryPolicy.MaxAgentDelayMs)
	if settings == nil {
		return resolved
	}
	resolved.Extensions = settings.Extensions
	if settings.Stream != nil {
		resolved.Stream = *settings.Stream
	}
	if retry := settings.Retry; retry != nil {
		assign(&resolved.Retry.Enabled, retry.Enabled)
		assign(&resolved.Retry.MaxRetries, retry.MaxRetries)
		assign(&resolved.Retry.BaseDelayMs, retry.BaseDelayMs)
		if retry.MaxAgentDelayMs != nil {
			resolved.Retry.MaxAgentDelayMs = new(*retry.MaxAgentDelayMs)
		}
	}
	if compaction := settings.Compaction; compaction != nil {
		assign(&resolved.Compaction.Enabled, compaction.Enabled)
		assign(&resolved.Compaction.ReserveTokens, compaction.ReserveTokens)
		assign(&resolved.Compaction.KeepRecentTokens, compaction.KeepRecentTokens)
		assign(&resolved.Compaction.BackgroundTokens, compaction.BackgroundTokens)
		resolved.Compaction.ModelOverrides = compaction.ModelOverrides
	}
	if progress := settings.Progress; progress != nil {
		assign(&resolved.Progress.PartialIntervalMs, progress.PartialIntervalMs)
		assign(&resolved.Progress.OutputIntervalMs, progress.OutputIntervalMs)
	}
	assign(&resolved.ContextRetentionMs, settings.ContextRetentionMs)
	resolved.DocumentRetentionMs = session.DocumentRetention(settings.DocumentRetentionMs)
	if settings.AfterFailedRun != "" {
		resolved.AfterFailedRun = settings.AfterFailedRun
	}
	resolved.RestartLimit = restartLimit(settings.RestartLimit)
	if settings.ToolExecution != "" {
		resolved.ToolExecution = settings.ToolExecution
	}
	if settings.SteeringMode != "" {
		resolved.SteeringMode = settings.SteeringMode
	}
	if settings.FollowUpMode != "" {
		resolved.FollowUpMode = settings.FollowUpMode
	}
	return resolved
}

// restartLimit is a restart limit as given when it is a number of restarts, 1 or more, rounded down (+Inf never parks);
// else the default (agent.ts restartLimit).
func restartLimit(limit *float64) float64 {
	if limit != nil && *limit >= 1 {
		return math.Floor(*limit)
	}
	return durable.DefaultRestartLimit
}

func assign[T any](target *T, value *T) {
	if value != nil {
		*target = *value
	}
}

// Configure applies one change to pi.agent: a set field replaces the stored one, a cleared field removes it, and an unset field changes nothing.
func Configure(tx durable.Tx, conversationId durable.ConversationId, change AgentChange) error {
	state, err := durable.TxDoc[AgentState](tx, AgentDoc, conversationId)
	if err != nil {
		return err
	}
	return applyChange(state, change)
}

// AddTools applies the addTools control of a tool round to tools and modelTools, so the next request offers the tools: an
// exact list gets each name it lacks appended, and {remove} loses the names. An unset tools already enables every tool, so
// nothing is written for it. An unset or filter modelTools also gets each name appended to its add, which loads deferred
// tools and has no effect for others. By name, without the registry: a tool that is not registered now, such as one of an
// MCP server that is reconnecting, is offered once it is again.
//
// Ports packages/durable/src/harness/agent.ts (addTools).
func AddTools(tx durable.Tx, conversationId durable.ConversationId, added []string) error {
	state, err := durable.TxDoc[AgentState](tx, AgentDoc, conversationId)
	if err != nil {
		return err
	}
	if err := addToField(state, "tools", added); err != nil {
		return err
	}
	if state.Array("modelTools") != nil {
		return addToField(state, "modelTools", added)
	}
	// Assigned whole; a document write of an equal value records no change.
	var remove, add []any
	if filter := state.Object("modelTools"); filter != nil {
		// A stored container reads back as a draft handle, not as a decoded slice.
		if stored := filter.Array("remove"); stored != nil {
			for _, name := range stored.Snapshot() {
				if text, ok := name.(string); !ok || !slices.Contains(added, text) {
					remove = append(remove, name)
				}
			}
		}
		if stored := filter.Array("add"); stored != nil {
			add = stored.Snapshot()
		}
	}
	for _, name := range added {
		if !slices.Contains(add, any(name)) {
			add = append(add, name)
		}
	}
	if add == nil {
		add = []any{}
	}
	next := delta.NewJsonObject(2)
	if len(remove) > 0 {
		next.Set("remove", remove)
	}
	next.Set("add", add)
	return state.Set("modelTools", next)
}

func addToField(state durable.Draft[AgentState], field string, added []string) error {
	if tools := state.Array(field); tools != nil {
		for _, name := range added {
			if !slices.Contains(tools.Snapshot(), any(name)) {
				if _, err := tools.Push(name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	filter := state.Object(field)
	if filter == nil {
		return nil
	}
	// A stored container reads back as a draft handle, not as a decoded slice.
	var remove []any
	if stored := filter.Array("remove"); stored != nil {
		remove = stored.Snapshot()
	}
	isAdded := func(name any) bool {
		text, ok := name.(string)
		return ok && slices.Contains(added, text)
	}
	if !slices.ContainsFunc(remove, isAdded) {
		return nil
	}
	remaining := []any{}
	for _, name := range remove {
		if !isAdded(name) {
			remaining = append(remaining, name)
		}
	}
	return state.Set(field, delta.JsonObjectOf("remove", remaining))
}

func applyChange(state durable.Draft[AgentState], change AgentChange) error {
	var model any
	if change.Model.state == changeSet {
		model = delta.JsonObjectOf("provider", change.Model.value.Provider, "modelId", change.Model.value.ModelId)
	}
	if err := setField(state, "model", change.Model.state, model); err != nil {
		return err
	}
	if err := setField(state, "thinkingLevel", change.ThinkingLevel.state, string(change.ThinkingLevel.value)); err != nil {
		return err
	}
	var extensions any
	if change.Extensions.state == changeSet {
		selection := change.Extensions.value
		if selection.List != nil {
			extensions = namesJSON(extensionNames(selection.List))
		} else {
			edit := delta.NewJsonObject(2)
			if selection.Edit != nil {
				if selection.Edit.Add != nil {
					edit.Set("add", namesJSON(extensionNames(selection.Edit.Add)))
				}
				if selection.Edit.Remove != nil {
					edit.Set("remove", namesJSON(extensionNames(selection.Edit.Remove)))
				}
			}
			extensions = edit
		}
	}
	if err := setField(state, "extensions", change.Extensions.state, extensions); err != nil {
		return err
	}
	for _, field := range []struct {
		key    string
		change Change[ToolChange]
	}{{"tools", change.Tools}, {"modelTools", change.ModelTools}} {
		var filter any
		if field.change.state == changeSet {
			if field.change.value.List != nil {
				filter = namesJSON(toolNames(field.change.value.List))
			} else {
				edit := field.change.value.Edit
				if edit == nil {
					edit = &ToolEdit{Remove: []*durable.ToolRegistration{}}
				}
				if field.key == "tools" {
					filter = delta.JsonObjectOf("remove", namesJSON(toolNames(edit.Remove)))
				} else {
					object := delta.NewJsonObject(2)
					if edit.Remove != nil {
						object.Set("remove", namesJSON(toolNames(edit.Remove)))
					}
					if edit.Add != nil {
						object.Set("add", namesJSON(toolNames(edit.Add)))
					}
					filter = object
				}
			}
		}
		if err := setField(state, field.key, field.change.state, filter); err != nil {
			return err
		}
	}
	if err := setField(state, "instructions", change.Instructions.state, change.Instructions.value); err != nil {
		return err
	}
	return setField(state, "cwd", change.Cwd.state, change.Cwd.value)
}

// setField is applyChange's set (agent.ts:89-93): a set value replaces the stored field, a cleared one deletes it.
func setField(state durable.Draft[AgentState], key string, change changeState, value any) error {
	switch change {
	case changeClear:
		state.Delete(key)
	case changeSet:
		return state.Set(key, value)
	}
	return nil
}

func namesJSON(names []string) []any {
	out := make([]any, 0, len(names))
	for _, name := range names {
		out = append(out, name)
	}
	return out
}

func extensionNames(extensions []*durable.Extension) []string {
	names := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		names = append(names, extension.Name)
	}
	return names
}

func toolNames(tools []*durable.ToolRegistration) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

// CreateAgent is the built-in part of every Harness commit that creates or forks a conversation, for pi.agent: a fork keeps its asOf copy; a new task-owned conversation copies the stored agent of its owner task's conversation; a new ownerless one starts empty.
func CreateAgent(tx durable.Tx, conversation durable.ConversationRecord) error {
	if conversation.Parent != nil {
		return nil
	}
	agent, err := durable.TxDoc[AgentState](tx, AgentDoc, conversation.Id)
	if err != nil {
		return err
	}
	if conversation.Owner == nil {
		return nil
	}
	owner, err := durable.TxDoc[AgentState](tx, AgentDoc, conversation.Owner.ConversationId)
	if err != nil {
		return err
	}
	copied, err := durable.CopyJson(owner.Snapshot())
	if err != nil {
		return err
	}
	// Object.assign(agent, copy): each owner field is one assignment, in the owner's key order.
	values := copied.(*delta.JsonObject)
	for _, key := range owner.Keys() {
		if err := agent.Set(key, values.Value(key)); err != nil {
			return err
		}
	}
	return nil
}

// HookHandlers is the handlers of one extension's hook, with the extension's name.
type HookHandlers struct {
	Extension string
	Handlers  any
}

// AgentHooks returns the handlers of the selected extensions' hooks for a task name, in extension order, each with the
// name of its extension.
func AgentHooks(agent durable.Agent, taskName string) []HookHandlers {
	handlers := []HookHandlers{}
	for _, extension := range agent.Extensions {
		for _, hook := range extension.Hooks {
			if hook.Task == taskName {
				handlers = append(handlers, HookHandlers{Extension: extension.Name, Handlers: hook.Handlers})
			}
		}
	}
	return handlers
}

// SectionExtension is the extension whose code a section of agent runs: the last selected one that provides or wraps it;
// empty when there is none.
func SectionExtension(agent durable.Agent, key string) string {
	found := ""
	for _, extension := range agent.Extensions {
		provides := slices.ContainsFunc(extension.Sections, func(section *durable.PromptSection) bool { return section.Key == key })
		wraps := slices.ContainsFunc(extension.Wraps, func(wrap durable.Wrap) bool {
			section, ok := wrap.(durable.SectionWrap)
			return ok && section.Section == key
		})
		if provides || wraps {
			found = extension.Name
		}
	}
	return found
}

// ToolExtension is the extension whose code a tool of agent runs: the last selected one that provides or wraps it; empty
// when there is none.
func ToolExtension(agent durable.Agent, name string) string {
	found := ""
	for _, extension := range agent.Extensions {
		provides := slices.ContainsFunc(extension.Tools, func(tool *durable.ToolRegistration) bool { return tool.Name == name })
		wraps := slices.ContainsFunc(extension.Wraps, func(wrap durable.Wrap) bool {
			tool, ok := wrap.(durable.ToolWrap)
			return ok && tool.Tool == name
		})
		if provides || wraps {
			found = extension.Name
		}
	}
	return found
}

// ResolveAgent resolves an agent from its stored state (nil: every field unset), a registry snapshot, and resolved settings. A wrapper that panics or renames drops its target and is reported; a wrapper without a target does nothing.
func ResolveAgent(state *AgentState, snapshot durable.RegistrySnapshot, settings durable.Settings, report func(err error, extension string)) durable.Agent {
	if state == nil {
		state = &AgentState{}
	}
	extensions := selectExtensions(state.Extensions, snapshot, settings)

	composed := NewOrderedMap[*durable.ToolRegistration]()
	for _, extension := range extensions {
		for _, tool := range extension.Tools {
			composed.Set(tool.Name, tool)
		}
	}
	sections := NewOrderedMap[*durable.PromptSection]()
	for _, extension := range extensions {
		for _, section := range extension.Sections {
			sections.Set(section.Key, section)
		}
	}
	for _, extension := range extensions {
		wrapReport := func(err error) { report(err, extension.Name) }
		for _, wrap := range extension.Wraps {
			switch wrap := wrap.(type) {
			case durable.ToolWrap:
				if wrap.Wrap != nil {
					applyWrap(composed, wrap.Tool, wrap.Wrap, func(tool *durable.ToolRegistration) string { return tool.Name }, wrapReport)
				}
			case durable.SectionWrap:
				if wrap.Wrap != nil {
					applyWrap(sections, wrap.Section, wrap.Wrap, func(section *durable.PromptSection) string { return section.Key }, wrapReport)
				}
			}
		}
	}

	enabled := filterTools(composed.Values(), state.Tools)
	modelCallable := slices.DeleteFunc(slices.Clone(enabled), func(tool *durable.ToolRegistration) bool { return !tool.CallableBy(durable.CallerModel) })
	tools := offeredTools(modelCallable, state.ModelTools)
	deferred := slices.DeleteFunc(slices.Clone(modelCallable), func(tool *durable.ToolRegistration) bool { return !tool.Deferred || slices.Contains(tools, tool) })
	callable := slices.DeleteFunc(slices.Clone(enabled), func(tool *durable.ToolRegistration) bool { return !tool.CallableBy(durable.CallerTools) })

	agentSections := sections.Values()
	if state.Instructions != nil {
		instructions := *state.Instructions
		agentSections = append(agentSections, &durable.PromptSection{
			Key: InstructionsKey,
			Render: func(_ context.Context, _ durable.PromptInput) (*string, error) {
				return new(instructions), nil
			},
		})
	}

	agent := durable.Agent{
		ThinkingLevel: ai.ThinkingOff,
		Extensions:    extensions,
		Tools:         tools,
		Callable:      callable,
		Deferred:      deferred,
		Sections:      agentSections,
	}
	if state.Model != nil {
		agent.Model = new(*state.Model)
	}
	if state.ThinkingLevel != "" {
		agent.ThinkingLevel = state.ThinkingLevel
	}
	if state.Instructions != nil {
		agent.Instructions = new(*state.Instructions)
	}
	if state.Cwd != nil {
		agent.Cwd = new(*state.Cwd)
	}
	return agent
}

// offeredTools is the tools offered to the model, as AgentState.ModelTools describes.
//
// Ports packages/durable/src/harness/agent.ts (offeredTools).
func offeredTools(tools []*durable.ToolRegistration, filter *ToolSelection) []*durable.ToolRegistration {
	if filter != nil && filter.Exact {
		return filterTools(tools, filter)
	}
	var remove, add []string
	if filter != nil {
		remove, add = filter.Remove, filter.Add
	}
	kept := []*durable.ToolRegistration{}
	for _, tool := range tools {
		if !tool.Deferred && !slices.Contains(remove, tool.Name) {
			kept = append(kept, tool)
		}
	}
	// Loaded deferred tools follow in load order, so loading one appends to the declared tools.
	seen := map[string]bool{}
	for _, name := range add {
		if seen[name] || slices.Contains(remove, name) {
			continue
		}
		seen[name] = true
		if index := slices.IndexFunc(tools, func(tool *durable.ToolRegistration) bool { return tool.Name == name }); index >= 0 && tools[index].Deferred {
			kept = append(kept, tools[index])
		}
	}
	return kept
}

// selectExtensions returns the selected installed extensions: the stored exact list, or the default selection edited by {add, remove}.
func selectExtensions(stored *ExtensionSelection, snapshot durable.RegistrySnapshot, settings durable.Settings) []*durable.Extension {
	var selected []string
	if stored != nil && stored.Exact {
		selected = stored.Names
	} else {
		var base []string
		if settings.Extensions != nil {
			base = extensionNames(settings.Extensions)
		} else {
			base = extensionNames(snapshot.Installed())
		}
		var add, remove []string
		if stored != nil {
			add, remove = stored.Add, stored.Remove
		}
		for _, name := range slices.Concat(base, add) {
			if !slices.Contains(remove, name) {
				selected = append(selected, name)
			}
		}
	}
	extensions := []*durable.Extension{}
	seen := map[string]bool{}
	for _, name := range selected {
		if seen[name] {
			continue
		}
		seen[name] = true
		if extension := snapshot.Extension(name); extension != nil {
			extensions = append(extensions, extension)
		}
	}
	return extensions
}

func applyWrap[T any](items *OrderedMap[T], target string, wrap func(T) T, nameOf func(T) string, report func(error)) {
	item, ok := items.Get(target)
	if !ok {
		return
	}
	wrapped, err := callWrapper(wrap, nameOf, item, target)
	if err != nil {
		items.Delete(target)
		report(err)
		return
	}
	items.Set(target, wrapped)
}

// callWrapper runs a wrapper and checks the wrapped name inside one recovered scope, as upstream's try covers both (agent.ts:240-246): a panic is the Go form of an upstream throw, including a nil result whose name cannot be read, and drops the target.
func callWrapper[T any](wrap func(T) T, nameOf func(T) string, item T, target string) (wrapped T, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = env.ToError(recovered)
		}
	}()
	wrapped = wrap(item)
	if name := nameOf(wrapped); name != target {
		return wrapped, fmt.Errorf("Wrapper renamed %s to %s", target, name)
	}
	return wrapped, nil
}

// filterTools is tools through a stored filter: an exact list selects exactly its names, in its order, a repeated name at
// its first position; {remove} drops names.
func filterTools(tools []*durable.ToolRegistration, filter *ToolSelection) []*durable.ToolRegistration {
	if filter == nil {
		return tools
	}
	selected := []*durable.ToolRegistration{}
	if filter.Exact {
		seen := map[string]bool{}
		for _, name := range filter.Names {
			if seen[name] {
				continue
			}
			seen[name] = true
			if index := slices.IndexFunc(tools, func(tool *durable.ToolRegistration) bool { return tool.Name == name }); index >= 0 {
				selected = append(selected, tools[index])
			}
		}
		return selected
	}
	for _, tool := range tools {
		if !slices.Contains(filter.Remove, tool.Name) {
			selected = append(selected, tool)
		}
	}
	return selected
}
