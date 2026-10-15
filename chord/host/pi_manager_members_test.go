package host

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// The manager's side of the Driver and Instance contracts (manager.ts Driver, Instance): what createHostManager calls on
// the driver and on the child it spawned, and what Liveness reports from the child's status. Pi's manager.test.ts drives
// these only through real child processes (forkDriver); these tests drive CreateHostManager with the fake child of
// manager_upstream_test.go behind closures that object each call the manager makes through the interfaces.

// driverFunc is a Driver whose Spawn is a closure.
type driverFunc func() (Instance, error)

func (spawn driverFunc) Spawn() (Instance, error) { return spawn() }

// instanceProbe is an Instance whose control methods are closures; the rest is the embedded child.
type instanceProbe struct {
	Instance
	send      func(message ControlMessage)
	killTree  func()
	onControl func(listener func(message ControlMessage))
}

func (probe *instanceProbe) Send(message ControlMessage) { probe.send(message) }
func (probe *instanceProbe) KillTree()                   { probe.killTree() }
func (probe *instanceProbe) OnControl(listener func(message ControlMessage)) {
	probe.onControl(listener)
}

// callLog records the manager's calls in order.
type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (log *callLog) add(call string) {
	log.mu.Lock()
	log.calls = append(log.calls, call)
	log.mu.Unlock()
}

func (log *callLog) snapshot() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.calls)
}

// awaitStart runs start and fails the test when it does not return within five seconds.
func awaitStart(t *testing.T, start func() (Instance, error)) (Instance, error) {
	t.Helper()
	type result struct {
		instance Instance
		err      error
	}
	done := make(chan result, 1)
	go func() {
		instance, err := start()
		done <- result{instance, err}
	}()
	select {
	case got := <-done:
		return got.instance, got.err
	case <-time.After(5 * time.Second):
		t.Fatal("start did not return")
		return nil, nil
	}
}

// awaitStop runs stop and fails the test when it does not return within five seconds.
func awaitStop(t *testing.T, stop func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- stop() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return")
		return nil
	}
}

// Pi packages/chord/src/manager.ts:305 doStart spawns the child through options.driver.spawn() once per start: a running
// child is returned as is (manager.ts:299), a replace spawns the next child, and a spawn that throws fails the start with
// the driver's error and leaves the manager stopped (manager.ts:306-309). manager.ts:334 registers the control listener
// with current.onControl before the start can settle, and the child's ready settles it.
func TestHostManagerSpawnsThroughTheDriverAndListensForControl(t *testing.T) {
	log := &callLog{}
	children := newFakeDriver()
	children.onSend = gracefulOnShutdown
	var base Driver = children
	spawnErr := errors.New("spawn refused")
	refuse := false
	manager := CreateHostManager(HostManagerOptions{Driver: driverFunc(func() (Instance, error) {
		log.add("spawn")
		if refuse {
			return nil, spawnErr
		}
		spawned, err := base.Spawn()
		if err != nil {
			return nil, err
		}
		return &instanceProbe{
			Instance: spawned,
			send: func(message ControlMessage) {
				log.add("send " + string(message.Type))
				spawned.Send(message)
			},
			killTree: func() {
				log.add("killTree")
				spawned.KillTree()
			},
			onControl: func(listener func(message ControlMessage)) {
				log.add("onControl")
				spawned.OnControl(listener)
			},
		}, nil
	})})
	ready := func() {
		child := <-children.spawned
		child.waitListening(t)
		child.emit(ControlMessage{Type: ControlReady})
	}

	go ready()
	first, err := awaitStart(t, manager.Start)
	if err != nil || first == nil || manager.Status() != HostManagerRunning {
		t.Fatalf("start = %v, %v (status %s)", first, err, manager.Status())
	}
	if calls := log.snapshot(); !slices.Equal(calls, []string{"spawn", "onControl"}) {
		t.Fatalf("calls after start = %v, want [spawn onControl]", calls)
	}

	again, err := awaitStart(t, manager.Start)
	if err != nil || again != first {
		t.Fatalf("second start = %v, %v, want the running child", again, err)
	}
	if calls := log.snapshot(); !slices.Equal(calls, []string{"spawn", "onControl"}) {
		t.Fatalf("calls after a second start = %v, want no second spawn", calls)
	}

	go ready()
	next, err := awaitStart(t, manager.Replace)
	if err != nil || next == nil || next == first || manager.Instance() != next {
		t.Fatalf("replace = %v, %v", next, err)
	}
	want := []string{"spawn", "onControl", "send shutdown", "spawn", "onControl"}
	if calls := log.snapshot(); !slices.Equal(calls, want) {
		t.Fatalf("calls after replace = %v, want %v", calls, want)
	}

	if err := awaitStop(t, manager.Stop); err != nil {
		t.Fatal(err)
	}
	refuse = true
	instance, err := awaitStart(t, manager.Start)
	if !errors.Is(err, spawnErr) || instance != nil {
		t.Fatalf("start with a refusing driver = %v, %v, want the driver's error", instance, err)
	}
	if manager.Status() != HostManagerStopped || manager.Instance() != nil || manager.Liveness().Booting {
		t.Fatalf("after a failed spawn: status %s, instance %v, liveness %+v", manager.Status(), manager.Instance(), manager.Liveness())
	}
}

// Pi packages/chord/src/manager.ts:283 doStop asks the child to shut down with current.send({ type: "shutdown" }); a child
// that exits within the grace period is not killed, and one still running past it is killed with current.killTree()
// (manager.ts:292). A start that fails (fatal or no ready in time) kills the child's tree before it rejects (manager.ts:352).
func TestHostManagerSendsShutdownAndKillsTheTreePastTheGrace(t *testing.T) {
	type run struct {
		name    string
		onSend  func(instance *fakeInstance, message ControlMessage)
		control *ControlMessage
		want    []string
		failed  bool
	}
	runs := []run{
		{name: "graceful", onSend: gracefulOnShutdown, want: []string{"spawn", "onControl", "send shutdown"}},
		{name: "ignores shutdown", want: []string{"spawn", "onControl", "send shutdown", "killTree"}},
		{name: "fatal start", control: &ControlMessage{Type: ControlFatal, Message: "no"}, want: []string{"spawn", "onControl", "killTree"}, failed: true},
	}
	for _, run := range runs {
		t.Run(run.name, func(t *testing.T) {
			log := &callLog{}
			children := newFakeDriver()
			children.onSend = run.onSend
			var base Driver = children
			manager := CreateHostManager(HostManagerOptions{Grace: ms(30), Driver: driverFunc(func() (Instance, error) {
				log.add("spawn")
				spawned, err := base.Spawn()
				if err != nil {
					return nil, err
				}
				return &instanceProbe{
					Instance: spawned,
					send: func(message ControlMessage) {
						log.add("send " + string(message.Type))
						spawned.Send(message)
					},
					killTree: func() {
						log.add("killTree")
						spawned.KillTree()
					},
					onControl: func(listener func(message ControlMessage)) {
						log.add("onControl")
						spawned.OnControl(listener)
					},
				}, nil
			})})
			go func() {
				child := <-children.spawned
				child.waitListening(t)
				if run.control != nil {
					child.emit(*run.control)
					return
				}
				child.emit(ControlMessage{Type: ControlReady})
			}()
			instance, err := awaitStart(t, manager.Start)
			if run.failed {
				if err == nil {
					t.Fatalf("start = %v, want it to fail", instance)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err := awaitStop(t, manager.Stop); err != nil {
					t.Fatal(err)
				}
				exit := instance.Exit()
				killed := slices.Contains(run.want, "killTree")
				if killed != (exit.Signal != nil && *exit.Signal == "SIGKILL") {
					t.Fatalf("exit = %+v, killed = %v", exit, killed)
				}
			}
			if calls := log.snapshot(); !slices.Equal(calls, run.want) {
				t.Fatalf("calls = %v, want %v", calls, run.want)
			}
			if manager.Status() != HostManagerStopped || manager.Instance() != nil {
				t.Fatalf("status %s, instance %v", manager.Status(), manager.Instance())
			}
		})
	}
}

// Pi packages/chord/src/manager.ts:341-345 keeps the child's status report (viewers and durableTasks default to 0 when
// absent) and liveness (manager.ts:268-272) reports them; either count is work, so idleFor stays 0 while one is set
// (manager.ts:347). A new start resets the report (manager.ts:302).
func TestHostManagerLivenessReportsViewersAndDurableTasks(t *testing.T) {
	children := newFakeDriver()
	children.onSend = gracefulOnShutdown
	manager := CreateHostManager(HostManagerOptions{Driver: children})
	startReady(t, manager, children)
	child := children.instances[0]

	child.emit(ControlMessage{Type: ControlStatus, Viewers: new(2.0), DurableTasks: new(3.0)})
	time.Sleep(20 * time.Millisecond)
	liveness := manager.Liveness()
	if liveness.Viewers != 2 || liveness.DurableTasks != 3 || liveness.ActiveCalls != 0 || liveness.IdleFor != 0 {
		t.Fatalf("liveness = %+v, want viewers 2, durableTasks 3, idleFor 0", liveness)
	}

	child.emit(ControlMessage{Type: ControlStatus, DurableTasks: new(1.0)})
	time.Sleep(20 * time.Millisecond)
	liveness = manager.Liveness()
	if liveness.Viewers != 0 || liveness.DurableTasks != 1 || liveness.IdleFor != 0 {
		t.Fatalf("liveness = %+v, want viewers 0 (absent), durableTasks 1, idleFor 0", liveness)
	}

	child.emit(ControlMessage{Type: ControlStatus, Viewers: new(4.0)})
	if liveness = manager.Liveness(); liveness.Viewers != 4 || liveness.DurableTasks != 0 {
		t.Fatalf("liveness = %+v, want viewers 4, durableTasks 0 (absent)", liveness)
	}

	if err := awaitStop(t, manager.Stop); err != nil {
		t.Fatal(err)
	}
	startReady(t, manager, children)
	if liveness = manager.Liveness(); liveness.Viewers != 0 || liveness.DurableTasks != 0 || liveness.Booting {
		t.Fatalf("liveness after a new start = %+v, want the report reset", liveness)
	}
}
