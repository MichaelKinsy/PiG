package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
)

// Not upstream's: what an upgrade across Pi ff7414d51 ("Sign-ins, not sessions") does to a kit app that already ran. Pi
// has no migration and says so (decision "Sign-ins, not sessions": "a kit app's existing sign-ins and cookies are not read
// under the new names (everyone signs in again)"); PiG does the same, and this test is what that sentence means for the
// people using the app:
//
//   - the document the kit stored before (`chord.app.sessions`, field `sessions`) stays in the store as it was: nothing
//     reads it, touches it or removes it;
//   - a browser holding the old cookie `chord-session` is nobody: 401, no redirect, no cookie set or cleared; the owner
//     opens the start's owner link again and gets the cookie `chord-sign-in`;
//   - a paired device's token is nobody: 401; it pairs again and its new token signs it in;
//   - the owner token file is replaced at the next start (its token authenticates nobody, so the kit writes a new device
//     sign-in of the owner there, mode 0600): a command-line client on the same machine, which reads the file, keeps
//     working.
//
// The old layout is written here by a facet declared with the names the kit had before the rename (the contract, its
// document and its field as sessions.go declared them), on a host without the kit.

// upgradeOldSessions is the kit's object before the rename, as far as its stored document goes: `put` stores one entry of
// the field `sessions` as `issue` did, `all` answers the field.
var upgradeOldSessions = mustLocal("chord.app.sessions", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("put", contracts.NewMethod(contracts.Obj(
			contracts.Prop("id", contracts.Str()),
			contracts.Prop("entry", contracts.Unknown()),
		))),
		contracts.Mem("all", contracts.NewMethod(contracts.Str(), contracts.Unknown())),
	},
})

// upgradeOldDocument is the stored document before the rename, declared as sessions.go declared it.
var upgradeOldDocument = mustState("chord.app.sessions", documents.StateOptions{
	Schema: contracts.Obj(contracts.Prop("sessions", contracts.Map(contracts.Obj(
		contracts.Prop("hash", contracts.Str()),
		contracts.Prop("label", contracts.Str()),
		contracts.Prop("kind", contracts.OneOf("browser", "device")),
		contracts.Prop("principal", contracts.Str()),
		contracts.Prop("created", contracts.Num()),
		contracts.Prop("lastUsed", contracts.Num()),
	)))),
	Version: 1,
	Initial: func() *delta.JsonObject { return delta.JsonObjectOf("sessions", delta.NewJsonObject(0)) },
	Persist: true,
	CheckpointWhen: func(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
		return info.DeltasSinceBase >= 50
	},
})

// upgradeOldKit is a host on the store of dir serving only the old object.
type upgradeOldKit struct{ *kitHost }

func upgradeOldHost(t *testing.T, dir string) *upgradeOldKit {
	t.Helper()
	chordStore := openStoreAt(t, dir+"/store.sqlite")
	hostErrors := &errorLog{}
	h, err := host.CreateHost(host.HostOptions{Store: chordStore, Principal: delta.JsonObjectOf("kind", "server"), OnError: hostErrors.push})
	if err != nil {
		t.Fatal(err)
	}
	facet := host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(upgradeOldSessions, func(_ *delta.JsonObject, self host.Self) (contracts.MembersImplementation, error) {
			document := self.State(upgradeOldDocument).(*documents.Singleton)
			return contracts.MembersImplementation{
				Methods: map[string]contracts.MethodFunction{
					"put": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						arguments := args.(*delta.JsonObject)
						_, err := document.Change(ctx, func(draft *delta.Object) (any, error) {
							return nil, draft.Object("sessions").Set(arguments.Value("id").(string), arguments.Value("entry").(*delta.JsonObject))
						})
						return nil, err
					},
					"all": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
						value, err := document.Value()
						if err != nil {
							return nil, err
						}
						return value.Value("sessions"), nil
					},
				},
			}, nil
		}, host.ServeOptions{})
	})
	report, err := h.Load([]host.HostFacet{{Name: "chord.app.sessions", Facet: facet}}, background)
	if err != nil {
		t.Fatal(err)
	}
	for _, failed := range report.Failed {
		t.Fatalf("plugin %q was left out: %v", failed.Plugin, failed.Error)
	}
	return &upgradeOldKit{&kitHost{t: t, store: chordStore, host: h, errors: hostErrors}}
}

// call calls a method of the old object.
func (k *upgradeOldKit) call(method string, args delta.JsonValue) delta.JsonValue {
	k.t.Helper()
	old, err := k.host.Get(background, upgradeOldSessions, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		k.t.Fatal(err)
	}
	defer old.Release()
	value, err := old.Call(method, args, background)
	if err != nil {
		k.t.Fatal(err)
	}
	return value
}

// issue stores what the kit's `issue` stored before the rename for a browser or device of principal, and answers its
// token.
func (k *upgradeOldKit) issue(principal delta.JsonValue, label, kind string) string {
	k.t.Helper()
	id, secret := NewToken(12), NewToken(32)
	stored, err := json.Marshal(principal)
	if err != nil {
		k.t.Fatal(err)
	}
	now := jsclock.DateNow()
	k.call("put", delta.JsonObjectOf("id", id, "entry", delta.JsonObjectOf(
		"hash", HashToken(secret), "label", label, "kind", kind, "principal", string(stored), "created", now, "lastUsed", now,
	)))
	return id + "." + secret
}

// stored is the old document's field as JSON text.
func (k *upgradeOldKit) stored() string {
	k.t.Helper()
	text, err := json.Marshal(k.call("all", ""))
	if err != nil {
		k.t.Fatal(err)
	}
	return string(text)
}

func TestAnUpgradeAcrossTheRenameSignsEveryBrowserAndPairedDeviceOutAndReplacesTheOwnerTokenFile(t *testing.T) {
	dir := t.TempDir()
	owner := ownerPrincipal()
	tokenPath := filepath.Join(dir, "owner-token")

	// The data directory as the kit left it before the rename: the owner's browser, a paired device and the owner token
	// file's device, all in the old document; the token file itself.
	old := upgradeOldHost(t, dir)
	browser := old.issue(owner, "owner browser", "browser")
	laptop := old.issue(owner, "laptop", "device")
	file := old.issue(owner, "owner token file", "device")
	before := old.stored()
	old.close()
	if strings.Count(before, `"hash"`) != 3 {
		t.Fatalf("the old document holds %s, want the three sessions", before)
	}
	if err := os.WriteFile(tokenPath, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}

	// The kit of this version starts on it.
	link := &OwnerLink{}
	linkToken := link.Renew()
	s := newKitServer(t, kitServerOptions{Dir: dir, Facets: []host.HostFacet{
		{Name: "chord.app.owner", Facet: OwnerFacet(link, owner)},
		{Name: "chord.app.pairing", Facet: PairingFacet(NewPairings(PairingLimits{}), owner)},
	}})

	// The browser: its old cookie is nobody. It gets a plain 401: it is sent nowhere and its cookie is left alone.
	oldCookie := upgradeOldCookie(browser)
	refused := s.send("GET", "/whoami", map[string]string{"cookie": oldCookie}, "")
	if refused.status != 401 || refused.headers.Get("location") != "" || refused.headers.Get("set-cookie") != "" {
		t.Fatalf("the old cookie: %d, Location %q, Set-Cookie %q; want 401 and neither header", refused.status, refused.headers.Get("location"), refused.headers.Get("set-cookie"))
	}
	// Nor does its token sign in under the new cookie's name: the sign-in is not in the new document.
	renamed := strings.Replace(oldCookie, "chord-session=", SignInCookie+"=", 1)
	if status := s.send("GET", "/whoami", map[string]string{"cookie": renamed}, "").status; status != 401 {
		t.Fatalf("the old token under the new cookie name = %d, want 401", status)
	}
	// The owner signs in again with this start's owner link and holds the new cookie.
	signed := postToken(s, linkToken)
	if signed.status != 200 || !strings.HasPrefix(cookieOf(signed), "chord-sign-in=") {
		t.Fatalf("the owner link: %d %q, want 200 and the cookie chord-sign-in", signed.status, cookieOf(signed))
	}
	if got := whoamiOf(t, s, map[string]string{"cookie": cookieOf(signed)}); got != `{"kind":"owner"}` {
		t.Fatalf("whoami after signing in again = %s", got)
	}

	// The paired device: its token is nobody; it pairs again (the owner approves) and its new token signs it in.
	if status := s.send("GET", "/whoami", bearer(laptop), "").status; status != 401 {
		t.Fatalf("the paired device's old token = %d, want 401", status)
	}
	begun := begunOf(t, postJSON(s, PairPath, map[string]any{"label": "laptop"}, nil))
	_, approver, _ := s.issue(owner, "owner", "device")
	if status := s.send("POST", PairPath+"/"+begun.Code, bearer(approver), "").status; status != 200 {
		t.Fatalf("the approval of the new pairing = %d, want 200", status)
	}
	var paired struct{ Status, Token string }
	if approved := postJSON(s, PairPath+"/poll", map[string]any{"poll": begun.Poll}, nil); json.Unmarshal(approved.body, &paired) != nil || paired.Status != "approved" {
		t.Fatalf("the new pairing's poll = %d %s", approved.status, approved.body)
	}
	if got := whoamiOf(t, s, bearer(paired.Token)); got != `{"kind":"owner"}` {
		t.Fatalf("whoami of the device paired again = %s", got)
	}

	// The owner token file: replaced at the start (the kit calls OwnerTokenFile then), mode 0600; the local command-line
	// client reads the file and is the owner; the token the file held is nobody.
	replaced := tokenFile(t, s, tokenPath, owner)
	if replaced == file || readToken(t, tokenPath) != replaced {
		t.Fatalf("the owner token file was not replaced: it holds the old token: %v", readToken(t, tokenPath) == file)
	}
	mustMode0600(t, tokenPath)
	if got := whoamiOf(t, s, bearer(readToken(t, tokenPath))); got != `{"kind":"owner"}` {
		t.Fatalf("whoami with the replaced owner token file = %s", got)
	}
	if status := s.send("GET", "/whoami", bearer(file), "").status; status != 401 {
		t.Fatalf("the owner token file's old token = %d, want 401", status)
	}

	// The kit's list holds what was issued since the upgrade and none of the old document's.
	for _, token := range []string{browser, laptop, file} {
		id, _, _ := strings.Cut(token, ".")
		if listedSignIn(t, s, id) != nil {
			t.Fatalf("the old document's %s is listed by the kit", id)
		}
	}
	signInWrites.Wait()
	s.close()

	// The old document is still in the store, exactly as it was: not read, not touched (no `lastUsed` moved), not removed.
	again := upgradeOldHost(t, dir)
	after := again.stored()
	again.close()
	if after != before {
		t.Fatalf("the old document changed:\nbefore %s\nafter  %s", before, after)
	}
}
