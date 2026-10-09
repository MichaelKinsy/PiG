package chord

import (
	"slices"
	"testing"
)

// Pi packages/chord/src/types.ts:269,308 take services as `readonly { readonly id: string }[]`: each entry names a service by id,
// in the order given; the binding allowlist reads exactly those ids and rejects a repeated one.
func TestServiceIdentitiesNameServicesInOrderAndFeedTheBindingAllowlist(t *testing.T) {
	references := ServiceIDs("b", "a")
	if !slices.Equal(serviceIdentityIds(references), []string{"b", "a"}) {
		t.Fatalf("ServiceIDs = %+v", references)
	}
	if len(ServiceIDs()) != 0 {
		t.Fatal("no ids must give no references")
	}
	binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: references})
	if err != nil || !binding.allowlist["a"] || !binding.allowlist["b"] || binding.allowlist["c"] {
		t.Fatalf("allowlist = %v, %v", binding, err)
	}
	if _, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: ServiceIDs("a", "a")}); err == nil {
		t.Fatal("a repeated service id must be rejected")
	}
}
