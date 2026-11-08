package app

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/node/app/ephemeral.test.ts (Pi main c23b9ec1): tests of the ephemeral login plugin (slice 13 §3): a public
// route taking a name signs the browser in as a fresh `{ kind: "user", id, name }` with a cookie that ends with the browser, and the
// session ends with the server. The server is the kit's facets on a host (kitServer, D457).

func mustDecode(t *testing.T, body []byte) delta.JsonValue {
	t.Helper()
	value, err := delta.DecodeJson(body)
	if err != nil {
		t.Fatalf("%q: %v", body, err)
	}
	return value
}

func loginServer(t *testing.T, dir string) *kitServer {
	t.Helper()
	return newKitServer(t, kitServerOptions{Dir: dir, Facets: []host.HostFacet{{Name: "chord.app.ephemeral", Facet: EphemeralLoginFacet()}}})
}

func logIn(s *kitServer, name any) reply {
	return postJSON(s, LoginPath, map[string]any{"name": name}, nil)
}

func TestEphemeralLoginSignsInUnderAPickedNameAsAFreshUserEachLoginItsOwn(t *testing.T) {
	s := loginServer(t, "")
	defer s.close()
	first, second := logIn(s, "  ada  "), logIn(s, "ada")
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
	whoami := s.send("GET", "/whoami", map[string]string{"cookie": strings.SplitN(set, ";", 2)[0]}, "")
	if got := mustDecode(t, whoami.body); !reflect.DeepEqual(got, principal) {
		t.Fatalf("whoami = %s, want %s", whoami.body, first.body)
	}
	if status := logIn(s, "   ").status; status != 400 {
		t.Fatalf("blank name = %d, want 400", status)
	}
	if status := logIn(s, 7).status; status != 400 {
		t.Fatalf("numeric name = %d, want 400", status)
	}
	long := mustDecode(t, logIn(s, strings.Repeat("n", 100)).body).(*delta.JsonObject).Value("name").(string)
	if len(long) != 40 {
		t.Fatalf("long name has %d units, want 40", len(long))
	}
}

func TestEphemeralLoginEndsItsSessionsWithTheServer(t *testing.T) {
	dir := t.TempDir()
	first := loginServer(t, dir)
	set := logIn(first, "ada").headers.Get("set-cookie")
	first.close()
	second := loginServer(t, dir)
	defer second.close()
	if status := second.send("GET", "/whoami", map[string]string{"cookie": strings.SplitN(set, ";", 2)[0]}, "").status; status != 401 {
		t.Fatalf("the session outlived its server: %d, want 401", status)
	}
}
