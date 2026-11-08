// Package voice is p2's voice mode (apps/p2/plugins/voice): browsers reach the speech service (voice-server, sts-server) only
// through p2, which signs it in and tells it who is talking (the token never leaves the server); and each session worker's
// per-conversation switch and voice. The browser's own half (engine, playback, barge-in, transcripts) is Pi's TypeScript,
// carried verbatim under web/upstream.
package voice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	voicecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/voice/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
)

// Ports apps/p2/plugins/voice/server.ts.
//
// Voice mode (server): `voice.json` in the data dir names the service and its token; without it voice mode is off. Each
// conversation is a voice room: the owner's browser has Pi's speech synthesized once for everyone, each person's microphone
// becomes their own messages and is relayed to the others. The owner and read/write guests of the session take part (a
// guest's grant is rechecked while its socket lives); only the owner speaks for Pi or sets the voice. View-only guests get
// nothing.
//
// The configuration is pushed, not asked for: VoiceService publishes the service's engines and default (the service's
// /voices), or null without a valid voice.json. While anyone holds it, the data dir is watched, and each change of voice.json
// (written, replaced, removed) fetches the voices again, so the composer's button follows it live.
//
//	WS   /voice/room?session=…&conversation=…  bridged to the service's /room (room p2-<session>-<conversation>)
//	POST /voice/sample  { engine?, voice?, speed } -> audio/wav of a short sample (the owner's settings sheet)
//	POST /voice/log     { client, events: [{ t, message }] } -> 204; each event becomes a `[voice …]` line of the log
//
// Forced Go differences. Node's `fs.watch` of the directory is fsnotify's; `ws` is gorilla/websocket (each bridged socket
// is two reader goroutines, one per side, that end with their socket; Node's `ws` maxPayload is the read limit); the
// browser's diagnostics go to os.Stdout, where Node's `console.log` does.

var (
	checkConfig   = compiled(configSchema)
	checkVoices   = compiled(voicecontract.VoicesSchema)
	checkLog      = compiled(voicecontract.LogSchema)
	checkSample   = compiled(voicecontract.SampleSchema)
	checkCommand  = compiled(voicecontract.RoomCommandSchema)
	checkPersonal = compiled(voicecontract.RoomPersonalSchema)
)

func compiled(s schema.Schema) func(value any) bool {
	validator, err := schema.Compile(s, schema.CompileOptions{})
	if err != nil {
		panic(err)
	}
	return func(value any) bool {
		ok, err := validator.Check(value)
		return err == nil && ok
	}
}

// configSchema is `voice.json`.
var configSchema = schema.Obj(schema.Properties{
	// The speech service's base URL, `https://voice.example.com`.
	schema.Prop("url", schema.Str(schema.Pattern("^https?://"))),
	schema.Prop("token", schema.Str(schema.MinLength(1))),
}, schema.Open())

type config struct{ url, token string }

// readConfig is `voice.json`, read on every request so edits apply at once; nil when missing or invalid.
func readConfig(dataDir string) (*config, error) {
	text, err := p2runtime.OptionalText(filepath.Join(dataDir, "voice.json"))
	if err != nil || text == nil {
		return nil, err
	}
	parsed, err := chordjson.Decode([]byte(p2runtime.StripJSONComments(*text)))
	if err != nil || !checkConfig(parsed) {
		return nil, nil
	}
	object := objectOf(parsed)
	url, _ := object.Value("url").(string)
	token, _ := object.Value("token").(string)
	return &config{url: strings.TrimRight(url, "/"), token: token}, nil
}

// fetchVoices is the service's engines and default (its `/voices`).
func fetchVoices(ctx context.Context, current *config) (*delta.JsonObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, current.url+"/voices", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+current.token)
	reply, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer reply.Body.Close()
	if reply.StatusCode < 200 || reply.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d", reply.StatusCode)
	}
	body, err := io.ReadAll(reply.Body)
	if err != nil {
		return nil, err
	}
	voices, err := chordjson.Decode(body)
	if err != nil || !checkVoices(voices) {
		return nil, errors.New("unexpected /voices answer")
	}
	return delta.JsonObjectOf("engines", objectOf(voices).Value("engines"), "default", objectOf(voices).Value("default")), nil
}

// settle is how long voice.json must be quiet before it is read (editors and `mv` change it in several steps).
const settle = 200 * time.Millisecond

// configChanges counts changes of `voice.json` (and refresh requests) and wakes the task waiting for the next one.
type configChanges struct {
	mu      sync.Mutex
	version int
	changed chan struct{}
	timer   *time.Timer
	closed  bool
	watcher *fsnotify.Watcher
	stopped chan struct{}
}

func newConfigChanges(dataDir string) (*configChanges, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	changes := &configChanges{changed: make(chan struct{}), watcher: watcher, stopped: make(chan struct{})}
	// The directory, not the file: the file may not exist yet, and a replaced or removed file ends a file watch.
	if err := watcher.Add(dataDir); err != nil {
		_ = watcher.Close()
		return nil, err
	}
	//portlint:allow golifetime ends when close closes the watcher
	go changes.watch()
	return changes, nil
}

func (c *configChanges) watch() {
	defer close(c.stopped)
	for {
		select {
		case event, ok := <-c.watcher.Events:
			if !ok {
				return
			}
			if filepath.Base(event.Name) != "voice.json" {
				continue
			}
			c.mu.Lock()
			if !c.closed {
				if c.timer != nil {
					c.timer.Stop()
				}
				c.timer = time.AfterFunc(settle, c.bump)
			}
			c.mu.Unlock()
		case err, ok := <-c.watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "[voice] Watching voice.json failed: %v\n", err)
		}
	}
}

func (c *configChanges) bump() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version++
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *configChanges) current() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

// next returns once the version is no longer seen (or the context ends, or the changes are closed).
func (c *configChanges) next(seen int, ctx context.Context) {
	c.mu.Lock()
	if c.version != seen || c.closed {
		c.mu.Unlock()
		return
	}
	changed := c.changed
	c.mu.Unlock()
	select {
	case <-changed:
	case <-ctx.Done():
	case <-c.stopped:
	}
}

func (c *configChanges) close() {
	c.mu.Lock()
	c.closed = true
	if c.timer != nil {
		c.timer.Stop()
	}
	c.mu.Unlock()
	_ = c.watcher.Close()
	<-c.stopped
}

func objectOf(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

// serviceState is the speech service's voices and why they are unknown (in memory).
var serviceState = func() *documents.StateDefinition {
	definition, err := documents.DefineState("p2.voice-service-store", documents.StateOptions{
		Schema: p2runtime.Contract(schema.Obj(schema.Properties{
			schema.Prop("voices", schema.Union([]schema.Schema{voicecontract.VoicesSchema, schema.Nul()})),
			schema.Prop("failure", schema.Union([]schema.Schema{schema.Str(), schema.Nul()})),
		})),
		Version: 2,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("voices", nil, "failure", nil) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// linger is how long a conversation's voice mode object stays live after its last reference.
var linger = 30 * time.Second

const (
	// maxSockets are the sockets one principal may hold per route (tabs and reconnects; more is abuse of the speech service).
	maxSockets = 4
	// guestRecheck is how often a guest's open socket checks that its grant still holds.
	guestRecheck = 30 * time.Second
	// maxBrowserFrame and maxServiceFrame are the largest frames either side of the bridge may send.
	maxBrowserFrame = 256 * 1024
	maxServiceFrame = 4 * 1024 * 1024
	// maxEarly are the commands kept for the service while its socket connects.
	maxEarly = 64
)

var serverFacet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := facet.Serve(voicecontract.VoiceServiceStore, host.LocalFactory(serveStore), host.ServeOptions{}); err != nil {
		return err
	}
	if err := facet.Serve(voicecontract.VoiceService, host.RemoteFactory(serveService), host.ServeOptions{}); err != nil {
		return err
	}
	return p2runtime.Contribute(facet, "p2.voice-routes", contributeRoutes)
})

func dataDirOf(server *host.Reference) string {
	info, _ := server.Member("info").Value()
	dir, _ := objectOf(info).Value("dataDir").(string)
	return dir
}

func serveStore(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
	service := self.State(serviceState).(documents.SingletonHandle)
	changes := self.Open(
		func(context.Context) (any, error) { return newConfigChanges(dataDirOf(server)) },
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*configChanges).close()
			return nil
		},
	)
	// Subscribed before reading, so no change is missed; a change during a fetch reads again.
	self.OnLive(func(ctx host.Ctx) *host.Handled {
		dir := dataDirOf(server)
		value, err := changes.Value()
		if err != nil {
			return host.AwaitingCtx(ctx, func() error { return err })
		}
		watched := value.(*configChanges)
		return host.AwaitingCtx(ctx, func() error {
			for ctx.Err() == nil {
				seen := watched.current()
				var voices *delta.JsonObject
				var failure any
				current, err := readConfig(dir)
				if err == nil && current != nil {
					voices, err = fetchVoices(ctx, current)
				}
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					voices, failure = nil, "Speech service unreachable: "+err.Error()
				}
				if watched.current() == seen {
					if _, err := service.Change(ctx, func(draft *delta.Object) (any, error) {
						if voices == nil {
							if err := draft.Set("voices", nil); err != nil {
								return nil, err
							}
						} else if err := draft.Set("voices", voices); err != nil {
							return nil, err
						}
						return nil, draft.Set("failure", failure)
					}); err != nil && ctx.Err() == nil {
						return err
					}
				}
				watched.next(seen, ctx)
			}
			return nil
		})
	})
	through := func(member string) chord.Watchable[delta.JsonValue] {
		return self.Derive([]any{service}, func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value(member), nil })
	}
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"voices": through("voices"), "failure": through("failure")},
		Methods: map[string]contracts.MethodFunction{
			"refresh": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				value, err := changes.Value()
				if err != nil {
					return nil, err
				}
				value.(*configChanges).bump()
				return nil, nil
			},
		},
	}}, nil
}

// serveService is served to the owner and read/write guests: guests' objects are per grant revision, so a revoked grant ends
// this one.
func serveService(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	who, err := p2runtime.FromChord(principal)
	if err != nil || !(p2runtime.IsOwner(who) || hasFullGrant(who)) {
		return contracts.Implementation{}, p2runtime.Forbidden("owners and read/write guests only")
	}
	store := self.Get(voicecontract.VoiceServiceStore, delta.NewJsonObject(0))
	// A service that was unreachable is asked again whenever someone starts looking.
	self.OnLive(func(ctx host.Ctx) *host.Handled {
		if failure, _ := store.Member("failure").Value(); failure == nil {
			return nil
		}
		return host.AwaitingCtx(ctx, func() error {
			_, err := store.Call("refresh", nil, ctx)
			return err
		})
	})
	through := func(member string) chord.Watchable[delta.JsonValue] {
		return self.Derive([]any{store.Member(member)}, func(values []any) (delta.JsonValue, error) { return values[0], nil })
	}
	return contracts.Implementation{Variant: "talker", Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"voices": through("voices"), "failure": through("failure")},
	}}, nil
}

func hasFullGrant(principal p2runtime.Principal) bool {
	guest, ok := principal.(p2runtime.GuestPrincipal)
	if !ok {
		return false
	}
	for _, mode := range guest.Sessions {
		if mode == p2runtime.GuestFull {
			return true
		}
	}
	return false
}

// readBody is the request's body up to limit bytes; ok is false beyond it.
func readBody(request *http.Request, limit int64) (body []byte, ok bool) {
	body, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	return body, err == nil && int64(len(body)) <= limit
}

func parseJSON(body []byte, ok bool) delta.JsonValue {
	if !ok {
		return nil
	}
	parsed, err := chordjson.Decode(body)
	if err != nil {
		return nil
	}
	return parsed
}

func refuse(response http.ResponseWriter, status int) {
	response.Header().Set("Connection", "close")
	response.WriteHeader(status)
}

var controlCharacters = regexp.MustCompile(`\p{Cc}`)

// sockets are the bridged browser sockets of one routes object, and how many each principal holds per route.
type sockets struct {
	mu     sync.Mutex
	closed bool
	done   chan struct{}
	open   map[string]int
	conns  map[*websocket.Conn]struct{}
}

func newSockets() *sockets {
	return &sockets{done: make(chan struct{}), open: map[string]int{}, conns: map[*websocket.Conn]struct{}{}}
}

// admit counts a socket against its principal's limit until release; false (and the socket closed) beyond it.
func (s *sockets) admit(route string, principal p2runtime.Principal, browser *websocket.Conn) (release func(), ok bool) {
	key := route + " " + p2runtime.Key(principal)
	s.mu.Lock()
	if s.closed || s.open[key] >= maxSockets {
		closed := s.closed
		s.mu.Unlock()
		if !closed {
			closeWith(browser, websocket.ClosePolicyViolation, "Too many voice connections")
		}
		_ = browser.Close()
		return nil, false
	}
	s.open[key]++
	s.conns[browser] = struct{}{}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.conns, browser)
		if left := s.open[key] - 1; left > 0 {
			s.open[key] = left
		} else {
			delete(s.open, key)
		}
	}, true
}

func (s *sockets) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.done)
	conns := make([]*websocket.Conn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

// closeWith sends a close frame (it needs no lock: control writes are safe beside data writes).
func closeWith(conn *websocket.Conn, code int, text string) {
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, text), time.Now().Add(time.Second))
}

var upgrader = websocket.Upgrader{
	// The server's dispatcher checked the request's origin (hosts.SameOrigin) before it reached a handler.
	CheckOrigin: func(*http.Request) bool { return true },
}

func contributeRoutes(self host.StoreSelf) error {
	server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
	access := self.Registry(p2runtime.SessionAccess.Registry)
	natives := p2runtime.ServerOf(self)
	models := self.Get(contract.ModelConfig, delta.NewJsonObject(0))
	config := func() (*config, error) { return readConfig(dataDirOf(server)) }
	// mayTalk is whether principal is the owner, or a guest `session` (by default its home) is shared with read/write, whose
	// grant is current.
	mayTalk := func(principal p2runtime.Principal, ctx host.Ctx, session string) bool {
		if p2runtime.IsOwner(principal) {
			return true
		}
		guest, ok := principal.(p2runtime.GuestPrincipal)
		if !ok {
			return false
		}
		target := session
		if target == "" {
			target = guest.Home
		}
		if !p2runtime.CanSend(principal, target) {
			return false
		}
		table, err := p2runtime.SessionAccess.Table(access)
		if err != nil {
			return false
		}
		admitted, err := p2runtime.MayReach(table, principal, target, ctx)
		return err == nil && admitted
	}
	// ownerName is settings.json's `ownerName`, read when the owner joins a room.
	ownerName := func() string {
		snapshot, err := models.Member("snapshot").Value()
		if err != nil {
			return p2runtime.DefaultOwnerName
		}
		if name, ok := objectOf(objectOf(snapshot).Value("settings")).Value("ownerName").(string); ok {
			return name
		}
		return p2runtime.DefaultOwnerName
	}
	rooms := self.Open(
		func(context.Context) (any, error) { return newSockets(), nil },
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*sockets).close()
			return nil
		},
	)

	// The browser's diagnostics: the phone's console is out of reach, the server's log (stdout) is not.
	logRoute := func(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, ctx host.Ctx) error {
		if !p2runtime.FromOrigin(request, access.Origin.Origin) || !mayTalk(access.Principal, ctx, "") {
			return p2runtime.SendJSON(response, http.StatusForbidden, map[string]any{"error": "Forbidden"}, nil)
		}
		// A guest's lines say whose they are (its client ID is its own choice).
		who := ""
		if guest, ok := access.Principal.(p2runtime.GuestPrincipal); ok {
			who = "guest:" + guest.ID + " "
		}
		body, within := readBody(request, voicecontract.MaxLogEvents*(4*voicecontract.MaxLogMessage+64)+1024)
		input := objectOf(parseJSON(body, within))
		if input == nil || !checkLog(input) {
			return p2runtime.SendJSON(response, http.StatusBadRequest, map[string]any{"error": "Expected { client, events: [{ t, message }] }"}, nil)
		}
		client, _ := input.Value("client").(string)
		events, _ := input.Value("events").([]any)
		for _, each := range events {
			event := objectOf(each)
			at, _ := event.Value("t").(float64)
			message, _ := event.Value("message").(string)
			fmt.Fprintf(os.Stdout, "[voice %s%s] %s %s\n", who, client, time.UnixMilli(int64(at)).UTC().Format("15:04:05.000"), controlCharacters.ReplaceAllString(message, " "))
		}
		response.WriteHeader(http.StatusNoContent)
		return nil
	}

	// The owner tries a voice: synthesized whole for this browser alone (the room would play it to everyone).
	sample := func(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, _ host.Ctx) error {
		if !p2runtime.IsOwner(access.Principal) || !p2runtime.FromOrigin(request, access.Origin.Origin) {
			return p2runtime.SendJSON(response, http.StatusForbidden, map[string]any{"error": "Forbidden"}, nil)
		}
		current, err := config()
		if err != nil {
			return err
		}
		if current == nil {
			return p2runtime.SendJSON(response, http.StatusServiceUnavailable, map[string]any{"error": "Voice is not configured (voice.json)"}, nil)
		}
		body, within := readBody(request, 4096)
		input := objectOf(parseJSON(body, within))
		if input == nil || !checkSample(input) {
			return p2runtime.SendJSON(response, http.StatusBadRequest, map[string]any{"error": "Expected { engine?, voice?, speed }"}, nil)
		}
		speak := delta.JsonObjectOf("text", voicecontract.SampleText)
		for key, value := range input.All() {
			speak.Set(key, value)
		}
		encoded, err := chordjson.Marshal(speak)
		if err != nil {
			return err
		}
		reply, audio, err := post(request.Context(), current, encoded)
		if err != nil {
			return p2runtime.SendJSON(response, http.StatusBadGateway, map[string]any{"error": "Speech service failed: " + err.Error()}, nil)
		}
		contentType := reply.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		response.Header().Set("Content-Type", contentType)
		response.Header().Set("Content-Length", strconv.Itoa(len(audio)))
		response.Header().Set("Cache-Control", "no-store")
		response.WriteHeader(reply.StatusCode)
		_, _ = response.Write(audio)
		return nil
	}
	p2runtime.ContributeKeyed(self, p2runtime.Routes,
		p2runtime.Entry("POST /voice/sample", p2runtime.RouteHandler(sample)),
		p2runtime.Entry("POST /voice/log", p2runtime.RouteHandler(logRoute)))

	// The conversation's voice room. The service trusts the identity headers because of the token, so they are p2's alone: set
	// from the authenticated principal, never taken from the browser's request.
	p2runtime.ContributeKeyed(self, p2runtime.Upgrades, p2runtime.Entry("/voice/room", p2runtime.UpgradeHandler(
		func(response http.ResponseWriter, request *http.Request, principal p2runtime.Principal, ctx host.Ctx) error {
			query := request.URL.Query()
			session := query.Get("session")
			conversation, valid := conversationOf(query.Get("conversation"))
			room := ""
			if valid {
				room = voicecontract.RoomName(session, conversation)
			}
			if room == "" {
				refuse(response, http.StatusBadRequest)
				return nil
			}
			if principal == nil || !mayTalk(principal, ctx, session) {
				refuse(response, http.StatusForbidden)
				return nil
			}
			current, err := config()
			if err != nil {
				return err
			}
			if current == nil {
				refuse(response, http.StatusServiceUnavailable)
				return nil
			}
			var participant participantOf
			switch who := principal.(type) {
			case p2runtime.OwnerPrincipal:
				participant = participantOf{id: "owner", name: ownerName(), role: "owner"}
			case p2runtime.GuestPrincipal:
				participant = participantOf{id: who.ID, name: who.Name, role: "guest"}
			default:
				refuse(response, http.StatusForbidden)
				return nil
			}
			if !participantID.MatchString(participant.id) {
				refuse(response, http.StatusForbidden)
				return nil
			}
			value, err := rooms.Value()
			if err != nil {
				return err
			}
			browser, err := upgrader.Upgrade(response, request, nil)
			if err != nil {
				return nil
			}
			browser.SetReadLimit(maxBrowserFrame)
			bridge := &bridge{
				browser: browser, sockets: value.(*sockets), config: current, room: room, participant: participant,
				owner: participant.role == "owner",
			}
			release, ok := bridge.sockets.admit("room", principal, browser)
			if !ok {
				return nil
			}
			bridge.release = release
			bridge.start(natives, principal, session, mayTalk)
			return nil
		})))
	return nil
}

var participantID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// conversationOf is the query's conversation as `Number.isInteger(Number(text)) && >= 1` takes it.
func conversationOf(text string) (int, bool) {
	number, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || number != float64(int(number)) || number < 1 {
		return 0, false
	}
	return int(number), true
}

func post(ctx context.Context, current *config, body []byte) (*http.Response, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, current.url+"/speak", strings.NewReader(string(body)))
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+current.token)
	request.Header.Set("Content-Type", "application/json")
	reply, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer reply.Body.Close()
	audio, err := io.ReadAll(reply.Body)
	return reply, audio, err
}

type participantOf struct{ id, name, role string }

// bridge is one browser socket bridged to the speech service's room.
type bridge struct {
	browser     *websocket.Conn
	sockets     *sockets
	config      *config
	room        string
	participant participantOf
	owner       bool
	release     func()

	browserWrite sync.Mutex
	mu           sync.Mutex
	upstream     *websocket.Conn
	failed       bool
	// early are the commands sent before the service answers wait for it (microphone frames are dropped: a few ms).
	early    [][]byte
	closing  sync.Once
	finished chan struct{}
}

func (b *bridge) sendBrowser(kind int, payload []byte) {
	b.browserWrite.Lock()
	defer b.browserWrite.Unlock()
	_ = b.browser.WriteMessage(kind, payload)
}

func (b *bridge) sendError(code, message string) {
	encoded, _ := chordjson.Marshal(delta.JsonObjectOf("type", "error", "code", code, "message", message))
	b.sendBrowser(websocket.TextMessage, encoded)
}

func (b *bridge) closeBoth() {
	b.closing.Do(func() {
		b.release()
		closeWith(b.browser, websocket.CloseNormalClosure, "")
		_ = b.browser.Close()
		b.mu.Lock()
		upstream := b.upstream
		b.failed = true
		b.mu.Unlock()
		if upstream != nil {
			closeWith(upstream, websocket.CloseNormalClosure, "")
			_ = upstream.Close()
		}
		close(b.finished)
	})
}

func (b *bridge) start(natives func() (*p2runtime.ServerParts, error), principal p2runtime.Principal, session string, mayTalk func(p2runtime.Principal, host.Ctx, string) bool) {
	b.finished = make(chan struct{})
	//portlint:allow golifetime ends with the browser socket: closeBoth closes it
	go b.dial()
	//portlint:allow golifetime ends with the browser socket
	go b.readBrowser()
	if _, guest := principal.(p2runtime.GuestPrincipal); guest {
		//portlint:allow golifetime stops when the socket is finished or the routes object closes
		go b.recheck(natives, principal, session, mayTalk)
	}
}

// recheck closes a guest's socket once its grant no longer holds. A timer of the socket, nothing at hand: the background
// context.
func (b *bridge) recheck(natives func() (*p2runtime.ServerParts, error), principal p2runtime.Principal, session string, mayTalk func(p2runtime.Principal, host.Ctx, string) bool) {
	ticker := time.NewTicker(guestRecheck)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-b.finished:
			return
		case <-b.sockets.done:
			return
		}
		allowed := false
		if parts, err := natives(); err == nil {
			value, err := parts.Root(func(ctx host.Ctx) (any, error) { return mayTalk(principal, ctx, session), nil }, chordctx.Background())
			allowed, _ = value.(bool)
			_ = err
		}
		if !allowed {
			closeWith(b.browser, websocket.ClosePolicyViolation, "Access ended")
			b.closeBoth()
			return
		}
	}
}

// dial connects the service's room as p2's identity for this browser, then relays what the service sends.
func (b *bridge) dial() {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+b.config.token)
	headers.Set("X-Room", b.room)
	headers.Set("X-Participant-Id", b.participant.id)
	headers.Set("X-Participant-Name", encodeURIComponent(truncateUTF16(b.participant.name, 64)))
	headers.Set("X-Participant-Role", b.participant.role)
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	// PCM hardly compresses; deflating every 21 ms frame and every speech chunk only costs the host CPU and adds jitter.
	url := strings.Replace(b.config.url, "http", "ws", 1) + "/room"
	upstream, reply, err := dialer.Dial(url, headers)
	if err != nil {
		if reply != nil {
			b.sendError("unavailable", fmt.Sprintf("Speech service refused the room (HTTP %d)", reply.StatusCode))
		} else {
			b.sendError("unavailable", "Speech service unreachable")
		}
		b.closeBoth()
		return
	}
	upstream.SetReadLimit(maxServiceFrame)
	b.mu.Lock()
	if b.failed {
		b.mu.Unlock()
		_ = upstream.Close()
		return
	}
	b.upstream = upstream
	early := b.early
	b.early = nil
	for _, command := range early {
		_ = upstream.WriteMessage(websocket.TextMessage, command)
	}
	b.mu.Unlock()
	for {
		kind, payload, err := upstream.ReadMessage()
		if err != nil {
			b.closeBoth()
			return
		}
		b.sendBrowser(kind, payload)
	}
}

// readBrowser takes what the browser sends: microphone audio as it is (the service checks its size and rate), commands
// checked against what this participant may send.
func (b *bridge) readBrowser() {
	for {
		kind, payload, err := b.browser.ReadMessage()
		if err != nil {
			b.closeBoth()
			return
		}
		if kind == websocket.BinaryMessage {
			b.mu.Lock()
			if b.upstream != nil {
				_ = b.upstream.WriteMessage(websocket.BinaryMessage, payload)
			}
			b.mu.Unlock()
			continue
		}
		// A guest may only tune its own microphone; the owner also speaks for Pi and sets the voice.
		command := parseJSON(payload, true)
		check, code, message := checkPersonal, "forbidden", "Guests may only send reset or vad (silence 0.5-2 s)"
		if b.owner {
			check, code, message = checkCommand, "invalid_message", "Expected reset, vad, config, speak or cancel"
		}
		if command == nil || !check(command) {
			b.sendError(code, message)
			continue
		}
		b.mu.Lock()
		switch {
		case b.upstream != nil:
			_ = b.upstream.WriteMessage(websocket.TextMessage, payload)
		case !b.failed && len(b.early) < maxEarly:
			b.early = append(b.early, payload)
		}
		b.mu.Unlock()
	}
}
