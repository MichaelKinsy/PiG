package codingagent

import (
	"runtime"
	"testing"
	"testing/synctest"
	"weak"

	"github.com/MichaelKinsy/PiG/ai"
)

func scheduledWarmerLifetime(t *testing.T) (*CacheWarmer, weak.Pointer[Session], weak.Pointer[ai.Model]) {
	t.Helper()
	session := NewSession("lifetime", t.TempDir())
	model := newCacheWarmingModels(t).adaptive
	warmer := NewCacheWarmer(nil, session, func() CacheWarmingMode { return "idle" }, nil)
	warmer.Start(warmRequest(model, ai.StreamOptions{}), alwaysCurrent)
	return warmer, weak.Make(session), weak.Make(model)
}

// A stopped AfterFunc can remain in Go's timer heap until the next timer check. Like upstream clearTimeout, disposal must release its owner and request even while the stopped callback remains reachable.
func TestCacheWarmerCloseReleasesScheduledRequestAndSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		warmer, session, model := scheduledWarmerLifetime(t)
		warmer.Close()
		warmer.Wait()
		runtime.GC()
		if session.Value() != nil {
			t.Error("drained warmer retains its Session manager")
		}
		if model.Value() != nil {
			t.Error("stopped timer retains its request model")
		}
		if status := warmer.Status(); status.State != "inactive" {
			t.Errorf("closed warmer status = %+v", status)
		}
		runtime.KeepAlive(warmer)
	})
}
