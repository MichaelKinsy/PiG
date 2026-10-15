package host

import (
	"slices"
	"testing"
)

// host.ts:558-581 with errors.ts:63-74: retireImplementation calls runAll, which runs the first step synchronously up to its
// first await. For a resource close (object.ts:156-163) that is: closed, out of the open order, and its spec close
// called; only the close's own await is later. So when retireImplementation returns, the first resource is already closed.
func TestRetireImplementationClosesTheFirstResourceSynchronously(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	closed := make(chan any, 2)
	spec := ResourceDeclaration[any]{Version: 1, Close: func(value any) error { closed <- value; return nil }}
	object := newObjectImpl(host, ObjectIdentity{Address: "t.r", TypeName: "t.r"})
	first := newResourceImpl(host, "first", spec, nil)
	second := newResourceImpl(host, "second", spec, nil)
	retired := newDecls()
	for _, resource := range []*ResourceImpl{first, second} {
		resource.state, resource.value = "open", resource.key
		retired.resources.Set(resource.key, resource)
		object.openOrder = append(object.openOrder, resource)
	}
	var state string
	var order []*ResourceImpl
	host.run(func() {
		object.implementations.Set(7, &Installed{codeVersion: 7, decls: retired})
		host.retireImplementation(object, 7)
		state, order = second.state, slices.Clone(object.openOrder)
	})
	// Reverse open order: second closes first.
	if state != "closed" || slices.Contains(order, second) || !slices.Contains(order, first) {
		t.Fatalf("after retireImplementation returned: second %s, open order %d", state, len(order))
	}
	if got := []any{<-closed, <-closed}; !slices.Equal(got, []any{"second", "first"}) {
		t.Fatalf("closed %v", got)
	}
}
