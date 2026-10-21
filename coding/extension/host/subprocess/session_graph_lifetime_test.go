package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// lifetimeObject is a session graph object that counts its releases, as the ModelRuntime closes its model tasks when it is released.
type lifetimeObject struct{ released atomic.Int32 }

func (*lifetimeObject) Call(context.Context, string, json.RawMessage) (any, error) { return nil, nil }
func (o *lifetimeObject) Release()                                                 { o.released.Add(1) }

type lifetimeBackend struct{ objects chan SessionGraphObject }

func (b lifetimeBackend) New(context.Context, SessionGraphScope, string, string, json.RawMessage) (SessionGraphObject, error) {
	return <-b.objects, nil
}

func (lifetimeBackend) Static(context.Context, SessionGraphScope, string, string, json.RawMessage) (any, error) {
	return nil, nil
}

// lifetimeSession is a child session that remembers it was closed.
type lifetimeSession struct {
	mu     sync.Mutex
	closed int
}

func (*lifetimeSession) Info() ChildSessionInfo                           { return ChildSessionInfo{} }
func (*lifetimeSession) Prompt(context.Context, ChildSessionPrompt) error { return nil }
func (*lifetimeSession) Queue(context.Context, ChildSessionQueue) error   { return nil }
func (*lifetimeSession) Abort(context.Context) error                      { return nil }
func (*lifetimeSession) BindExtensions(context.Context) error             { return nil }
func (*lifetimeSession) State() ChildSessionState                         { return ChildSessionState{} }
func (*lifetimeSession) Messages() (json.RawMessage, error)               { return json.RawMessage("[]"), nil }
func (*lifetimeSession) Subscribe(func(json.RawMessage)) func()           { return func() {} }
func (*lifetimeSession) Dispose()                                         {}
func (s *lifetimeSession) Close() {
	s.mu.Lock()
	s.closed++
	s.mu.Unlock()
}

type lifetimeRig struct {
	t       *testing.T
	host    *Host
	conn    *Conn
	objects chan SessionGraphObject
	// fail makes the factory refuse the next session.
	fail atomic.Bool
	// seen is the objects the factory was given for the last session.
	seen ChildSessionRequest
	// session is the last session the factory built.
	session *lifetimeSession
}

func newLifetimeRig(t *testing.T) *lifetimeRig {
	t.Helper()
	// The tables watch the connection's Done channel, and the package's leak gate fails a watcher nothing can end.
	r := &lifetimeRig{t: t, host: NewHostWithConfigRoot(t.TempDir(), t.TempDir()), conn: &Conn{done: make(chan struct{})}, objects: make(chan SessionGraphObject, 4)}
	t.Cleanup(func() {
		close(r.conn.done)
		r.host.Shutdown("test complete")
		// The watchers run the closing: let them finish before the leak gate looks.
		time.Sleep(50 * time.Millisecond)
	})
	r.host.SetSessionGraph(lifetimeBackend{objects: r.objects})
	bridge := NewUIBridge(func() {})
	r.host.SetUIBridge(bridge)
	bridge.SetHostAction("createChildSession", ChildSessionFactory(func(_ context.Context, request ChildSessionRequest) (ChildSession, error) {
		r.seen = request
		if r.fail.Load() {
			return nil, errors.New("refused")
		}
		r.session = &lifetimeSession{}
		return r.session, nil
	}))
	return r
}

// build makes the extension construct object and returns its handle.
func (r *lifetimeRig) build(object SessionGraphObject) string {
	r.t.Helper()
	r.objects <- object
	args, _ := json.Marshal(SessionGraphNew{Kind: "ModelRuntime", Op: "create"})
	result, err := r.host.handleSessionGraphCall(r.t.Context(), r.conn, &CallPayload{Method: CallSessionGraphNew, Args: args})
	if err != nil {
		r.t.Fatal(err)
	}
	var handle SessionGraphHandle
	if err := json.Unmarshal(result.Result, &handle); err != nil {
		r.t.Fatal(err)
	}
	return handle.Handle
}

func (r *lifetimeRig) release(handle string) {
	r.t.Helper()
	args, _ := json.Marshal(SessionGraphHandle{Handle: handle})
	if _, err := r.host.handleSessionGraphCall(r.t.Context(), r.conn, &CallPayload{Method: CallSessionGraphRelease, Args: args}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *lifetimeRig) createChild(id string, create ChildSessionCreate) error {
	r.t.Helper()
	create.ID = id
	args, _ := json.Marshal(create)
	_, err := r.host.createChildSession(r.t.Context(), r.conn, &CallPayload{Method: CallChildSessionCreate, Args: args})
	return err
}

// An object a child session was created over outlives the extension's release of its handle: Pi's runtime lives while the session holds it, and the extension's reference is only one of the holders.
func TestSessionGraphObjectOutlivesHandleReleaseWhileAChildUsesIt(t *testing.T) {
	r := newLifetimeRig(t)
	runtime, settings := &lifetimeObject{}, &lifetimeObject{}
	runtimeHandle, settingsHandle := r.build(runtime), r.build(settings)
	if err := r.createChild("child", ChildSessionCreate{ModelRuntime: runtimeHandle, SettingsManager: settingsHandle}); err != nil {
		t.Fatal(err)
	}
	if r.seen.ModelRuntime != SessionGraphObject(runtime) || r.seen.SettingsManager != SessionGraphObject(settings) {
		t.Fatal("the factory was not given the objects the handles name")
	}
	r.release(runtimeHandle)
	r.release(settingsHandle)
	if got := runtime.released.Load(); got != 0 {
		t.Fatalf("the runtime was released %d times while a child session uses it", got)
	}
	if got := settings.released.Load(); got != 0 {
		t.Fatalf("the settings manager was released %d times while a child session uses it", got)
	}
	r.host.childSessions.closeConn(r.conn)
	if got := runtime.released.Load(); got != 1 {
		t.Fatalf("the runtime was released %d times after its last child closed, want 1", got)
	}
	if got := settings.released.Load(); got != 1 {
		t.Fatalf("the settings manager was released %d times after its last child closed, want 1", got)
	}
}

// Every child that uses an object keeps it: it is released when the last holder lets go, whichever lets go last.
func TestSessionGraphObjectIsReleasedByItsLastHolder(t *testing.T) {
	r := newLifetimeRig(t)
	runtime := &lifetimeObject{}
	handle := r.build(runtime)
	for _, id := range []string{"first", "second"} {
		if err := r.createChild(id, ChildSessionCreate{ModelRuntime: handle}); err != nil {
			t.Fatal(err)
		}
	}
	r.release(handle)
	r.host.childSessions.remove(r.conn, "first").close()
	if got := runtime.released.Load(); got != 0 {
		t.Fatalf("released %d times while the second child uses it", got)
	}
	r.host.childSessions.remove(r.conn, "second").close()
	if got := runtime.released.Load(); got != 1 {
		t.Fatalf("released %d times after its last holder, want 1", got)
	}
}

// A child that closes before the extension releases the handle leaves the release to the extension.
func TestSessionGraphObjectStaysWhileTheExtensionHoldsItsHandle(t *testing.T) {
	r := newLifetimeRig(t)
	runtime := &lifetimeObject{}
	handle := r.build(runtime)
	if err := r.createChild("child", ChildSessionCreate{ModelRuntime: handle}); err != nil {
		t.Fatal(err)
	}
	r.host.childSessions.closeConn(r.conn)
	if got := runtime.released.Load(); got != 0 {
		t.Fatalf("released %d times while the extension holds the handle", got)
	}
	r.release(handle)
	r.release(handle)
	if got := runtime.released.Load(); got != 1 {
		t.Fatalf("released %d times, want 1", got)
	}
}

// A creation that fails holds nothing: releasing the handle releases the object.
func TestSessionGraphObjectIsNotHeldByAFailedChildCreation(t *testing.T) {
	r := newLifetimeRig(t)
	runtime := &lifetimeObject{}
	handle := r.build(runtime)
	r.fail.Store(true)
	if err := r.createChild("child", ChildSessionCreate{ModelRuntime: handle}); err == nil {
		t.Fatal("the factory's refusal was swallowed")
	}
	r.release(handle)
	if got := runtime.released.Load(); got != 1 {
		t.Fatalf("released %d times, want 1: the failed creation kept its hold", got)
	}
}

// A creation that names an object together with the option it replaces fails before it holds anything.
func TestSessionGraphObjectIsNotHeldByARefusedChildCreation(t *testing.T) {
	r := newLifetimeRig(t)
	object := &lifetimeObject{}
	handle := r.build(object)
	if err := r.createChild("child", ChildSessionCreate{SettingsManager: handle, Settings: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("settingsManager and settings together were accepted")
	}
	r.release(handle)
	if got := object.released.Load(); got != 1 {
		t.Fatalf("released %d times, want 1", got)
	}
}

// A host that shuts down with a live child session closes it, and releases the objects it was created over, before Shutdown returns. The session outlives the extension's dispose of it, and the object outlives the extension's handle: nothing else ends the process either one started. The connection stays open here, so only Shutdown can end them.
func TestShutdownClosesLiveChildSessionsAndReleasesTheirObjects(t *testing.T) {
	r := newLifetimeRig(t)
	runtime := &lifetimeObject{}
	handle := r.build(runtime)
	if err := r.createChild("child", ChildSessionCreate{ModelRuntime: handle}); err != nil {
		t.Fatal(err)
	}
	session := r.session
	r.host.Shutdown("test shutdown")
	session.mu.Lock()
	closed := session.closed
	session.mu.Unlock()
	if closed != 1 {
		t.Fatalf("the live child session was closed %d times by Shutdown, want 1", closed)
	}
	if got := runtime.released.Load(); got != 1 {
		t.Fatalf("the object the child was created over was released %d times by Shutdown, want 1", got)
	}
}
