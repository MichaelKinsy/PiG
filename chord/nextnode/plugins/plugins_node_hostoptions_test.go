package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Not upstream's host options (docs/plan/chord-plugins-node-evaluator.md, 10.6 #7): `principalKey` and `revokedBy` of the app's host are the Node host's.
// Pi's host calls them synchronously, so the Node host calls the Go functions on a second connection of the socket
// (callbacks.mjs `callSync`). Two cases port Pi's own: host.test.ts's "identity includes the principal key: equal keys share
// one object", and revocation.test.ts's "revoke on one host ends the principal's objects at once, whatever references remain:
// each hold gets gone forbidden", with the owner's hold, kept.

// cbWhoContract is a remote type on the worker whose one state member, `log`, is the principal kinds of the objects the plugin
// made so far, joined by `;` at the time this object was made: the factory calls of host.test.ts's `Who` as data.
var cbWhoContract = mustContract(contracts.Remote("test.who", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return workerKind.Address(nil), nil },
	Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
		State: []contracts.Property{contracts.Prop("log", contracts.Str())},
	})},
}))

// cbWhoPlugin serves `test.who`.
func cbWhoPlugin(t *testing.T) PluginScript {
	t.Helper()
	return handmade(t, "@test/who", "worker", `(function (require, module, exports) {
const { defineFacet, remote, str, hostKind, constant } = require("@earendil-works/chord/next");
const Worker = hostKind("test.worker");
const Who = remote("test.who", {
	version: 1,
	params: {},
	hostAddress: () => Worker(),
	variants: { main: { state: { log: str() } } },
});
const made = [];
module.exports = { default: defineFacet((host) =>
	host.serve(Who, (_params, principal) => {
		made.push(principal.kind);
		return { variant: "main", members: { log: constant(made.join(";")) } };
	}),
) };
})`)
}

var (
	cbGuest1 = delta.JsonObjectOf("kind", "guest", "session", "S1")
	cbGuest2 = delta.JsonObjectOf("kind", "guest", "session", "S2")
	cbOwner  = delta.JsonObjectOf("kind", "owner")
)

// cbRevokedBy is the test app's revocations: a guest's session, or every owner (revocation.test.ts's `revokedBy`).
func cbRevokedBy(principal data.Principal) ([]delta.JsonValue, error) {
	switch principal.Value("kind") {
	case "guest":
		return []delta.JsonValue{delta.JsonObjectOf("session", principal.Value("session"))}, nil
	case "owner":
		return []delta.JsonValue{delta.JsonObjectOf("owners", true)}, nil
	}
	return nil, nil
}

// cbAs runs fn as principal on server: a scope whose `ctx.Get` acts as it. fn runs on a goroutine of the host's, so it returns
// its failures (a Fatal there would never settle the scope).
func cbAs(server host.Host, principal data.Principal, fn func(ctx host.Ctx) error) error {
	_, err := server.Root(func(ctx host.Ctx) (any, error) { return nil, fn(ctx) }, background, host.RootOptions{Principal: principal})
	return err
}

// cbHold takes the `test.who` object ctx's principal holds.
func cbHold(ctx host.Ctx) (*host.Reference, error) {
	return ctx.Get(cbWhoContract, delta.NewJsonObject(0), host.GetOptions{})
}

// cbLog reads the `log` of the `test.who` object ctx's principal holds, and keeps the reference until the scope ends.
func cbLog(ctx host.Ctx) (string, error) {
	ref, err := cbHold(ctx)
	if err != nil {
		return "", err
	}
	value, err := ref.Member("log").Value()
	if err != nil {
		return "", err
	}
	return value.(string), nil
}

// host.test.ts › identity includes the principal key: equal keys share one object. The app's `PrincipalKey` keys a principal by
// its kind, so two guests of different sessions share one object (the plugin's factory ran once) and an owner has its own.
// Without it (canonical JSON) the second guest would have an object of its own.
func TestPrincipalKeyOfTheAppIsTheNodeHostsForIdentities(t *testing.T) {
	byKind := data.NewPrincipalKeyFunction(func(principal data.Principal) string { return principal.Value("kind").(string) })
	address := workerKind.Address(nil)
	node, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
		Kind: "worker", Scripts: []PluginScript{cbWhoPlugin(t)}, Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
		PrincipalKey: byKind,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	server := serverFor(t, workerRoute(node))
	var logs []string
	err = cbAs(server, cbGuest1, func(ctx host.Ctx) error {
		log, err := cbLog(ctx)
		if err != nil {
			return err
		}
		logs = append(logs, log)
		return cbAs(server, cbGuest2, func(ctx host.Ctx) error {
			log, err := cbLog(ctx)
			if err != nil {
				return err
			}
			logs = append(logs, log)
			return cbAs(server, cbOwner, func(ctx host.Ctx) error {
				log, err := cbLog(ctx)
				logs = append(logs, log)
				return err
			})
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	// The factories ran for guest, and for owner: guest S2 found guest S1's object.
	if want := []string{"guest", "guest", "guest;owner"}; fmt.Sprint(logs) != fmt.Sprint(want) {
		t.Fatalf("logs = %q, want %q", logs, want)
	}
}

// The host asks for the key of every principal object it decodes (one per request), and a synchronous call costs tens of
// microseconds (BenchmarkNodeSyncCall), so the Node host keeps the answers by the principal's canonical JSON: three holds of
// one principal (the second with its keys in another order) and one of another ask the Go function twice, not four times.
func TestPrincipalKeyIsAskedOncePerCanonicalPrincipal(t *testing.T) {
	var mu sync.Mutex
	asked := map[string]int{}
	counting := data.NewPrincipalKeyFunction(func(principal data.Principal) string {
		mu.Lock()
		defer mu.Unlock()
		if session, ok := principal.Value("session").(string); ok {
			asked[session]++
		}
		return principal.Value("kind").(string)
	})
	address := workerKind.Address(nil)
	node, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
		Kind: "worker", Scripts: []PluginScript{cbWhoPlugin(t)}, Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
		PrincipalKey: counting,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	server := serverFor(t, workerRoute(node))
	reordered := delta.JsonObjectOf("session", "S1", "kind", "guest")
	for _, principal := range []data.Principal{cbGuest1, reordered, cbGuest1, cbGuest2} {
		if err := cbAs(server, principal, func(ctx host.Ctx) error { _, err := cbLog(ctx); return err }); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if want := map[string]int{"S1": 1, "S2": 1}; !reflect.DeepEqual(asked, want) {
		t.Fatalf("the Go function was asked %v times per session, want %v", asked, want)
	}
}

// The kept answers are bounded: the oldest is dropped first (the memo of node/callbacks.mjs, with a limit of 2).
func TestPrincipalKeyMemoIsBounded(t *testing.T) {
	cache := t.TempDir()
	tree, err := extractNode(cache)
	if err != nil {
		t.Fatal(err)
	}
	callbacks := "file://" + filepath.ToSlash(filepath.Join(tree, "callbacks.mjs"))
	address := workerKind.Address(nil)
	node, _, err := NewPluginRuntime(NodeOptions{CacheDir: cache}).Load(RuntimeLoad{
		Kind: "worker", Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
		Core: []CoreFacet{probeCore(t, cache, `memo: async () => {
	const { memoizedByPrincipal } = await import("`+callbacks+`");
	let calls = 0;
	const key = memoizedByPrincipal((principal) => JSON.stringify(principal), (principal) => { calls++; return principal.id; }, 2);
	const keys = [1, 2, 1, 3, 1, 2].map((id) => key({ id }));
	return { calls, keys };
},`)},
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	got := probeOn(t, serverFor(t, workerRoute(node))).must("memo", nil)
	// Misses: 1, 2, (1 hits), 3 (drops 1), 1 (drops 2), 2 (drops 3).
	if want := map[string]any{"calls": 5.0, "keys": []any{1.0, 2.0, 1.0, 3.0, 1.0, 2.0}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("memo = %v, want %v", got, want)
	}
}

// cbRevocation loads the `test.who` plugin on a Node host whose `RevokedBy` is the app's, and returns it with a Go host (which
// has no `RevokedBy` of its own, so only the Node host can revoke what its plugin serves) that routes to it.
func cbRevocation(t *testing.T) (*NodeHost, host.Host) {
	t.Helper()
	address := workerKind.Address(nil)
	node, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
		Kind: "worker", Scripts: []PluginScript{cbWhoPlugin(t)}, Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
		RevokedBy: cbRevokedBy,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	return node, serverFor(t, workerRoute(node))
}

// cbEnded waits until ref ended and returns its error.
func cbEnded(ref *host.Reference) (*data.ChordError, error) {
	deadline := time.Now().Add(15 * time.Second)
	for ref.State().Value().Kind != host.RefStateEnded {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the reference is %v, want it ended", ref.State().Value().Kind)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return ref.State().Value().Error, nil
}

// cbForbidden is the error of a reference that ended revoked: forbidden/revoked.
func cbForbidden(chord *data.ChordError, err error) error {
	if err != nil {
		return err
	}
	if chord == nil {
		return errors.New("the reference ended with no error, want forbidden/revoked")
	}
	if chord.Code != data.CodeForbidden || chord.Reason != data.ReasonRevoked {
		return fmt.Errorf("the reference ended with %w, want forbidden/revoked", chord)
	}
	return nil
}

// revocation.test.ts › revoke on one host › ends the principal's objects at once, whatever references remain: each hold gets
// gone forbidden/revoked; the owner's hold stays. `revokedBy` is the app's Go function, which the Node host calls once per
// principal; `NodeHost.Revoke` forwards `host.revoke` to the Node host.
func TestRevokeNodeHostEndsTheRevokedPrincipalsObjectsAndKeepsTheOwners(t *testing.T) {
	node, server := cbRevocation(t)
	err := cbAs(server, cbGuest1, func(ctx host.Ctx) error {
		ref, err := cbHold(ctx)
		if err != nil {
			return err
		}
		return cbAs(server, cbOwner, func(owner host.Ctx) error {
			kept, err := cbHold(owner)
			if err != nil {
				return err
			}
			if err := node.Revoke(delta.JsonObjectOf("session", "S1"), background); err != nil {
				return err
			}
			if err := cbForbidden(cbEnded(ref)); err != nil {
				return err
			}
			if kind := kept.State().Value().Kind; kind != host.RefStateLive {
				return fmt.Errorf("the owner's reference is %v, want live (S1's revocation does not revoke it)", kind)
			}
			// A revocation of owners revokes it.
			if err := node.Revoke(delta.JsonObjectOf("owners", true), background); err != nil {
				return err
			}
			return cbForbidden(cbEnded(kept))
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// revocation.test.ts › revoke on one host: a Go host that routes to the Node host sends `revoke` down the route
// (`host.Revoke`), which the Node host applies with its own `revokedBy` (a call to the Go function): the objects of the
// revoked principal end there, though the Go host's own `RevokedBy` is none.
func TestRevokeAGoHostsRevokeAppliesTheNodeHostsRevokedBy(t *testing.T) {
	_, server := cbRevocation(t)
	err := cbAs(server, cbGuest1, func(ctx host.Ctx) error {
		ref, err := cbHold(ctx)
		if err != nil {
			return err
		}
		if err := server.Revoke(delta.JsonObjectOf("session", "S1"), background); err != nil {
			return err
		}
		return cbForbidden(cbEnded(ref))
	})
	if err != nil {
		t.Fatal(err)
	}
}

// An error of the app's `revokedBy` is Pi's throw: Revoke returns it.
func TestRevokeAnErrorOfRevokedByIsReturned(t *testing.T) {
	address := workerKind.Address(nil)
	node, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
		Kind: "worker", Scripts: []PluginScript{cbWhoPlugin(t)}, Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
		RevokedBy: func(data.Principal) ([]delta.JsonValue, error) { return nil, errors.New("revokedBy failed") },
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	server := serverFor(t, workerRoute(node))
	err = cbAs(server, cbGuest1, func(ctx host.Ctx) error {
		if _, err := cbHold(ctx); err != nil {
			return err
		}
		err := node.Revoke(delta.JsonObjectOf("session", "S1"), background)
		if err == nil {
			return errors.New("Revoke returned no error, want the error of revokedBy")
		}
		if !strings.Contains(err.Error(), "revokedBy failed") {
			return fmt.Errorf("Revoke = %w, want the error of revokedBy", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// cbNestedProbe loads a core facet probe (the ops run in the process with `callbacks`, its module, in scope) on a Node host
// that has a synchronous connection (it has a RevokedBy).
func cbNestedProbe(t *testing.T, ops string) (*NodeHost, *probe) {
	t.Helper()
	cache := t.TempDir()
	tree, err := extractNode(cache)
	if err != nil {
		t.Fatal(err)
	}
	callbacks := "file://" + filepath.ToSlash(filepath.Join(tree, "callbacks.mjs"))
	address := workerKind.Address(nil)
	node, _, err := NewPluginRuntime(NodeOptions{CacheDir: cache}).Load(RuntimeLoad{
		Kind: "worker", Core: []CoreFacet{probeCore(t, cache, `callSync: async ({ name, args }) => (await import("`+callbacks+`")).callSync(name, args), `+ops)},
		Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
		RevokedBy: func(data.Principal) ([]delta.JsonValue, error) { return nil, nil },
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	return node, probeOn(t, serverFor(t, workerRoute(node)))
}

// A synchronous callback runs while the process's host thread is blocked, so a call from it to its NodeHost would wait for ever:
// every NodeHost call that waits for the process is refused with ErrSyncCallback, with the context the callback got; a
// context of anything else is served as it always is.
func TestSyncACallbackCannotCallItsNodeHost(t *testing.T) {
	node, p := cbNestedProbe(t, "")
	refused := make(chan map[string]bool, 1)
	node.handleSync("test.nested", func(ctx context.Context, _ json.RawMessage) (any, error) {
		_, reloadErr := NewPluginRuntime(NodeOptions{}).Reload(node, nil, ctx)
		refused <- map[string]bool{
			"revoke": errors.Is(node.Revoke(delta.JsonObjectOf("a", 1), ctx), ErrSyncCallback),
			"reload": errors.Is(reloadErr, ErrSyncCallback),
			"close":  errors.Is(node.Close(ctx), ErrSyncCallback),
		}
		return "returned", nil
	})
	answered := make(chan any, 1)
	go func() { answered <- p.must("callSync", map[string]any{"name": "test.nested", "args": nil}) }()
	select {
	case got := <-answered:
		if got != "returned" {
			t.Fatalf("the callback answered %v", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the synchronous callback never returned: a call to the NodeHost waited for the blocked process")
	}
	if got := <-refused; !got["revoke"] || !got["reload"] || !got["close"] {
		t.Fatalf("refused with ErrSyncCallback = %v, want revoke, reload and close", got)
	}
	// An ordinary context is served: the process is not blocked any more.
	if err := node.Revoke(delta.JsonObjectOf("a", 1), background); err != nil {
		t.Fatalf("Revoke after the callback = %v", err)
	}
}

// A synchronous call of a name no Go function answers throws in the process, and so does an error of the Go function (the
// message is the Go error's); the connection serves the next call.
func TestSyncAnErrorOfTheGoFunctionIsThrownAndTheConnectionServesTheNext(t *testing.T) {
	node, p := cbNestedProbe(t, "")
	node.handleSync("test.fails", func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("the callback failed") })
	node.handleSync("test.ok", func(context.Context, json.RawMessage) (any, error) { return []any{1, "two"}, nil })
	for name, want := range map[string]string{
		"test.fails":  "the callback failed",
		"test.nobody": `plugins: no synchronous Go function "test.nobody" is offered`,
	} {
		if got, err := p.call("callSync", map[string]any{"name": name, "args": nil}); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("callSync(%s) = %v, %v, want an error with %q", name, got, err, want)
		}
	}
	if got := p.must("callSync", map[string]any{"name": "test.ok", "args": nil}); fmt.Sprint(got) != fmt.Sprint([]any{1.0, "two"}) {
		t.Fatalf("callSync(test.ok) = %v", got)
	}
}

// BenchmarkNodeSyncCall is the cost of one synchronous call of the process to the Go side as the Node host makes it for
// `principalKey`: the host thread posts the request to its worker thread, which sends it over the second connection, Go
// answers, and the worker wakes the host thread. The loop runs in the process, so the figure has no Go-to-Node hop in it.
// `uncached` is the call itself (every call a new principal); `memoized` is a call of the memo the host keeps the answers in
// (node/callbacks.mjs `memoizedByPrincipal`) over 8 principals, so all but 8 are answered without the Go side.
func BenchmarkNodeSyncCall(b *testing.B) {
	byKind := data.NewPrincipalKeyFunction(func(principal data.Principal) string { return principal.Value("kind").(string) })
	cache := b.TempDir()
	tree, err := extractNode(cache)
	if err != nil {
		b.Fatal(err)
	}
	module := filepath.Join(b.TempDir(), "cost.mjs")
	if err := os.WriteFile(module, []byte(`import * as next from "@earendil-works/chord/next";
import { callSync, memoizedByPrincipal } from "`+"file://"+filepath.ToSlash(filepath.Join(tree, "callbacks.mjs"))+`";
const { defineFacet, remote, method, str, hostKind } = next;
const Probe = remote("test.probe", {
	version: 1,
	params: {},
	hostAddress: () => hostKind("test.worker")(),
	variants: { main: { state: {}, methods: { call: method(str(), str()) } } },
});
const uncached = (i) => callSync("principalKey", { kind: "guest", session: "S" + i });
const memoized = memoizedByPrincipal((principal) => JSON.stringify(principal), (principal) => callSync("principalKey", principal));
const keyOf = { uncached, memoized: (i) => memoized({ kind: "guest", session: "S" + (i % 8) }) };
export default defineFacet((host) =>
	host.serve(Probe, () => ({
		variant: "main",
		members: {
			call: async (request) => {
				const { n, mode } = JSON.parse(request);
				const start = process.hrtime.bigint();
				for (let i = 0; i < n; i++) keyOf[mode](i);
				return JSON.stringify(Number(process.hrtime.bigint() - start) / n);
			},
		},
	})),
);
`), 0o644); err != nil {
		b.Fatal(err)
	}
	address := workerKind.Address(nil)
	node, _, err := NewPluginRuntime(NodeOptions{CacheDir: cache}).Load(RuntimeLoad{
		Kind: "worker", Address: &address, Principal: workerPrincipal, Peer: serverPrincipal, PrincipalKey: byKind,
		Core: []CoreFacet{{Name: "@bench/cost", Module: module}},
	}, background)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = node.Close(background) })
	ref, err := serverFor(b, workerRoute(node)).Get(background, probeContract, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(ref.Release)
	for _, mode := range []string{"uncached", "memoized"} {
		b.Run(mode, func(b *testing.B) {
			b.ResetTimer()
			answer, err := ref.Call("call", `{"n":`+strconv.Itoa(b.N)+`,"mode":"`+mode+`"}`, background)
			if err != nil {
				b.Fatal(err)
			}
			perCall, err := strconv.ParseFloat(answer.(string), 64)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(perCall/1000, "us/call")
		})
	}
}
