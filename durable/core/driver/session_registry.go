// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// handlerKey names one host function or object by identity. Go funcs are not comparable, so a function is keyed by the
// pointer of its owner and a role.
type handlerKey struct {
	owner any
	role  string
}

// handlerTable is the append-only table of host objects a registry snapshot's `handler` indexes name (durable/core/host/PROTOCOL.md).
// An index never changes meaning, so an effect issued under an older snapshot still resolves after a reload.
type handlerTable struct {
	mu    sync.Mutex
	items []any
	ids   map[handlerKey]int
}

func newHandlerTable() *handlerTable { return &handlerTable{ids: map[handlerKey]int{}} }

// id returns the index of the object, assigning one on first sight.
func (t *handlerTable) id(owner any, role string, item any) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := handlerKey{owner, role}
	if id, ok := t.ids[key]; ok {
		return id
	}
	t.items = append(t.items, item)
	t.ids[key] = len(t.items) - 1
	return len(t.items) - 1
}

// at returns the object at an index.
func (t *handlerTable) at(index int) (any, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if index < 0 || index >= len(t.items) {
		return nil, false
	}
	return t.items[index], true
}

// toolHandle and sectionHandle give a table entry its kind, so a core that names the wrong index fails loudly.
type toolHandle struct{ tool *durable.ToolRegistration }
type sectionHandle struct{ section *durable.PromptSection }
type prepareHandle struct{ driverPrepare PrepareArguments }

type snapshotTool struct {
	// Extension is the last selected extension that provides or wraps the tool (agent.ts toolExtension); set in an agent's
	// tool list, empty in a registry snapshot.
	Extension     string          `json:"extension,omitempty"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Parameters    json.RawMessage `json:"parameters"`
	Replay        string          `json:"replay"`
	Handler       int             `json:"handler"`
	ExecutionMode string          `json:"executionMode,omitempty"`
	OutputLimits  json.RawMessage `json:"outputLimits,omitempty"`
	Prepare       *int            `json:"prepare,omitempty"`
	// StructuredOutput is whether the tool declares a structured output schema, which decides what a nested call returns
	// when the tool gives none.
	StructuredOutput bool `json:"structuredOutput,omitempty"`
}
type snapshotSection struct {
	// Extension is the last selected extension that provides or wraps the section; set in an agent's section list.
	Extension string `json:"extension,omitempty"`
	Key       string `json:"key"`
	Tag       bool   `json:"tag"`
	Handler   int    `json:"handler"`
}
type snapshotHook struct {
	// Extension is the extension that registered the hook; set in an agent's hook list.
	Extension string         `json:"extension,omitempty"`
	Task      string         `json:"task"`
	Handlers  map[string]int `json:"handlers"`
}
type snapshotWrap struct {
	Tool    string `json:"tool,omitempty"`
	Section string `json:"section,omitempty"`
	Handler int    `json:"handler"`
}
type snapshotTask struct {
	Name    string   `json:"name"`
	Version int      `json:"version"`
	Phases  []string `json:"phases"`
	Abort   bool     `json:"abort"`
	// Migrate: the definition has a migrate function, which the host runs (the `migrate` hook).
	Migrate bool `json:"migrate"`
	Handler int  `json:"handler"`
	// Extension is the extension that installed the definition (scheduler.ts installerOf): the code that fails is its.
	Extension string `json:"extension,omitempty"`
}
type snapshotExtension struct {
	Name     string            `json:"name"`
	Tools    []snapshotTool    `json:"tools"`
	Sections []snapshotSection `json:"sections"`
	Hooks    []snapshotHook    `json:"hooks"`
	Wraps    []snapshotWrap    `json:"wraps"`
	Tasks    []snapshotTask    `json:"tasks"`
}

// SnapshotJSON is the registry snapshot the core receives (PROTOCOL.md, "Registry snapshot").
type SnapshotJSON struct {
	Extensions []snapshotExtension `json:"extensions"`
}

// hookNames returns the hook names a handler struct sets: its non-nil func fields, by the camelCase names of the hooks.
func hookNames(handlers any) []string {
	v := reflect.Indirect(reflect.ValueOf(handlers))
	if v.Kind() != reflect.Struct {
		return nil
	}
	var names []string
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Kind() == reflect.Func && !v.Field(i).IsNil() {
			name := v.Type().Field(i).Name
			names = append(names, string(name[0]+('a'-'A'))+name[1:])
		}
	}
	sort.Strings(names)
	return names
}

// toolParameters is a tool's JSON Schema as its declaration stores it: ai.ToolSchema's own encoding keeps the key order
// the schema was defined or imported with (Pi stores TypeBox's order, for example type, required, properties), where
// encoding the bare map would sort the keys and make the core see a changed declaration.
func toolParameters(tool ai.ToolSchema) (json.RawMessage, error) {
	encoded, err := json.Marshal(tool)
	if err != nil {
		return nil, err
	}
	var fields struct {
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	return fields.Parameters, nil
}

// hook renders one hook registration: each handler name the registration sets, to the registration's table index.
func (t *handlerTable) hook(hook *durable.HookRegistration) snapshotHook {
	id := t.id(hook, "hook", hook.Handlers)
	handlers := map[string]int{}
	for _, name := range hookNames(hook.Handlers) {
		handlers[name] = id
	}
	return snapshotHook{Task: hook.Task, Handlers: handlers}
}

// snapshot renders the registry for the core and fills the table.
func (t *handlerTable) snapshot(reg durable.RegistrySnapshot) (SnapshotJSON, error) {
	out := SnapshotJSON{Extensions: []snapshotExtension{}}
	for _, extension := range reg.Installed() {
		e := snapshotExtension{Name: extension.Name, Tools: []snapshotTool{}, Sections: []snapshotSection{}, Hooks: []snapshotHook{}, Wraps: []snapshotWrap{}, Tasks: []snapshotTask{}}
		for _, tool := range extension.Tools {
			parameters, err := toolParameters(tool.ToolSchema)
			if err != nil {
				return out, fmt.Errorf("tool %s parameters: %w", tool.Name, err)
			}
			replay := string(tool.Replay)
			if replay == "" {
				replay = string(durable.ReplayUnsafe)
			}
			st := snapshotTool{Name: tool.Name, Description: tool.Description, Parameters: parameters, Replay: replay, Handler: t.id(tool, "tool", toolHandle{tool}), ExecutionMode: string(tool.ExecutionMode)}
			limits, err := outputLimitsJSON(tool)
			if err != nil {
				return out, err
			}
			st.OutputLimits = limits
			if tool.PrepareArguments != nil {
				id := t.id(tool, "prepare", prepareHandle{PrepareArguments(tool.PrepareArguments)})
				st.Prepare = &id
			}
			e.Tools = append(e.Tools, st)
		}
		for _, section := range extension.Sections {
			tag := section.Tag == nil || *section.Tag
			e.Sections = append(e.Sections, snapshotSection{Key: section.Key, Tag: tag, Handler: t.id(section, "section", sectionHandle{section})})
		}
		for i := range extension.Hooks {
			e.Hooks = append(e.Hooks, t.hook(&extension.Hooks[i]))
		}
		for i := range extension.Wraps {
			wrap := &extension.Wraps[i]
			switch typed := (*wrap).(type) {
			case durable.ToolWrap:
				e.Wraps = append(e.Wraps, snapshotWrap{Tool: typed.Tool, Handler: t.id(wrap, "wrap", wrap)})
			case durable.SectionWrap:
				e.Wraps = append(e.Wraps, snapshotWrap{Section: typed.Section, Handler: t.id(wrap, "wrap", wrap)})
			}
		}
		for _, task := range extension.Tasks {
			def := task.AnyDefinition()
			phases := make([]string, 0, len(def.Phases))
			for name := range def.Phases {
				phases = append(phases, name)
			}
			sort.Strings(phases)
			e.Tasks = append(e.Tasks, snapshotTask{Name: def.Name, Version: def.Version, Phases: phases, Abort: def.Abort != nil, Migrate: def.Migrate != nil, Handler: t.id(task, "task", task), Extension: extension.Name})
		}
		out.Extensions = append(out.Extensions, e)
	}
	return out, nil
}

// lookupHook is Hooks.Lookup over the table: what a hook handler index names.
func (t *handlerTable) lookupHook(handler int) (any, bool) {
	item, ok := t.at(handler)
	if !ok {
		return nil, false
	}
	if p, isPrepare := item.(prepareHandle); isPrepare {
		return p.driverPrepare, true
	}
	switch item.(type) {
	case *harness.GenerationHooks, *harness.ToolHooks, *harness.CompactionHooks:
		return item, true
	}
	return nil, false
}

// tool returns the registration a tool handler index names.
func (t *handlerTable) tool(handler int) (*durable.ToolRegistration, bool) {
	item, ok := t.at(handler)
	if !ok {
		return nil, false
	}
	h, ok := item.(toolHandle)
	return h.tool, ok
}

// outputLimitsJSON is the tool's outputLimits as the core reads them (PROTOCOL.md, Resolved agent JSON); nil without limits.
func outputLimitsJSON(tool *durable.ToolRegistration) (json.RawMessage, error) {
	if tool.OutputLimits == nil {
		return nil, nil
	}
	//portlint:allow mapkeyorder driver-to-core wire (durable/core/host/PROTOCOL.md): the core reads its members by name and stores none of this object
	return json.Marshal(map[string]any{"maxBytes": tool.OutputLimits.MaxBytes, "maxLines": tool.OutputLimits.MaxLines, "retain": tool.OutputLimits.Retain})
}
