// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// taskGraph is the Harness's task graph mount (harness/task-graph.ts TaskGraphView): built from the committed tasks by
// its first watch, advanced by each commit publication, and dropped with its last watch.
type taskGraph struct {
	value   *jv.Object // {tasks: {"<id>": TaskGraphNode}}
	watches []uint32   // open graph watches, in open order
}

// buildTaskGraph is TaskGraphView.#build: every live task of the Session, with the conversations each owns in ID order.
func (h *handler) buildTaskGraph() *jv.Object {
	tasks := jv.NewObject()
	for i, id := range h.mir.tasks.ids {
		if h.mir.tasks.heads[i].status == "terminal" {
			continue
		}
		record := h.mir.task(id)
		// The conversations the task owns, in ID order, from the table's owner index: no pass over the conversations per task.
		var conversations []any
		for _, c := range h.mir.convs.byTask[id] {
			conversations = append(conversations, float64(c))
		}
		tasks.Set(strconv.FormatInt(int64(id), 10), graphNode(record, conversations, h.blockedOf(record)))
	}
	graph := jv.NewObject()
	graph.Set("tasks", tasks)
	return graph
}

// graphNode is nodeOf: a live task's durable status without its checkpoint and outcome payloads.
func graphNode(record *jv.Object, conversations []any, blocked string) *jv.Object {
	n := jv.NewObject()
	n.Set("id", valueOf(record, "id"))
	n.Set("kind", valueOf(record, "kind"))
	n.Set("conversationId", valueOf(record, "conversationId"))
	if owner, ok := record.Get("owner"); ok {
		n.Set("owner", owner)
	}
	n.Set("background", valueOf(record, "background"))
	n.Set("abortRequested", valueOf(record, "abortRequested"))
	state := objOf(record, "state")
	s := jv.NewObject()
	switch status := strOf(state, "status"); status {
	case "pending", "running":
		s.Set("status", status)
		s.Set("phase", valueOf(objOf(state, "checkpoint"), "phase"))
	case "waiting":
		s.Set("status", status)
		s.Set("phase", valueOf(objOf(state, "checkpoint"), "phase"))
		s.Set("on", jv.Clone(valueOf(state, "on")))
		s.Set("policy", jv.Clone(valueOf(state, "policy")))
	case "sleeping":
		s.Set("status", status)
		s.Set("phase", valueOf(objOf(state, "checkpoint"), "phase"))
		s.Set("until", valueOf(state, "until"))
	default:
		s.Set("status", "completing")
		s.Set("outcome", valueOf(objOf(state, "outcome"), "status"))
	}
	n.Set("state", s)
	if conversations == nil {
		conversations = []any{}
	}
	n.Set("conversations", conversations)
	if blocked != "" {
		n.Set("blocked", blocked)
	}
	return n
}

// blockedOf is why a live task cannot run here (scheduler.ts blockedReason): empty when it can.
func (h *handler) blockedOf(record *jv.Object) string {
	if h.sched == nil {
		return ""
	}
	return h.sched.BlockedReason(record)
}

// blockedChanged is the scheduler's BlockedChanged: blocked reasons may have changed without a commit, so the graph derives
// every node's reason again and publishes the ones that changed (task-graph.ts #refreshBlocked).
func (h *handler) blockedChanged() {
	if h.graph == nil || h.closed {
		return
	}
	tasks := objOf(h.graph.value, "tasks")
	var ops []any
	tasks.Members(func(key string, value any) bool {
		node, _ := value.(*jv.Object)
		taskID, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			return true
		}
		record := h.mir.task(session.ID(taskID))
		if record == nil {
			return true
		}
		blocked := h.blockedOf(record)
		current, _ := valueOf(node, "blocked").(string)
		if blocked == current {
			return true
		}
		updated := node.Shallow()
		if blocked == "" {
			updated.Delete("blocked")
			ops = append(ops, []any{"d", []any{"tasks", key, "blocked"}})
		} else {
			updated.Set("blocked", blocked)
			ops = append(ops, []any{"s", []any{"tasks", key, "blocked"}, blocked})
		}
		tasks.Set(key, updated)
		return true
	})
	h.publishGraph(ops)
}

// graphWatchOpen answers watch.open for {kind: "graph"}: the current graph, after which each publication that changes it
// becomes a frame (advanceTaskGraph).
func (h *handler) graphWatchOpen(req *jv.Object) (any, error) {
	if h.graph == nil {
		h.graph = &taskGraph{value: h.buildTaskGraph()}
	}
	h.graph.watches = append(h.graph.watches, uint32(numOf(req, "watchId")))
	answer := jv.NewObject()
	answer.Set("value", jv.Clone(h.graph.value))
	return answer, nil
}

// graphWatchClose drops one graph watch and the mount with the last.
func (h *handler) graphWatchClose(id uint32) {
	if h.graph == nil {
		return
	}
	for i, w := range h.graph.watches {
		if w == id {
			h.graph.watches = append(h.graph.watches[:i], h.graph.watches[i+1:]...)
			break
		}
	}
	if len(h.graph.watches) == 0 {
		h.graph = nil
	}
}

// advanceTaskGraph is task-graph.ts advance: the operations one publication makes on the graph, applied to the mount
// and sent to every graph watch as a published frame {watchId, frame: {ops}}: the host applies the ops to the graph it holds,
// because serializing the whole graph for every commit would make a commit cost O(live tasks).
//
// Not inlined: it does work only for an open graph watch, and an inlined copy would be compiled by V8 with its hot caller, publishWatches.
//
//go:noinline
func (h *handler) advanceTaskGraph(pub *session.Publication) {
	if h.graph == nil {
		return
	}
	tasks := objOf(h.graph.value, "tasks")
	var ops []any
	for _, c := range pub.Changes {
		if c.Kind != session.ChangeTable || c.Table != session.WriteTask {
			continue
		}
		record := c.Record
		key := strconv.FormatInt(int64(numOf(record, "id")), 10)
		previous := objOf(tasks, key)
		if str(sub(record, "state"), "status") == "terminal" {
			if previous == nil {
				continue
			}
			ops = append(ops, []any{"d", []any{"tasks", key}})
			tasks.Delete(key)
			continue
		}
		var conversations []any
		if previous != nil {
			conversations, _ = valueOf(previous, "conversations").([]any)
		}
		next := graphNode(record, conversations, h.blockedOf(record))
		if previous != nil && jv.Stringify(next) == jv.Stringify(previous) {
			continue
		}
		ops = append(ops, []any{"s", []any{"tasks", key}, jv.Clone(next)})
		tasks.Set(key, next)
	}
	// After the tasks, so a conversation created with its owner task in one commit finds the owner's node.
	var owners []string
	created := map[string][]any{}
	for _, c := range pub.Changes {
		if c.Kind != session.ChangeTable || c.Table != session.WriteConversation {
			continue
		}
		owner := objOf(c.Record, "owner")
		if owner == nil {
			continue
		}
		key := strconv.FormatInt(int64(numOf(owner, "taskId")), 10)
		if objOf(tasks, key) == nil {
			continue
		}
		if _, seen := created[key]; !seen {
			owners = append(owners, key)
		}
		created[key] = append(created[key], valueOf(c.Record, "id"))
	}
	for _, key := range owners {
		node := objOf(tasks, key)
		old, _ := valueOf(node, "conversations").([]any)
		conversations := append(append([]any{}, old...), created[key]...)
		sortNumbers(conversations)
		updated := node.Shallow()
		updated.Set("conversations", conversations)
		tasks.Set(key, updated)
		ops = append(ops, []any{"s", []any{"tasks", key, "conversations"}, jv.Clone(conversations)})
	}
	h.publishGraph(ops)
}

// publishGraph sends the frame of ops to every graph watch.
func (h *handler) publishGraph(ops []any) {
	if len(ops) == 0 {
		return
	}
	for _, id := range h.graph.watches {
		out := append(make([]byte, 0, 256), `{"watchId":`...)
		out = jv.Append(out, float64(id))
		out = append(out, `,"frame":{"ops":`...)
		out = jv.Append(out, ops)
		h.b.Notice(abi.NoticePublished, append(out, '}', '}'))
	}
}

func sortNumbers(s []any) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].(float64) < s[j-1].(float64); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// usage is Harness.usage: the pi.usage states of every conversation, added in conversation ID order.
func (h *handler) usage() (any, error) {
	total := jv.NewObject()
	total.Set("models", jv.NewObject())
	total.Set("tools", jv.NewObject())
	for _, id := range h.mir.convs.ids {
		state, err := h.sess.Snapshot(turn.UsageDef, session.DocArgs{Owner: id})
		if err != nil {
			return nil, err
		}
		if state != nil {
			turn.AddUsageState(total, state)
		}
	}
	return total, nil
}
