package app

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/node/app/ephemeral.test.ts (Pi main c23b9ec1): tests of the ephemeral login plugin (slice 13 §3): a public
// route taking a name signs the browser in as a fresh `{ kind: "user", id, name }` with a cookie that ends with the browser, and the
// session ends with the server. The app is the whole app (StartServer, as Pi's testApp): the login facet is loaded through
// AppOptions.Facets, where Pi's app loads it with a one-line plugin (a TypeScript plugin cannot import a Go facet), and `whoami` is the tests' route facet.

func mustDecode(t *testing.T, body []byte) delta.JsonValue {
	t.Helper()
	value, err := delta.DecodeJson(body)
	if err != nil {
		t.Fatalf("%q: %v", body, err)
	}
	return value
}

// loginApp starts the app with the ephemeral login facet and the whoami route; a later start may reuse the data directory and root
// of an earlier one.
func loginApp(t *testing.T, earlier *started) started {
	t.Helper()
	return startTestApp(t, nil, func(o *AppOptions) {
		o.Facets = []host.HostFacet{{Name: "chord.app.ephemeral", Facet: EphemeralLoginFacet()}, {Name: "@test/whoami", Facet: whoami()}}
		if earlier != nil {
			o.DataDir, o.Root = earlier.dataDir, earlier.root
		}
	})
}

func logIn(t *testing.T, s started, name any) reply {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name})
	return sendTo(t, s.port, "POST", LoginPath, nil, string(body))
}

func TestEphemeralLoginSignsInUnderAPickedNameAsAFreshUserEachLoginItsOwn(t *testing.T) {
	s := loginApp(t, nil)
	first, second := logIn(t, s, "  ada  "), logIn(t, s, "ada")
	principal, ok := mustDecode(t, first.body).(*delta.JsonObject)
	if !ok || principal.Len() != 3 || principal.Value("kind") != "user" || principal.Value("name") != "ada" ||
		!regexp.MustCompile(`^[A-Za-z0-9_-]{16}$`).MatchString(principal.Value("id").(string)) {
		t.Fatalf("principal = %s", first.body)
	}
	if mustDecode(t, second.body).(*delta.JsonObject).Value("id") == principal.Value("id") {
		t.Fatal("two logins share an id")
	}
	set := first.headers.Get("set-cookie")
	if strings.Contains(set, "Max-Age") {
		t.Fatalf("the cookie has a lifetime: %q", set)
	}
	whoami := sendTo(t, s.port, "GET", "/whoami", map[string]string{"cookie": strings.SplitN(set, ";", 2)[0]}, "")
	if got := mustDecode(t, whoami.body); !reflect.DeepEqual(got, principal) {
		t.Fatalf("whoami = %s, want %s", whoami.body, first.body)
	}
	if status := logIn(t, s, "   ").status; status != 400 {
		t.Fatalf("blank name = %d, want 400", status)
	}
	if status := logIn(t, s, 7).status; status != 400 {
		t.Fatalf("numeric name = %d, want 400", status)
	}
	long := mustDecode(t, logIn(t, s, strings.Repeat("n", 100)).body).(*delta.JsonObject).Value("name").(string)
	if len(long) != 40 {
		t.Fatalf("long name has %d units, want 40", len(long))
	}
}

func TestEphemeralLoginEndsItsSessionsWithTheServer(t *testing.T) {
	first := loginApp(t, nil)
	set := logIn(t, first, "ada").headers.Get("set-cookie")
	if err := first.app.Close(background); err != nil {
		t.Fatal(err)
	}
	second := loginApp(t, &first)
	if status := sendTo(t, second.port, "GET", "/whoami", map[string]string{"cookie": strings.SplitN(set, ";", 2)[0]}, "").status; status != 401 {
		t.Fatalf("the session outlived its server: %d, want 401", status)
	}
}
