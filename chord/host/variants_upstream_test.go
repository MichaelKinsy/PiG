package host

// Ports packages/chord/test/variants.test.ts (Pi b45e0ebc): a binding applies a shape only once its new state members
// have snapshots; members outside the variant throw not_in_variant; a re-hold after a link loss keeps the last values
// readable and sends one fresh unit.

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

var variantsDoc = Remote("t.e8-doc", RemoteDef{
	Version: 1, Input: tObject(), Host: NewHostNamed("server"),
	Variants: Fields[VariantDef]{
		{"a", VariantDef{State: Fields[Schema]{{"x", tNumber()}}, Methods: Fields[MethodSpec]{{"bump", Method(nil, nil)}}}},
		{"b", VariantDef{State: Fields[Schema]{{"x", tNumber()}, {"y", tString()}}, Methods: Fields[MethodSpec]{{"bump", Method(nil, nil)}}}},
		{"c", VariantDef{State: Fields[Schema]{{"y", tString()}}, Methods: Fields[MethodSpec]{{"bump", Method(nil, nil)}}}},
	},
})

// variantsDocFacet is the server's facet: Doc in variant variant (the cells survive reloads by name).
func variantsDocFacet(variant string) *Facet {
	return &Facet{Name: "doc", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(variantsDoc, func(_ any, _ Principal, self Self) (Implementation, error) {
			x, err := self.State("x", tNumber(), float64(1), nil)
			if err != nil {
				return nil, err
			}
			y, err := self.State("y", tString(), "why", nil)
			if err != nil {
				return nil, err
			}
			bump := methodFunc(func(_ any, ctx Ctx) (any, error) {
				return nil, x.Replace(ctx, x.Value().(float64)+1, CellReplaceOptions{})
			})
			switch variant {
			case "a":
				return chordjson.ObjectOf("variant", variant, "x", x, "bump", bump), nil
			case "b":
				return chordjson.ObjectOf("variant", variant, "x", x, "y", y, "bump", bump), nil
			}
			return chordjson.ObjectOf("variant", variant, "y", y, "bump", bump), nil
		}, ServeOptions{})
	}}}
}

func variantsValue(t *testing.T, ref Ref, name string) (value any, thrown *ChordError) {
	t.Helper()
	member := ref.Member(name)
	readable, ok := member.(Readable[any])
	if !ok {
		t.Fatalf("%s is not a readable (%T)", name, member)
	}
	thrown = chordPanic(func() { value = readable.Value() })
	return value, thrown
}

func variantsIs(ref Ref, want string) bool {
	variant := ref.Variant()
	return variant != nil && *variant == want
}

func variantsServerAndBrowser(t *testing.T) (server, browser *HostImpl, link *countingLink) {
	t.Helper()
	server = requestsHost(t, HostOptions{Name: "server"})
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"user": "u"}}); err != nil {
		t.Fatal(err)
	}
	link = newCountingLink(up)
	browser = requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: link, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	return server, browser, link
}

// variants.test.ts:84-136 "a new variant's members arrive with its shape; a member outside the variant throws
// not_in_variant"
func TestVariantsANewVariantsMembersArriveWithItsShapeAMemberOutsideTheVariantThrowsNotInVariant(t *testing.T) {
	server, browser, link := variantsServerAndBrowser(t)
	if err := server.Install(*variantsDocFacet("a"), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	doc := bindingsHold(t, browser, variantsDoc, map[string]any{}, nil)
	ready(t, doc)
	if !variantsIs(doc, "a") {
		t.Fatalf("variant %v, want a", doc.Variant())
	}
	if value, thrown := variantsValue(t, doc, "x"); thrown != nil || value != float64(1) {
		t.Fatalf("x = %v, %v", value, thrown)
	}
	if _, thrown := variantsValue(t, doc, "y"); thrown == nil || thrown.Code != CodeNotInVariant {
		t.Fatalf("y outside the variant threw %v, want not_in_variant", thrown)
	}
	// Every shape the reference shows is readable in full, at the moment it is shown.
	var mu sync.Mutex
	var failures []error
	doc.OnShape(func(shape Shape) {
		for _, name := range []string{"x", "y"} {
			member, ok := variantsDoc.Description.member(*shape.Variant, name)
			if !ok || member.Kind != MemberState {
				continue
			}
			if _, thrown := variantsValue(t, doc, name); thrown != nil {
				mu.Lock()
				failures = append(failures, thrown)
				mu.Unlock()
			}
		}
	})
	before := len(stateFrames(link.receivedFrames()))
	if _, err := server.Reload(reloadPlugins(variantsDocFacet("b")), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "variant b", func() bool { return variantsIs(doc, "b") })
	if value, thrown := variantsValue(t, doc, "y"); thrown != nil || value != "why" {
		t.Fatalf("y = %v, %v", value, thrown)
	}
	if value, thrown := variantsValue(t, doc, "x"); thrown != nil || value != float64(1) {
		t.Fatalf("x = %v, %v", value, thrown)
	}
	var snaps []string
	for _, frame := range stateFrames(link.receivedFrames())[before:] {
		if frame.As == "snap" {
			snaps = append(snaps, frame.M)
		}
	}
	if !slices.Equal(snaps, []string{"y"}) {
		t.Fatalf("snapshots %v, want only the new member's", snaps)
	}
	if _, err := server.Reload(reloadPlugins(variantsDocFacet("c")), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "variant c", func() bool { return variantsIs(doc, "c") })
	if _, thrown := variantsValue(t, doc, "x"); thrown == nil || thrown.Code != CodeNotInVariant {
		t.Fatalf("x outside the variant threw %v, want not_in_variant", thrown)
	}
	if value, thrown := variantsValue(t, doc, "y"); thrown != nil || value != "why" {
		t.Fatalf("y = %v, %v", value, thrown)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(failures) != 0 {
		t.Fatalf("a shown shape was not readable in full: %v", failures)
	}
}

// variants.test.ts:139-168 "keeps the last values readable, sends one fresh unit, and a reference joining meanwhile
// waits for it"
func TestReHoldAfterALinkLossKeepsTheLastValuesReadableSendsOneFreshUnitAndAReferenceJoiningMeanwhileWaitsForIt(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	if err := server.Install(*variantsDocFacet("b"), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"user": "u"}}); err != nil {
		t.Fatal(err)
	}
	browser := requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	doc := bindingsHold(t, browser, variantsDoc, map[string]any{}, nil)
	ready(t, doc)
	mustCall(t, doc, "bump", nil, nil)
	waitFor(t, "x = 2", func() bool { value, _ := variantsValue(t, doc, "x"); return value == float64(2) })
	up.Close()
	waitFor(t, "loading", func() bool { return doc.Status() == RefLoading })
	// The last values stay readable while the binding re-holds.
	if value, thrown := variantsValue(t, doc, "x"); thrown != nil || value != float64(2) {
		t.Fatalf("x while re-holding = %v, %v", value, thrown)
	}
	joining := bindingsHold(t, browser, variantsDoc, map[string]any{}, nil)
	if joining.Status() != RefLoading {
		t.Fatalf("joining status %s", joining.Status())
	}
	joined := make(chan struct{})
	go func() {
		_ = joining.Ready(t.Context())
		close(joined)
	}()
	select {
	case <-joined:
		t.Fatal("a reference joining during the re-hold waits for the fresh unit")
	case <-time.After(5 * time.Millisecond):
	}

	down2, up2 := MemoryLinkPair()
	if err := server.Accept(down2, AcceptOptions{Principal: map[string]any{"user": "u"}}); err != nil {
		t.Fatal(err)
	}
	link := newCountingLink(up2)
	browser.Connect(link, ConnectOptions{Principal: kindServer()})
	ready(t, joining)
	if doc.Status() != RefLive {
		t.Fatalf("doc status %s", doc.Status())
	}
	var hold HoldFrame
	for _, frame := range link.sentFrames() {
		if h, ok := frame.(HoldFrame); ok {
			hold = h
			break
		}
	}
	if !hold.Again {
		t.Fatalf("hold %+v: a re-hold says again", hold)
	}
	if got := link.countSent("hold"); got != 1 {
		t.Fatalf("holds = %d, want one for both references", got)
	}
	var snaps []string
	for _, frame := range stateFrames(link.receivedFrames()) {
		if frame.As == "snap" {
			snaps = append(snaps, frame.M)
		}
	}
	slices.Sort(snaps)
	if !slices.Equal(snaps, []string{"x", "y"}) {
		t.Fatalf("snapshots %v, want one per member", snaps)
	}
	// The old hold was released with the link: the server made a fresh object (its cells start over).
	if value, thrown := variantsValue(t, doc, "x"); thrown != nil || value != float64(1) {
		t.Fatalf("x after the re-hold = %v, %v", value, thrown)
	}
}
