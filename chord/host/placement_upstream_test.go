package host

// Ports packages/chord/test/placement.test.ts (Pi aa9cf15b, protocol 3). The test's last case checks TypeScript types
// (`@ts-expect-error`); Go's closed Placement union and NewHostFamily take the family's field as a string, and the
// runtime half of it (a remote contract without host, an empty prefix) is TestPlacementTypesAndRemoteContractsNeedAHost.

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

func placementWhere(name string) Implementation {
	return chordjson.ObjectOf("variant", "main", "where", methodFunc(func(any, Ctx) (any, error) { return name, nil }))
}

func placementContract(name string, host Placement, input Schema) *Contract {
	return Remote(name, RemoteDef{
		Version: 1,
		Input:   input,
		Host:    host,
		Variants: Fields[VariantDef]{{"main", VariantDef{
			Methods: Fields[MethodSpec]{{"where", Method(nil, tString())}},
		}}},
	})
}

var (
	// placementNote lives on `session:` + its input's `session`.
	placementNote = placementContract("t.note", NewHostFamily("session:", "session"), tObject("session", tString(), "id", tString()))
	// placementCatalog lives on `server`.
	placementCatalog = placementContract("t.catalog", NewHostNamed("server"), tObject())
)

type placementCluster struct {
	server, s1, s2 *HostImpl
	// toChild records what the server sends down each child's link, by host name.
	toChild map[string]*recordingLink
}

// placementClusterOf is placement.test.ts cluster(): a server with two session hosts as children; every host serves
// what it may and reports its name.
func placementClusterOf(t *testing.T) placementCluster {
	t.Helper()
	server := requestsHost(t, HostOptions{Name: "server", Self: "host"})
	mustServe(t, server, placementCatalog, func(any, Principal, Self) (Implementation, error) { return placementWhere("server"), nil }, ServeOptions{})
	var mu sync.Mutex
	links := map[string]*recordingLink{}
	child := func(name string) *HostImpl {
		memoryDown, up := MemoryLinkPair()
		down := &recordingLink{inner: memoryDown}
		mu.Lock()
		links[name] = down
		mu.Unlock()
		if err := server.Accept(down, AcceptOptions{Kind: KindHost, Name: &name, Principal: "server-side"}); err != nil {
			t.Fatal(err)
		}
		host := requestsHost(t, HostOptions{Name: name, Self: "host", Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: "server"}}})
		mustServe(t, host, placementNote, func(input any, _ Principal, _ Self) (Implementation, error) {
			return placementWhere(host.Name() + " " + inputField(input, "id").(string)), nil
		}, ServeOptions{})
		return host
	}
	s1 := child("session:S1")
	s2 := child("session:S2")
	// Relays need a route (where authorization lives); it finds the connected child.
	server.Route("session:", Route{
		Authorize: func(Principal, string) (bool, error) { return true, nil },
		Resolve: func(name string) (Link, error) {
			mu.Lock()
			defer mu.Unlock()
			return links[name], nil
		},
		Principal: func(name string) Principal { return name },
	})
	waitLinked(t, server, s1)
	waitLinked(t, server, s2)
	return placementCluster{server: server, s1: s1, s2: s2, toChild: links}
}

func placementGet(t *testing.T, host *HostImpl, typ *Contract, input any) string {
	t.Helper()
	ref := requestsGet(t, host, typ, input, nil)
	got, err := callMember(ref, "where", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got.(string)
}

// placement.test.ts "a get goes where the contract places the input: here, a child, or the parent, never named by the
// caller"
func TestPlacementAGetGoesWhereTheContractPlacesTheInputHereAChildOrTheParentNeverNamedByTheCaller(t *testing.T) {
	c := placementClusterOf(t)
	for _, test := range []struct {
		host  *HostImpl
		typ   *Contract
		input map[string]any
		want  string
	}{
		{c.server, placementNote, map[string]any{"session": "S1", "id": "a"}, "session:S1 a"},
		{c.server, placementNote, map[string]any{"session": "S2", "id": "b"}, "session:S2 b"},
		// From a session host: its own notes locally, the other session's through the server, the catalog upstream.
		{c.s1, placementNote, map[string]any{"session": "S1", "id": "c"}, "session:S1 c"},
		{c.s1, placementCatalog, map[string]any{}, "server"},
	} {
		if got := placementGet(t, test.host, test.typ, test.input); got != test.want {
			t.Errorf("%s get %v = %q, want %q", test.host.Name(), test.input, got, test.want)
		}
	}
}

// placement.test.ts "serve refuses a type the host is not the place of, also at a reload"
func TestPlacementServeRefusesATypeTheHostIsNotThePlaceOfAlsoAtAReload(t *testing.T) {
	host := requestsHost(t, HostOptions{Name: "session:S1", Self: "host"})
	factory := func(any, Principal, Self) (Implementation, error) { return placementWhere(""), nil }
	err := host.Serve(placementCatalog, factory, ServeOptions{})
	if err == nil || !strings.Contains(err.Error(), "t.catalog is declared for server, but session:S1 serves it") {
		t.Fatalf("serve catalog on a session host: %v", err)
	}
	if _, ok := errors.AsType[*TypeError](err); !ok {
		t.Fatalf("a placement refusal is a TypeError: %T", err)
	}
	server := requestsHost(t, HostOptions{Name: "server", Self: "host"})
	err = server.Serve(placementNote, factory, ServeOptions{})
	if err == nil || !strings.Contains(err.Error(), "t.note is declared for session:<session>, but server serves it") {
		t.Fatalf("serve note on the server: %v", err)
	}
	misplaced := Facet{Name: "misplaced", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(placementNote, factory, ServeOptions{})
	}}}
	_, err = server.Reload([]PluginRef{&misplaced}, ReloadOptions{})
	if err == nil || !strings.Contains(err.Error(), "declared for session:<session>, but server serves it") {
		t.Fatalf("reload with a misplaced facet: %v", err)
	}
}

// holdAnswer sends a hold for token with input to target over a fresh raw client link of server and returns the shape
// or gone frame the server answers with (nil if none came).
func placementHold(t *testing.T, server *HostImpl, target string, input any) Frame {
	t.Helper()
	down, raw := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: "guest"}); err != nil {
		t.Fatal(err)
	}
	var frames frameLog
	raw.Receive(frames.add)
	_, _ = raw.Send(HelloFrame{Protocol: ProtocolVersion, Name: "raw", Kind: "client", CodeVersion: 0})
	_, _ = raw.Send(HoldFrame{ID: 1, At: target, Token: placementNote.Description, Input: input})
	var answer Frame
	waitFor(t, "a shape or gone frame", func() bool {
		for _, frame := range frames.list() {
			if frame.T() == "shape" || frame.T() == "gone" {
				answer = frame
				return true
			}
		}
		return false
	})
	return answer
}

// placement.test.ts "a host refuses a hold its own contract places elsewhere, and a relay one whose target disagrees"
func TestPlacementAHostRefusesAHoldItsOwnContractPlacesElsewhereAndARelayOneWhoseTargetDisagrees(t *testing.T) {
	c := placementClusterOf(t)
	// A client picks session:S2 for a note of S1: the relay refuses, nothing reaches S2.
	relayed := placementHold(t, c.server, "session:S2", map[string]any{"session": "S1", "id": "x"})
	gone, ok := relayed.(GoneFrame)
	if !ok {
		t.Fatalf("relayed = %T, want gone", relayed)
	}
	if message := gone.Error.Message; !strings.Contains(message, "placement") && !strings.Contains(message, "does not live on session:S2") {
		t.Fatalf("relay refusal %q", message)
	}
	// Nothing reaches S2: the relay refused before it forwarded the hold (S2's own check would answer the same).
	if holds := c.toChild["session:S2"].count("hold"); holds != 0 {
		t.Fatalf("%d holds reached session:S2", holds)
	}
	// The right target is served.
	if served := placementHold(t, c.server, "session:S1", map[string]any{"session": "S1", "id": "x"}); served.T() != "shape" {
		t.Fatalf("right target = %s", served.T())
	}
	// Addressed to the server itself, a note is refused by the server's own contract check.
	direct := placementHold(t, c.server, "server", map[string]any{"session": "S1", "id": "x"})
	if gone, ok := direct.(GoneFrame); !ok || !strings.Contains(gone.Error.Message, "t.note with this input does not live on server") {
		t.Fatalf("direct = %+v, want gone: t.note with this input does not live on server", direct)
	}
}

// placement.test.ts "the host comes from the normalized input: unknown fields and defaults do not change it"
func TestPlacementTheHostComesFromTheNormalizedInputUnknownFieldsAndDefaultsDoNotChangeIt(t *testing.T) {
	doc := placementContract("t.doc-default", NewHostFamily("session:", "session"), tObject("session", tString(Schema{"default": "S1"})))
	c := placementClusterOf(t)
	mustServe(t, c.s1, doc, func(any, Principal, Self) (Implementation, error) { return placementWhere("session:S1"), nil }, ServeOptions{})
	// An untyped caller leaves the field out: the default places it.
	if got := placementGet(t, c.server, doc, map[string]any{}); got != "session:S1" {
		t.Fatalf("defaulted = %q", got)
	}
	_, err := c.server.Hold(placementNote, map[string]any{"session": "", "id": "x"}, nil)
	if err == nil || !strings.Contains(err.Error(), "input field session names no host") {
		t.Fatalf("empty session: %v", err)
	}
}

// placement.test.ts "placement is part of the contract: a moved type is a breaking change, never bridged by an adapter"
func TestPlacementIsPartOfTheContractAMovedTypeIsABreakingChangeNeverBridgedByAnAdapter(t *testing.T) {
	at := func(host string) *Contract {
		return placementMoved(1, host)
	}
	if at("server").Fingerprint == at("session:S1").Fingerprint {
		t.Fatal("a moved type keeps its fingerprint")
	}
	host := requestsHost(t, HostOptions{Name: "server", Self: "host"})
	facet := func(version int, place string) *Facet {
		return &Facet{Name: "moved", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(placementMoved(version, place), func(any, Principal, Self) (Implementation, error) {
				return chordjson.ObjectOf("variant", "main", "ping", methodFunc(func(any, Ctx) (any, error) { return nil, nil })), nil
			}, ServeOptions{Accepts: Accepts{1: func(current Implementation) Implementation { return current }}})
		}}}
	}
	if err := host.Install(*facet(1, "server"), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := requestsGet(t, host, at("server"), map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	// Same version, other placement: a forgotten bump.
	_, err := host.Reload([]PluginRef{facet(1, "session:S1")}, ReloadOptions{})
	if err == nil || (!strings.Contains(err.Error(), "declared for session:S1") && !strings.Contains(err.Error(), "without a version bump")) {
		t.Fatalf("same version, other placement: %v", err)
	}
	// A new version that accepts version 1 but lives elsewhere: no adapter makes version 1 consumers bind.
	moved := placementMoved(2, "session:S1")
	_, err = Bind(moved, Accepts{1: func(current Implementation) Implementation { return current }}, at("server"))
	if !IsChordError(err, CodeContractMismatch) || !strings.Contains(err.Error(), "lives on session:S1") {
		t.Fatalf("bind across hosts: %v", err)
	}
}

// placementMoved is placement.test.ts's t.moved: the same type at another host or version.
func placementMoved(version int, host string) *Contract {
	return Remote("t.moved", RemoteDef{
		Version: version,
		Input:   tObject(),
		Host:    NewHostNamed(host),
		Variants: Fields[VariantDef]{{"main", VariantDef{
			Methods: Fields[MethodSpec]{{"ping", Method(nil, nil)}},
		}}},
	})
}

// placement.test.ts "types: the family's field is a required string of the input, and remote contracts need a host":
// the TypeScript type errors have no Go form; the runtime refusals are these.
func TestPlacementTypesAndRemoteContractsNeedAHost(t *testing.T) {
	input := tObject("session", tString())
	variants := Fields[VariantDef]{{"main", VariantDef{}}}
	Remote("t.ok", RemoteDef{Version: 1, Input: input, Host: NewHostFamily("session:", "session"), Variants: variants})
	mustPanicTypeError(t, "a remote contract without host", func() {
		Remote("t.nowhere", RemoteDef{Version: 1, Input: input, Variants: variants})
	})
	for _, test := range []struct {
		name string
		run  func()
		want string
	}{
		{"an empty prefix", func() { NewHostFamily("", "session") }, "hostFamily needs a host-name prefix"},
		{"an empty key", func() { NewHostFamily("session:", "") }, "hostFamily needs an input field name"},
		{"an empty name", func() { NewHostNamed("") }, "hostNamed needs a host name"},
	} {
		func() {
			defer func() {
				recovered := recover()
				typeErr, ok := recovered.(*TypeError)
				if !ok || typeErr.Message != test.want {
					t.Errorf("%s panicked with %v, want TypeError %q", test.name, recovered, test.want)
				}
			}()
			test.run()
		}()
	}
}
