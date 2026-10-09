package chord

import (
	"context"
	"testing"
)

// consumer.ts MemberSlot.#expect: a member's first use fixes the kind it is expected to be (`#call` expects "method",
// `.value` and `subscribe` expect "state"), and a later use as the other kind throws service_member_mismatch "Remote
// service member <service>.<member> was used as two different kinds", before any description arrives and whether or not
// the binding is active.
func TestRemoteServiceMemberUsedAsTwoKindsIsRejected(t *testing.T) {
	fixture := newRemoteFixture(t, SingletonService(counterDefinition))
	service, err := UseRemote(fixture.binding, counterDefinition)
	if err != nil {
		t.Fatal(err)
	}
	const methodFirst = "Remote service member test.counter.methodFirst was used as two different kinds"
	if _, err := service.Call(context.Background(), "methodFirst"); hasRemoteServiceErrorCode(err, ErrServiceMemberMismatch) {
		t.Fatalf("first use as a method = %v", err)
	}
	if _, err := service.State("methodFirst"); !hasRemoteServiceErrorCode(err, ErrServiceMemberMismatch) || err.Error() != methodFirst {
		t.Fatalf("state use of a method = %v, want %q", err, methodFirst)
	}

	const stateFirst = "Remote service member test.counter.stateFirst was used as two different kinds"
	if _, err := service.State("stateFirst"); err != nil {
		t.Fatalf("first use as state = %v", err)
	}
	if _, err := service.Call(context.Background(), "stateFirst"); !hasRemoteServiceErrorCode(err, ErrServiceMemberMismatch) || err.Error() != stateFirst {
		t.Fatalf("method use of state = %v, want %q", err, stateFirst)
	}
	// The expected kind stays the first one: using the member again as that kind is accepted.
	if _, err := service.State("stateFirst"); err != nil {
		t.Fatalf("second use as state = %v", err)
	}
}
