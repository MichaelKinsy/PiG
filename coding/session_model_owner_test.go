package coding

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi agent-session.ts:2118-2137 mutates the model, audit, defaults and thinking
// before awaiting model_select. The Go owner dispatcher encloses only that
// synchronous state phase; no extension callback may block the dispatcher.
func TestSetModelOnMainDispatchesAllStateBeforeNotifications(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "superseded"}[reject], func(t *testing.T) {
			svcs := newTestServices(t)
			if err := svcs.SettingsManager().SetDefaultThinkingLevel("medium"); err != nil {
				t.Fatal(err)
			}
			if err := svcs.SettingsManager().SetModelThinkingLevel("fake", "next", "low"); err != nil {
				t.Fatal(err)
			}
			initial := fakeModel()
			initial.Capabilities.MaxThinking = ai.ThinkingLevelHigh
			sess, err := NewSession(svcs, SessionOptions{Model: initial})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sess.Close() }()
			if err := sess.SetThinkingLevel(ai.ThinkingHigh); err != nil {
				t.Fatal(err)
			}
			before := len(sess.Inner().GetEntries())
			callbacks, release := make(chan struct{}, 1), make(chan struct{})
			var inDispatch atomic.Bool
			sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{
				"thinking_level_select": {func(...any) (any, error) {
					if inDispatch.Load() {
						t.Error("thinking callback ran inside owner dispatch")
					}
					return nil, nil
				}},
				"model_select": {func(args ...any) (any, error) {
					event := args[0].(extension.ModelSelectEvent)
					if event.Model.ID == "next" {
						callbacks <- struct{}{}
						if inDispatch.Load() {
							t.Error("model callback ran inside owner dispatch")
							return nil, nil
						}
						<-release
					}
					return nil, nil
				}},
			}}}, t.TempDir()))
			next := *initial
			next.ID = "next"
			dispatched := make(chan func() error)
			result := make(chan error, 1)
			done := make(chan error, 1)
			go func() {
				done <- sess.SetModelOnMain(&next, ModelMutationOptions{Persist: true}, func(mutate func() error) error {
					dispatched <- mutate
					return <-result
				})
			}()
			mutate := <-dispatched
			if sess.Model() != initial || svcs.SettingsManager().GetDefaultModel() != "" || len(sess.Inner().GetEntries()) != before || sess.ThinkingLevel() != ai.ThinkingHigh {
				t.Fatal("state changed before owner dispatch")
			}
			if reject {
				stale := errors.New("superseded")
				result <- stale
				if err := <-done; !errors.Is(err, stale) {
					t.Fatalf("error = %v", err)
				}
				if sess.Model() != initial || svcs.SettingsManager().GetDefaultModel() != "" || len(sess.Inner().GetEntries()) != before || sess.ThinkingLevel() != ai.ThinkingHigh {
					t.Fatal("rejected dispatch changed state")
				}
				select {
				case <-callbacks:
					t.Fatal("rejected mutation emitted model_select")
				default:
				}
				close(release)
				return
			}
			inDispatch.Store(true)
			mutationErr := mutate()
			inDispatch.Store(false)
			if mutationErr != nil {
				t.Fatal(mutationErr)
			}
			if sess.Model() != &next || svcs.SettingsManager().GetDefaultModel() != "next" || sess.ThinkingLevel() != ai.ThinkingLow {
				t.Fatal("owner dispatch did not commit all state")
			}
			result <- nil
			<-callbacks
			later := *initial
			later.ID = "later"
			if err := sess.SetModel(&later, ModelMutationOptions{Persist: true}); err != nil {
				t.Fatal(err)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if sess.Model() != &later || sess.Agent().Model() != &later || svcs.SettingsManager().GetDefaultModel() != "later" || sess.ThinkingLevel() != ai.ThinkingMedium {
				t.Fatal("old notifications changed newer model state")
			}
		})
	}
}
