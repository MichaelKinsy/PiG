package host

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/test/host/ctx.test.ts:153 ("a local handler's ctx.principal is the host's, its incarnation this host's"):
// the host's incarnation is one value for its run, and another host's is another (`readonly incarnation`, host.ts:261).
func TestHostIncarnationIsOneValuePerRunOfAHost(t *testing.T) {
	a := newHost(t, HostOptions{OnError: noopOnError})
	b := newHost(t, HostOptions{OnError: noopOnError})
	first := a.Incarnation()
	if first == "" || first != a.Incarnation() {
		t.Fatalf("incarnation = %q then %q", first, a.Incarnation())
	}
	if first == b.Incarnation() {
		t.Fatalf("two hosts share the incarnation %q", first)
	}
}

// Ports the contract of packages/chord/src/host/host.ts:265 principalAttributes(principal, context): the hook's attributes,
// else `chord.principal`, the principal key; none without a principal.
func TestHostPrincipalAttributesAreTheHooksElseThePrincipalKeyAndNoneWithoutAPrincipal(t *testing.T) {
	user := delta.JsonObjectOf("kind", "user", "id", "u1")
	plain := newHost(t, HostOptions{OnError: noopOnError})
	if got := plain.PrincipalAttributes(nil, background); len(got) != 0 {
		t.Fatalf("no principal: %v", got)
	}
	got := plain.PrincipalAttributes(user, background)
	if len(got) != 1 || got[0].Name != "chord.principal" || got[0].Value != `{"id":"u1","kind":"user"}` {
		t.Fatalf("default attributes = %v", got)
	}
	hooked := newHost(t, HostOptions{OnError: noopOnError, PrincipalAttributes: func(p data.Principal) telemetry.SpanAttributes {
		return telemetry.SpanAttributes{telemetry.Attr("user.name", "Ada")}
	}})
	if got := hooked.PrincipalAttributes(user, background); len(got) != 1 || got[0].Name != "user.name" {
		t.Fatalf("hook attributes = %v", got)
	}
}
