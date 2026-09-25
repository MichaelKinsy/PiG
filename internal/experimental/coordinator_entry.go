package experimental

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
)

// RunCoordinatorEntry validates and consumes the internal role before starting the coordinator.
// An opt-in executable calls this entry explicitly; importing the package never changes CLI dispatch.
func RunCoordinatorEntry(ctx context.Context, args []string) error {
	role, err := ConsumeInternalProcessRole()
	if err != nil {
		return err
	}
	if role != "coordinator" {
		return errors.New("Coordinator entrypoint requires an internal coordinator invocation")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return RunCoordinatorProcess(ctx, args)
}
