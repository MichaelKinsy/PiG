package coding

import (
	"context"
	"os"
	"testing"
)

func TestAvailabilityFixtureClosesServicesBeforeTempCleanup(t *testing.T) {
	// Availability setup owns Services separately from Session. Its background work must settle while the fixture paths still exist.
	var services *Services
	finished := make(chan error, 1)
	defer func() {
		if services != nil {
			services.Close()
		}
	}()
	t.Run("fixture lifetime", func(t *testing.T) {
		_, services = newAvailabilitySession(t)
		started := make(chan struct{})
		if !services.Registry().StartModelTask(context.Background(), func(ctx context.Context) {
			close(started)
			<-ctx.Done()
			_, err := os.Stat(services.AgentDir())
			finished <- err
		}) {
			t.Fatal("fixture did not admit owned background work")
		}
		<-started
	})
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("fixture paths were removed before Services work drained: %v", err)
		}
	default:
		t.Fatal("availability fixture returned without closing its caller-owned Services")
	}
}
