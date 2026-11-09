package hosts_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/devices"
)

func TestMain(m *testing.M) { p2test.Main(m) }

// pingFacet is a probe facet: a route and a request resolver-free server, as a plugin contributes them.
var pingFacet = host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
	return p2runtime.Routes.Contribute(self, p2runtime.Entry("GET /ping", p2runtime.RouteHandler(
		func(response http.ResponseWriter, _ *http.Request, access p2runtime.RequestAccess, _ host.Ctx) error {
			kind := "nobody"
			if p2runtime.IsOwner(access.Principal) {
				kind = "owner"
			}
			return p2runtime.SendJSON(response, http.StatusOK, map[string]any{"pong": true, "as": kind}, nil)
		})))
})

func TestServerBootsAndServesChord(t *testing.T) {
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Plugins: []string{"devices"}, Facets: []host.HostFacet{{Name: "probe", Facet: pingFacet}}})
	port := fixture.Server.Port

	health, err := p2test.HTTP(port, "/health", p2test.HTTPOptions{})
	if err != nil || health.Status != 200 || !strings.Contains(health.Body, `"name":"p2"`) {
		t.Fatalf("/health: %+v %v", health, err)
	}
	// Any other Host header is refused (DNS rebinding).
	foreign, err := p2test.HTTP(port, "/health", p2test.HTTPOptions{Headers: map[string]string{"Host": "evil.example"}})
	if err != nil || foreign.Status != 403 {
		t.Fatalf("foreign host: %+v %v", foreign, err)
	}
	// A plugin's route answers as the local owner with its session, and as nobody on loopback without a credential.
	ping, err := fixture.HTTP("/ping", p2test.HTTPOptions{})
	if err != nil || ping.Status != 200 || !strings.Contains(ping.Body, `"as":"owner"`) {
		t.Fatalf("/ping: %+v %v", ping, err)
	}
	anonymous, err := p2test.HTTP(port, "/ping", p2test.HTTPOptions{})
	if err != nil || anonymous.Status != 200 || !strings.Contains(anonymous.Body, `"as":"nobody"`) {
		t.Fatalf("/ping without a credential: %+v %v", anonymous, err)
	}
	// A request no route answers: the owner gets "Not found" (no web UI yet), nobody the sign-in page.
	missing, err := fixture.HTTP("/nothing", p2test.HTTPOptions{})
	if err != nil || missing.Status != 404 {
		t.Fatalf("/nothing: %+v %v", missing, err)
	}

	if signIn, err := p2test.HTTP(port, "/nothing", p2test.HTTPOptions{}); err != nil || signIn.Status != 401 {
		t.Fatalf("/nothing without a credential: %+v %v", signIn, err)
	}

	// /chord: a browser-like client holds Access and is the owner.
	browser := fixture.Browser()
	access, err := p2test.HoldIn(browser, p2runtime.Access, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer access.Release()

	// The server's own locals: its start data and the workers' states.
	server, err := fixture.Server.Host.Get(p2test.Background, p2runtime.ServerLocal, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Release()
	info, err := server.Member("info").Value()
	if err != nil {
		t.Fatal(err)
	}
	object := info.(*delta.JsonObject)
	if got, _ := object.Value("fake").(bool); !got {
		t.Fatalf("info.fake: %v", object)
	}
	if got, _ := object.Value("port").(float64); int(got) != port {
		t.Fatalf("info.port %v, server on %d", got, port)
	}
	workers, err := fixture.Server.Host.Get(p2test.Background, p2runtime.Hosts, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer workers.Release()
	states, err := workers.Member("workers").Value()
	if err != nil || len(states.(*delta.JsonObject).Keys()) != 0 {
		t.Fatalf("workers: %v %v", states, err)
	}
}

func TestChordRefusesAForeignOrigin(t *testing.T) {
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Plugins: []string{"devices"}})
	owner := fixture.Owner()
	if conn, err := p2test.Dial(fixture.Server.Port, owner.OwnerHeaders(map[string]string{"Origin": "http://evil.example"})); err == nil {
		_ = conn.Close()
		t.Fatal("a WebSocket from another origin was accepted")
	}
	if conn, err := p2test.Dial(fixture.Server.Port, owner.OwnerHeaders(map[string]string{"Host": "evil.example"})); err == nil {
		_ = conn.Close()
		t.Fatal("a WebSocket to another Host was accepted")
	}
}

func TestSecondServerOnTheSameDataDirIsRefused(t *testing.T) {
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Plugins: []string{}})
	zero := 0
	if second, err := hosts.StartServer(hosts.ServerOptions{Plugins: []string{}, DataDir: fixture.DataDir, Port: &zero}); err == nil {
		_ = second.Close(p2test.Background)
		t.Fatal("two servers shared a data directory")
	}
}
