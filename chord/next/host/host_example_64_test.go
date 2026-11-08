package host

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Twin of packages/chord/test/examples/64-old-work.ts: old work finishes on old code. The four scenarios run on the shapes of
// host_old_work_test.go (a provider whose `work` reaches back into the plugin once its gate opens, a dependent that holds it).

func TestExample64OldWork(t *testing.T) {
	reentry := func(raise string) owShape {
		return owShape{
			raise: raise,
			work: func(ctx Ctx, code string) (string, error) {
				got, err := owCall(ctx, owDependent, jsonObject(), "ping")
				return code + " work -> " + got, err
			},
			ping: func(provider *Reference, ctx Ctx, code string) (string, error) {
				value, err := provider.Call("x", nil, ctx)
				text, _ := value.(string)
				return code + " ping -> " + text, err
			},
		}
	}

	t.Run("1 both restart: no call waits for the drain it is part of", func(t *testing.T) {
		// The dependent's new attempt binds the provider's open old attempt; the back-call `x` is old work too.
		answer, took := owRun(t, reentry(""))
		if answer != "v1 work -> v2 ping -> v1 x" || took > time.Second {
			t.Fatalf("work = %q after %v, want \"v1 work -> v2 ping -> v1 x\" at once", answer, took)
		}
	})

	t.Run("2 the provider's version raised: the work runs on the dependent's old attempt", func(t *testing.T) {
		answer, took := owRun(t, reentry("provider"))
		if answer != "v1 work -> v1 ping -> v1 x" || took > time.Second {
			t.Fatalf("work = %q after %v, want \"v1 work -> v1 ping -> v1 x\" at once", answer, took)
		}
	})

	t.Run("3 a wait through its own drain fails at once with reload_cycle", func(t *testing.T) {
		answer, took := owRun(t, owShape{raise: "provider", work: func(ctx Ctx, code string) (string, error) {
			return owCall(ctx, owThird, jsonObject("id", "new"), "ping")
		}})
		if !strings.HasPrefix(answer, "rejected: ") || !strings.Contains(answer, "waits for it, and it waits for that drain") || took > time.Second {
			t.Fatalf("work = %q after %v, want a reload_cycle rejection at once", answer, took)
		}
	})

	t.Run("4 a call still running at reloadDrainTimeout is cut released; a new call is served by the new code", func(t *testing.T) {
		shape := reentry("")
		gate := newReloadGate()
		queue := reloadGates(gate)
		drain := 200 * time.Millisecond
		host := owHost(t, shape, queue, HostOptions{ReloadDrainTimeout: &drain})
		defer closeHosts(t, host)
		provider := mustGet(t, host, owProvider, jsonObject())
		stuck := startReloadCall(provider, "work", nil)
		gate.entered.wait(t)
		cutAt := time.Now()
		owReload(t, host, shape, queue)
		wantChordError(t, stuck.rejected(t), data.CodeReleased, "", "")
		// The drain's due time is the loop's clock (whole milliseconds, as Pi's Date.now) plus the bound, taken after cutAt: the
		// cut may come up to a millisecond before cutAt + drain.
		if took := time.Since(cutAt); took < drain-time.Millisecond || took > 3*time.Second {
			t.Fatalf("the call was cut after %v, want about %v", took, drain)
		}
		if got, err := provider.Call("x", nil, background); err != nil || got != "v2 x" {
			t.Fatalf("a new call = %v, %v, want \"v2 x\"", got, err)
		}
		gate.open.fire()
	})
}
