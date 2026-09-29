package subprocess

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

func (h *Host) collectProviderObjectLocked(provider *nativeProviderProxy) []*nativeProviderProxy {
	if provider.registered || provider.calls != 0 || len(provider.references) != 0 || h.nativeProviderHandles[provider.declaration.Handle] != provider {
		return nil
	}
	delete(h.nativeProviderHandles, provider.declaration.Handle)
	return []*nativeProviderProxy{provider}
}
func (h *Host) releaseProviderCallbacks(providers []*nativeProviderProxy) {
	for _, provider := range providers {
		args, _ := json.Marshal(map[string]string{"key": provider.declaration.Key})
		// Closed owners release their callback table with the connection.
		_ = provider.conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "provider_release", Args: args}})
	}
}

// transferProviderOwnershipLocked makes me, delivering on conn, the only extension whose teardown removes provider id. Pi keeps one effective registration per provider (model-runtime.ts:744-766), so a later registrant's acceptance supersedes the earlier owner's cleanup claim.
// A bridged OAuth login moves with the claim: Pi's merged root keeps the earlier oauth until the latest registrant replaces or unregisters the root (model-runtime.ts:753-797).
// The returned function tells superseded owners in other processes that their local root is no longer the provider's; call it after releasing h.mu and before the registration completes.
func (h *Host) transferProviderOwnershipLocked(me *managedExt, conn *Conn, id string) func() {
	var superseded []*Conn
	inheritedOAuth := false
	for _, other := range h.exts {
		if other == me {
			continue
		}
		if otherConn := other.connection(); slices.Contains(other.providerNames, id) && otherConn != nil && otherConn != conn {
			superseded = append(superseded, otherConn)
		}
		inheritedOAuth = inheritedOAuth || slices.Contains(other.oauthProviderNames, id)
		other.providerNames = slices.DeleteFunc(other.providerNames, func(name string) bool { return name == id })
		other.oauthProviderNames = slices.DeleteFunc(other.oauthProviderNames, func(name string) bool { return name == id })
	}
	if !slices.Contains(me.providerNames, id) {
		me.providerNames = append(me.providerNames, id)
	}
	if inheritedOAuth && !slices.Contains(me.oauthProviderNames, id) {
		me.oauthProviderNames = append(me.oauthProviderNames, id)
	}
	return func() {
		args, _ := json.Marshal(map[string]string{"name": id})
		for _, target := range superseded {
			_ = target.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: notifyProviderSuperseded, Args: args}})
		}
	}
}

func (h *Host) retireNativeProviderLocked(id string) []*nativeProviderProxy {
	var released []*nativeProviderProxy
	for _, providers := range h.nativeProviders {
		provider := providers[id]
		if provider == nil {
			continue
		}
		delete(providers, id)
		provider.registered = false
		provider.owner.providerNames = slices.DeleteFunc(provider.owner.providerNames, func(name string) bool { return name == id })
		provider.owner.oauthProviderNames = slices.DeleteFunc(provider.owner.oauthProviderNames, func(name string) bool { return name == id })
		if provider.declaration.OAuth != nil {
			ai.UnregisterOAuthProvider(id)
		}
		released = append(released, h.collectProviderObjectLocked(provider)...)
	}
	return released
}
func (h *Host) handleProviderReference(conn *Conn, call *CallPayload) (*CallResultPayload, error) {
	var args struct {
		Handle string `json:"handle"`
		Token  string `json:"token"`
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, err
	}
	if args.Token == "" {
		return nil, errors.New("Provider reference requires a token")
	}
	h.mu.Lock()
	provider := h.nativeProviderHandles[args.Handle]
	if provider == nil {
		h.mu.Unlock()
		if call.Method == "provider.release" {
			return &CallResultPayload{}, nil
		}
		return nil, errors.New("Provider object owner is no longer connected")
	}
	if call.Method == "provider.retain" {
		if provider.references[conn] == nil {
			provider.references[conn] = map[string]struct{}{}
		}
		provider.references[conn][args.Token] = struct{}{}
	} else {
		delete(provider.references[conn], args.Token)
		if len(provider.references[conn]) == 0 {
			delete(provider.references, conn)
		}
	}
	released := h.collectProviderObjectLocked(provider)
	h.mu.Unlock()
	h.releaseProviderCallbacks(released)
	return &CallResultPayload{}, nil
}
