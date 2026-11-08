// Package sharing ports apps/p2/plugins/sharing (server part): share links, sharing with people by email, the join page,
// guest authentication and live revocation. The TUI and web parts (tui.ts, web.tsx) belong to the UI lanes.
package sharing

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	sessioncontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	"github.com/MichaelKinsy/PiG/schema"
)

func cookieName(port int) string { return "p2_guest_" + strconv.Itoa(port) }

// shareURL is the URL of a share link: on the first tunnel host, or on loopback when there is none.
func shareURL(token string, info *delta.JsonObject) string {
	origin := ""
	if allowed, _ := info.Value("allowedHosts").([]any); len(allowed) > 0 {
		origin = fmt.Sprintf("https://%v", allowed[0])
	} else {
		port, _ := info.Value("port").(float64)
		origin = fmt.Sprintf("http://127.0.0.1:%d", int(port))
	}
	return origin + "/join/" + token
}

// NormalizeEmail is an email as Radius normalizes it (`normalizeEmail` in radius/packages/control-plane/src/utils/mail.ts),
// so that the address the owner typed matches the verified one Radius names. Keep the two in step. "" when it is no
// address.
func NormalizeEmail(email string) string {
	parts := strings.Split(jsstring.Trim(email), "@")
	if len(parts) != 2 || jsstring.Trim(parts[0]) == "" || parts[1] == "" {
		return ""
	}
	local := jsstring.ToLower(jsstring.Trim(parts[0]))
	domain := jsstring.ToLower(domainToASCII(parts[1]))
	if domain == "" || !strings.Contains(domain, ".") {
		return ""
	}
	if domain == "gmail.com" || domain == "googlemail.com" {
		return strings.ReplaceAll(local, ".", "") + "@gmail.com"
	}
	return local + "@" + domain
}

// domainToASCII is `url.domainToASCII`: the host of a special URL, "" for what is no host. A delimiter ends the host as it
// does in the URL parser.
func domainToASCII(domain string) string {
	if end := strings.IndexAny(domain, "/?#\\"); end >= 0 {
		domain = domain[:end]
	}
	ascii, err := nodeurl.SpecialHost(domain)
	if err != nil {
		return ""
	}
	return ascii
}

// sendPage sends a server-rendered page; its one inline script is allowed by a per-response nonce.
func sendPage(response http.ResponseWriter, request *http.Request, status int, title, heading, body, script string) error {
	random := make([]byte, 18)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	nonce := base64.RawURLEncoding.EncodeToString(random)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; script-src 'nonce-%s'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'", nonce))
	response.WriteHeader(status)
	if request.Method == http.MethodHead {
		return nil
	}
	_, err := io.WriteString(response, p2runtime.Page(p2runtime.PageOptions{Title: title, Heading: heading, Body: body, Script: script, Nonce: nonce}))
	return err
}

const joinScript = `document.getElementById("join").onsubmit=async(e)=>{e.preventDefault();const status=document.getElementById("status");try{const r=await fetch("/join",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({token:location.pathname.split("/").pop(),name:document.getElementById("name").value})});const v=await r.json();if(!r.ok)throw new Error(v.error);location.replace("/s/"+v.session);}catch(e){status.className="error";status.textContent=e.message;}};`

// readJSON reads a request's JSON body of at most limit bytes.
func readJSON(request *http.Request, limit int) (any, error) {
	body, err := io.ReadAll(io.LimitReader(request.Body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, &JoinError{"Too large"}
	}
	return chordjson.Decode(body)
}

// archivedState is the archived sessions as the routine below last saw them (null before it first saw the catalog; in
// memory).
var archivedState = func() *documents.StateDefinition {
	definition, err := documents.DefineState("p2.shares.archived", documents.StateOptions{
		Schema:  p2runtime.Contract(schema.Obj(schema.Properties{schema.Prop("sessions", schema.Union([]schema.Schema{schema.Arr(schema.Str()), schema.Nul()}))})),
		Version: 1,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("sessions", nil) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func objectArgs(args delta.JsonValue) *delta.JsonObject {
	if object := objectOf(args); object != nil {
		return object
	}
	return delta.NewJsonObject(0)
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}

func linkJSON(link Link) *delta.JsonObject {
	return delta.JsonObjectOf("id", link.ID, "session", link.Session, "mode", link.Mode, "token", link.Token)
}

// serveShares serves Shares.
func serveShares(facet host.FacetHost) error {
	return facet.Serve(contract.Shares, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		catalog := self.Get(sessioncontract.Catalog, delta.NewJsonObject(0))
		children := self.Get(subagentscontract.ChildStore, delta.NewJsonObject(0))
		server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
		archived := self.State(archivedState).(*documents.Singleton)
		opened := self.Open(func(ctx context.Context) (any, error) {
			info, err := server.Member("info").Value()
			if err != nil {
				return nil, err
			}
			return OpenShareDB(ctx, stringOf(objectOf(info).Value("dataDir")))
		}, func(value any, _ host.NativeCloseReason, _ context.Context) error { return value.(*ShareDB).Close() })
		db := func() *ShareDB {
			value, err := opened.Value()
			if err != nil {
				panic(err)
			}
			return value.(*ShareDB)
		}
		// The database's links, people and email grants as it holds them: a method's write tells this source before the
		// method returns.
		stored := p2runtime.FollowNative(self,
			func(changed func()) func() { return db().Subscribe(changed) },
			func() (delta.JsonValue, error) {
				//portlint:allow ctxbackground the read runs on the database's change listener (a method's write or the opener), which has no caller context; it ends with the database
				ctx := context.Background()
				links, err := db().Links(ctx)
				if err != nil {
					return nil, err
				}
				people, err := db().People(ctx)
				if err != nil {
					return nil, err
				}
				emails, err := db().EmailGrants(ctx)
				if err != nil {
					return nil, err
				}
				linkRows, personRows, emailRows := make([]any, len(links)), make([]any, len(people)), make([]any, len(emails))
				for i, link := range links {
					linkRows[i] = linkJSON(link)
				}
				for i, person := range people {
					personRows[i] = delta.JsonObjectOf("id", person.ID, "name", person.Name, "session", person.Session, "mode", person.Mode)
				}
				for i, grant := range emails {
					emailRows[i] = delta.JsonObjectOf("email", grant.Email, "session", grant.Session)
				}
				return delta.JsonObjectOf("links", linkRows, "people", personRows, "emails", emailRows), nil
			},
		)
		// open is whether the session is known and not archived.
		open := func(session string, ctx context.Context) bool {
			found, err := catalog.Call("get", delta.JsonObjectOf("id", session), ctx)
			row := objectOf(found)
			return err == nil && row != nil && row.Value("archivedAt") == nil
		}
		// principalOf is a guest's principal: its grants on sessions that are open now; nil when there are none.
		principalOf := func(row *GuestRow, ctx context.Context) (delta.JsonValue, error) {
			if row == nil {
				return nil, nil
			}
			grants, err := db().Grants(ctx, row.ID)
			if err != nil {
				return nil, err
			}
			sessions := delta.NewJsonObject(len(grants))
			home, homeAt := "", int64(0)
			for _, grant := range grants {
				if grant.Removed || !open(grant.Session, ctx) {
					continue
				}
				sessions.Set(grant.Session, grant.Mode)
				if home == "" || grant.GrantedAt >= homeAt {
					home, homeAt = grant.Session, grant.GrantedAt
				}
			}
			if home == "" {
				return nil, nil
			}
			principal := delta.JsonObjectOf("kind", "guest", "id", row.ID, "name", row.Name)
			if row.Email != nil {
				principal.Set("email", *row.Email)
			}
			principal.Set("home", home)
			principal.Set("sessions", sessions)
			principal.Set("revision", float64(row.Revision))
			return principal, nil
		}
		// revoke ends the objects of guests whose grants changed, so that they continue with their new principal.
		revoke := func(guests []string, ctx context.Context) error {
			for _, id := range guests {
				if _, err := server.Call("revokeGuests", delta.JsonObjectOf("id", id), ctx); err != nil {
					return err
				}
			}
			return nil
		}
		// confirmRemote refuses read/write access to a session on a remote environment the owner did not confirm.
		confirmRemote := func(session, mode string, confirmed bool, ctx context.Context) error {
			if !open(session, ctx) {
				return errors.New("Session not found or archived")
			}
			row, err := catalog.Call("get", delta.JsonObjectOf("id", session), ctx)
			if err != nil {
				return err
			}
			stored, err := children.Member("children").Value()
			if err != nil {
				return err
			}
			// A subagent's own location may be remote while the session is not: guests reach it as well.
			list, _ := objectOf(stored).Value(session).([]any)
			if remote := subagentscontract.RemoteEnvironments(objectOf(row), list); mode == "full" && len(remote) > 0 && !confirmed {
				return errors.New("Confirm first: guests with send access can run commands on this remote host")
			}
			return nil
		}
		// Archive ends every guest's access to the session, restore gives it back: both change their grants.
		self.On(catalog.Member("sessions"), func(call chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
			return host.AwaitingCtx(ctx, func() error {
				now := []any{}
				sessions, _ := call.Value.([]any)
				for _, each := range sessions {
					if row := objectOf(each); row != nil && row.Value("archivedAt") != nil {
						now = append(now, row.Value("id"))
					}
				}
				current, err := archived.Value()
				if err != nil {
					return err
				}
				previous, known := current.Value("sessions").([]any)
				if _, err := archived.Change(ctx, func(draft *delta.Object) (any, error) { return nil, draft.Set("sessions", now) }); err != nil {
					return err
				}
				if !known {
					return nil
				}
				var changed []string
				for _, id := range now {
					if !slices.Contains(previous, id) {
						changed = append(changed, stringOf(id))
					}
				}
				for _, id := range previous {
					if !slices.Contains(now, id) {
						changed = append(changed, stringOf(id))
					}
				}
				for _, session := range changed {
					guests, err := db().Bump(ctx, session)
					if err != nil {
						return err
					}
					if err := revoke(guests, ctx); err != nil {
						return err
					}
				}
				return nil
			})
		})
		linksOf := self.Derive([]any{stored}, func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value("links"), nil })
		peopleOf := self.Derive([]any{stored}, func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value("people"), nil })
		emailsOf := self.Derive([]any{stored, catalog.Member("sessions")}, func(values []any) (delta.JsonValue, error) {
			archivedNow := map[string]struct{}{}
			sessions, _ := values[1].([]any)
			for _, each := range sessions {
				if row := objectOf(each); row != nil && row.Value("archivedAt") != nil {
					archivedNow[stringOf(row.Value("id"))] = struct{}{}
				}
			}
			grants := []EmailGrant{}
			held, _ := objectOf(values[0]).Value("emails").([]any)
			for _, each := range held {
				grants = append(grants, EmailGrant{Email: stringOf(objectOf(each).Value("email")), Session: stringOf(objectOf(each).Value("session"))})
			}
			emails := OpenEmails(grants, func(session string) bool { _, gone := archivedNow[session]; return !gone })
			rows := make([]any, len(emails))
			for i, email := range emails {
				rows[i] = email
			}
			return rows, nil
		})
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"links": linksOf, "emails": emailsOf, "people": peopleOf},
			Methods: map[string]contracts.MethodFunction{
				"authenticate": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					guest, err := db().GuestOfCookie(ctx, stringOf(objectArgs(args).Value("cookie")))
					if err != nil {
						return nil, err
					}
					return seen(ctx, db(), guest, principalOf)
				},
				"authenticateEmail": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					guest, err := db().GuestOfEmail(ctx, stringOf(objectArgs(args).Value("email")))
					if err != nil {
						return nil, err
					}
					return seen(ctx, db(), guest, principalOf)
				},
				"grant": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					guest, err := db().Guest(ctx, stringOf(objectArgs(args).Value("id")))
					if err != nil {
						return nil, err
					}
					return principalOf(guest, ctx)
				},
				"active": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					link, err := db().LinkByToken(ctx, stringOf(objectArgs(args).Value("token")))
					if err != nil || link == nil || !open(link.Session, ctx) {
						return nil, err
					}
					return linkJSON(*link), nil
				},
				"link": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					session, mode := stringOf(in.Value("session")), stringOf(in.Value("mode"))
					if err := confirmRemote(session, mode, in.Value("remoteConfirmed") == true, ctx); err != nil {
						return nil, err
					}
					ended, err := db().MakeLink(ctx, session, mode, in.Value("reset") == true)
					if err != nil {
						return nil, err
					}
					return nil, revoke(ended, ctx)
				},
				"share": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					session, email, mode := stringOf(in.Value("session")), stringOf(in.Value("email")), stringOf(in.Value("mode"))
					normalized := NormalizeEmail(email)
					if normalized == "" {
						return nil, errors.New("Enter an email address")
					}
					if err := confirmRemote(session, mode, in.Value("remoteConfirmed") == true, ctx); err != nil {
						return nil, err
					}
					shared, err := db().Share(ctx, session, normalized, jsstring.Trim(email), mode)
					if err != nil {
						return nil, err
					}
					if shared.Changed {
						return nil, revoke([]string{shared.Guest}, ctx)
					}
					return nil, nil
				},
				"remove": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					guestID := stringOf(in.Value("guestId"))
					removed, err := db().Remove(ctx, stringOf(in.Value("session")), guestID)
					if err != nil || !removed {
						return nil, err
					}
					return nil, revoke([]string{guestID}, ctx)
				},
				"join": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					link, err := db().LinkByToken(ctx, stringOf(in.Value("token")))
					if err != nil {
						return nil, err
					}
					if link == nil || !open(link.Session, ctx) {
						return nil, &JoinError{"This share has ended"}
					}
					// The same browser keeps its identity and gains the session; a removed one cannot join it again.
					var existing *GuestRow
					if cookie, ok := in.Value("cookie").(string); ok {
						if existing, err = db().GuestOfCookie(ctx, cookie); err != nil {
							return nil, err
						}
					}
					joined, err := db().Join(ctx, *link, stringOf(in.Value("name")), existing)
					if err != nil {
						return nil, err
					}
					if joined.Changed {
						if err := revoke([]string{joined.Guest}, ctx); err != nil {
							return nil, err
						}
					}
					answer := delta.JsonObjectOf("session", link.Session)
					if joined.Cookie != "" {
						answer.Set("cookie", joined.Cookie)
					}
					return answer, nil
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

// seen records that the guest was seen when it has a principal now, and returns the principal.
func seen(ctx context.Context, db *ShareDB, guest *GuestRow, principalOf func(*GuestRow, context.Context) (delta.JsonValue, error)) (delta.JsonValue, error) {
	principal, err := principalOf(guest, ctx)
	if err != nil || principal == nil {
		return nil, err
	}
	return principal, db.Seen(ctx, guest.ID)
}

// servePresence serves Presence and GuestPresence: the guests connected now are every guest principal holding a
// `GuestPresence` (one per tab; a link drop is a leaving).
func servePresence(facet host.FacetHost) error {
	holders := facet.References(contract.GuestPresence)
	if err := facet.Serve(contract.Presence, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		guests := self.Derive([]any{holders}, func(values []any) (delta.JsonValue, error) {
			connected := delta.NewJsonObject(0)
			references, _ := values[0].([]any)
			for _, reference := range references {
				principal, ok := objectOf(reference).Value("principal").(*delta.JsonObject)
				if !ok {
					continue
				}
				who, err := p2runtime.FromChord(principal)
				if err != nil {
					continue
				}
				if guest, ok := who.(p2runtime.GuestPrincipal); ok {
					sessions := delta.NewJsonObject(len(guest.Sessions))
					for _, id := range slices.Sorted(mapsKeys(guest.Sessions)) {
						sessions.Set(id, string(guest.Sessions[id]))
					}
					connected.Set(guest.ID, delta.JsonObjectOf("id", guest.ID, "name", guest.Name, "sessions", sessions))
				}
			}
			return connected, nil
		})
		return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"guests": guests}}}, nil
	}), host.ServeOptions{}); err != nil {
		return err
	}
	return facet.Serve(contract.GuestPresence, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, _ host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		if err != nil {
			return contracts.Implementation{}, p2runtime.Forbidden("owners and guests only")
		}
		switch who.(type) {
		case p2runtime.OwnerPrincipal:
			return contracts.Implementation{Variant: "owner"}, nil
		case p2runtime.GuestPrincipal:
			return contracts.Implementation{Variant: "guest"}, nil
		}
		return contracts.Implementation{}, p2runtime.Forbidden("owners and guests only")
	}), host.ServeOptions{})
}

func mapsKeys[V any](m map[string]V) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for key := range m {
			if !yield(key) {
				return
			}
		}
	}
}

// serveSharing serves Sharing: one session's links, people and connected guests, for its owner.
func serveSharing(facet host.FacetHost) error {
	return facet.Serve(contract.Sharing, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		session := stringOf(params.Value("session"))
		who, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.IsOwner(who) {
			return contracts.Implementation{}, p2runtime.Forbidden("sharing is owner-only")
		}
		shares := self.Get(contract.Shares, delta.NewJsonObject(0))
		presence := self.Get(contract.Presence, delta.NewJsonObject(0))
		server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
		catalog := self.Get(sessioncontract.Catalog, delta.NewJsonObject(0))
		children := self.Get(subagentscontract.ChildStore, delta.NewJsonObject(0))
		// Every remote environment a read/write guest can reach: the session's and its subagents' own.
		shareURLs := self.Registry(p2runtime.ShareUrls.Registry)
		env := self.Derive([]any{catalog.Member("sessions"), children.Member("children")}, func(values []any) (delta.JsonValue, error) {
			var row *delta.JsonObject
			sessions, _ := values[0].([]any)
			for _, each := range sessions {
				if candidate := objectOf(each); candidate != nil && candidate.Value("id") == session {
					row = candidate
					break
				}
			}
			list, _ := objectOf(values[1]).Value(session).([]any)
			if remote := subagentscontract.RemoteEnvironments(row, list); len(remote) > 0 {
				return strings.Join(remote, ", "), nil
			}
			return nil, nil
		})
		linksView := self.Derive([]any{shares.Member("links"), server.Member("info")}, func(values []any) (delta.JsonValue, error) {
			rows := []any{}
			links, _ := values[0].([]any)
			for _, each := range links {
				link := objectOf(each)
				if link.Value("session") == session {
					rows = append(rows, delta.JsonObjectOf("mode", link.Value("mode"), "token", link.Value("token"), "url", shareURL(stringOf(link.Value("token")), objectOf(values[1]))))
				}
			}
			return rows, nil
		})
		peopleView := self.Derive([]any{shares.Member("people")}, func(values []any) (delta.JsonValue, error) {
			rows := []any{}
			people, _ := values[0].([]any)
			for _, each := range people {
				person := objectOf(each)
				if person.Value("session") == session {
					rows = append(rows, delta.JsonObjectOf("id", person.Value("id"), "name", person.Value("name"), "mode", person.Value("mode")))
				}
			}
			return rows, nil
		})
		// Read when the people change; a brain learns its Radius page from the owner's first visit through Radius.
		peopleURL := self.Derive([]any{shares.Member("people"), shareURLs}, func(values []any) (delta.JsonValue, error) {
			table, err := p2runtime.ShareUrls.Table(shareURLs)
			if err != nil {
				return nil, err
			}
			for _, key := range table.Keys() {
				url, _ := table.Get(key)
				if value, ok := url(session); ok {
					return value, nil
				}
			}
			return nil, nil
		})
		guestsView := self.Derive([]any{presence.Member("guests")}, func(values []any) (delta.JsonValue, error) {
			type guestRow struct{ id, name, mode string }
			var rows []guestRow
			connected := objectOf(values[0])
			for _, key := range connected.Keys() {
				guest := objectOf(connected.Value(key))
				if mode, ok := objectOf(guest.Value("sessions")).Value(session).(string); ok {
					rows = append(rows, guestRow{stringOf(guest.Value("id")), stringOf(guest.Value("name")), mode})
				}
			}
			slices.SortStableFunc(rows, func(a, b guestRow) int { return p2runtime.LocaleCompare(a.name, b.name) })
			out := make([]any, len(rows))
			for i, row := range rows {
				out[i] = delta.JsonObjectOf("id", row.id, "name", row.name, "mode", row.mode)
			}
			return out, nil
		})
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"env": env, "links": linksView, "guests": guestsView, "people": peopleView, "peopleUrl": peopleURL,
			},
			Methods: map[string]contracts.MethodFunction{
				"link": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					call := delta.JsonObjectOf("session", session, "mode", in.Value("mode"), "reset", in.Value("reset") == true)
					if in.Value("remoteConfirmed") == true {
						call.Set("remoteConfirmed", true)
					}
					return shares.Call("link", call, ctx)
				},
				"share": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					call := delta.JsonObjectOf("session", session, "email", in.Value("email"), "mode", in.Value("mode"))
					if in.Value("remoteConfirmed") == true {
						call.Set("remoteConfirmed", true)
					}
					return shares.Call("share", call, ctx)
				},
				"remove": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return shares.Call("remove", delta.JsonObjectOf("session", session, "guestId", objectArgs(args).Value("guestId")), ctx)
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

// serveHTTP contributes the join routes, the guest cookie and Radius authenticators, and guest session access
// (`p2.sharing-http`).
func serveHTTP(facet host.FacetHost) error {
	return p2runtime.Contribute(facet, "p2.sharing-http", func(self host.StoreSelf) error {
		shares := self.Get(contract.Shares, delta.NewJsonObject(0))
		server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
		catalog := self.Get(sessioncontract.Catalog, delta.NewJsonObject(0))
		dataDir := func() (string, error) {
			info, err := server.Member("info").Value()
			return stringOf(objectOf(info).Value("dataDir")), err
		}
		joinPage := func(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, ctx host.Ctx) error {
			segments := strings.Split(request.URL.EscapedPath(), "/")
			token := ""
			if len(segments) > 2 {
				token = segments[2]
			}
			found, err := shares.Call("active", delta.JsonObjectOf("token", token), ctx)
			if err != nil {
				return err
			}
			link := objectOf(found)
			if link == nil {
				return sendPage(response, request, http.StatusGone, "Share ended", "This share has ended",
					"<p>The link was reset or the session was archived. Ask the owner for a new one.</p>", "")
			}
			// The owner, or a guest who already joined through this link, goes straight to the session. As in Pi Mobile, an
			// owner only by its cookie (or relay): a cookieless loopback browser (a fresh profile opening the link) is not
			// the device owner here, but someone joining.
			owner := false
			if who, ok := access.Principal.(p2runtime.OwnerPrincipal); ok {
				owner = who.Access != p2runtime.AccessLocal
				if !owner {
					directory, err := dataDir()
					if err != nil {
						return err
					}
					if owner, err = p2runtime.HasLoopbackCookie(request, access.Origin.Port, directory); err != nil {
						return err
					}
				}
			}
			session := stringOf(link.Value("session"))
			if guest, ok := access.Principal.(p2runtime.GuestPrincipal); owner || (ok && string(guest.Sessions[session]) == stringOf(link.Value("mode"))) {
				response.Header().Set("Location", "/s/"+session)
				response.WriteHeader(http.StatusSeeOther)
				return nil
			}
			row, err := catalog.Call("get", delta.JsonObjectOf("id", session), ctx)
			if err != nil {
				return err
			}
			grants := "You can read along, but not send messages."
			if link.Value("mode") == "full" {
				grants = "You can read, send messages and uploads, and stop runs. This lets you make the agent run commands on the host."
			}
			return sendPage(response, request, http.StatusOK, "Join shared session", stringOf(objectOf(row).Value("title")),
				"<p>A session has been shared with you. "+grants+" Enter your name to join; names are not verified.</p><form id=\"join\"><label for=\"name\">Your name</label><input id=\"name\" maxlength=\"40\" required autocomplete=\"nickname\"><button>Join session</button></form><p id=\"status\" role=\"status\"></p>",
				joinScript)
		}
		join := func(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, ctx host.Ctx) error {
			// A state-changing request: exactly same-origin (a cross-site form cannot send this Origin).
			if !p2runtime.FromOrigin(request, access.Origin.Origin) {
				return p2runtime.SendJSON(response, http.StatusForbidden, map[string]any{"error": "Invalid origin"}, nil)
			}
			message, status, headers := "", http.StatusOK, map[string]string(nil)
			var session string
			err := func() error {
				input, err := readJSON(request, 2048)
				if err != nil {
					return err
				}
				fields := objectOf(input)
				token, okToken := fields.Value("token").(string)
				name, okName := fields.Value("name").(string)
				if fields == nil || !okToken || !okName {
					return &JoinError{"Invalid join"}
				}
				call := delta.JsonObjectOf("token", token, "name", name)
				if cookie, ok := p2runtime.CookieValue(request, cookieName(access.Origin.Port)); ok && cookie != "" {
					call.Set("cookie", cookie)
				}
				joined, err := shares.Call("join", call, ctx)
				if err != nil {
					return err
				}
				session = stringOf(objectOf(joined).Value("session"))
				if cookie, ok := objectOf(joined).Value("cookie").(string); ok {
					secure := ""
					if access.Origin.Remote {
						secure = "; Secure"
					}
					headers = map[string]string{"Set-Cookie": cookieName(access.Origin.Port) + "=" + cookie + "; HttpOnly; SameSite=Strict; Path=/; Max-Age=2592000" + secure}
				}
				return nil
			}()
			if err != nil {
				status, message = http.StatusBadRequest, "Could not join this session."
				var failure *JoinError
				if errors.As(err, &failure) {
					message = failure.Message
				}
				return p2runtime.SendJSON(response, status, map[string]any{"error": message}, nil)
			}
			return p2runtime.SendJSON(response, status, map[string]any{"session": session}, headers)
		}
		// Through Radius, people are shared with by email and carry no cookie, so links do not apply there.
		refuseRelayed := func(response http.ResponseWriter, request *http.Request) error {
			_, _ = io.Copy(io.Discard, request.Body)
			response.Header().Set("Content-Type", "text/plain; charset=utf-8")
			response.WriteHeader(http.StatusForbidden)
			_, err := io.WriteString(response, "Share links do not work through Radius. Ask the owner to share the session with your email.\n")
			return err
		}
		relayed := func(handler p2runtime.RouteHandler) p2runtime.RouteHandler {
			return func(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, ctx host.Ctx) error {
				if access.Origin.RelayUser != nil {
					return refuseRelayed(response, request)
				}
				return handler(response, request, access, ctx)
			}
		}
		p2runtime.ContributeKeyed(self, p2runtime.Routes,
			p2runtime.Entry("GET /join/*", relayed(joinPage)),
			p2runtime.Entry("HEAD /join/*", relayed(joinPage)),
			p2runtime.Entry("POST /join", relayed(join)),
		)
		guestOf := func(grant delta.JsonValue) (p2runtime.Principal, error) {
			if grant == nil {
				return nil, nil
			}
			return p2runtime.FromChord(grant.(*delta.JsonObject))
		}
		p2runtime.ContributeKeyed(self, p2runtime.Authenticators, p2runtime.Entry("guest", p2runtime.Authenticator(
			func(request *http.Request, origin p2runtime.RequestOrigin, ctx host.Ctx) (p2runtime.Principal, error) {
				if origin.RelayUser != nil {
					grant, err := shares.Call("authenticateEmail", delta.JsonObjectOf("email", origin.RelayUser.Email), ctx)
					if err != nil {
						return nil, err
					}
					return guestOf(grant)
				}
				cookie, ok := p2runtime.CookieValue(request, cookieName(origin.Port))
				if !ok {
					return nil, nil
				}
				grant, err := shares.Call("authenticate", delta.JsonObjectOf("cookie", cookie), ctx)
				if err != nil {
					return nil, err
				}
				return guestOf(grant)
			})))
		p2runtime.ContributeKeyed(self, p2runtime.SessionAccess, p2runtime.Entry("guest", p2runtime.SessionAuthorizer(
			// A guest reaches the sessions shared with it while its principal (revision included) is current.
			func(principal p2runtime.Principal, session string, ctx host.Ctx) (bool, error) {
				guest, ok := principal.(p2runtime.GuestPrincipal)
				if !ok {
					return false, nil
				}
				if _, shared := guest.Sessions[session]; !shared {
					return false, nil
				}
				current, err := shares.Call("grant", delta.JsonObjectOf("id", guest.ID), ctx)
				if err != nil || current == nil {
					return false, err
				}
				return objectOf(current).Value("revision") == float64(guest.Revision), nil
			})))
		return nil
	})
}

// Server is the sharing plugin's server facet.
var Server = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	for _, serve := range []func(host.FacetHost) error{serveShares, servePresence, serveSharing, serveHTTP} {
		if err := serve(facet); err != nil {
			return err
		}
	}
	return nil
})
