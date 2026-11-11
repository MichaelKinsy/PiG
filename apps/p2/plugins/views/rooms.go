package views

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/views/rooms.ts: rooms of custom views (server, apps/p2/docs/rooms.md): the tokens a page gets for its frames'
// links and their redemption, and the per-principal `ViewRoom` that frames and the session's worker hold, over one `RoomStore` per
// opening of a view (room_store*.go).

const (
	// tokenTTL is how long a token may wait for its link.
	tokenTTL = 60 * time.Second
	// maxTokens is the unused tokens one viewer object keeps; a newer one drops the oldest.
	maxTokens = 8
	// editorTTL is how long an editor ID is kept for a frame's reconnects since it was last used.
	editorTTL = 24 * time.Hour
)

// linkToken is a token waiting for its link: the principal it stamps, for whom it was made, until when.
type linkToken struct {
	principal p2runtime.ViewPrincipal
	by        p2runtime.Principal
	expires   time.Time
}

// editorID is an editor ID: whose frame it was minted for, and when it was last used.
type editorID struct {
	viewer, opening string
	used            time.Time
}

// rooms is the server's tokens and editors; the lock guards the maps only, never a wait.
type rooms struct {
	mu      sync.Mutex
	tokens  map[string]*linkToken
	editors map[string]*editorID
}

func randomToken() string {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes)
}

// openingOf is view view's current opening in session, read by the server; "" and false: no such custom view.
func openingOf(session, view string, ctx host.Ctx) (string, bool, error) {
	openings, err := ctx.Get(contract.ViewOpenings, delta.JsonObjectOf("session", session), host.GetOptions{})
	if err != nil {
		return "", false, err
	}
	defer openings.Release()
	value, err := openings.Member("openings").Value()
	if err != nil {
		return "", false, err
	}
	opening, found := objectOf(value).Value(view).(string)
	return opening, found, nil
}

// dropTokens drops the unused tokens of one opening of a view.
func (r *rooms) dropTokens(session, view, opening string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for token, found := range r.tokens {
		if found.principal.Session == session && found.principal.View == view && found.principal.Opening == opening {
			delete(r.tokens, token)
		}
	}
}

func (r *rooms) take(token string) *linkToken {
	r.mu.Lock()
	defer r.mu.Unlock()
	found := r.tokens[token]
	delete(r.tokens, token)
	return found
}

func (r *rooms) serveViewLinks(facet host.FacetHost) error {
	return facet.Serve(contract.ViewLinks, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		session := text(params, "session")
		viewer, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.CanView(viewer, session) || (!p2runtime.IsOwner(viewer) && !p2runtime.IsGuest(viewer)) {
			return contracts.Implementation{}, p2runtime.Forbidden("the session's viewers only")
		}
		access := self.Registry(p2runtime.SessionAccess.Registry)
		models := self.Get(modelscontract.ModelConfig, delta.NewJsonObject(0))
		server := p2runtime.ServerOf(self)
		// Its unused tokens end with it: revoking the viewer ends this object, and so the tokens it made.
		minted := self.Open(func(context.Context) (any, error) { return &mintedTokens{}, nil }, func(value any, _ host.NativeCloseReason, _ context.Context) error {
			own := value.(*mintedTokens)
			r.mu.Lock()
			for _, token := range own.list {
				delete(r.tokens, token)
			}
			r.mu.Unlock()
			return nil
		})
		name := func() string {
			if guest, ok := viewer.(p2runtime.GuestPrincipal); ok {
				return guest.Name
			}
			if snapshot, err := models.Member("snapshot").Value(); err == nil {
				if settings, ok := objectOf(snapshot).Value("settings").(*delta.JsonObject); ok {
					if configured, ok := settings.Value("ownerName").(string); ok {
						return configured
					}
				}
			}
			return p2runtime.DefaultOwnerName
		}
		return contracts.Implementation{Variant: "viewer", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"token": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				input := objectOf(args)
				view := text(input, "view")
				requested, hasEditor := input.Value("editor").(string)
				if guest, isGuest := viewer.(p2runtime.GuestPrincipal); isGuest {
					table, err := p2runtime.SessionAccess.Table(access)
					if err != nil {
						return nil, err
					}
					reach, err := p2runtime.MayReach(table, guest, session, p2runtime.CtxOf(ctx))
					if err != nil {
						return nil, err
					}
					if !reach {
						return nil, p2runtime.Forbidden("this session is no longer shared with you")
					}
				}
				parts, err := server()
				if err != nil {
					return nil, err
				}
				found, err := parts.Root(func(root host.Ctx) (any, error) {
					opening, ok, err := openingOf(session, view, root)
					if err != nil || !ok {
						return nil, err
					}
					return opening, nil
				}, chordctx.WithoutAbortSignal(ctx))
				if err != nil {
					return nil, err
				}
				opening, _ := found.(string)
				if opening == "" {
					return nil, invalidArgs("No custom view \"" + view + "\"")
				}
				now := time.Now()
				key := p2runtime.Key(viewer)
				r.mu.Lock()
				defer r.mu.Unlock()
				for id, each := range r.editors {
					if now.Sub(each.used) > editorTTL {
						delete(r.editors, id)
					}
				}
				// A reconnecting frame keeps its editor, if it was minted for this viewer and opening.
				editor := newUUID()
				if known := r.editors[requested]; hasEditor && known != nil && known.viewer == key && known.opening == opening {
					editor = requested
				}
				r.editors[editor] = &editorID{viewer: key, opening: opening, used: now}
				own, _ := minted.Value()
				set := own.(*mintedTokens)
				kept := set.list[:0]
				for _, token := range set.list {
					if found := r.tokens[token]; found != nil && !found.expires.Before(now) {
						kept = append(kept, token)
					}
				}
				set.list = kept
				for len(set.list) >= maxTokens {
					delete(r.tokens, set.list[0])
					set.list = set.list[1:]
				}
				mode := p2runtime.ViewRead
				if p2runtime.CanSend(viewer, session) {
					mode = p2runtime.ViewWrite
				}
				role := "guest"
				if p2runtime.IsOwner(viewer) {
					role = "owner"
				}
				token := randomToken()
				r.tokens[token] = &linkToken{by: viewer, expires: now.Add(tokenTTL), principal: p2runtime.ViewPrincipal{
					Session: session, View: view, Opening: opening, Mode: mode, ForName: name(), ForRole: role, Link: newUUID(),
					Editor: editor, Viewer: p2runtime.RevokedBy(viewer),
				}}
				set.list = append(set.list, token)
				return delta.JsonObjectOf("token", token, "opening", opening, "editor", editor), nil
			},
		}}}, nil
	}), host.ServeOptions{})
}

// mintedTokens are the unused tokens one `ViewLinks` object made, oldest first.
type mintedTokens struct{ list []string }

// serveLinkTokens contributes the `view` entry of the token links: single use (taken before anything waits), then checked again.
func (r *rooms) serveLinkTokens(facet host.FacetHost) error {
	return p2runtime.Contribute(facet, "p2.view-link-tokens", func(self host.StoreSelf) error {
		access := self.Registry(p2runtime.SessionAccess.Registry)
		p2runtime.ContributeKeyed(self, p2runtime.LinkTokens, p2runtime.Entry("view", p2runtime.LinkToken(func(token string, ctx host.Ctx) (p2runtime.Principal, error) {
			found := r.take(token)
			if found == nil || found.expires.Before(time.Now()) {
				return nil, nil
			}
			if _, guest := found.by.(p2runtime.GuestPrincipal); guest {
				table, err := p2runtime.SessionAccess.Table(access)
				if err != nil {
					return nil, err
				}
				reach, err := p2runtime.MayReach(table, found.by, found.principal.Session, ctx)
				if err != nil || !reach {
					return nil, err
				}
			}
			opening, ok, err := openingOf(found.principal.Session, found.principal.View, ctx)
			if err != nil || !ok || opening != found.principal.Opening {
				return nil, err
			}
			return found.principal, nil
		})))
		return nil
	})
}

func (r *rooms) serveViewRoom(facet host.FacetHost) error {
	return facet.Serve(contract.ViewRoom, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		session, view, opening := text(params, "session"), text(params, "view"), text(params, "opening")
		who, err := p2runtime.FromChord(principal)
		if err != nil {
			return contracts.Implementation{}, p2runtime.Forbidden("this view's frames only")
		}
		store := self.Get(RoomStore, delta.JsonObjectOf("session", session, "view", view, "opening", opening))
		if worker, ok := who.(p2runtime.WorkerPrincipal); ok {
			if worker.Session != session {
				return contracts.Implementation{}, p2runtime.Forbidden("this session's worker only")
			}
			return contracts.Implementation{Variant: "worker", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"methods": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return store.Call("methods", nil, ctx)
				},
				"call": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return store.Call("call", args, ctx)
				},
			}}}, nil
		}
		frame, ok := who.(p2runtime.ViewPrincipal)
		if !ok || frame.Session != session || frame.View != view || frame.Opening != opening {
			return contracts.Implementation{}, p2runtime.Forbidden("this view's frames only")
		}
		peer := frame.Link
		writer := frame.Mode == p2runtime.ViewWrite
		// gate is a member forwarded to the store with the arguments shaped by shape; one that needs write is refused for a frame that
		// may only watch.
		gate := func(name string, needsWrite bool, shape func(args delta.JsonValue) *delta.JsonObject) contracts.MethodFunction {
			return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				if needsWrite && !writer {
					return nil, p2runtime.Forbidden("this frame may only watch")
				}
				return store.Call(name, shape(args), ctx)
			}
		}
		peerOf := func(args delta.JsonValue) *delta.JsonObject {
			merged := delta.JsonObjectOf("peer", peer)
			if object, ok := args.(*delta.JsonObject); ok {
				for key, value := range object.All() {
					merged.Set(key, value)
				}
			}
			return merged
		}
		return contracts.Implementation{Variant: "peer", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"room": host.Expose(store.Member("room")), "presence": host.Expose(store.Member("presence")),
				"log": host.Expose(store.Member("log")), "keyed": host.Expose(store.Member("keyed")),
			},
			Methods: map[string]contracts.MethodFunction{
				"join": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					shaped := peerOf(args)
					shaped.Set("writer", writer)
					joined, err := store.Call("join", shaped, ctx)
					if err != nil {
						return nil, err
					}
					return delta.JsonObjectOf("me", peer, "editor", frame.Editor, "from", objectOf(joined).Value("from")), nil
				},
				"send":    gate("send", true, peerOf),
				"ack":     gate("ack", false, peerOf),
				"history": gate("history", false, func(args delta.JsonValue) *delta.JsonObject { return objectOf(args) }),
				"now": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					return float64(time.Now().UnixMilli()), nil
				},
				"offer":  gate("offer", true, peerOf),
				"answer": gate("answer", true, func(args delta.JsonValue) *delta.JsonObject { return delta.JsonObjectOf("peer", peer, "answer", args) }),
				"snapshot": gate("snapshot", true, func(args delta.JsonValue) *delta.JsonObject {
					return delta.JsonObjectOf("peer", peer, "snapshot", args)
				}),
				"edit": gate("edit", true, func(args delta.JsonValue) *delta.JsonObject {
					return delta.JsonObjectOf("editor", frame.Editor, "batch", args)
				}),
			},
		}}, nil
	}), host.ServeOptions{})
}

// serveRooms serves the rooms: tokens and their links, the stores, `ViewRoom`.
func serveRooms(facet host.FacetHost) error {
	r := &rooms{tokens: map[string]*linkToken{}, editors: map[string]*editorID{}}
	for _, serve := range []func(host.FacetHost) error{r.serveViewLinks, r.serveLinkTokens, r.serveViewRoom} {
		if err := serve(facet); err != nil {
			return err
		}
	}
	return serveRoomStore(facet, roomStoreParts{memory: &roomMemory{}, dropTokens: r.dropTokens})
}
