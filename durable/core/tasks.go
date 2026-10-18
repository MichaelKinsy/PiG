// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// taskKey remembers a custom task definition as the host sent it and the TaskImpl built from it.
type taskKey struct {
	key  string
	impl *session.TaskImpl
}

// loadRegistry rebuilds the registry snapshot from the host's registry JSON (host/PROTOCOL.md, registry): the custom
// tasks of its extensions. Built-in tasks are the core's own.
func (h *handler) loadRegistry(snapshot *jv.Object) {
	tasks := map[string]*session.TaskImpl{}
	keys := map[string]taskKey{}
	if snapshot != nil {
		if extensions, ok := snapshot.Get("extensions"); ok {
			list, _ := extensions.([]any)
			for _, e := range list {
				extension, _ := e.(*jv.Object)
				declared, _ := extension.Get("tasks")
				defs, _ := declared.([]any)
				for _, d := range defs {
					def, _ := d.(*jv.Object)
					if def == nil {
						continue
					}
					name := strOf(def, "name")
					// A definition the host sent unchanged keeps its identity, so the scheduler sees no reload.
					key := jv.Stringify(def)
					if known, same := h.taskKeys[name]; same && known.key == key {
						tasks[name] = known.impl
						keys[name] = known
						continue
					}
					impl := &session.TaskImpl{Def: session.TaskDef{Name: name, Version: int(numOf(def, "version"))}, Extension: strOf(extension, "name")}
					// The host registration this definition is: an invocation keeps running under it after a reload
					// replaces or removes the definition (scheduler.ts: the phase's task is fixed for the invocation).
					registration, _ := def.Get("handler")
					impl.Phase = func(rt *session.Runtime, task *jv.Object, done func(error)) {
						h.runPhase(rt, task, registration, false, done)
					}
					if abort, _ := boolOf(def, "abort"); abort {
						impl.Abort = func(rt *session.Runtime, task *jv.Object, done func(error)) {
							h.runPhase(rt, task, registration, true, done)
						}
					} else {
						// Pi's TaskDefinition requires abort() (types.ts); a Go definition can leave the field nil. The abort invocation then
						// faults the task with a named error instead of dereferencing a missing handler.
						impl.Abort = func(_ *session.Runtime, _ *jv.Object, done func(error)) {
							done(errors.New("Task kind " + name + " has no abort handler"))
						}
					}
					if migrate, _ := boolOf(def, "migrate"); migrate {
						impl.MigrateAsync = h.migrateInHost(name)
					}
					tasks[name] = h.tracked(impl)
					keys[name] = taskKey{key, tasks[name]}
				}
			}
		}
	}
	if h.builtins == nil {
		for _, impl := range turn.Machines(h.newTurnHost()) {
			h.builtins = append(h.builtins, h.tracked(impl))
		}
	}
	for _, impl := range h.builtins {
		tasks[impl.Def.Name] = impl
	}
	h.registry = snapshot
	h.snap = &registrySnapshot{tasks: tasks}
	h.taskKeys = keys
}

// migrateInHost runs a custom task's migrate(input, checkpoint, from) in the host, as a hook effect named migrate: the
// answer is {input, checkpoint}, or a throw the scheduler records as the task's failed migration (scheduler.ts #resolve).
func (h *handler) migrateInHost(kind string) func(input, checkpoint any, from int, done func(any, any, error)) {
	return func(input, checkpoint any, from int, done func(any, any, error)) {
		payload := jv.NewObject()
		payload.Set("kind", kind)
		payload.Set("input", input)
		payload.Set("checkpoint", checkpoint)
		payload.Set("from", float64(from))
		h.hookEffect("migrate", 0, 0, 0, payload, nil, func(v any, err error) {
			if err != nil {
				done(nil, nil, err)
				return
			}
			out := asObject(v)
			newInput, _ := valueOfPair(out, "input")
			newCheckpoint, _ := valueOfPair(out, "checkpoint")
			done(newInput, newCheckpoint, nil)
		})
	}
}

func valueOfPair(o *jv.Object, key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	return o.Get(key)
}

// runPhase starts a custom task's phase (or abort) handler in the host: a phase effect {taskId, kind, phase?, record,
// invocation, abort?} (ABI section 6, effect 8). The handler commits through transactions begun with the invocation and
// ends with phase_done; an aborted invocation cancels the effect.
func (h *handler) runPhase(rt *session.Runtime, task *jv.Object, registration any, abort bool, done func(error)) {
	taskID := rt.TaskID()
	payload := jv.NewObject()
	payload.Set("taskId", float64(taskID))
	payload.Set("kind", strOf(task, "kind"))
	if state := objOf(task, "state"); state != nil {
		if checkpoint := objOf(state, "checkpoint"); checkpoint != nil {
			if phase := strOf(checkpoint, "phase"); phase != "" {
				payload.Set("phase", phase)
			}
		}
	}
	payload.Set("record", task)
	payload.Set("invocation", float64(taskID))
	if ext := rt.Extension(); ext != "" {
		payload.Set("extension", ext)
	}
	if registration != nil {
		payload.Set("handler", registration)
	}
	if abort {
		payload.Set("abort", true)
	}
	var remove func()
	id := h.startEffect(abi.EffectPhase, jv.Append(nil, payload), &pending{done: func(outcome uint8, body []byte) {
		remove()
		done(phaseOutcome(outcome, body))
	}})
	remove = rt.Signal().OnAbort(func() { h.cancelEffect(id) })
	// The invocation outlives its phases (scheduler.ts: one Runtime from the first phase to the task's end). When its
	// signal aborts the host is told by a cancel of the latest phase effect, which it also reads as the end of the
	// invocation; the cancel goes out even when that effect has settled.
	key := invocationKey{taskID, abort}
	if h.invocations == nil {
		h.invocations = map[invocationKey]uint32{}
	}
	if _, running := h.invocations[key]; !running {
		rt.Signal().OnAbort(func() {
			if last, ok := h.invocations[key]; ok {
				delete(h.invocations, key)
				if _, live := h.effects[last]; !live {
					h.b.Cancel(last)
				}
			}
		})
	}
	h.invocations[key] = id
}

// phaseOutcome is a custom task handler's settlement (phase_done): outcome 0 is a return, outcome 1 a throw {name, message}.
func phaseOutcome(outcome uint8, body []byte) error {
	if outcome == abi.OutcomeResult {
		return nil
	}
	message := "the task handler threw"
	if v, err := jv.Parse(body); err == nil {
		if thrown, _ := v.(*jv.Object); thrown != nil {
			message = strOf(thrown, "message")
		}
	}
	return errors.New(message)
}
