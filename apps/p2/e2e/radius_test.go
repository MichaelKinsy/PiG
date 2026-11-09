// Ports test/p2/radius.test.ts (Pi main c23b9ec1), the cases that run a p2 server against a fake Radius relay: the owner's
// admission, sharing by email, credit flow control, port forwards, relay v5 channel sockets and client channels. The brain's
// reconnection cases are plugins/radius/contract/brain_test.go; the status lines plugins/radius/server_test.go. Pi's fake
// relay (radius-fixture.ts) is plugins/radius/radiustest.

package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	devicescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/devices/contract"
	logincontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/login/contract"
	portscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/ports/contract"
	radiuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/radius/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/radius/radiustest"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/gorilla/websocket"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/devices"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/login"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/ports"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/radius"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing"
)

const shellOrigin = "https://radius.earendil.com"

type radiusRig struct {
	t       *testing.T
	dir     string
	fixture *p2test.Fixture
	radius  *radiustest.Relay
	relay   *radiuscontract.RelayAccess
}

// radiusFixture starts a server signed in to a fake Radius, with a probe plugin's routes: who the server sees, a large body,
// an echo.
func radiusFixture(t *testing.T, content []byte, options radiustest.Options) *radiusRig {
	t.Helper()
	if content == nil {
		content = []byte("body")
	}
	relay := radiustest.Start(t, options)
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := radiuscontract.WriteConfig(dir, radiuscontract.Config{
		Gateway: relay.Gateway, ServerID: radiustest.NewID(),
		Credential: ai.OAuthCredentials{Access: "fixture-token", Refresh: "fixture-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()},
	}); err != nil {
		t.Fatal(err)
	}
	probe := host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return p2runtime.Routes.Contribute(self,
			p2runtime.Entry("GET /probe", p2runtime.RouteHandler(func(response http.ResponseWriter, _ *http.Request, access p2runtime.RequestAccess, _ host.Ctx) error {
				principal := map[string]any{"kind": "guest"}
				if owner, ok := access.Principal.(p2runtime.OwnerPrincipal); ok {
					principal = map[string]any{"kind": "owner", "access": string(owner.Access)}
				}
				origin := map[string]any{"origin": access.Origin.Origin, "remote": access.Origin.Remote, "port": access.Origin.Port}
				if access.Origin.RelayShellOrigin != "" {
					origin["relayShellOrigin"] = access.Origin.RelayShellOrigin
				}
				status := http.StatusForbidden
				if p2runtime.IsOwner(access.Principal) {
					status = http.StatusOK
				}
				return p2runtime.SendJSON(response, status, map[string]any{"principal": principal, "origin": origin}, nil)
			})),
			p2runtime.Entry("GET /body", p2runtime.RouteHandler(func(response http.ResponseWriter, _ *http.Request, _ p2runtime.RequestAccess, _ host.Ctx) error {
				_, err := response.Write(content)
				return err
			})),
			p2runtime.Entry("POST /echo", p2runtime.RouteHandler(func(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, _ host.Ctx) error {
				if !p2runtime.IsOwner(access.Principal) {
					response.WriteHeader(http.StatusForbidden)
					return nil
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					return err
				}
				_, err = response.Write(body)
				return err
			})),
		)
	})
	fixture := p2test.StartFixture(t, hosts.ServerOptions{DataDir: dir, Facets: []host.HostFacet{{Name: "radius-probe", Facet: probe}}})
	select {
	case <-relay.Ready():
	case <-time.After(30 * time.Second):
		t.Fatal("the brain never connected to the relay")
	}
	rig := &radiusRig{t: t, dir: dir, fixture: fixture, radius: relay}
	// The natives the radius and ports plugins contribute once open (their objects live from the load).
	radiustest.Wait(t, "the radius native", 20*time.Second, func() bool {
		registry, err := fixture.Server.Host.Registry(p2test.Background, radiuscontract.RadiusConnections, host.GetOptions{})
		if err != nil {
			return false
		}
		parts, _ := registry.Value().(*radiuscontract.RadiusConnectionsParts)
		if parts == nil {
			return false
		}
		rig.relay = parts.Connection().Relay
		return rig.forwards() != nil
	})
	return rig
}

func (r *radiusRig) forwards() *portscontract.Forwards {
	registry, err := r.fixture.Server.Host.Registry(p2test.Background, portscontract.ForwardsNative, host.GetOptions{})
	if err != nil {
		return nil
	}
	parts, _ := registry.Value().(*portscontract.ForwardsParts)
	if parts == nil {
		return nil
	}
	return parts.Forwards()
}

type radiusOpen struct {
	Method    string
	Kind      string
	Forward   string
	Anonymous bool
	Principal *radiustest.Principal
	Window    int
	Headers   [][2]string
}

// open is a channel the relay opens for path (`open` of radius-fixture.ts).
func (r *radiusRig) open(path string, options radiusOpen) string {
	id := radiustest.NewID()
	origin := radiustest.PublicOrigin
	if options.Forward != "" {
		origin = "https://fwd--0123456789abcdef0123456789abcdef--" + options.Forward + ".rdtun.com"
	}
	kind := options.Kind
	if kind == "" {
		kind = "http"
	}
	headers := options.Headers
	if headers == nil {
		headers = [][2]string{{"origin", origin}, {"x-radius-shell-origin", shellOrigin}}
	}
	window := options.Window
	if window == 0 {
		window = 64 * 1024
	}
	method := options.Method
	if method == "" {
		method = "GET"
	}
	message := map[string]any{
		"version": 2, "type": "connection_open", "connection_id": id, "kind": kind, "public_origin": origin,
		"request": map[string]any{"method": method, "path": path, "headers": headers}, "window": window,
	}
	if !options.Anonymous {
		principal := radiustest.Owner
		if options.Principal != nil {
			principal = *options.Principal
		}
		message["principal"] = principal
	}
	if options.Forward != "" {
		message["forward"] = options.Forward
	}
	r.radius.Send(message)
	if kind == "http" && method != "POST" {
		r.radius.SendData(id, nil, 3)
	}
	return id
}

func (r *radiusRig) closed(ids ...string) {
	r.t.Helper()
	for _, id := range ids {
		radiustest.Wait(r.t, "the channel "+id+" to be refused", 20*time.Second, func() bool { return r.radius.Closed(id, 0) })
	}
}

func (r *radiusRig) marked(extra map[string]string) map[string]string {
	headers := map[string]string{
		radiuscontract.RelaySecretHeader: r.relay.Secret(), radiuscontract.RelayOriginHeader: radiustest.PublicOrigin,
		radiuscontract.RelayShellHeader: shellOrigin, "Origin": radiustest.PublicOrigin,
	}
	for name, value := range extra {
		headers[name] = value
	}
	return headers
}

func (r *radiusRig) get(path string, headers map[string]string) p2test.HTTPResult {
	r.t.Helper()
	result, err := p2test.HTTP(r.fixture.Server.Port, path, p2test.HTTPOptions{Headers: headers})
	if err != nil {
		r.t.Fatal(err)
	}
	return result
}

func radiusJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	return out
}

func TestRadiusPluginAdmitsItsAuthenticatedRemoteOwnerIncludingProviderLoginWithoutLocalCookies(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	f := radiusFixture(t, nil, radiustest.Options{})
	id := f.open("/probe", radiusOpen{})
	response := f.radius.Response(id)
	if response["status"] != float64(200) {
		t.Fatalf("response = %v", response)
	}
	access := radiusJSON(t, f.radius.Body(id))
	if !reflect.DeepEqual(access["principal"], map[string]any{"kind": "owner", "access": "relay"}) {
		t.Fatalf("principal = %v", access["principal"])
	}
	if want := (map[string]any{"origin": radiustest.PublicOrigin, "remote": true, "port": float64(f.fixture.Server.Port), "relayShellOrigin": shellOrigin}); !reflect.DeepEqual(access["origin"], want) {
		t.Fatalf("origin = %v, want %v", access["origin"], want)
	}
	// Only the Radius shell may frame the relayed UI (X-Frame-Options cannot name it, so it is left out).
	csp, _ := radiustest.Header(response, "content-security-policy")
	if !regexp.MustCompile(`frame-ancestors https://radius\.earendil\.com$`).MatchString(csp) {
		t.Fatalf("content-security-policy = %q", csp)
	}
	if _, framed := radiustest.Header(response, "x-frame-options"); framed {
		t.Fatal("x-frame-options is set")
	}
	if _, cookie := radiustest.Header(response, "set-cookie"); cookie {
		t.Fatal("a relayed response sets a cookie")
	}
	if origin := f.forwards().RadiusOrigin(); origin == nil || *origin != radiustest.PublicOrigin {
		t.Fatalf("forwards' radius origin = %v", origin)
	}
	radiustest.Wait(t, "the public origin in radius.json", 10*time.Second, func() bool {
		config, err := radiuscontract.ReadConfig(f.dir)
		return err == nil && config != nil && config.PublicOrigin == radiustest.PublicOrigin
	})
	elsewhere := radiustest.Owner
	elsewhere.ID, elsewhere.OrganizationID = "someone-else", "org_2"
	service := radiustest.Owner
	service.Kind, service.ID = "service_account", "sa_1"
	other := radiustest.Owner
	other.ID = "usr_2"
	f.closed(
		f.open("/probe", radiusOpen{Anonymous: true}),
		f.open("/probe", radiusOpen{Principal: &elsewhere}),
		f.open("/probe", radiusOpen{Principal: &service}),
		f.open("/probe", radiusOpen{Principal: &other, Forward: "abc"}),
		f.open("/probe", radiusOpen{Kind: "stream"}),
	)
	marked := f.marked(nil)
	browser, _ := f.fixture.Client(marked)
	if _, err := p2test.HoldIn(browser, p2runtime.Access, delta.NewJsonObject(0), "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.HoldIn(browser, logincontract.ProviderLogin, delta.NewJsonObject(0), "owner"); err != nil {
		t.Fatal(err)
	}
	devices, err := p2test.HoldIn(browser, devicescontract.Devices, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	_, err = devices.Call("token", delta.NewJsonObject(0), p2test.Background)
	p2test.ExpectChord(t, err, data.CodeForbidden)
	if got := f.get("/owner-token", marked).Status; got != 403 {
		t.Fatalf("/owner-token = %d", got)
	}
	// The shell frames the relayed UI (a cross-site iframe load): Radius authenticates it, not a browser cookie, so the
	// foreign-request rule leaves it the owner.
	framed := f.get("/probe", f.marked(map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}))
	if !reflect.DeepEqual(radiusJSON(t, []byte(framed.Body))["principal"], map[string]any{"kind": "owner", "access": "relay"}) {
		t.Fatalf("framed = %d %s", framed.Status, framed.Body)
	}
	if got := f.get("/probe", f.marked(map[string]string{"Origin": "https://foreign.example"})).Status; got != 403 {
		t.Fatalf("foreign origin = %d", got)
	}
	if got := f.get("/probe", f.marked(map[string]string{radiuscontract.RelaySecretHeader: "forged"})).Status; got != 403 {
		t.Fatalf("forged secret = %d", got)
	}
	if got := f.get("/probe", nil).Status; got != 200 {
		t.Fatalf("local = %d", got)
	}
}

// Ports "Radius-relayed artifact previews may sit below the Radius shell's frame" (Pi main 82d6f34f).
func TestRadiusRelayedArtifactPreviewsMaySitBelowTheRadiusShellsFrame(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	f := radiusFixture(t, nil, radiustest.Options{})
	owner := f.fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", f.fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	chat, err := p2test.HoldIn(owner, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Call("send", delta.JsonObjectOf("text", "artifact", "requestId", "artifact"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(chat.Member("view"), func(view delta.JsonValue) bool {
		return view != nil && len(toolResults(view, "artifact")) == 1 && !running(view)
	}, 0); err != nil {
		t.Fatal(err)
	}
	chat.Release()
	// The relayed app is framed by the shell, and `frame-ancestors` checks every ancestor: `'self'` alone showed Chrome's
	// blocked-frame page instead of every inline preview.
	query := url.Values{"session": {session}, "name": {"demo"}, "version": {"1"}}.Encode()
	response := f.radius.Response(f.open("/artifact?"+query, radiusOpen{}))
	if response["status"] != float64(200) {
		t.Fatalf("response = %v", response)
	}
	csp, _ := radiustest.Header(response, "content-security-policy")
	if !strings.HasSuffix(csp, "; frame-ancestors 'self' https://radius.earendil.com") {
		t.Fatalf("content-security-policy = %q", csp)
	}
}

func TestRadiusAdmitsPeopleSessionsAreSharedWithByEmailAcrossOrganizations(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	f := radiusFixture(t, nil, radiustest.Options{})
	// The owner's relayed request names the shell, so people get links into it.
	if got := f.radius.Response(f.open("/probe", radiusOpen{}))["status"]; got != float64(200) {
		t.Fatalf("probe = %v", got)
	}
	config, err := radiuscontract.ReadConfig(f.dir)
	if err != nil || config == nil {
		t.Fatalf("config = %v %v", config, err)
	}
	owner := f.fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	create := func(title string) string {
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", f.fixture.Workspace, "title", title), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return object(created).Value("id").(string)
	}
	first, second := create("First"), create("Second")
	sharing, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", first), "owner")
	if err != nil {
		t.Fatal(err)
	}
	radiustest.Wait(t, "the first report", 20*time.Second, func() bool { return len(f.radius.Shares()) > 0 })
	if last := f.radius.Shares()[len(f.radius.Shares())-1]; len(last) != 0 {
		t.Fatalf("the brain reports its shares when it connects: %v", last)
	}
	if _, err := sharing.Call("share", delta.JsonObjectOf("email", " Ann.Example@GoogleMail.com ", "mode", "view"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	people, err := p2test.Until(sharing.Member("people"), func(value delta.JsonValue) bool { return len(list(value)) == 1 }, 0)
	if err != nil {
		t.Fatal(err)
	}
	if person := object(list(people)[0]); person.Value("name") != "Ann.Example@GoogleMail.com" || person.Value("mode") != "view" {
		t.Fatalf("people are listed as the owner wrote them: %v", person)
	}
	url, err := p2test.Until(sharing.Member("peopleUrl"), func(value delta.JsonValue) bool { return value != nil }, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://radius.earendil.com/pi/brains/org_1/" + config.ServerID + "?path=%2Fs%2F" + first; url != want {
		t.Fatalf("peopleUrl = %v, want %s", url, want)
	}
	radiustest.Wait(t, "Radius learns the email", 10*time.Second, func() bool {
		shares := f.radius.Shares()
		return len(shares) > 0 && len(shares[len(shares)-1]) > 0 && shares[len(shares)-1][0] == "annexample@gmail.com"
	})
	secondSharing, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", second), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondSharing.Call("share", delta.JsonObjectOf("email", "annexample@gmail.com", "mode", "full"), p2test.Background); err != nil {
		t.Fatal(err)
	}

	// Radius admits Ann, of another organization, with her verified email; the brain decides what she sees.
	ann := radiustest.Owner
	ann.ID, ann.Email, ann.OrganizationID = "usr_2", "annexample@gmail.com", "org_9"
	if got := f.radius.Response(f.open("/probe", radiusOpen{Principal: &ann}))["status"]; got != float64(403) {
		t.Fatalf("admitted, but not the owner: %v", got)
	}
	withoutEmail := radiustest.Owner
	withoutEmail.ID = "usr_3"
	f.closed(f.open("/probe", radiusOpen{Principal: &withoutEmail}))
	as := func(id, email string) map[string]string {
		return f.marked(map[string]string{radiuscontract.RelayUserHeader: radiuscontract.EncodeRelayUser(p2runtime.RelayUser{ID: id, Email: email})})
	}
	browser, conn := f.fixture.Client(as("usr_2", "annexample@gmail.com"))
	access, err := p2test.HoldIn(browser, p2runtime.Access, delta.NewJsonObject(0), "guest")
	if err != nil {
		t.Fatal(err)
	}
	grantValue, err := p2test.Until(access.Member("grant"), func(value delta.JsonValue) bool { return value != nil }, 0)
	if err != nil {
		t.Fatal(err)
	}
	grant := object(grantValue)
	granted := object(grant.Value("sessions"))
	if granted.Len() != 2 || granted.Value(first) != "view" || granted.Value(second) != "full" {
		t.Fatalf("grant sessions = %v", granted)
	}
	if grant.Value("session") != second {
		t.Fatalf("she lands in the session shared last: %v", grant.Value("session"))
	}
	if grant.Value("email") != "annexample@gmail.com" {
		t.Fatalf("grant email = %v", grant.Value("email"))
	}
	guestSessions, err := p2test.HoldIn(browser, sessionscontract.Sessions, delta.NewJsonObject(0), "guest")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := p2test.Until(guestSessions.Member("sessions"), func(value delta.JsonValue) bool { return len(list(value)) == 2 }, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, each := range list(listed) {
		ids[object(each).Value("id").(string)] = true
	}
	if len(ids) != 2 || !ids[first] || !ids[second] {
		t.Fatalf("her sessions = %v", ids)
	}
	_, err = p2test.HoldIn(browser, sharingcontract.Sharing, delta.JsonObjectOf("session", first), "owner")
	if err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("sharing for a guest: %v", err)
	}
	// Links are for browsers on this device or a tunnel; Radius users have no cookie to join with.
	if _, err := sharing.Call("link", delta.JsonObjectOf("mode", "view"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	links, err := p2test.Until(sharing.Member("links"), func(value delta.JsonValue) bool { return len(list(value)) == 1 }, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, _ := object(list(links)[0]).Value("token").(string)
	if got := f.get("/join/"+token, as("usr_2", "annexample@gmail.com")).Status; got != 403 {
		t.Fatalf("join = %d", got)
	}
	stranger := f.get("/", as("usr_4", "bob@example.com"))
	if stranger.Status != 403 || !strings.Contains(stranger.Body, "Nothing on this brain is shared with you") {
		t.Fatalf("stranger = %d %s", stranger.Status, stranger.Body)
	}

	// Removing her from one session ends her old principal; the brain keeps reporting her while one remains.
	if _, err := sharing.Call("remove", delta.JsonObjectOf("guestId", grant.Value("id")), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if err := p2test.Poll(func() (bool, error) { return p2test.SocketClosed(conn), nil }, 10*time.Second, "her old principal ended"); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(sharing.Member("people"), func(value delta.JsonValue) bool { return len(list(value)) == 0 }, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := secondSharing.Call("remove", delta.JsonObjectOf("guestId", grant.Value("id")), p2test.Background); err != nil {
		t.Fatal(err)
	}
	radiustest.Wait(t, "Radius forgets her", 10*time.Second, func() bool {
		shares := f.radius.Shares()
		return len(shares) > 0 && len(shares[len(shares)-1]) == 0
	})
}

func TestRadiusHTTPBodiesFollowCreditInBothDirectionsUplinkCongestionLosesNoData(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	content := make([]byte, 8*1024*1024)
	_, _ = rand.Read(content)
	f := radiusFixture(t, content, radiustest.Options{})
	id := f.open("/body", radiusOpen{})
	if f.radius.Response(id)["status"] != float64(200) {
		t.Fatal("body status")
	}
	radiustest.Wait(t, "the first window", 20*time.Second, func() bool { return f.radius.Received(id) == 64*1024 })
	time.Sleep(200 * time.Millisecond)
	if got := f.radius.Received(id); got != 64*1024 {
		t.Fatalf("received %d past the window", got)
	}
	f.radius.Send(map[string]any{"version": 2, "type": "credit", "connection_id": id, "bytes": 64 * 1024})
	radiustest.Wait(t, "the second window", 20*time.Second, func() bool { return f.radius.Received(id) == 128*1024 })
	f.radius.Send(map[string]any{"version": 2, "type": "credit", "connection_id": id, "bytes": len(content)})
	if !bytes.Equal(f.radius.Body(id), content) {
		t.Fatal("the body differs")
	}
	upload := f.open("/echo", radiusOpen{Method: "POST"})
	f.radius.SendData(upload, []byte("hello "), 1)
	f.radius.SendData(upload, []byte("relay"), 1)
	f.radius.SendData(upload, nil, 3)
	if f.radius.Response(upload)["status"] != float64(200) {
		t.Fatal("echo status")
	}
	if got := string(f.radius.Body(upload)); got != "hello relay" {
		t.Fatalf("echo = %q", got)
	}
	credit := 0.0
	for _, control := range f.radius.Controls() {
		if control["type"] == "credit" && control["connection_id"] == upload {
			credit += control["bytes"].(float64)
		}
	}
	if credit != 11 {
		t.Fatalf("credit = %v", credit)
	}
	f.radius.Reading(false)
	congested := f.open("/body", radiusOpen{Window: len(content) * 2})
	time.Sleep(700 * time.Millisecond)
	f.radius.Reading(true)
	if !bytes.Equal(f.radius.Body(congested), content) {
		t.Fatal("the congested body differs")
	}
}

func TestRadiusForwardsRelayHTTPAndWebSocketsRewriteOriginsAndRedirectsAndRefusePrivateAnonymousRequests(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	f := radiusFixture(t, nil, radiustest.Options{})
	var mu sync.Mutex
	var seen []http.Header
	var seenHost []string
	record := func(request *http.Request) {
		mu.Lock()
		seen, seenHost = append(seen, request.Header.Clone()), append(seenHost, request.Host)
		mu.Unlock()
	}
	last := func() (http.Header, string) {
		mu.Lock()
		defer mu.Unlock()
		return seen[len(seen)-1], seenHost[len(seenHost)-1]
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	servicePort := listener.Addr().(*net.TCPAddr).Port
	var sockets sync.Map
	service := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		record(request)
		if websocket.IsWebSocketUpgrade(request) {
			conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(response, request, nil)
			if err != nil {
				return
			}
			sockets.Store(conn, true)
			defer sockets.Delete(conn)
			for {
				kind, message, err := conn.ReadMessage()
				if err != nil {
					return
				}
				_ = conn.WriteMessage(kind, append([]byte("echo "), message...))
			}
		}
		if request.URL.Path == "/raw" {
			// The map key is written as it is: the name's case reaches the relay as the server wrote it, as in Pi.
			response.Header()["x-Lower-MIXED"] = []string{"1"}
			_, _ = io.WriteString(response, "raw")
			return
		}
		if request.URL.Path == "/login" {
			response.Header().Set("Location", "http://localhost:"+itoa(servicePort)+"/home?x=1")
			response.WriteHeader(http.StatusFound)
			return
		}
		_, _ = io.WriteString(response, "service "+request.URL.Path)
	})}
	go func() { _ = service.Serve(listener) }()
	t.Cleanup(func() {
		sockets.Range(func(key, _ any) bool { _ = key.(*websocket.Conn).Close(); return true })
		_ = service.Close()
	})
	name := "web"
	web, err := f.forwards().Add(servicePort, &name, []int{f.fixture.Server.Port}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	origin := "https://fwd--0123456789abcdef0123456789abcdef--" + web.ID + ".rdtun.com"
	hello := f.open("/hello", radiusOpen{Forward: web.ID, Headers: [][2]string{
		{"origin", origin}, {"cookie", "app=1"}, {"x-radius-shell-origin", shellOrigin}, {strings.ToUpper(radiuscontract.RelaySecretHeader), "forged"},
	}})
	if f.radius.Response(hello)["status"] != float64(200) {
		t.Fatal("hello status")
	}
	if got := string(f.radius.Body(hello)); got != "service /hello" {
		t.Fatalf("hello = %q", got)
	}
	headers, requestHost := last()
	parsed, _ := http.NewRequest("GET", origin, nil)
	for name, want := range map[string]string{"Origin": "http://localhost:" + itoa(servicePort), "X-Forwarded-Host": parsed.URL.Host, "X-Forwarded-Proto": "https", "Cookie": "app=1"} {
		if got := headers.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if requestHost != "localhost:"+itoa(servicePort) {
		t.Errorf("host = %q", requestHost)
	}
	if headers.Get(radiuscontract.RelaySecretHeader) != "" || headers.Get("X-Radius-Shell-Origin") != "" {
		t.Errorf("relay headers leaked: %v", headers)
	}
	redirect, _ := radiustest.Header(f.radius.Response(f.open("/login", radiusOpen{Forward: web.ID})), "location")
	if redirect != origin+"/home?x=1" {
		t.Fatalf("location = %q", redirect)
	}
	rawPairs, _ := f.radius.Response(f.open("/raw", radiusOpen{Forward: web.ID}))["headers"].([]any)
	rawName := ""
	for _, each := range rawPairs {
		if pair, _ := each.([]any); len(pair) == 2 && strings.EqualFold(fmt.Sprint(pair[0]), "x-lower-mixed") {
			rawName = fmt.Sprint(pair[0])
		}
	}
	if rawName != "x-Lower-MIXED" {
		t.Fatalf("response header name = %q, want the server's own case", rawName)
	}
	status := func(id string) any { return f.radius.Response(id)["status"] }
	if got := status(f.open("/", radiusOpen{Forward: "unknown"})); got != float64(404) {
		t.Fatalf("unknown forward = %v", got)
	}
	if got := status(f.open("/", radiusOpen{Forward: web.ID, Anonymous: true})); got != float64(404) {
		t.Fatalf("private anonymous = %v", got)
	}
	if err := f.forwards().SetPublic(web.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := status(f.open("/", radiusOpen{Forward: web.ID, Anonymous: true})); got != float64(200) {
		t.Fatalf("public anonymous = %v", got)
	}
	if err := f.forwards().SetPublic(web.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := status(f.open("/", radiusOpen{Forward: web.ID, Anonymous: true})); got != float64(404) {
		t.Fatalf("private again = %v", got)
	}
	closedName := "closed"
	closed, err := f.forwards().Add(1, &closedName, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	unavailable := f.open("/", radiusOpen{Forward: closed.ID})
	if got := status(unavailable); got != float64(502) {
		t.Fatalf("unavailable = %v", got)
	}
	if !strings.Contains(string(f.radius.Body(unavailable)), "Nothing is listening") {
		t.Fatal("unavailable text")
	}
	ws := f.open("/socket", radiusOpen{Forward: web.ID, Kind: "ws"})
	radiustest.Wait(t, "the forward's socket", 10*time.Second, func() bool {
		count := 0
		sockets.Range(func(any, any) bool { count++; return true })
		return count == 1
	})
	if headers, _ := last(); headers.Get("Origin") != "http://localhost:"+itoa(servicePort) {
		t.Errorf("socket origin = %q", headers.Get("Origin"))
	}
	f.radius.SendData(ws, []byte("ping"), 2)
	radiustest.Wait(t, "the socket's echo", 10*time.Second, func() bool {
		for _, frame := range f.radius.Frames() {
			if frame.ID == ws && string(frame.Payload) == "echo ping" {
				return true
			}
		}
		return false
	})
	if origin := f.forwards().RadiusOrigin(); origin != nil {
		t.Fatalf("forwards' radius origin before a UI request = %v", *origin)
	}
	f.radius.Response(f.open("/probe", radiusOpen{}))
	if origin := f.forwards().RadiusOrigin(); origin == nil || *origin != radiustest.PublicOrigin {
		t.Fatalf("forwards' radius origin = %v", origin)
	}
}

// echoService is a WebSocket service on a free port that echoes text and answers `flood <bytes>` with that much binary output.
func echoService(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var sockets sync.Map
	service := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(response, request, nil)
		if err != nil {
			return
		}
		sockets.Store(conn, true)
		defer sockets.Delete(conn)
		for {
			kind, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if text := string(message); kind == websocket.TextMessage && strings.HasPrefix(text, "flood ") {
				var total int
				_, _ = fmt.Sscan(text[6:], &total)
				chunk := make([]byte, 64*1024)
				_, _ = rand.Read(chunk)
				for sent := 0; sent < total; sent += len(chunk) {
					if conn.WriteMessage(websocket.BinaryMessage, chunk) != nil {
						return
					}
				}
			} else {
				_ = conn.WriteMessage(websocket.TextMessage, []byte("echo "+string(message)))
			}
		}
	})}
	go func() { _ = service.Serve(listener) }()
	t.Cleanup(func() {
		sockets.Range(func(key, _ any) bool { _ = key.(*websocket.Conn).Close(); return true })
		_ = service.Close()
	})
	return listener.Addr().(*net.TCPAddr).Port
}

func TestRadiusV5GivesEachWebSocketChannelItsOwnSocketToTheRelay(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	f := radiusFixture(t, nil, radiustest.Options{Channels: true})
	id := f.open("/chord", radiusOpen{Kind: "ws"})
	channel := f.radius.Channel(id)
	browser, err := p2test.ClientHost(channel)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = browser.Close(p2test.Background) })
	owner, err := p2test.HoldIn(browser, p2runtime.Access, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	// Nothing of the channel crossed the host socket.
	for _, frame := range f.radius.Frames() {
		if frame.ID == id {
			t.Fatal("a channel frame crossed the host socket")
		}
	}
	// HTTP stays on the host socket.
	if f.radius.Response(f.open("/probe", radiusOpen{}))["status"] != float64(200) {
		t.Fatal("probe status")
	}
	// Closing the relay's end closes the loopback socket, and the brain forgets the channel.
	_ = channel.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4000, ""), time.Now().Add(5*time.Second))
	if _, err := p2test.UntilNative(owner.State(), func(state *host.RefState) bool { return state.Kind != host.RefStateLive }, 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestRadiusV5AChannelWhoseReaderStallsHoldsBackOnlyItselfAndLosesNothing(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	f := radiusFixture(t, nil, radiustest.Options{Channels: true})
	name := "web"
	web, err := f.forwards().Add(echoService(t), &name, []int{f.fixture.Server.Port}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	bulkID := f.open("/bulk", radiusOpen{Forward: web.ID, Kind: "ws"})
	echoID := f.open("/echo", radiusOpen{Forward: web.ID, Kind: "ws"})
	bulk, echo := f.radius.Channel(bulkID), f.radius.Channel(echoID)
	const flood = 16 * 1024 * 1024
	if err := bulk.WriteMessage(websocket.TextMessage, []byte("flood 16777216")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	// The stalled channel's output waits in TCP buffers; the other channel answers at once.
	var worst time.Duration
	for i := 0; i < 5; i++ {
		started := time.Now()
		text := "ping " + itoa(i)
		if err := echo.WriteMessage(websocket.TextMessage, []byte(text)); err != nil {
			t.Fatal(err)
		}
		_, answer, err := echo.ReadMessage()
		if err != nil || string(answer) != "echo "+text {
			t.Fatalf("echo = %q %v", answer, err)
		}
		worst = max(worst, time.Since(started))
	}
	if worst >= 100*time.Millisecond {
		t.Fatalf("the echo waited %v behind a stalled channel", worst)
	}
	received := 0
	_ = bulk.SetReadDeadline(time.Now().Add(20 * time.Second))
	for received < flood {
		_, message, err := bulk.ReadMessage()
		if err != nil {
			t.Fatalf("after %d of %d bytes: %v", received, flood, err)
		}
		received += len(message)
	}
}

func TestRadiusV5RefusesAChannelOnTheHostSocketWhenItsOwnSocketCannotConnect(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	f := radiusFixture(t, nil, radiustest.Options{Channels: true, RejectChannels: true})
	id := f.open("/chord", radiusOpen{Kind: "ws"})
	radiustest.Wait(t, "the refusal", 20*time.Second, func() bool { return f.radius.Closed(id, 1011) })
}

func TestRadiusClientChannelsTheOwnersTUILauncherReachesTheServerOneRadiusConnectionPerRequest(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	content := make([]byte, 3*1024*1024)
	_, _ = rand.Read(content)
	f := radiusFixture(t, content, radiustest.Options{Channels: true})
	transport := radiustest.NewTransport(radiustest.Client{Gateway: f.radius.Gateway, Access: func() (string, error) { return "fixture-token", nil }}, radiustest.NewID())
	ctx, cancel := context.WithTimeout(p2test.Background, 20*time.Second)
	defer cancel()
	probe, err := transport.Request(ctx, "GET", "/probe", nil)
	if err != nil || probe.Status != 200 {
		t.Fatalf("probe = %v %v", probe, err)
	}
	// The owner by Radius alone, framed by no shell; until a browser opened the brain, its origin is a placeholder.
	body, _ := probe.Bytes()
	access := radiusJSON(t, body)
	if !reflect.DeepEqual(access["principal"], map[string]any{"kind": "owner", "access": "relay"}) {
		t.Fatalf("principal = %v", access["principal"])
	}
	if want := (map[string]any{"origin": "https://radius.invalid", "remote": true, "port": float64(f.fixture.Server.Port)}); !reflect.DeepEqual(access["origin"], want) {
		t.Fatalf("origin = %v", access["origin"])
	}
	if transport.Origin() != "https://radius.invalid" {
		t.Fatalf("transport origin = %q", transport.Origin())
	}
	// Bodies larger than one message, both ways; a request body may stream.
	big, err := transport.Request(ctx, "GET", "/body", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := big.Bytes(); !bytes.Equal(got, content) {
		t.Fatalf("the body differs (%d bytes)", len(got))
	}
	upload := make([]byte, 1024*1024)
	_, _ = rand.Read(upload)
	echo, err := transport.Request(ctx, "POST", "/echo", io.MultiReader(bytes.NewReader(upload[:1000]), bytes.NewReader(upload[1000:])))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := echo.Bytes(); !bytes.Equal(got, upload) {
		t.Fatalf("the echo differs (%d bytes)", len(got))
	}
	// Only the device itself mints owner links.
	token, err := transport.Request(ctx, "GET", "/owner-token", nil)
	if err != nil || token.Status != 403 {
		t.Fatalf("/owner-token = %v %v", token, err)
	}
	_, _ = token.Bytes()
	// Once the owner opened the brain in a browser, clients get its Radius origin.
	if f.radius.Response(f.open("/probe", radiusOpen{}))["status"] != float64(200) {
		t.Fatal("probe status")
	}
	again, err := transport.Request(ctx, "GET", "/probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = again.Bytes()
	if transport.Origin() != radiustest.PublicOrigin {
		t.Fatalf("transport origin = %q", transport.Origin())
	}
}

func TestRadiusClientChannelsAdmitOnlyTheOwnerAndOnlyThroughChannelSockets(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	f := radiusFixture(t, nil, radiustest.Options{Channels: true})
	stranger := radiustest.NewTransport(radiustest.Client{Gateway: f.radius.Gateway, Access: func() (string, error) { return "stranger-token", nil }}, radiustest.NewID())
	ctx, cancel := context.WithTimeout(p2test.Background, 10*time.Second)
	defer cancel()
	denied, err := stranger.Request(ctx, "GET", "/probe", nil)
	if err != nil || denied.Status != 403 {
		t.Fatalf("stranger = %v %v", denied, err)
	}
	conn, refused, err := stranger.Socket(ctx, "/chord")
	if conn != nil {
		_ = conn.Close()
	}
	if err != nil || refused != 403 {
		t.Fatalf("stranger socket = %d %v", refused, err)
	}
	// A request that is not one is refused on the channel.
	id := radiustest.NewID()
	f.radius.Send(map[string]any{"version": 2, "type": "connection_open", "connection_id": id, "kind": "ws", "principal": radiustest.Owner})
	channel := f.radius.Channel(id)
	if err := channel.WriteMessage(websocket.TextMessage, []byte(`{"type":"http","method":"GET","path":"//elsewhere.example/","headers":{}}`)); err != nil {
		t.Fatal(err)
	}
	_ = channel.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, _, err = channel.ReadMessage()
	if closed, ok := err.(*websocket.CloseError); !ok || closed.Code != 1008 {
		t.Fatalf("channel closed with %v, want 1008", err)
	}
	// Relays before channel sockets do not carry them.
	old := radiusFixture(t, nil, radiustest.Options{})
	oldID := radiustest.NewID()
	old.radius.Send(map[string]any{"version": 2, "type": "connection_open", "connection_id": oldID, "kind": "ws", "principal": radiustest.Owner})
	radiustest.Wait(t, "the refusal", 20*time.Second, func() bool { return old.radius.Closed(oldID, 1008) })
}
