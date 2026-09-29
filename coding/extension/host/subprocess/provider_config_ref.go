package subprocess

import (
	"context"
	"encoding/json"
	"slices"
)

// Pi keeps one effective registered configuration root per provider and returns it from getRegisteredProviderConfig (model-runtime.ts:438-443,753-766). A Node author sends that root as an xref value; the Host holds the transmission while the registration is current and hands a counted delivery to each foreign reader.
const (
	callProviderConfigRef = "provider.configRef" // author → Host: {name, configRef}
	callProviderConfig    = "provider.config"    // reader → Host: {name} → {ref}
	// notifyProviderSuperseded tells a previous owner in another process that a later registration replaced its root: {name}.
	notifyProviderSuperseded = "provider_superseded"
)

type providerConfigRef struct {
	owner *managedExt
	frame xrefFrame
	raw   json.RawMessage
}

// providerConfigRefArg returns the configRef field of a registerProvider or provider.configRef call.
func providerConfigRefArg(args json.RawMessage) json.RawMessage {
	var fields struct {
		ConfigRef json.RawMessage `json:"configRef"`
	}
	if json.Unmarshal(args, &fields) != nil {
		return nil
	}
	return fields.ConfigRef
}

// attachProviderConfigRef records the root reference a registration call carried. The previous registration's hold ends; a reference from an extension that no longer owns the provider is released at once.
func (h *Host) attachProviderConfigRef(me *managedExt, conn *Conn, name, callID string, raw json.RawMessage) {
	frame := xrefFrame{conn, callID}
	if len(raw) == 0 || string(raw) == "null" {
		h.dropProviderConfigRef(name)
		h.xref.unhold(conn, callID)
		return
	}
	h.mu.Lock()
	owned := slices.Contains(me.providerNames, name)
	previous, hadPrevious := h.providerConfigRefs[name]
	if owned {
		if h.providerConfigRefs == nil {
			h.providerConfigRefs = map[string]providerConfigRef{}
		}
		h.providerConfigRefs[name] = providerConfigRef{owner: me, frame: frame, raw: raw}
	}
	h.mu.Unlock()
	if owned && hadPrevious {
		h.xref.unhold(previous.frame.conn, previous.frame.id)
	}
	if !owned {
		h.xref.unhold(conn, callID)
	}
}

// dropProviderConfigRef ends the hold of a provider whose registration was removed or replaced by a native Provider.
func (h *Host) dropProviderConfigRef(name string) {
	h.mu.Lock()
	previous, ok := h.providerConfigRefs[name]
	delete(h.providerConfigRefs, name)
	h.mu.Unlock()
	if ok {
		h.xref.unhold(previous.frame.conn, previous.frame.id)
	}
}

// providerConfigFor answers a foreign reader with the current root reference, counted as one delivery to its realm. A null result means the root has no reference (a native SDK author), and the reader keeps its registry data.
func (h *Host) providerConfigFor(conn *Conn, call *CallPayload) (*CallResultPayload, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, err
	}
	// The delivery is counted while the registration's hold is known to exist, so a concurrent replacement cannot unpin the root between lookup and delivery.
	h.mu.Lock()
	entry, ok := h.providerConfigRefs[args.Name]
	if ok {
		h.xref.deliver(h.xref.realmOf(conn), scanXrefRefs(entry.raw))
	}
	h.mu.Unlock()
	if !ok {
		return &CallResultPayload{Result: json.RawMessage(`{"ref":null}`)}, nil
	}
	result, err := json.Marshal(struct {
		Ref json.RawMessage `json:"ref"`
	}{entry.raw})
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: result}, nil
}

func (h *Host) handleProviderConfigCall(_ context.Context, me *managedExt, conn *Conn, callID string, call *CallPayload) (*CallResultPayload, error) {
	if call.Method == callProviderConfig {
		return h.providerConfigFor(conn, call)
	}
	var args struct {
		Name string `json:"name"`
	}
	configRef := providerConfigRefArg(call.Args)
	h.xref.hold(conn, callID, configRef)
	if err := json.Unmarshal(call.Args, &args); err != nil {
		h.xref.unhold(conn, callID)
		return nil, err
	}
	h.attachProviderConfigRef(me, conn, args.Name, callID, configRef)
	return &CallResultPayload{Result: json.RawMessage("null")}, nil
}
