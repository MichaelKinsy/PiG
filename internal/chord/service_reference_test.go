package chord

import (
	"slices"
	"testing"
)

// serviceReferenceIDs lists the ids of service references, for tests that record what a source was asked to open.
func serviceReferenceIDs(references []ServiceReference) []string {
	ids := make([]string, len(references))
	for i, reference := range references {
		ids[i] = reference.Id()
	}
	return ids
}

// types.ts:269-270,308-312: a binding and a source take `readonly { readonly id: string }[]`, so a service definition and a plain id are both service references,
// and the binding allowlists exactly the ids the references name.
// mutation-checked: a binding that reads every reference as the empty id fails the distinct-references check.
func TestServiceReferencesAreDefinitionsOrIds(t *testing.T) {
	definition := DefineService[struct{}]("test.reference-definition")
	references := []ServiceReference{definition, ServiceID("test.reference-id")}
	if got := serviceReferenceIDs(references); !slices.Equal(got, []string{"test.reference-definition", "test.reference-id"}) {
		t.Fatalf("ids = %q", got)
	}
	if got := serviceReferenceIDs(ServiceIDs("a", "b")); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("ServiceIDs = %q", got)
	}
	if _, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: references}); err != nil {
		t.Fatalf("a definition and an id naming different services are both allowed: %v", err)
	}
	if _, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: []ServiceReference{ServiceID("dup"), definition, ServiceID("dup")}}); err == nil {
		t.Fatal("a duplicate service id in the references must be rejected")
	}
}
