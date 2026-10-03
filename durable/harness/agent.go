// Ports packages/durable/src/harness/agent.ts.

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
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

// ToolSelection is the stored tools field of AgentState: an exact list (Exact), or {remove}.
type ToolSelection struct {
	Exact  bool
	Names  []string
	Remove []string
}

// MarshalJSON writes an exact selection as an array and a filter as {remove}.
func (selection ToolSelection) MarshalJSON() ([]byte, error) {
	if selection.Exact {
		return json.Marshal(nonNilStrings(selection.Names))
	}
	return json.Marshal(struct {
		Remove []string `json:"remove"`
	}{Remove: nonNilStrings(selection.Remove)})
}

// UnmarshalJSON reads an array as an exact selection and an object as {remove}.
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
		Remove []string `json:"remove"`
	}
	if err := json.Unmarshal(data, &filter); err != nil {
		return err
	}
	selection.Remove = nonNilStrings(filter.Remove)
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
	// Instructions render after every extension section, as the section instructions.
	Instructions *string `json:"instructions,omitempty"`
	// Cwd is a directory within the environment's file system, passed to HarnessOptions.Env.
	Cwd *string `json:"cwd,omitempty"`
}

// AgentDoc is the built-in agent document; rewindable so forks start from the agent at their fork entry.
var AgentDoc = durable.DefineDoc(durable.DocDefinition[AgentState]{
	CommonDocDefinition: durable.CommonDocDefinition[AgentState]{
		Kind:           "pi.agent",
		Version:        1,
		CheckpointWhen: func(AgentState, []durable.Op, durable.CheckpointInfo) bool { return true },
	},
	DocumentSemantics: durable.DocumentSemantics{
		Scope:   durable.ScopeConversation,
		History: durable.HistoryRewindable,
		Fork:    durable.ForkAsOf,
	},
	Initial: func() AgentState { return AgentState{} },
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

// ExtensionChange is a change to the stored extension selection: an exact list, or an edit with optional Add and Remove lists; a nil Add or Remove is absent.
type ExtensionChange struct {
	Exact  bool
	List   []*durable.Extension
	Add    []*durable.Extension
	Remove []*durable.Extension
}

// ToolChange is a change to the stored tool filter: an exact list, or {remove}.
type ToolChange struct {
	Exact  bool
	List   []*durable.ToolRegistration
	Remove []*durable.ToolRegistration
}

// AgentChange is a change to pi.agent: a set field replaces the stored one, a cleared field removes it, and an unset field changes nothing.
type AgentChange struct {
	Model         Change[durable.ModelRef]
	ThinkingLevel Change[ai.ModelThinkingLevel]
	Extensions    Change[ExtensionChange]
	Tools         Change[ToolChange]
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
	// ToolExecution, SteeringMode, and FollowUpMode are empty when unset.
	ToolExecution durable.ToolExecutionMode
	SteeringMode  durable.QueueMode
	FollowUpMode  durable.QueueMode
}

// RetryPolicyPatch is Partial<ConversationRetryPolicy>; nil fields keep the defaults.
type RetryPolicyPatch struct {
	Enabled         *bool
	MaxRetries      *int
	BaseDelayMs     *int
	MaxAgentDelayMs *int
}

// CompactionPolicyPatch is Partial<CompactionPolicy>; nil fields keep the defaults.
type CompactionPolicyPatch struct {
	Enabled          *bool
	ReserveTokens    *int
	KeepRecentTokens *int
	BackgroundTokens *int
}

// ResolveSettings resolves host settings: every field over its built-in default, object fields merged.
func ResolveSettings(settings *HarnessSettings) durable.Settings {
	resolved := durable.Settings{
		Retry:         DefaultRetryPolicy,
		Compaction:    DefaultCompactionPolicy,
		ToolExecution: durable.ToolExecutionParallel,
		SteeringMode:  durable.QueueOneAtATime,
		FollowUpMode:  durable.QueueOneAtATime,
	}
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
	}
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

// AddTools applies the addTools control of a tool round: an exact list gets each name it lacks appended, {remove} loses the names, and unset tools already offer every tool, so nothing is written.
func AddTools(tx durable.Tx, conversationId durable.ConversationId, added []string) error {
	state, err := durable.TxDoc[AgentState](tx, AgentDoc, conversationId)
	if err != nil {
		return err
	}
	if tools := state.Array("tools"); tools != nil {
		for _, name := range added {
			if !slices.Contains(tools.Snapshot(), any(name)) {
				if _, err := tools.Push(name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	filter := state.Object("tools")
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
	return state.Set("tools", map[string]any{"remove": remaining})
}

func applyChange(state durable.Draft[AgentState], change AgentChange) error {
	var model any
	if change.Model.state == changeSet {
		model = map[string]any{"provider": change.Model.value.Provider, "modelId": change.Model.value.ModelId}
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
		if selection.Exact {
			extensions = namesJSON(extensionNames(selection.List))
		} else {
			edit := map[string]any{}
			if selection.Add != nil {
				edit["add"] = namesJSON(extensionNames(selection.Add))
			}
			if selection.Remove != nil {
				edit["remove"] = namesJSON(extensionNames(selection.Remove))
			}
			extensions = edit
		}
	}
	if err := setField(state, "extensions", change.Extensions.state, extensions); err != nil {
		return err
	}
	var tools any
	if change.Tools.state == changeSet {
		if change.Tools.value.Exact {
			tools = namesJSON(toolNames(change.Tools.value.List))
		} else {
			tools = map[string]any{"remove": namesJSON(toolNames(change.Tools.value.Remove))}
		}
	}
	if err := setField(state, "tools", change.Tools.state, tools); err != nil {
		return err
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
	values := copied.(map[string]any)
	for _, key := range owner.Keys() {
		if err := agent.Set(key, values[key]); err != nil {
			return err
		}
	}
	return nil
}

// AgentHooks returns the handlers of the selected extensions' hooks for a task name, in extension order.
func AgentHooks(agent durable.Agent, taskName string) []any {
	handlers := []any{}
	for _, extension := range agent.Extensions {
		for _, hook := range extension.Hooks {
			if hook.Task == taskName {
				handlers = append(handlers, hook.Handlers)
			}
		}
	}
	return handlers
}

// ResolveAgent resolves an agent from its stored state (nil: every field unset), a registry snapshot, and resolved settings. A wrapper that panics or renames drops its target and is reported; a wrapper without a target does nothing.
func ResolveAgent(state *AgentState, snapshot durable.RegistrySnapshot, settings durable.Settings, report func(error)) durable.Agent {
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
		for _, wrap := range extension.Wraps {
			if wrap.WrapTool != nil {
				applyWrap(composed, wrap.Tool, wrap.WrapTool, func(tool *durable.ToolRegistration) string { return tool.Name }, report)
			} else if wrap.WrapSection != nil {
				applyWrap(sections, wrap.Section, wrap.WrapSection, func(section *durable.PromptSection) string { return section.Key }, report)
			}
		}
	}

	var tools []*durable.ToolRegistration
	switch filter := state.Tools; {
	case filter == nil:
		tools = composed.Values()
	case filter.Exact:
		tools = []*durable.ToolRegistration{}
		seen := map[string]bool{}
		for _, name := range filter.Names {
			if seen[name] {
				continue
			}
			seen[name] = true
			if tool, ok := composed.Get(name); ok {
				tools = append(tools, tool)
			}
		}
	default:
		tools = []*durable.ToolRegistration{}
		for _, tool := range composed.Values() {
			if !slices.Contains(filter.Remove, tool.Name) {
				tools = append(tools, tool)
			}
		}
	}

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
