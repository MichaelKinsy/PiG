package coding

import (
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// runner.ts:265-274 emitSessionShutdownEvent: `if (hasHandlers("session_shutdown")) { await emit(event); return true }`. Session.EmitSessionShutdown
// returns only after the handlers ran, and a session whose extensions have no session_shutdown handler emits nothing.
func TestSessionEmitSessionShutdownAwaitsTheHandlerBeforeReturning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svcs := newTestServices(t)
		entered := make(chan struct{})
		release := make(chan struct{})
		letGo := sync.OnceFunc(func() { close(release) })
		t.Cleanup(letGo)
		var finished atomic.Bool
		exts := []extension.Extension{
			extensionWithHandler("/fixture/lifecycle", "session_shutdown", func(args ...any) (any, error) {
				close(entered)
				<-release
				finished.Store(true)
				return nil, nil
			}),
		}
		rt, err := NewRuntime(RuntimeOptions{Services: svcs, NewExtensions: exts})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rt.Close() }()
		sess, err := rt.New(SessionStartOptions{Model: fakeModel()})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = sess.Close() }()

		returned := make(chan struct{})
		go func() { sess.EmitSessionShutdown("quit"); close(returned) }()
		<-entered
		synctest.Wait()
		select {
		case <-returned:
			t.Fatal("EmitSessionShutdown returned while the session_shutdown handler was still running")
		default:
		}
		letGo()
		synctest.Wait()
		select {
		case <-returned:
		default:
			t.Fatal("EmitSessionShutdown did not return after the handler finished")
		}
		if !finished.Load() {
			t.Fatal("EmitSessionShutdown returned before the handler finished")
		}
	})
}
