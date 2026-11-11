package runtime

import (
	"fmt"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports apps/p2/src/runtime/principals.ts: the principals of p2. Links stamp them; plugins check them in factories and
// methods.
//
// Go forced difference: upstream's discriminated union of object literals is a sealed interface with one struct per
// `kind`; Chord's own principal type is a plain JSON object (data.Principal), so ToChord and FromChord convert at the
// Chord boundary (principal stamping, the host's principalKey and revokedBy functions).

// Principal is a p2 principal: OwnerPrincipal, GuestPrincipal, WorkerPrincipal, ServerPrincipal or HostPrincipal.
type Principal interface{ principalKind() string }

// OwnerAccess is how an owner reached p2.
type OwnerAccess string

const (
	// AccessLocal is a local owner session on loopback: a client that proved to run as the server's user, or a browser that
	// redeemed an owner link on loopback. Its local-only powers (minting owner links, loopback provider sign-ins) rest on
	// that proof.
	AccessLocal OwnerAccess = "local"
	// AccessRelay is an owner through Radius, authenticated by the relay.
	AccessRelay OwnerAccess = "relay"
	// AccessDevice is an associated device's owner cookie, on any address (also on loopback). Revoked per device.
	AccessDevice OwnerAccess = "device"
)

// OwnerPrincipal is the person running this p2 (one today). Device is set exactly for AccessDevice.
type OwnerPrincipal struct {
	Access OwnerAccess
	Device string
}

// WorkerPrincipal is a session worker calling the server: stamped by the server's `session:` route, with the spawn
// generation.
type WorkerPrincipal struct {
	Session    string
	Generation int
}

// GuestMode is what a guest may do in a session.
type GuestMode string

const (
	// GuestView reads the session.
	GuestView GuestMode = "view"
	// GuestFull reads, sends, stops and uploads.
	GuestFull GuestMode = "full"
)

// GuestPrincipal is someone sessions were shared with: a browser that joined through share links (Name is
// self-chosen and unverified), or a person the owner shared with by email, whom Radius authenticated (Email,
// verified; Name is how the owner wrote it). Sessions are the sessions shared with them now and their modes; Home is
// the one shared or joined last, where they land. Revision is the guest's: any change of its grants (share, join,
// removal, link reset, archive) bumps it and invalidates every object of the older principal.
type GuestPrincipal struct {
	ID       string
	Name     string
	Email    *string
	Home     string
	Sessions map[string]GuestMode
	Revision int
}

// ServerPrincipal is the server's own code calling into a session worker (fork, transfers, the conversation broker).
type ServerPrincipal struct{}

// HostPrincipal is a host's own code calling objects on the same host.
type HostPrincipal struct{}

func (OwnerPrincipal) principalKind() string  { return "owner" }
func (GuestPrincipal) principalKind() string  { return "guest" }
func (WorkerPrincipal) principalKind() string { return "worker" }
func (ServerPrincipal) principalKind() string { return "server" }
func (HostPrincipal) principalKind() string   { return "host" }
// ViewMode is what a view's frame may do in its room: watch (`read`), or also send (`write`).
type ViewMode string

const (
	// ViewRead frames only watch.
	ViewRead ViewMode = "read"
	// ViewWrite frames may also send, offer, answer, snapshot and edit.
	ViewWrite ViewMode = "write"
)

// ViewPrincipal is a custom view's frame on its own link (apps/p2/docs/rooms.md): stamped by the server's `/chord/view` route
// from a token the page asked for. It reaches only types declared `ForViews` (its room), never what its viewer may reach.
type ViewPrincipal struct {
	Session string
	View    string
	// Opening is which opening of the view: a view closed and opened again under its ID is another opening.
	Opening string
	// Mode is the viewer's mode when the token was made.
	Mode ViewMode
	// ForName and ForRole say whom the frame is shown to ("owner" or "guest"): for names and presence only, never authority.
	ForName string
	ForRole string
	// Link is the token's ID, minted by the server: each frame link is a principal (and a room's peer) of its own.
	Link string
	// Editor is the ID of the frame's first link: one frame across its reconnects (a text room's editor).
	Editor string
	// Viewer is the viewer's revocations when the token was made: revoking the viewer revokes its frames.
	Viewer []Revocation
}

func (ViewPrincipal) principalKind() string { return "view" }


// Host is every host's own principal (`createHost({ principal })`), upstream's HOST.
var Host Principal = HostPrincipal{}

// Server is what a worker's parent (the server) acts as when its own code calls the worker (`connect`), upstream's
// SERVER.
var Server Principal = ServerPrincipal{}

// Revocation is what revokes principals (`host.revoke`, the same on every host): one associated device's owner, every
// remote owner (relay and devices), every local owner session ("sign out all devices" revokes both), or one guest (all
// revisions of its grant). It is the
// plain JSON object Chord compares by canonical JSON.
type Revocation = *delta.JsonObject

// RevokeDevice is the revocation of one associated device's owner.
func RevokeDevice(device string) Revocation { return delta.JsonObjectOf("device", device) }

// RevokeRemoteOwners is the revocation of every remote owner.
func RevokeRemoteOwners() Revocation { return delta.JsonObjectOf("owners", "remote") }

// RevokeLocalOwners is the revocation of every local owner session.
func RevokeLocalOwners() Revocation { return delta.JsonObjectOf("owners", "local") }

// RevokeGuest is the revocation of one guest, all its revisions.
func RevokeGuest(guest string) Revocation { return delta.JsonObjectOf("guest", guest) }

// RevokeView is the revocation of every frame of one opening of a view (`ViewKey`).
func RevokeView(key string) Revocation { return delta.JsonObjectOf("view", key) }

// RevokeLink is the revocation of one frame's link.
func RevokeLink(link string) Revocation { return delta.JsonObjectOf("link", link) }

// ViewKey is the `view` revocation's value for one opening of a view of a session.
func ViewKey(session, view, opening string) string { return session + "/" + view + "/" + opening }


// RevokedBy returns the revocations that end principal's objects and links.
func RevokedBy(principal Principal) []Revocation {
	switch p := principal.(type) {
	case OwnerPrincipal:
		switch p.Access {
		case AccessDevice:
			return []Revocation{RevokeDevice(p.Device), RevokeRemoteOwners()}
		case AccessRelay:
			return []Revocation{RevokeRemoteOwners()}
		}
		return []Revocation{RevokeLocalOwners()}
	case GuestPrincipal:
		return []Revocation{RevokeGuest(p.ID)}
	case ViewPrincipal:
		revoked := []Revocation{RevokeView(ViewKey(p.Session, p.View, p.Opening)), RevokeLink(p.Link)}
		return append(revoked, p.Viewer...)
	}
	return []Revocation{}
}

// IsOwner reports whether principal is an owner. A nil principal is no principal (a request nobody authenticated).
func IsOwner(principal Principal) bool { _, ok := principal.(OwnerPrincipal); return ok }

// IsWorker reports whether principal is a session worker.
func IsWorker(principal Principal) bool { _, ok := principal.(WorkerPrincipal); return ok }

// IsGuest reports whether principal is a guest.
func IsGuest(principal Principal) bool { _, ok := principal.(GuestPrincipal); return ok }

// CanView reports whether principal may read the session: the owner, or a guest it was shared with.
func CanView(principal Principal, session string) bool {
	if IsOwner(principal) {
		return true
	}
	guest, ok := principal.(GuestPrincipal)
	if !ok {
		return false
	}
	_, shared := guest.Sessions[session]
	return shared
}

// CanSend reports whether principal may send, stop and upload in the session: the owner, or a guest it was shared with
// read/write.
func CanSend(principal Principal, session string) bool {
	if IsOwner(principal) {
		return true
	}
	guest, ok := principal.(GuestPrincipal)
	return ok && guest.Sessions[session] == GuestFull
}

// Key is the object key of a principal: owner tabs of one access share objects, each worker generation and guest
// grant its own. upstream's principalKey.
func Key(principal Principal) string {
	switch p := principal.(type) {
	case OwnerPrincipal:
		if p.Access == AccessDevice {
			return "owner:device:" + p.Device
		}
		return "owner:" + string(p.Access)
	case GuestPrincipal:
		return fmt.Sprintf("guest:%s:r%d", p.ID, p.Revision)
	case WorkerPrincipal:
		return fmt.Sprintf("worker:%s:%d", p.Session, p.Generation)
	case ViewPrincipal:
		return "view:" + p.Link
	}
	return principal.principalKind()
}

// AuthorKind is who wrote a message: the owner or a guest.
type AuthorKind string

const (
	// AuthorOwner is the owner.
	AuthorOwner AuthorKind = "owner"
	// AuthorGuest is a guest.
	AuthorGuest AuthorKind = "guest"
)

// Author is who wrote a message: assigned by the worker from the principal, never by the client.
type Author struct {
	Kind AuthorKind
	ID   string
	Name string
}

// DefaultOwnerName is the owner's name when none is configured.
const DefaultOwnerName = "Owner"

// AuthorID is whose texts and drafts these are: the owner (one today), or a guest by grant. principal is an
// OwnerPrincipal or a GuestPrincipal.
func AuthorID(principal Principal) string {
	if guest, ok := principal.(GuestPrincipal); ok {
		return guest.ID
	}
	return "owner"
}

// AuthorOf returns the author an entry records. ownerName is settings.json's `ownerName` read when writing, so a
// renamed owner's next entries carry the new name; entries already written keep theirs. principal is an OwnerPrincipal
// or a GuestPrincipal.
func AuthorOf(principal Principal, ownerName string) Author {
	if guest, ok := principal.(GuestPrincipal); ok {
		return Author{Kind: AuthorGuest, ID: guest.ID, Name: guest.Name}
	}
	return Author{Kind: AuthorOwner, ID: "owner", Name: ownerName}
}

// ToChord converts principal to Chord's principal: a plain JSON object with a `kind`, as upstream's literals are.
func ToChord(principal Principal) data.Principal {
	switch p := principal.(type) {
	case OwnerPrincipal:
		if p.Access == AccessDevice {
			return delta.JsonObjectOf("kind", "owner", "access", "device", "device", p.Device)
		}
		return delta.JsonObjectOf("kind", "owner", "access", string(p.Access))
	case GuestPrincipal:
		sessions := delta.NewJsonObject(len(p.Sessions))
		for _, id := range sortedKeys(p.Sessions) {
			sessions.Set(id, string(p.Sessions[id]))
		}
		object := delta.JsonObjectOf("kind", "guest", "id", p.ID, "name", p.Name)
		if p.Email != nil {
			object.Set("email", *p.Email)
		}
		object.Set("home", p.Home)
		object.Set("sessions", sessions)
		object.Set("revision", float64(p.Revision))
		return object
	case WorkerPrincipal:
		return delta.JsonObjectOf("kind", "worker", "session", p.Session, "generation", float64(p.Generation))
	case ViewPrincipal:
		viewer := make([]any, len(p.Viewer))
		for i, revocation := range p.Viewer {
			viewer[i] = revocation
		}
		return delta.JsonObjectOf("kind", "view", "session", p.Session, "view", p.View, "opening", p.Opening, "mode", string(p.Mode),
			"for", delta.JsonObjectOf("name", p.ForName, "role", p.ForRole), "link", p.Link, "editor", p.Editor, "viewer", viewer)
	}
	return delta.JsonObjectOf("kind", principal.principalKind())
}

// FromChord converts a principal Chord stamped on a link back to a p2 principal; a principal that is not one of p2's
// is an error (links only carry what p2's own code stamped).
func FromChord(principal data.Principal) (Principal, error) {
	if principal == nil {
		return nil, fmt.Errorf("p2 principal: none")
	}
	text := func(key string) (string, error) {
		value, _ := principal.Get(key)
		s, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("p2 principal: %q is not a string", key)
		}
		return s, nil
	}
	number := func(key string) (int, error) {
		value, _ := principal.Get(key)
		n, ok := value.(float64)
		if !ok || n != float64(int(n)) {
			return 0, fmt.Errorf("p2 principal: %q is not an integer", key)
		}
		return int(n), nil
	}
	kind, err := text("kind")
	if err != nil {
		return nil, err
	}
	switch kind {
	case "owner":
		access, err := text("access")
		if err != nil {
			return nil, err
		}
		switch OwnerAccess(access) {
		case AccessLocal, AccessRelay:
			return OwnerPrincipal{Access: OwnerAccess(access)}, nil
		case AccessDevice:
			device, err := text("device")
			if err != nil {
				return nil, err
			}
			return OwnerPrincipal{Access: AccessDevice, Device: device}, nil
		}
		return nil, fmt.Errorf("p2 principal: unknown owner access %q", access)
	case "guest":
		guest := GuestPrincipal{Sessions: map[string]GuestMode{}}
		if guest.ID, err = text("id"); err != nil {
			return nil, err
		}
		if guest.Name, err = text("name"); err != nil {
			return nil, err
		}
		if guest.Home, err = text("home"); err != nil {
			return nil, err
		}
		if guest.Revision, err = number("revision"); err != nil {
			return nil, err
		}
		if value, ok := principal.Get("email"); ok {
			email, isText := value.(string)
			if !isText {
				return nil, fmt.Errorf("p2 principal: \"email\" is not a string")
			}
			guest.Email = &email
		}
		value, _ := principal.Get("sessions")
		sessions, ok := value.(*delta.JsonObject)
		if !ok {
			return nil, fmt.Errorf("p2 principal: \"sessions\" is not an object")
		}
		for id, mode := range sessions.All() {
			switch GuestMode(fmt.Sprint(mode)) {
			case GuestView, GuestFull:
				guest.Sessions[id] = GuestMode(fmt.Sprint(mode))
			default:
				return nil, fmt.Errorf("p2 principal: unknown guest mode %v", mode)
			}
		}
		return guest, nil
	case "worker":
		worker := WorkerPrincipal{}
		if worker.Session, err = text("session"); err != nil {
			return nil, err
		}
		if worker.Generation, err = number("generation"); err != nil {
			return nil, err
		}
		return worker, nil
	case "view":
		view := ViewPrincipal{}
		if view.Session, err = text("session"); err != nil {
			return nil, err
		}
		if view.View, err = text("view"); err != nil {
			return nil, err
		}
		if view.Opening, err = text("opening"); err != nil {
			return nil, err
		}
		mode, err := text("mode")
		if err != nil {
			return nil, err
		}
		if view.Mode = ViewMode(mode); view.Mode != ViewRead && view.Mode != ViewWrite {
			return nil, fmt.Errorf("p2 principal: unknown view mode %q", mode)
		}
		if view.Link, err = text("link"); err != nil {
			return nil, err
		}
		if view.Editor, err = text("editor"); err != nil {
			return nil, err
		}
		value, _ := principal.Get("for")
		who, ok := value.(*delta.JsonObject)
		if !ok {
			return nil, fmt.Errorf("p2 principal: \"for\" is not an object")
		}
		view.ForName, _ = who.Value("name").(string)
		view.ForRole, _ = who.Value("role").(string)
		if view.ForRole != "owner" && view.ForRole != "guest" {
			return nil, fmt.Errorf("p2 principal: unknown view role %q", view.ForRole)
		}
		value, _ = principal.Get("viewer")
		viewer, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("p2 principal: \"viewer\" is not a list")
		}
		for _, each := range viewer {
			revocation, ok := each.(*delta.JsonObject)
			if !ok {
				return nil, fmt.Errorf("p2 principal: a revocation is not an object")
			}
			view.Viewer = append(view.Viewer, revocation)
		}
		return view, nil
	case "server":
		return Server, nil
	case "host":
		return Host, nil
	}
	return nil, fmt.Errorf("p2 principal: unknown kind %q", kind)
}

// PrincipalKeyFunction is the principal key function of every p2 host (`createHost({ principalKey })`): the same
// function on every host, as Chord requires.
var PrincipalKeyFunction = data.NewPrincipalKeyFunction(func(principal data.Principal) string {
	p, err := FromChord(principal)
	if err != nil {
		// upstream's `default` branch: the key of a kind p2 has no case for is the kind itself.
		kind, _ := principal.Get("kind")
		return fmt.Sprint(kind)
	}
	return Key(p)
})

// ChordRevokedBy is the host's `revokedBy` option (`createHost({ revokedBy })`): RevokedBy on Chord's principal.
func ChordRevokedBy(principal data.Principal) ([]delta.JsonValue, error) {
	p, err := FromChord(principal)
	if err != nil {
		return nil, err
	}
	revocations := RevokedBy(p)
	out := make([]delta.JsonValue, len(revocations))
	for i, revocation := range revocations {
		out[i] = revocation
	}
	return out, nil
}

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
