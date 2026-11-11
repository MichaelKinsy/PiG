// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"errors"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// maxDocReplicas is how many document values a Client keeps; the least recently read goes first.
const maxDocReplicas = 32

// docReplica is the value of a document at a revision the core named (PROTOCOL.md, `doc`): the incarnation and the number of
// changes adopted to it. It is never changed once made.
type docReplica struct {
	epoch   uint64
	adopted int
	value   durable.JsonObject
}

// docReplicas are the latest values of the documents a Client's transactions read, so that a transaction opening a document
// again asks the core for what changed since and not for the whole document, which a nested call fan-out reads once per call.
type docReplicas struct {
	mu     sync.Mutex
	byID   map[string]*docReplica
	recent []string // least recently read first
}

// held is the replica of document id, or nil.
func (r *docReplicas) held(id string) *docReplica {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byID[id]
}

// revisions is the `revisions` member of a `doc` request for a document of which the host holds replica (nil: none): the revision
// held, or an empty one, which asks the core to name the revision of the value it answers with.
func revisions(replica *docReplica) map[string]any {
	if replica == nil {
		return map[string]any{"epoch": 0, "adopted": 0}
	}
	return map[string]any{"epoch": replica.epoch, "adopted": replica.adopted}
}

// docReading is the core's answer to a `doc` request that has no need: the document's value, or the operations that take the
// value of the held revision to it, and the revision it is at when the core named one.
type docReading struct {
	value       durable.JsonObject
	ops         delta.Ops
	incremental bool
	epoch       uint64
	adopted     int
	revised     bool
}

// resolve is the value of document id that answer stands for, given the replica the request named, and keeps it as the
// replica when the core named its revision: a value the core cannot name again is not one to catch up from.
func (r *docReplicas) resolve(id string, held *docReplica, answer docReading) (durable.JsonObject, error) {
	value := answer.value
	if answer.incremental {
		if held == nil || held.epoch != answer.epoch {
			return nil, errReplicaLost
		}
		next, err := delta.ApplyImmutable(held.value, answer.ops)
		if err != nil {
			return nil, err
		}
		if value, err = jsonObject(next); err != nil {
			return nil, err
		}
	}
	if !answer.revised {
		return value, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if current := r.byID[id]; current != nil && current.epoch == answer.epoch && current.adopted > answer.adopted {
		return value, nil
	}
	if r.byID == nil {
		r.byID = map[string]*docReplica{}
	}
	r.byID[id] = &docReplica{epoch: answer.epoch, adopted: answer.adopted, value: value}
	if at := slices.Index(r.recent, id); at >= 0 {
		r.recent = slices.Delete(r.recent, at, at+1)
	}
	r.recent = append(r.recent, id)
	if len(r.recent) > maxDocReplicas {
		delete(r.byID, r.recent[0])
		r.recent = r.recent[1:]
	}
	return value, nil
}

// errReplicaLost is the core answering with operations relative to a revision the request did not name.
var errReplicaLost = errors.New("the core answered with the operations since a revision the host did not name")
