// Package discord is p2's Discord plugin (apps/p2/plugins/discord): the owner's bots (tokens in `<data>/discord.json`), the
// links of sessions to channels with their cursors, threads and pending replies (persisted), the relay between them, and
// the owner's `Discord` view (server facet); and what the server hands a session's worker (worker facet).
package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/discord/contract"
	sessioncontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	envcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/discord/server.ts.

// typingEvery is how often "typing" is refreshed while a delivery is under way or a reply pending (Discord shows it about
// 10 s), and checkEvery every how many of those pending turns are checked anyway.
const (
	typingEvery = 8 * time.Second
	checkEvery  = 2
)

func mustState(name string, options documents.StateOptions) *documents.StateDefinition {
	definition, err := documents.DefineState(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

// state is the links and pending replies, persisted. 2: several bots; links and pending replies name theirs (the first
// release's one bot is `FirstBot`).
var state = mustState("p2.discord-bindings", documents.StateOptions{
	Schema: p2runtime.Contract(schema.Obj(schema.Properties{
		schema.Prop("bindings", p2runtime.Record(contract.BindingSchema)), schema.Prop("pending", p2runtime.Record(contract.PendingSchema)),
	})),
	Version: 2,
	Initial: func() *delta.JsonObject {
		return delta.JsonObjectOf("bindings", delta.NewJsonObject(0), "pending", delta.NewJsonObject(0))
	},
	Migrate: func(old *delta.JsonObject, from int) (*delta.JsonObject, error) {
		if from != 1 {
			return nil, fmt.Errorf("Unknown p2.discord-bindings version %d", from)
		}
		named := func(entries *delta.JsonObject) *delta.JsonObject {
			out := delta.NewJsonObject(0)
			if entries == nil {
				return out
			}
			for key, value := range entries.All() {
				entry, _ := value.(*delta.JsonObject)
				copy := entry.Clone()
				copy.Set("botId", contract.FirstBot)
				out.Set(key, copy)
			}
			return out
		}
		bindings, _ := old.Value("bindings").(*delta.JsonObject)
		pending, _ := old.Value("pending").(*delta.JsonObject)
		return delta.JsonObjectOf("bindings", named(bindings), "pending", named(pending)), nil
	},
	Persist:        true,
	CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return true },
})

func objectOf(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

func argsOf(args delta.JsonValue) *delta.JsonObject {
	if object := objectOf(args); object != nil {
		return object
	}
	return delta.NewJsonObject(0)
}

func str(object *delta.JsonObject, key string) string { s, _ := object.Value(key).(string); return s }

// roundTrip converts a value to its JSON form and back into out (typed state from documents and the reverse).
func roundTrip(value, out any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	//perf:ok cold: a link or a state change
	return json.Unmarshal(encoded, out) //portlint:allow jsonclone typed value converted from its stored JSON form, cold
}

func objectFrom(value any) (*delta.JsonObject, error) {
	var out *delta.JsonObject
	return out, roundTrip(value, &out)
}

// storeState reads the persisted relay state. The read is a section of the host's loop: the document is sealed in one when the
// object ends, and the relay's routines read it from goroutines of their own (Relay.Typing, the pending check).
func storeState(doc *documents.Singleton) contract.State {
	var value *delta.JsonObject
	var err error
	doc.Slot.Owner().Do(func() { value, err = doc.Value() })
	out := contract.State{Bindings: map[string]*contract.Binding{}, Pending: map[string]*contract.Pending{}}
	if err != nil {
		return out
	}
	_ = roundTrip(value, &out)
	if out.Bindings == nil {
		out.Bindings = map[string]*contract.Binding{}
	}
	if out.Pending == nil {
		out.Pending = map[string]*contract.Pending{}
	}
	return out
}

type docState struct{ doc *documents.Singleton }

func (s docState) Value() contract.State { return storeState(s.doc) }

// Change applies update to the state in one change of the document.
func (s docState) Change(ctx context.Context, update func(draft *contract.State)) error {
	_, err := s.doc.Change(ctx, func(draft *delta.Object) (any, error) {
		current := contract.State{}
		if err := roundTrip(draft.Snapshot(), &current); err != nil {
			return nil, err
		}
		update(&current)
		if current.Bindings == nil {
			current.Bindings = map[string]*contract.Binding{}
		}
		if current.Pending == nil {
			current.Pending = map[string]*contract.Pending{}
		}
		bindings, err := objectFrom(current.Bindings)
		if err != nil {
			return nil, err
		}
		pending, err := objectFrom(current.Pending)
		if err != nil {
			return nil, err
		}
		if err := draft.Set("bindings", bindings); err != nil {
			return nil, err
		}
		return nil, draft.Set("pending", pending)
	})
	return err
}

// statusJSON is a bot's status as `BotStatusSchema` has it.
func statusJSON(status contract.BotStatus) *delta.JsonObject {
	switch status.State {
	case "error":
		return delta.JsonObjectOf("state", "error", "error", status.Error)
	case "connected":
		guilds := make([]any, len(status.Guilds))
		for i, guild := range status.Guilds {
			guilds[i] = delta.JsonObjectOf("id", guild.ID, "name", guild.Name)
		}
		out := delta.JsonObjectOf("state", "connected", "user", status.User, "applicationId", status.ApplicationID)
		if status.AvatarURL != "" {
			out.Set("avatarUrl", status.AvatarURL)
		}
		out.Set("inviteUrl", status.InviteURL)
		out.Set("guilds", guilds)
		return out
	}
	return delta.JsonObjectOf("state", "connecting")
}

func viewsJSON(views []contract.BotView) []any {
	out := make([]any, len(views))
	for i, view := range views {
		out[i] = delta.JsonObjectOf("id", view.ID, "status", statusJSON(view.Status))
	}
	return out
}

func errorText(err error) string { return err.Error() }

// sandboxConfig is the configuration of sandbox name (type "gondolin"), or nil.
func sandboxConfig(configs *delta.JsonObject, name string) *delta.JsonObject {
	if configs == nil {
		return nil
	}
	config := objectOf(configs.Value(name))
	if config == nil || config.Value("type") != "gondolin" {
		return nil
	}
	return config
}

var serverFacet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := facet.Serve(contract.DiscordStore, host.LocalFactory(serveStore), host.ServeOptions{}); err != nil {
		return err
	}
	return facet.Serve(contract.Discord, host.RemoteFactory(serveDiscord), host.ServeOptions{
		Accepts: contracts.AcceptsTable{{Name: "2", Value: contracts.VersionMappings{}}},
	})
})

func serveDiscord(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	who, err := p2runtime.FromChord(principal)
	if err != nil || !p2runtime.IsOwner(who) {
		return contracts.Implementation{}, p2runtime.Forbidden("Discord is the owner's")
	}
	store := self.Get(contract.DiscordStore, delta.NewJsonObject(0))
	forward := func(name string) contracts.MethodFunction {
		return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return store.Call(name, args, ctx) }
	}
	return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"bots": host.Expose(store.Member("bots")), "bindings": host.Expose(store.Member("bindings")),
		},
		Methods: map[string]contracts.MethodFunction{
			"addBot": forward("addBot"), "setBotToken": forward("setBotToken"), "removeBot": forward("removeBot"),
			"server": forward("server"), "mcpServers": forward("mcpServers"), "link": forward("link"),
			"unlink": forward("unlink"), "updateLink": forward("updateLink"),
		},
	}}, nil
}

func serveStore(_ *delta.JsonObject, raw host.Self) (contracts.Implementation, error) {
	self := raw.(host.StoreSelf)
	server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
	catalog := self.Get(sessioncontract.Catalog, delta.NewJsonObject(0))
	envs := self.Get(envcontract.EnvService, delta.NewJsonObject(0))
	locks := self.Get(sessioncontract.SessionLocks, delta.NewJsonObject(0))
	moves := self.Get(sessioncontract.SessionMoves, delta.NewJsonObject(0))
	statuses := self.Get(statuscontract.StatusStore, delta.NewJsonObject(0))
	natives := p2runtime.ServerOf(self)
	doc := self.State(state).(*documents.Singleton)
	dataDir := func() string {
		info, _ := server.Member("info").Value()
		return str(argsOf(info), "dataDir")
	}
	fleetNative := self.Open(
		func(context.Context) (any, error) { return contract.OpenFleet(dataDir()) },
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*contract.Fleet).Close()
			return nil
		},
	)
	fleet := func() *contract.Fleet {
		value, err := fleetNative.Value()
		if err != nil {
			panic(err)
		}
		return value.(*contract.Fleet)
	}
	row := func(session string) *sessioncontract.Session {
		value, _ := catalog.Member("sessions").Value()
		rows, _ := value.([]any)
		for _, each := range rows {
			if found := sessioncontract.SessionFromJSON(each); found.ID == session {
				return &found
			}
		}
		return nil
	}
	port := &sessionsPort{row: row, locks: locks, natives: natives, configs: func() *delta.JsonObject {
		value, _ := envs.Member("configs").Value()
		return objectOf(value)
	}}
	relay := contract.NewRelay(func(bot string) contract.Port {
		if found := fleet().Connected(bot); found != nil {
			return found
		}
		return nil
	}, port, docState{doc})
	// live is the context of the store's live loop: replies, deliveries and state changes run under it.
	var live context.Context
	liveCtx := func() context.Context {
		if live == nil {
			panic(errors.New("The Discord store is not live"))
		}
		return live
	}
	self.OnLive(func(ctx host.Ctx) *host.Handled {
		live = ctx
		return host.AwaitingCtx(ctx, func() error {
			// One event at a time, in the order the bots received them: a catch-up and a live mention never race.
			for {
				received, ok := fleet().Next(ctx)
				if !ok {
					return nil
				}
				var err error
				if received.Event.Message != nil {
					err = relay.OnMessage(ctx, received.Bot, *received.Event.Message)
				} else {
					err = relay.CatchUp(ctx, received.Bot)
				}
				if err != nil {
					fmt.Fprintln(os.Stderr, "p2 discord:", errorText(err))
				}
			}
		})
	})
	self.OnLive(func(ctx host.Ctx) *host.Handled {
		return host.AwaitingCtx(ctx, func() error {
			for tick := 1; ctx.Err() == nil; tick++ {
				select {
				case <-time.After(typingEvery):
				case <-ctx.Done():
					return nil
				}
				relay.Typing(ctx)
				if tick%checkEvery == 0 && len(storeState(doc).Pending) > 0 {
					_ = relay.CheckPending(ctx, nil)
				}
			}
			return nil
		})
	})
	// A session's status changed: its turn may have ended.
	seen := map[string]string{}
	self.On(statuses.Member("statuses"), func(call chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
		return host.AwaitingCtx(ctx, func() error {
			value := objectOf(call.Value)
			if value == nil {
				return nil
			}
			changed := map[string]bool{}
			next := map[string]string{}
			for session, each := range value.All() {
				status := objectOf(each)
				busy, _ := status.Value("busy").([]any)
				parts := make([]string, len(busy))
				for i, entry := range busy {
					parts[i] = fmt.Sprint(entry)
				}
				next[session] = fmt.Sprintf("%v %v %s", status.Value("running"), jsNum(status.Value("tasks")), strings.Join(parts, ","))
				if next[session] != seen[session] {
					changed[session] = true
				}
			}
			seen = next
			if live != nil && len(changed) > 0 && len(storeState(doc).Pending) > 0 {
				if err := relay.CheckPending(liveCtx(), changed); err != nil {
					fmt.Fprintln(os.Stderr, "p2 discord:", errorText(err))
				}
			}
			return nil
		})
	})
	// The bots' avatars for the owner's sheets: the page's CSP allows only its own images, so they come through here.
	avatars := newAvatars()
	p2runtime.ContributeKeyed(self, p2runtime.Routes, p2runtime.Entry("GET /discord/avatar", p2runtime.RouteHandler(
		func(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, _ host.Ctx) error {
			bot := request.URL.Query().Get("bot")
			avatar := ""
			for _, view := range fleet().Views() {
				if view.ID == bot && view.Status.State == "connected" {
					avatar = view.Status.AvatarURL
				}
			}
			if !p2runtime.IsOwner(access.Principal) || avatar == "" {
				response.WriteHeader(http.StatusNotFound)
				return nil
			}
			kind, bytes, err := avatars.get(avatar)
			if err != nil {
				response.WriteHeader(http.StatusBadGateway)
				return nil
			}
			header := response.Header()
			header.Set("Content-Type", kind)
			header.Set("Content-Length", fmt.Sprint(len(bytes)))
			header.Set("Cache-Control", "private, max-age=86400")
			header.Set("Cross-Origin-Resource-Policy", "same-origin")
			response.WriteHeader(http.StatusOK)
			_, err = response.Write(bytes)
			return err
		})))
	connected := func(bot string) (*contract.Bot, error) {
		found := fleet().Connected(bot)
		if found == nil {
			return nil, errors.New("The Discord bot is not connected")
		}
		return found, nil
	}
	// unlink drops session's link and lifts its lock; its agents stop being told they answer Discord.
	unlink := func(session string, ctx context.Context) error {
		if err := (docState{doc}).Change(ctx, func(draft *contract.State) {
			for channel, binding := range draft.Bindings {
				if binding.Session == session {
					delete(draft.Bindings, channel)
				}
			}
		}); err != nil {
			return err
		}
		if _, err := locks.Call("set", delta.JsonObjectOf("session", session, "lock", nil), ctx); err != nil {
			return err
		}
		// A session that is gone or archived has no worker to tell (and none to start).
		if found := row(session); found != nil && found.ArchivedAt == nil {
			if _, err := port.call(ctx, session, "unlinked", nil); err != nil {
				fmt.Fprintln(os.Stderr, "p2 discord: telling the session it is unlinked failed:", errorText(err))
			}
		}
		return nil
	}
	bindings := self.Derive([]any{doc}, func(values []any) (delta.JsonValue, error) {
		var current contract.State
		if err := roundTrip(values[0], &current); err != nil {
			return nil, err
		}
		list := make([]*contract.Binding, 0, len(current.Bindings))
		for _, binding := range current.Bindings {
			list = append(list, binding)
		}
		slices.SortStableFunc(list, func(a, b *contract.Binding) int {
			switch {
			case a.CreatedAt < b.CreatedAt:
				return -1
			case a.CreatedAt > b.CreatedAt:
				return 1
			}
			return strings.Compare(a.ChannelID, b.ChannelID)
		})
		out := make([]any, len(list))
		for i, binding := range list {
			object, err := objectFrom(binding)
			if err != nil {
				return nil, err
			}
			out[i] = object
		}
		return out, nil
	})
	bots := p2runtime.FollowNative(self,
		func(changed func()) func() { return fleet().Subscribe(changed) },
		func() (delta.JsonValue, error) { return viewsJSON(fleet().Views()), nil })
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"bots": bots, "bindings": bindings},
		Methods: map[string]contracts.MethodFunction{
			"addBot": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				id, err := fleet().Add(str(argsOf(args), "token"))
				if err != nil {
					return nil, err
				}
				return delta.JsonObjectOf("id", id), nil
			},
			"setBotToken": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				in := argsOf(args)
				return nil, fleet().SetToken(str(in, "bot"), str(in, "token"))
			},
			"removeBot": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				bot := str(argsOf(args), "bot")
				for _, binding := range storeState(doc).Bindings {
					if binding.BotID == bot {
						if err := unlink(binding.Session, ctx); err != nil {
							return nil, err
						}
					}
				}
				return nil, fleet().Remove(bot)
			},
			"server": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				in := argsOf(args)
				bot, err := connected(str(in, "bot"))
				if err != nil {
					return nil, err
				}
				details, err := bot.Server(str(in, "guild"))
				if err != nil {
					return nil, err
				}
				named := func(list []contract.NamedChannel) []any {
					out := make([]any, len(list))
					for i, each := range list {
						out[i] = delta.JsonObjectOf("id", each.ID, "name", each.Name)
					}
					return out
				}
				return delta.JsonObjectOf("channels", named(details.Channels), "roles", named(details.Roles)), nil
			},
			"mcpServers": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				text, err := p2runtime.OptionalText(filepath.Join(dataDir(), "mcp.json"))
				if err != nil {
					return nil, err
				}
				names := []any{}
				if text != nil && *text != "" {
					for _, name := range mcpServerNames(*text) {
						names = append(names, name)
					}
				}
				return names, nil
			},
			"link": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return nil, link(argsOf(args), ctx, doc, row, envs, locks, moves, connected)
			},
			"unlink": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return nil, unlink(str(argsOf(args), "session"), ctx)
			},
			"updateLink": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := argsOf(args)
				session := str(in, "session")
				linked := false
				for _, binding := range storeState(doc).Bindings {
					linked = linked || binding.Session == session
				}
				if !linked {
					return nil, errors.New("This session is not linked")
				}
				// Both apply at once: who may wake Pi is read at each message, the lock is mirrored into the session.
				if _, err := locks.Call("set", delta.JsonObjectOf("session", session, "lock", in.Value("lock")), ctx); err != nil {
					return nil, err
				}
				var wake contract.Wake
				if err := roundTrip(in.Value("wake"), &wake); err != nil {
					return nil, err
				}
				return nil, (docState{doc}).Change(ctx, func(draft *contract.State) {
					for _, binding := range draft.Bindings {
						if binding.Session == session {
							binding.Wake = wake
						}
					}
				})
			},
		},
	}}, nil
}

func jsNum(value any) string {
	number, _ := value.(float64)
	return fmt.Sprint(number)
}

// link links a session to a channel: moves it into the sandbox (as the owner's Relocate does), locks it, and listens.
func link(in *delta.JsonObject, ctx context.Context, doc *documents.Singleton, row func(string) *sessioncontract.Session,
	envs, locks, moves *host.Reference, connected func(string) (*contract.Bot, error)) error {
	session, bot, guild, channelID, sandbox := str(in, "session"), str(in, "bot"), str(in, "guild"), str(in, "channel"), str(in, "sandbox")
	current := storeState(doc)
	for _, binding := range current.Bindings {
		if binding.Session == session {
			return errors.New("This session is linked already: unlink it first")
		}
	}
	if current.Bindings[channelID] != nil {
		return errors.New("This channel is linked to another session")
	}
	value, _ := envs.Member("configs").Value()
	config := sandboxConfig(objectOf(value), sandbox)
	if config == nil {
		return fmt.Errorf("No sandbox %s", sandbox)
	}
	found := row(session)
	if found == nil || found.ArchivedAt != nil {
		return errors.New("No such session")
	}
	port, err := connected(bot)
	if err != nil {
		return err
	}
	channel, err := port.Channel(ctx, channelID)
	if err != nil {
		return err
	}
	if channel.Thread != nil || channel.GuildID != guild {
		return errors.New("Pick a channel of the server")
	}
	// As the owner's Relocate: the session works in the sandbox from now on, and cannot leave it.
	if found.Env == nil || *found.Env != sandbox {
		cwd := "/root"
		if mounts, ok := config.Value("mounts").([]any); ok && len(mounts) > 0 {
			if guest := str(argsOf(mounts[0]), "guest"); guest != "" {
				cwd = guest
			}
		}
		moved, err := moves.Call("relocate", delta.JsonObjectOf("id", session, "cwd", cwd, "env", sandbox), ctx)
		if err != nil {
			return err
		}
		if refused, ok := argsOf(moved).Value("refused").(string); ok && refused != "" {
			return fmt.Errorf("The session could not move into %s: %s", sandbox, refused)
		}
	}
	if _, err := locks.Call("set", delta.JsonObjectOf("session", session, "lock", in.Value("lock")), ctx); err != nil {
		return err
	}
	var wake contract.Wake
	if err := roundTrip(in.Value("wake"), &wake); err != nil {
		return err
	}
	return (docState{doc}).Change(ctx, func(draft *contract.State) {
		draft.Bindings[channelID] = &contract.Binding{
			BotID: bot, ChannelID: channelID, ChannelName: channel.Name, GuildID: channel.GuildID, GuildName: channel.GuildName,
			Session: session, Wake: wake,
			// The first mention delivers what comes after linking.
			Cursor: channel.LastMessageID, Threads: map[string]*contract.ThreadBinding{}, CreatedAt: float64(time.Now().UnixMilli()),
		}
	})
}

// mcpServerNames is the server names of mcp.json (a trust boundary: the owner's file; the MCP plugin reports what is wrong
// in it).
func mcpServerNames(text string) []string {
	text = strings.TrimPrefix(text, "\uFEFF")
	var value struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(p2runtime.StripJSONComments(text)), &value); err != nil {
		return nil
	}
	names := make([]string, 0, len(value.Servers))
	for name := range value.Servers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// avatars caches Discord avatars (images of Discord's CDN, a trust boundary: only images pass).
type avatars struct {
	mu    chanLock
	cache map[string]avatar
}

type avatar struct {
	kind  string
	bytes []byte
}

type chanLock chan struct{}

func newAvatars() *avatars { return &avatars{mu: make(chanLock, 1), cache: map[string]avatar{}} }

func (a *avatars) get(source string) (string, []byte, error) {
	a.mu <- struct{}{}
	defer func() { <-a.mu }()
	if cached, ok := a.cache[source]; ok {
		return cached.kind, cached.bytes, nil
	}
	if !strings.HasPrefix(source, "https://cdn.discordapp.com/") {
		return "", nil, errors.New("Not a Discord avatar")
	}
	if _, err := url.Parse(source); err != nil {
		return "", nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Get(source)
	if err != nil {
		return "", nil, err
	}
	defer response.Body.Close()
	kind := response.Header.Get("Content-Type")
	if response.StatusCode < 200 || response.StatusCode > 299 || !slices.Contains([]string{"image/png", "image/jpeg", "image/gif", "image/webp"}, kind) {
		return "", nil, errors.New("No avatar")
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", nil, err
	}
	a.cache[source] = avatar{kind, body}
	return kind, body, nil
}
