package app

import (
	"context"
	"io"
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
)

// A server of the app kit as its own tests need it: a host on a store, the HTTP component on a loopback port, the kit's session
// facet, the `whoami` route of the tests' plugin, and the kit facets a test adds. Pi's `testApp` is the whole app (startServer);
// these tests exercise the kit's facets, and the app's own tests (app_test.go) the composition.

// cookieSecret is the secret the tests' sessions sign their cookies with.
const cookieSecret = "test-cookie-secret"

type kitServer struct {
	*kitHost
	component *chordhttp.HttpComponent
	sessions  *KitSessions
	port      int
	errs      *httpErrors
}

type httpErrors struct {
	mu   sync.Mutex
	list []chordhttp.HttpError
}

func (e *httpErrors) add(err chordhttp.HttpError) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, err)
}

func (e *httpErrors) all() []chordhttp.HttpError {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]chordhttp.HttpError(nil), e.list...)
}

// whoami is a plugin facet with a route (not public) answering who it runs as: `GET /whoami`.
func whoami() host.Facet {
	return host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](chordhttp.Http), chordhttp.HttpContribution{Routes: []chordhttp.HttpRouteEntry{
			chordhttp.RouteFunc("GET /whoami", func(_ chordhttp.HttpRequest, ctx host.Ctx) (chordhttp.HttpResponse, error) {
				var principal delta.JsonValue
				if who := ctx.Principal(); who != nil {
					principal = who
				}
				return chordhttp.HttpResponse{Kind: chordhttp.KindJSON, Value: principal}, nil
			}),
		}})
	})
}

type kitServerOptions struct {
	Dir      string
	Limits   SessionLimits
	Facets   []host.HostFacet
	Owner    data.Principal
	NoWhoami bool
}

func newKitServer(t *testing.T, options kitServerOptions) *kitServer {
	t.Helper()
	dir := options.Dir
	if dir == "" {
		dir = t.TempDir()
	}
	errs := &httpErrors{}
	chordStore := openStoreAt(t, dir+"/store.sqlite")
	hostErrors := &errorLog{}
	h, err := host.CreateHost(host.HostOptions{Store: chordStore, Principal: delta.JsonObjectOf("kind", "server"), OnError: hostErrors.push})
	if err != nil {
		t.Fatal(err)
	}
	component := chordhttp.CreateHttp(h, chordhttp.HttpOptions{Listen: &chordhttp.ListenAddress{Host: "127.0.0.1", Port: 0}, OnError: errs.add}, background)
	sessions := CreateSessions(cookieSecret, options.Limits)
	facets := []host.HostFacet{
		{Name: "chord.http", Facet: component.Facet()},
		{Name: "chord.app.sessions", Facet: sessions.Facet},
	}
	if !options.NoWhoami {
		facets = append(facets, host.HostFacet{Name: "@test/whoami", Facet: whoami()})
	}
	facets = append(facets, options.Facets...)
	report, err := h.Load(facets, background)
	if err != nil {
		t.Fatal(err)
	}
	for _, failed := range report.Failed {
		t.Fatalf("plugin %q was left out: %v", failed.Plugin, failed.Error)
	}
	address, err := component.Listening(background)
	if err != nil {
		t.Fatal(err)
	}
	server := &kitServer{kitHost: &kitHost{t: t, store: chordStore, host: h, errors: hostErrors}, component: component, sessions: sessions, port: address.Port, errs: errs}
	return server
}

// close closes the HTTP component, the host and the store.
func (s *kitServer) close() {
	s.t.Helper()
	s.component.Close(background)
	s.kitHost.close()
	s.t.Cleanup(func() { noKitGoroutines(s.t) })
}

// noKitGoroutines fails the test when a goroutine of the kit or the HTTP component outlives its server: a listener, a request
// scope, a socket or a timer left running. It polls for a few seconds (goroutines wind down after their close returns).
func noKitGoroutines(t *testing.T) {
	t.Helper()
	noGoroutinesOf(t, "chord/nextnode/app.", "chord/nextnode/http.")
}

// noGoroutinesOf is noKitGoroutines for the packages whose stacks name any of markers.
func noGoroutinesOf(t testing.TB, markers ...string) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		var leaked []string
		for _, stack := range strings.Split(string(buf), "\n\n") {
			if slices.ContainsFunc(markers, func(marker string) bool { return strings.Contains(stack, marker) }) &&
				!strings.Contains(stack, "testing.tRunner") && !strings.Contains(stack, "runtime.Stack") {
				leaked = append(leaked, stack)
			}
		}
		if len(leaked) == 0 {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("goroutines outlive the server:\n%s", strings.Join(leaked, "\n\n"))
		case <-tick.C:
		}
	}
}

// reply is what a request got: its status, its headers and its body.
type reply struct {
	status  int
	headers http.Header
	body    []byte
}

func (s *kitServer) send(method, path string, headers map[string]string, body string) reply {
	s.t.Helper()
	return sendTo(s.t, s.port, method, path, headers, body)
}

// sendTo is one request to 127.0.0.1:port (a JSON body when there is one), redirects not followed.
func sendTo(t testing.TB, port int, method, path string, headers map[string]string, body string) reply {
	t.Helper()
	ctx, cancel := context.WithTimeout(background, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:"+strconv.Itoa(port)+path, stringsReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	if body != "" {
		request.Header.Set("content-type", "application/json")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	text, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{status: response.StatusCode, headers: response.Header, body: text}
}

// issue issues a session on the kit's `Sessions` and returns its id, token and cookie.
func (s *kitServer) issue(principal delta.JsonValue, label, kind string) (id, token, cookie string) {
	s.t.Helper()
	sessions, err := s.host.Get(background, Sessions, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		s.t.Fatal(err)
	}
	defer sessions.Release()
	issued, err := sessions.Call("issue", delta.JsonObjectOf("principal", principal, "label", label, "kind", kind), background)
	if err != nil {
		s.t.Fatal(err)
	}
	result := issued.(*delta.JsonObject)
	return result.Value("id").(string), result.Value("token").(string), result.Value("cookie").(string)
}

func bearer(token string) map[string]string {
	return map[string]string{"authorization": "Bearer " + token}
}
