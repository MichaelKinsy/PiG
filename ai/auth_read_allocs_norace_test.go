//go:build !race

package ai

import (
	"context"
	"testing"
)

// The unchanged-file credential read may only allocate less: it was nine allocations when the revision was a formatted string and is four with a comparable struct.
func TestAuthStorageReadUnchangedAllocationCeiling(t *testing.T) {
	storage := newBenchAuthStorage(t)
	ctx := context.Background()
	if _, err := storage.Read(ctx, "anthropic"); err != nil {
		t.Fatal(err)
	}
	got := testing.AllocsPerRun(200, func() { _, _ = storage.Read(ctx, "anthropic") })
	if got > 4 {
		t.Fatalf("an unchanged read allocates %v times, ceiling 4", got)
	}
}
