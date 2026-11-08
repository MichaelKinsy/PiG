package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/node/app/invite.test.ts (Pi main c23b9ec1): the owner makes an invite for a principal, the invitee's
// page posts the token from the fragment and gets a browser session for it, once; only the owner (AppInfo.owner) makes invites,
// whichever principal the app chose as its owner.

func inviteServer(t *testing.T, owner *delta.JsonObject) *kitServer {
	t.Helper()
	dir := t.TempDir()
	return newKitServer(t, kitServerOptions{Dir: dir, Facets: []host.HostFacet{
		{Name: "chord.app.info", Facet: AppInfoFacet(AppInfoValues{DataDir: dir, Name: "chord-test", Version: "v1", Owner: owner})},
		{Name: "chord.app.invite", Facet: InviteFacet()},
	}})
}

func postJSON(s *kitServer, path string, body any, headers map[string]string) reply {
	text, _ := json.Marshal(body)
	return s.send("POST", path, headers, string(text))
}

func TestInvitesSignTheInviteeInAsTheChosenPrincipalOnceOnlyTheOwnerInvites(t *testing.T) {
	s := inviteServer(t, ownerPrincipal())
	defer s.close()
	_, ownerToken, _ := s.issue(ownerPrincipal(), "owner", "device")
	_, userToken, _ := s.issue(delta.JsonObjectOf("kind", "user", "id", "u", "name", "u"), "u", "device")
	guest := map[string]any{"kind": "user", "id": "g1", "name": "Guest"}
	body := map[string]any{"principal": guest, "label": "guest"}
	if status := postJSON(s, InvitesPath, body, nil).status; status != 401 {
		t.Fatalf("anonymous invite = %d, want 401", status)
	}
	if status := postJSON(s, InvitesPath, body, bearer(userToken)).status; status != 403 {
		t.Fatalf("user invite = %d, want 403", status)
	}
	if status := postJSON(s, InvitesPath, map[string]any{"principal": "x", "label": "guest"}, bearer(ownerToken)).status; status != 400 {
		t.Fatalf("bad principal = %d, want 400", status)
	}
	made := postJSON(s, InvitesPath, body, bearer(ownerToken))
	var created struct{ Path string }
	if err := json.Unmarshal(made.body, &created); err != nil || made.status != 200 {
		t.Fatalf("made = %d %q: %v", made.status, made.body, err)
	}
	page, secret, _ := strings.Cut(created.Path, "#")
	if got := string(s.send("GET", page, nil, "").body); !strings.Contains(got, "location.hash") {
		t.Fatalf("page = %q", got)
	}
	used := postJSON(s, page, map[string]any{"token": secret}, nil)
	if used.status != 200 {
		t.Fatalf("used = %d", used.status)
	}
	if got := whoamiOf(t, s, map[string]string{"cookie": cookieOf(used)}); got != `{"id":"g1","kind":"user","name":"Guest"}` {
		t.Fatalf("whoami = %s", got)
	}
	if status := postJSON(s, page, map[string]any{"token": secret}, nil).status; status != 403 {
		t.Fatalf("second use = %d, want 403", status)
	}
}

func TestInvitesAcceptTheOwnerTheAppChoseAndRefuseAnotherUser(t *testing.T) {
	s := inviteServer(t, adaUser())
	defer s.close()
	_, other, _ := s.issue(bobUser(), "bob", "device")
	_, owner, _ := s.issue(adaUser(), "ada", "device")
	body := map[string]any{"principal": map[string]any{"kind": "user", "id": "g1"}, "label": "guest"}
	if status := postJSON(s, InvitesPath, body, bearer(other)).status; status != 403 {
		t.Fatalf("other user = %d, want 403", status)
	}
	if status := postJSON(s, InvitesPath, body, bearer(owner)).status; status != 200 {
		t.Fatalf("owner = %d, want 200", status)
	}
}
