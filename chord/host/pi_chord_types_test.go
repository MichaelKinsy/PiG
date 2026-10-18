package host

// Pi chord type-level members that Go spells differently: method()'s overloads (contract.ts:31-33), PlacementFor
// (contract.ts:82) and PrincipalPattern (host.ts:170-172). Expectations come from the Pi tests cited on each case.

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// contract.test.ts:100-107: method() is described as {kind: "method"}; method(args) and method(undefined, result) differ
// from it by arguments and result.
func TestMethodOverloadsMatchPiMethod(t *testing.T) {
	spec := Method()
	if spec.Args != nil || spec.Result != nil {
		t.Fatalf("method() = %+v", spec)
	}
	args := tString()
	if got := Method(args); got.Args == nil || got.Result != nil {
		t.Fatalf("method(args) = %+v", got)
	}
	result := tNumber()
	if got := Method(nil, result); got.Args != nil || got.Result == nil {
		t.Fatalf("method(undefined, result) = %+v", got)
	}
	if got := Method(args, result); got.Args == nil || got.Result == nil {
		t.Fatalf("method(args, result) = %+v", got)
	}
	none := Local("t.methods", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"m", Method()}}}).Description
	variant, _ := none.Variants.Get("")
	if member, _ := variant.Get("m"); !reflect.DeepEqual(member, MemberDescription{Kind: MemberMethod}) {
		t.Fatalf("method() described as %+v", member)
	}
	mustPanicTypeError(t, "three schemas", func() { Method(args, result, args) })
}

// placement.test.ts:184-197: PlacementFor<I> is HostNamed | HostFamily; a family whose field names no host at runtime is
// invalid/placement (resolveHost), the runtime half of the compile-time key restriction.
func TestPlacementForIsHostNamedOrHostFamily(t *testing.T) {
	var named PlacementFor = NewHostNamed("server")
	var family PlacementFor = NewHostFamily("session:", "session")
	if name, err := resolveHost("t.ok", named, map[string]any{}); err != nil || name != "server" {
		t.Fatalf("named: %q %v", name, err)
	}
	if name, err := resolveHost("t.ok", family, chordjson.ObjectOf("session", "S1")); err != nil || name != "session:S1" {
		t.Fatalf("family: %q %v", name, err)
	}
	if _, err := resolveHost("t.number", NewHostFamily("session:", "count"), chordjson.ObjectOf("session", "S1")); err == nil {
		t.Fatal("a family key the input lacks must be refused")
	}
}

// host.ts:170-175, principals.test.ts:81: a pattern names fields of the app's principals and matches those whose fields
// are equal; a predicate matches this host only.
func TestPrincipalPatternMatchesEqualFields(t *testing.T) {
	match := PrincipalMatch{Pattern: PrincipalPattern(chordjson.ObjectOf("kind", "host"))}
	if !matchesPattern(match.Pattern, chordjson.ObjectOf("kind", "host", "name", "h")) {
		t.Fatal("an equal field must match")
	}
	if matchesPattern(match.Pattern, chordjson.ObjectOf("kind", "guest")) {
		t.Fatal("a different field must not match")
	}
}
