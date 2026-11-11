package app

// pi: packages/chord/src/node/app/pairing.ts

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
)

// Ports packages/chord/test/node/app/pairing.test.ts (Pi main c23b9ec1): a client asks for a code, the owner approves it in a
// signed-in browser, the client's poll gets a device token once; codes are unambiguous, owner-only to approve, single use, and
// expire. The `testApp` of Pi is a kit server with the pairing facet (the whole app's own tests are app_test.go).

func pairServer(t *testing.T, owner *delta.JsonObject, limits PairingLimits) *kitServer {
	t.Helper()
	return newKitServer(t, kitServerOptions{Facets: []host.HostFacet{{Name: "chord.app.pairing", Facet: PairingFacet(NewPairings(limits), owner)}}})
}

type pairBegun struct{ Code, Poll string }

func begunOf(t *testing.T, r reply) pairBegun {
	t.Helper()
	var begun pairBegun
	if err := json.Unmarshal(r.body, &begun); err != nil || r.status != 200 {
		t.Fatalf("begun = %d %q: %v", r.status, r.body, err)
	}
	return begun
}

func anonymous(address string) PairingStarter { return PairingStarter{Address: address} }

func TestPairsAClientCodeOwnerApprovalOneStatusPollWithTheTokenThenNothing(t *testing.T) {
	s := pairServer(t, ownerPrincipal(), PairingLimits{})
	defer s.close()
	_, ownerToken, _ := s.issue(ownerPrincipal(), "owner", "device")
	owner := bearer(ownerToken)
	begun := begunOf(t, postJSON(s, PairPath, map[string]any{"label": "laptop"}, nil))
	if !regexp.MustCompile(`^[A-HJKMNP-Z2-9]{4}-[A-HJKMNP-Z2-9]{4}$`).MatchString(begun.Code) {
		t.Fatalf("code = %q", begun.Code)
	}
	if got := postJSON(s, PairPath+"/poll", map[string]any{"poll": begun.Poll}, nil); got.status != 200 || string(got.body) != `{"status":"waiting"}` {
		t.Fatalf("poll = %d %s", got.status, got.body)
	}
	// Anonymous and non-owner principals cannot see or approve it.
	if status := s.send("GET", PairPath+"/"+begun.Code, nil, "").status; status != 401 {
		t.Fatalf("anonymous page = %d, want 401", status)
	}
	_, userToken, _ := s.issue(delta.JsonObjectOf("kind", "user", "id", "u", "name", "u"), "u", "device")
	if status := s.send("POST", PairPath+"/"+begun.Code, bearer(userToken), "").status; status != 403 {
		t.Fatalf("user approval = %d, want 403", status)
	}
	if page := s.send("GET", PairPath+"/"+strings.ToLower(begun.Code), owner, ""); !strings.Contains(string(page.body), "laptop") {
		t.Fatalf("page = %q", page.body)
	}
	if status := s.send("POST", PairPath+"/"+begun.Code, owner, "").status; status != 200 {
		t.Fatalf("approval = %d, want 200", status)
	}
	// Approved once: the code is no longer waiting.
	if status := s.send("POST", PairPath+"/"+begun.Code, owner, "").status; status != 404 {
		t.Fatalf("second approval = %d, want 404", status)
	}
	approved := postJSON(s, PairPath+"/poll", map[string]any{"poll": begun.Poll}, nil)
	var token struct{ Status, Token string }
	if err := json.Unmarshal(approved.body, &token); err != nil || token.Status != "approved" {
		t.Fatalf("approved = %d %s", approved.status, approved.body)
	}
	if status := postJSON(s, PairPath+"/poll", map[string]any{"poll": begun.Poll}, nil).status; status != 404 {
		t.Fatalf("second poll = %d, want 404", status)
	}
	id, _, _ := strings.Cut(token.Token, ".")
	listed, ok := session(t, s, id).(*delta.JsonObject)
	if !ok || listed.Value("label") != "laptop" || listed.Value("kind") != "device" || fmt.Sprint(listed.Value("principal")) != fmt.Sprint(ownerPrincipal()) {
		t.Fatalf("listed = %v", listed)
	}
}

func TestRefusesAnUnknownPollSecret(t *testing.T) {
	s := pairServer(t, ownerPrincipal(), PairingLimits{})
	defer s.close()
	if status := postJSON(s, PairPath+"/poll", map[string]any{"poll": "nope"}, nil).status; status != 404 {
		t.Fatalf("unknown poll = %d, want 404", status)
	}
	if status := postJSON(s, PairPath+"/poll", map[string]any{}, nil).status; status != 404 {
		t.Fatalf("no poll = %d, want 404", status)
	}
}

func TestAnOwnerTheAppChoosesAloneApprovesGetsTheDeviceSessionAndIsNotCounted(t *testing.T) {
	ada := adaUser()
	s := pairServer(t, ada, PairingLimits{MaxPending: 1, PerAddress: 1})
	defer s.close()
	_, ownerToken, _ := s.issue(ada, "ada", "device")
	_, otherToken, _ := s.issue(delta.JsonObjectOf("kind", "user", "id", "u2"), "u2", "device")
	owner, other := bearer(ownerToken), bearer(otherToken)
	begun := begunOf(t, postJSON(s, PairPath, map[string]any{"label": "phone"}, nil))
	if status := postJSON(s, PairPath, map[string]any{}, other).status; status != 429 {
		t.Fatalf("other's pairing = %d, want 429", status)
	}
	if status := postJSON(s, PairPath, map[string]any{}, owner).status; status != 200 {
		t.Fatalf("owner's pairing = %d, want 200", status)
	}
	for _, step := range []struct {
		method string
		who    map[string]string
		want   int
	}{{"GET", other, 403}, {"POST", other, 403}, {"GET", owner, 200}, {"POST", owner, 200}} {
		if status := s.send(step.method, PairPath+"/"+begun.Code, step.who, "").status; status != step.want {
			t.Fatalf("%s as that principal = %d, want %d", step.method, status, step.want)
		}
	}
	var token struct{ Token string }
	if err := json.Unmarshal(postJSON(s, PairPath+"/poll", map[string]any{"poll": begun.Poll}, nil).body, &token); err != nil {
		t.Fatal(err)
	}
	id, _, _ := strings.Cut(token.Token, ".")
	listed, ok := session(t, s, id).(*delta.JsonObject)
	if !ok || listed.Value("label") != "phone" || fmt.Sprint(listed.Value("principal")) != fmt.Sprint(ada) {
		t.Fatalf("listed = %v", listed)
	}
}

func TestRefusesPairingsPastTheLimitPerClientAddressAndInAllWith429(t *testing.T) {
	s := pairServer(t, ownerPrincipal(), PairingLimits{MaxPending: 5, PerAddress: 3})
	var statuses []int
	for range 4 {
		statuses = append(statuses, postJSON(s, PairPath, map[string]any{}, nil).status)
	}
	s.close()
	if fmt.Sprint(statuses) != "[200 200 200 429]" {
		t.Fatalf("statuses = %v", statuses)
	}
	pairings := NewPairings(PairingLimits{MaxPending: 5, PerAddress: 3})
	var begun []bool
	for _, address := range []string{"a", "a", "b", "b", "c", "c", "d"} {
		_, ok := pairings.Begin("x", anonymous(address))
		begun = append(begun, ok)
	}
	if fmt.Sprint(begun) != "[true true true true true false false]" {
		t.Fatalf("begun = %v", begun)
	}
}

func TestFreesAClientAddressesPlaceOnceItsPairingEnds(t *testing.T) {
	pairings := NewPairings(PairingLimits{PerAddress: 1})
	first, ok := pairings.Begin("x", anonymous("a"))
	if !ok {
		t.Fatal("the first pairing was refused")
	}
	if _, ok := pairings.Begin("x", anonymous("a")); ok {
		t.Fatal("a second pairing of the address was admitted")
	}
	pairing, ok := pairings.Waiting(first.Code)
	if !ok {
		t.Fatal("the pairing is not waiting")
	}
	pairing.Approve("t")
	if polled, ok := pairings.Poll(first.Poll); !ok || polled != (Polled{Kind: PollApproved, Token: "t"}) {
		t.Fatalf("poll = %+v %v", polled, ok)
	}
	if _, ok := pairings.Begin("x", anonymous("a")); !ok {
		t.Fatal("the address's place was not freed")
	}
}

func TestLetsTheSignedInOwnerPairPastTheCapsWithoutCountingIt(t *testing.T) {
	s := pairServer(t, ownerPrincipal(), PairingLimits{MaxPending: 1, PerAddress: 1})
	_, ownerToken, _ := s.issue(ownerPrincipal(), "owner", "device")
	owner := bearer(ownerToken)
	if status := postJSON(s, PairPath, map[string]any{}, nil).status; status != 200 {
		t.Fatalf("first anonymous pairing = %d, want 200", status)
	}
	if status := postJSON(s, PairPath, map[string]any{}, nil).status; status != 429 {
		t.Fatalf("second anonymous pairing = %d, want 429", status)
	}
	for range 3 {
		if status := postJSON(s, PairPath, map[string]any{}, owner).status; status != 200 {
			t.Fatalf("owner's pairing = %d, want 200", status)
		}
	}
	s.close()
	pairings := NewPairings(PairingLimits{MaxPending: 1})
	_, byOwner := pairings.Begin("x", PairingStarter{Owner: true})
	_, byAnonymous := pairings.Begin("x", anonymous("a"))
	if !byOwner || !byAnonymous {
		t.Fatalf("owner %v, anonymous %v: the owner's pairing was counted", byOwner, byAnonymous)
	}
}

func TestPairingCodesExpireAfter10MinutesForTheOwnerAndThePollAlike(t *testing.T) {
	clock, restore := jsclock.MockDateNow(1_000_000)
	defer restore()
	pairings := NewPairings(PairingLimits{})
	begun, ok := pairings.Begin("device", anonymous("1.2.3.4"))
	if !ok {
		t.Fatal("refused")
	}
	clock.Set(1_000_000 + 10*60_000 - 1)
	if _, ok := pairings.Waiting(begun.Code); !ok {
		t.Fatal("expired a millisecond early")
	}
	clock.Set(1_000_000 + 10*60_000)
	if _, ok := pairings.Waiting(begun.Code); ok {
		t.Fatal("still waiting at 10 minutes")
	}
	if _, ok := pairings.Poll(begun.Poll); ok {
		t.Fatal("the poll outlived the code")
	}
}

func TestPairingCodesAreDistinct(t *testing.T) {
	pairings := NewPairings(PairingLimits{MaxPending: 200})
	codes := map[string]struct{}{}
	for index := range 200 {
		begun, ok := pairings.Begin("d", anonymous(fmt.Sprint(index)))
		if !ok {
			t.Fatalf("pairing %d was refused", index)
		}
		codes[begun.Code] = struct{}{}
	}
	if len(codes) != 200 {
		t.Fatalf("%d distinct codes of 200", len(codes))
	}
}

// Beyond Pi's tests: the address a pairing is counted against is the connection's peer address (HttpRequest.RemoteAddress).
func TestPairingsOfOneAddressShareItsLimitAndTheLabelComesFromTheBody(t *testing.T) {
	s := pairServer(t, ownerPrincipal(), PairingLimits{PerAddress: 1})
	defer s.close()
	_, ownerToken, _ := s.issue(ownerPrincipal(), "owner", "device")
	begun := begunOf(t, postJSON(s, PairPath, map[string]any{"label": "  " + strings.Repeat("é", 150) + "  "}, nil))
	if status := postJSON(s, PairPath, map[string]any{}, nil).status; status != 429 {
		t.Fatalf("second pairing of 127.0.0.1 = %d, want 429", status)
	}
	if got := string(s.send("GET", PairPath+"/"+begun.Code, bearer(ownerToken), "").body); !strings.Contains(got, strings.Repeat("é", 100)+"</b>") {
		t.Fatalf("the label is not clipped to 100 characters: %q", got)
	}
	if got := begunOf(t, s.send("POST", PairPath, bearer(ownerToken), "")); got.Code == "" {
		t.Fatal("a pairing without a body")
	}
}
