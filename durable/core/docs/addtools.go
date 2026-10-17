// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package docs

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// AddTools is agent.ts addTools, the effect of a tool round's `addTools` controls, applied to `tools` and `modelTools`: an
// array gets each name it lacks appended, and `{remove}` loses the names. An unset `tools` already enables every tool, so
// nothing is written for it. An unset or object `modelTools` also gets each name appended to its `add`, which loads deferred
// tools and has no effect for others. By name, without the registry: a tool that is not registered now, such as one of an
// MCP server that is reconnecting, is offered once it is again.
func AddTools(tx *session.Tx, conversation session.ID, added []any) error {
	state, err := tx.Doc(AgentDef, session.DocArgs{Owner: conversation})
	if err != nil {
		return err
	}
	if err := addToField(state, "tools", added); err != nil {
		return err
	}
	if modelTools := state.Arr("modelTools"); modelTools != nil {
		return addToField(state, "modelTools", added)
	}
	// Assigned whole; a document write of an equal value records no change.
	var remove []any
	var add []any
	if filter := state.Obj("modelTools"); filter != nil {
		if stored := filter.Arr("remove"); stored != nil {
			for _, name := range stored.Snapshot() {
				if !slices.Contains(added, name) {
					remove = append(remove, name)
				}
			}
		}
		if stored := filter.Arr("add"); stored != nil {
			add = stored.Snapshot()
		}
	}
	for _, name := range added {
		if !slices.Contains(add, name) {
			add = append(add, name)
		}
	}
	next := jv.NewObject()
	if len(remove) > 0 {
		next.Set("remove", remove)
	}
	if add == nil {
		add = []any{}
	}
	next.Set("add", add)
	return state.Set("modelTools", next)
}

func addToField(state *delta.Obj, field string, added []any) error {
	if tools := state.Arr(field); tools != nil {
		for _, name := range added {
			present := false
			for i := range tools.Len() {
				if tools.Get(i) == name {
					present = true
					break
				}
			}
			if !present {
				if _, err := tools.Push(name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	filter := state.Obj(field)
	if filter == nil {
		return nil
	}
	remove := filter.Arr("remove")
	if remove == nil {
		return nil
	}
	var kept []any
	dropped := false
	for i := range remove.Len() {
		name := remove.Get(i)
		if slices.Contains(added, name) {
			dropped = true
			continue
		}
		kept = append(kept, name)
	}
	if !dropped {
		return nil
	}
	next := jv.NewObject()
	next.Set("remove", kept)
	return state.Set(field, next)
}
