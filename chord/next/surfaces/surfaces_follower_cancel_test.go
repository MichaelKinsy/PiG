package surfaces

import (
	"context"
	"testing"
	"time"
)

// A caller that cancels the ctx it passed instead of calling Stop ends the pick watch before its start the same way (Pi ignores it).
// The theme follower shares the code, so only the language follower is looped. Under -race this loop met a host race of its own
// (`state.Batch.AssertOutside` read `running` while `Batch.Run` wrote it, and returned a spurious "watch inside a commit or a derive
// compute"); it passes with AssertOutside a section of the host (chord T2, 9485f9a5b0).
func TestCancellingAFollowersContextBeforeItsPickWatchBindsEndsQuietly(t *testing.T) {
	notes := notesEnDe(t)
	client, closeAll := languageSetup(t, notes)
	defer closeAll()
	var stops []func()
	for i := range 300 {
		ctx, cancel := context.WithCancel(bg)
		language, err := FollowActiveLanguage(client, []string{"de"}, func(*Language) {}, func(MessagesReport) {}, ctx)
		if err != nil {
			t.Fatal(err)
		}
		spin(time.Duration(i%13) * 20 * time.Microsecond)
		cancel()
		stops = append(stops, language.Stop)
	}
	// Only now are they stopped: until then the cancellation alone ended their watches.
	noSurfacesGoroutines(t)
	for _, stop := range stops {
		stop()
	}
}
