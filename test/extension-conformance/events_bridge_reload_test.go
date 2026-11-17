package extensionconformance

import (
	"bytes"
	"fmt"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// busReloadMembers is the size of the reproduction in issue 486: enough members that the runner of a packed cell does not start their factories in admission order by chance.
const busReloadMembers = 8

// reload runs the Host's Reload as /reload does and takes the new generation's extensions as the rig's own. A Reload that does not return fails the test with the goroutines the Host waits in.
func (r *busRig) reload() {
	r.t.Helper()
	type outcome struct {
		loaded []extension.Extension
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		loaded, err := r.host.Reload(r.t.Context())
		done <- outcome{loaded, err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			r.t.Fatalf("reload: %v", got.err)
		}
		if issues := r.host.LastReloadReport().Issues; len(issues) != 0 {
			r.t.Fatalf("reload issues: %q", issues)
		}
		r.exts = map[string]extension.Extension{}
		for _, ext := range got.loaded {
			r.exts[ext.Name] = ext
		}
	case <-time.After(testbudget.Wait(r.t)):
		r.t.Fatalf("Reload did not return: the Host waits for an extension whose factory cannot finish\n%s", reloadStacks())
	}
}

// reloadStacks is the goroutine dump of the stalled Reload and of the accept it waits in.
func reloadStacks() string {
	var all bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&all, 2)
	var stalled strings.Builder
	for block := range strings.SplitSeq(all.String(), "\n\n") {
		if strings.Contains(block, "(*Host).Reload") || strings.Contains(block, "acceptPackedExt") {
			stalled.WriteString(block + "\n\n")
		}
	}
	return stalled.String()
}

// Pi loads extensions one at a time: loadExtensionsInternal awaits each factory before it starts the next (extensions/loader.ts), and a factory's pi.events.on adds its listener to the shared emitter at once (loader.ts createExtensionAPI events.on, event-bus.ts on). A reload invalidates the old runtime, which unsubscribes its listeners (loader.ts runtime.invalidate), and loads every factory again in order, so one emit after a reload reaches each live listener once, in extension order. A native factory that subscribes while it constructs calls the Host before it returns, and the Host serves that call only for the connection it is accepting: a reload that started a later member's factory before an earlier member registered stalled (issue 486).
func TestNativeEventBusReloadKeepsEveryFactoryTimeListenerOnce(t *testing.T) {
	requireEventsMember(t)
	eachBusRealm(t, true, func(t *testing.T, language, isolation string) {
		specs := []busFixture{busSpec("node", isolation, "emitter")}
		var want []string
		for i := range busReloadMembers {
			name := fmt.Sprintf("member%d", i)
			specs = append(specs, busSpec(language, isolation, name, busListenerAt{"reload", "record"}))
			want = append(want, name+"|reload|7")
		}
		rig := newBusRig(t, specs...)
		for range 3 {
			rig.reload()
			rig.reset()
			rig.must("emitter", "emit", "reload json 7")
			rig.expect(want...)
		}
	})
}
