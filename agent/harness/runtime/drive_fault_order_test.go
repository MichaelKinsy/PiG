package runtime

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
)

// upstream: packages/agent/src/harness/runtime/lane.ts:982-1001; packages/agent/test/harness/runtime/drive-public.test.ts:601-610
func TestDetachedDriveFaultClearsFinishedPassBeforeObservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newPublicLane(t, false, agentharness.Resources{})
		_, err := fixture.harness.Hooks().OnBeforeDrive(func(harness.Context, agentharness.BeforeDriveEvent) error {
			return errors.New("drive failed")
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		published, release := laneTestBarrier(t)
		fault := fixture.lane.onFault
		var firstFault error
		fixture.lane.onFault = func(ctx harness.Context, cause error) error {
			firstFault = fault(ctx, cause)
			close(published)
			// Pi's synchronous rejection callback finishes before Promise observers resume. Hold its Go equivalent after sealing to expose early observation.
			<-release.done
			return errors.New("later fault callback failure")
		}
		acceptPublicRun(t, fixture.lane, "run")
		observation := asyncLaneCall(func() (agentharness.DriveOutcome, error) {
			return fixture.lane.Drive(t.Context(), agentharness.DriveOptions{OperationID: "run"})
		})
		<-published
		synctest.Wait()
		requireLanePending(t, observation)
		release.open()
		require.Same(t, firstFault, (<-observation).err)
		require.Nil(t, fixture.lane.CurrentDrive())
		synctest.Wait()
	})
}
