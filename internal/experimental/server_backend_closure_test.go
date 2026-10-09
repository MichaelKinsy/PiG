package experimental

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// upstream: packages/coding-agent/src/experimental/server.ts:462-473. A listener failure alone, a catalog failure alone, and both together are reported as the failures themselves, then as AggregateError("Server and repository shutdown failed", [server, catalog]) in that order.
func TestBackendClosureReportsServerAndRepositoryFailures(t *testing.T) {
	serverFailure, catalogFailure := errors.New("listener close failed"), errors.New("catalog dispose failed")
	fail := func(err error) func() error { return func() error { return err } }
	if err := settleBackendClosure(fail(nil), fail(nil)); err != nil {
		t.Fatalf("clean closure = %v", err)
	}
	if err := settleBackendClosure(fail(serverFailure), fail(nil)); !errors.Is(err, serverFailure) {
		t.Fatalf("server failure = %v", err)
	}
	if err := settleBackendClosure(fail(nil), fail(catalogFailure)); !errors.Is(err, catalogFailure) {
		t.Fatalf("catalog failure = %v", err)
	}
	aggregate, ok := errors.AsType[*services.AggregateError](settleBackendClosure(fail(serverFailure), fail(catalogFailure)))
	if !ok {
		t.Fatal("both failures were not aggregated")
	}
	if aggregate.Message != "Server and repository shutdown failed" || len(aggregate.Errors) != 2 || !isFailure(aggregate.Errors[0], serverFailure) || !isFailure(aggregate.Errors[1], catalogFailure) {
		t.Fatalf("aggregate = %q %v", aggregate.Message, aggregate.Errors)
	}
}

func isFailure(got any, want error) bool {
	err, ok := got.(error)
	return ok && errors.Is(err, want)
}
