package sdk

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// replicatedIdentity is the part of the host's state snapshots that answers Pi's `ctx.model` (as a full model object) and
// `ctx.sessionManager.getSessionId()` without a host round trip, as the Node runtime answers them (runtime.mjs applyState). The host
// pushes a snapshot before every dispatch, before the reply of a Session write and when the model or the Session changes
// (Host.hostStateChanged), so these are never older than the Node runtime's. It is guarded by Extension.mu.
//
// The context usage is not replicated: Pi computes it live from the branch (agent-session.ts getContextUsage), and a snapshot taken
// at a dispatch is stale after waitForIdle, compact or navigateTree and for a reader between requests.
type replicatedIdentity struct {
	// modelKnown is set once a snapshot carried the `model` key; until then the value is unknown, not absent, and a read asks the host.
	modelKnown bool
	// modelRaw is the model object exactly as the host sent it ("null" for no model). A snapshot that repeats it costs a byte compare.
	modelRaw []byte
	// modelInfo is modelRaw decoded, for the next read; nil until then or for no model. modelErr is the decode error of an object that does not decode.
	modelInfo   *ModelInfo
	modelErr    error
	modelParsed bool

	// sessionIDKnown is set once a snapshot carried the `session` object.
	sessionIDKnown bool
	sessionID      string
}

// withModel is the replicated state after a snapshot carrying the `model` key (an absent key never reaches it), and whether the model changed. A model that repeats the held bytes leaves the state as it is.
func (r replicatedIdentity) withModel(raw []byte) (replicatedIdentity, bool) {
	if r.modelKnown && bytes.Equal(r.modelRaw, raw) {
		return r, false
	}
	r.modelKnown = true
	r.modelRaw = bytes.Clone(raw)
	r.modelInfo, r.modelErr, r.modelParsed = nil, nil, false
	return r, true
}

// withSessionID is the replicated state after a snapshot carrying the `session` object. The host omits `sessionId` only when it is empty.
func (r replicatedIdentity) withSessionID(id string) replicatedIdentity {
	r.sessionIDKnown, r.sessionID = true, id
	return r
}

// replicatedModelInfo is the replicated GetModelInfo answer, private to the caller. known is false while no snapshot carried the model, when the caller asks the host.
func (e *Extension) replicatedModelInfo() (info *ModelInfo, known bool, err error) {
	e.mu.RLock()
	r := &e.identity
	known, parsed, raw := r.modelKnown, r.modelParsed, r.modelRaw
	info, err = r.modelInfo, r.modelErr
	e.mu.RUnlock()
	if !known {
		return nil, false, nil
	}
	// raw is never written once stored, so it is decoded outside the lock.
	fresh := !parsed
	if fresh {
		info, err = decodeReplicatedModel(raw)
		e.mu.Lock()
		if r := &e.identity; !r.modelParsed && bytes.Equal(r.modelRaw, raw) {
			r.modelInfo, r.modelErr, r.modelParsed = info, err, true
		}
		e.mu.Unlock()
	}
	if info == nil {
		return nil, true, err
	}
	// The held decode is shared, so the caller gets a copy, and a model with limits (a map the caller could write) a decode of its own.
	if info.InputLimits != nil {
		info, err = decodeReplicatedModel(raw)
	} else {
		cp := *info
		info = &cp
	}
	return info, true, err
}

// decodeReplicatedModel decodes a model object as GetModelInfo decodes the host's reply: null or an object without an id is no model.
func decodeReplicatedModel(raw []byte) (*ModelInfo, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	var info ModelInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("host reply to getModelInfo: %w", err)
	}
	if info.ID == "" {
		return nil, nil
	}
	return &info, nil
}

// replicatedSessionID is the replicated session id; known is false while no snapshot carried the Session.
func (e *Extension) replicatedSessionID() (id string, known bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.identity.sessionID, e.identity.sessionIDKnown
}

// staleError is the error a read of a replaced or reloaded runtime's context returns: the host answered every call of such a runtime with the stale message.
func (c Context) staleError() error {
	if c.replacement != nil || c.ext == nil {
		return nil
	}
	if message := c.ext.stale.Load(); message != nil {
		return errors.New(*message)
	}
	return nil
}

// localSessionID is the replicated session id of c's Session, for the reads that answer it locally: known is false for a replacement context, which belongs to another Session, and before the first snapshot.
func (c Context) localSessionID() (id string, known bool, err error) {
	if c.replacement != nil || c.ext == nil {
		return "", false, nil
	}
	if err := c.staleError(); err != nil {
		return "", true, err
	}
	id, known = c.ext.replicatedSessionID()
	return id, known, nil
}
