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
// The returned function tells superseded owners in other processes that their local root is no longer the provider's; call it after releasing h.mu and before the registration completes. retired are the Provider object owners retireNativeProviderLocked already removed, which it tells too.
func (h *Host) transferProviderOwnershipLocked(me *managedExt, conn *Conn, id string, retired ...*Conn) func() {
	notify, _ := h.claimProviderOwnershipLocked(me, conn, id, retired...)
	return notify
}

// providerOwner is what an extension held of one provider before another extension claimed it.
type providerOwner struct {
	ext           *managedExt
	hadProvider   bool
	hadOAuthLogin bool
}

// claimProviderOwnershipLocked is transferProviderOwnershipLocked that also returns undo, which gives the claim back: the previous owners hold the provider again and me holds what it held before. It does nothing once another extension claimed the provider after this claim (me no longer holds it), because that claim already took it from me and stands. Call undo with h.mu held.
func (h *Host) claimProviderOwnershipLocked(me *managedExt, conn *Conn, id string, retired ...*Conn) (notify func(), undo func()) {
	superseded := slices.Clone(retired)
	inheritedOAuth := false
	var previous []providerOwner
	for _, other := range h.exts {
		if other == me {
			continue
		}
		hadProvider, hadOAuth := slices.Contains(other.providerNames, id), slices.Contains(other.oauthProviderNames, id)
		if hadProvider || hadOAuth {
			previous = append(previous, providerOwner{ext: other, hadProvider: hadProvider, hadOAuthLogin: hadOAuth})
		}
		if otherConn := other.connection(); hadProvider && otherConn != nil && otherConn != conn && !slices.Contains(superseded, otherConn) {
			superseded = append(superseded, otherConn)
		}
		inheritedOAuth = inheritedOAuth || hadOAuth
		other.providerNames = slices.DeleteFunc(other.providerNames, func(name string) bool { return name == id })
		other.oauthProviderNames = slices.DeleteFunc(other.oauthProviderNames, func(name string) bool { return name == id })
	}
	ownedBefore, oauthBefore := slices.Contains(me.providerNames, id), slices.Contains(me.oauthProviderNames, id)
	if !ownedBefore {
		me.providerNames = append(me.providerNames, id)
	}
	if inheritedOAuth && !oauthBefore {
		me.oauthProviderNames = append(me.oauthProviderNames, id)
	}
	notify = func() {
		args, _ := json.Marshal(map[string]string{"name": id})
		for _, target := range superseded {
			_ = target.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: notifyProviderSuperseded, Args: args}})
		}
	}
	undo = func() {
		if !slices.Contains(me.providerNames, id) {
			return
		}
		if !ownedBefore {
			me.providerNames = slices.DeleteFunc(me.providerNames, func(name string) bool { return name == id })
		}
		if !oauthBefore {
			me.oauthProviderNames = slices.DeleteFunc(me.oauthProviderNames, func(name string) bool { return name == id })
		}
		for _, owner := range previous {
			if owner.hadProvider && !slices.Contains(owner.ext.providerNames, id) {
				owner.ext.providerNames = append(owner.ext.providerNames, id)
			}
			if owner.hadOAuthLogin && !slices.Contains(owner.ext.oauthProviderNames, id) {
				owner.ext.oauthProviderNames = append(owner.ext.oauthProviderNames, id)
			}
		}
	}
	return notify, undo
}

// retireNativeProviderLocked removes provider id's Provider object, whoever registered it, with its owner's claim to id. It returns the objects to release and the connection of each owner in another process than conn, me's connection, whose local registration the caller's registration or unregistration supersedes (Pi keeps one effective registration per provider, model-runtime.ts:884-888); pass them to transferProviderOwnershipLocked.
func (h *Host) retireNativeProviderLocked(me *managedExt, conn *Conn, id string) (released []*nativeProviderProxy, superseded []*Conn) {
	for _, providers := range h.nativeProviders {
		provider := providers[id]
		if provider == nil {
			continue
		}
		delete(providers, id)
		provider.registered = false
		if ownerConn := provider.owner.connection(); provider.owner != me && ownerConn != nil && ownerConn != conn && slices.Contains(provider.owner.providerNames, id) {
			superseded = append(superseded, ownerConn)
		}
		provider.owner.providerNames = slices.DeleteFunc(provider.owner.providerNames, func(name string) bool { return name == id })
		provider.owner.oauthProviderNames = slices.DeleteFunc(provider.owner.oauthProviderNames, func(name string) bool { return name == id })
		if provider.declaration.OAuth != nil {
			ai.UnregisterOAuthProvider(id)
		}
		released = append(released, h.collectProviderObjectLocked(provider)...)
	}
	return released, superseded
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
