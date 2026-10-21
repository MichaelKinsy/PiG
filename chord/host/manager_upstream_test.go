package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/test/manager.test.ts ("host manager (real child processes)"). The real-process cases fork
// testdata/manager_relay.mjs under Node through ForkDriver; the child host is this test binary
// (TestManagerWorkerProcess, Pi's test/fixtures/worker.ts) behind the relay, so the instance's pid is the relay's.
// The fake driver of manager.test.ts:80-105 also drives the unit tests below the Pi cases (manager.ts contracts).

// managerEcho is test/fixtures/echo.ts Echo.
var managerEcho = Remote("t.echo", RemoteDef{
	Version: 1,
	Input:   tObject(),
	Host:    NewHostNamed("w1"),
	Variants: Fields[VariantDef]{
		{"main", VariantDef{Methods: Fields[MethodSpec]{
			{"echo", Method(tString(), tString())},
			{"pid", Method(nil, tNumber())},
		}}},
	},
})

func managerScratch(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

func waitNotAlive(pid int) {
	for i := 0; i < 50 && alive(pid); i++ {
		time.Sleep(20 * time.Millisecond)
	}
}

// manager.test.ts (Pi a4d040d3) "a child without a lock does not inherit the parent's lock": a child host that forks
// its own children used to pass its CHORD2_LOCK on, so they failed with "locked".
func TestHostManagerAChildWithoutALockDoesNotInheritTheParentsLock(t *testing.T) {
	name := fmt.Sprintf("chord2-test-%d-inherited", os.Getpid())
	release, err := ProcessLock(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(lockEnv, name)
	manager := CreateHostManager(HostManagerOptions{Driver: managerDriver(t, nil, nil)})
	defer func() {
		_ = manager.Stop()
		_ = release()
	}()
	if _, err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	if manager.Status() != HostManagerRunning {
		t.Fatalf("status = %s", manager.Status())
	}
}

// manager.test.ts:27-40 "starts a child host, links it, stops it gracefully".
func TestHostManagerStartsAChildHostLinksItStopsItGracefully(t *testing.T) {
	manager := CreateHostManager(HostManagerOptions{Driver: managerDriver(t, nil, nil)})
	instance, err := manager.Start()
	if err != nil {
		t.Fatal(err)
	}
	if manager.Status() != HostManagerRunning {
		t.Fatalf("status = %s", manager.Status())
	}
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	if err := server.Accept(instance.Link(), AcceptOptions{Principal: chordjson.ObjectOf("worker", "w1"), Kind: KindHost, Name: new("w1")}); err != nil {
		t.Fatal(err)
	}
	echo, err := server.Hold(managerEcho, chordjson.ObjectOf(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := callMember(echo, "echo", "x", nil); err != nil || got != "hi x from "+strconv.Itoa(instance.PID()) {
		t.Fatalf("echo = %v, %v", got, err)
	}
	if again, err := manager.Start(); err != nil || again != instance {
		t.Fatalf("start again = %v, %v", again, err)
	}
	if err := manager.Stop(); err != nil {
		t.Fatal(err)
	}
	if manager.Status() != HostManagerStopped {
		t.Fatalf("status = %s", manager.Status())
	}
	if exit := instance.Exit(); exit.Code == nil || *exit.Code != 0 || exit.Signal != nil {
		t.Fatalf("exit = %+v", exit)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}

// watchedDriver spawns from base after checking that every earlier child is gone (manager.test.ts:46-53).
type watchedDriver struct {
	t    *testing.T
	base Driver
	pids []int
}

func (driver *watchedDriver) Spawn() (Instance, error) {
	// Exit before replace: the previous child is gone when the next one spawns.
	for _, pid := range driver.pids {
		if alive(pid) {
			driver.t.Errorf("pid %d still alive", pid)
		}
	}
	instance, err := driver.base.Spawn()
	if err == nil {
		driver.pids = append(driver.pids, instance.PID())
	}
	return instance, err
}

// manager.test.ts:42-65 "a child-owned kernel lock admits one live process; replace waits for the exit before the next
// start".
func TestHostManagerAChildOwnedKernelLockAdmitsOneLiveProcessReplaceWaitsForTheExitBeforeTheNextStart(t *testing.T) {
	lock := "chord2-test-" + strconv.Itoa(os.Getpid()) + "-lock"
	watched := &watchedDriver{t: t, base: managerDriver(t, nil, &lock)}
	first := CreateHostManager(HostManagerOptions{Driver: watched})
	second := CreateHostManager(HostManagerOptions{Driver: managerDriver(t, nil, &lock)})
	a, err := first.Start()
	if err != nil {
		t.Fatal(err)
	}
	// Both managers race for the same lock; only the kernel decides, so the second child cannot start.
	_, err = second.Start()
	var chordError *ChordError
	if !IsChordError(err, CodeUnavailable) || !errors.As(err, &chordError) || chordError.Reason != "locked" {
		t.Fatalf("second start = %v", err)
	}
	b, err := first.Replace()
	if err != nil {
		t.Fatal(err)
	}
	if a.PID() == b.PID() {
		t.Fatalf("replace kept pid %d", a.PID())
	}
	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Start(); err != nil {
		t.Fatal(err)
	}
	if err := second.Stop(); err != nil {
		t.Fatal(err)
	}
}

// manager.test.ts:67-78 "a graceful exit still takes the child's descendants with it".
func TestHostManagerAGracefulExitStillTakesTheChildsDescendantsWithIt(t *testing.T) {
	file := managerScratch(t, "grandchild-graceful")
	manager := CreateHostManager(HostManagerOptions{Driver: managerDriver(t, map[string]string{"GRANDCHILD_FILE": file}, nil)})
	if _, err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	grandchild := readPid(t, file)
	if !alive(grandchild) {
		t.Fatal("grandchild not running")
	}
	if err := manager.Stop(); err != nil {
		t.Fatal(err)
	}
	waitNotAlive(grandchild)
	if alive(grandchild) {
		t.Fatalf("grandchild %d outlived the child", grandchild)
	}
}

func readPid(t *testing.T, file string) int {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// manager.test.ts:107-122 "kills the process tree when a child ignores shutdown past the grace period".
func TestHostManagerKillsTheProcessTreeWhenAChildIgnoresShutdownPastTheGracePeriod(t *testing.T) {
	file := managerScratch(t, "grandchild")
	manager := CreateHostManager(HostManagerOptions{
		Driver: managerDriver(t, map[string]string{"FIXTURE_MODE": "ignore-shutdown", "GRANDCHILD_FILE": file}, nil),
		Grace:  ms(300),
	})
	instance, err := manager.Start()
	if err != nil {
		t.Fatal(err)
	}
	grandchild := readPid(t, file)
	if !alive(grandchild) {
		t.Fatal("grandchild not running")
	}
	if err := manager.Stop(); err != nil {
		t.Fatal(err)
	}
	if exit := instance.Exit(); exit.Signal == nil || *exit.Signal != "SIGKILL" {
		t.Fatalf("exit = %+v", exit)
	}
	waitNotAlive(grandchild)
	if alive(grandchild) {
		t.Fatalf("grandchild %d outlived the child", grandchild)
	}
}

// manager.test.ts:124-128 "reports a fatal child as a failed start".
func TestHostManagerReportsAFatalChildAsAFailedStart(t *testing.T) {
	manager := CreateHostManager(HostManagerOptions{Driver: managerDriver(t, map[string]string{"FIXTURE_MODE": "fatal"}, nil)})
	if _, err := manager.Start(); err == nil || !strings.Contains(err.Error(), "fixture refuses to start") {
		t.Fatalf("err = %v", err)
	}
	if manager.Status() != HostManagerStopped {
		t.Fatalf("status = %s", manager.Status())
	}
}

// manager.test.ts:130-143 "stops an idle child when keepAlive says so".
func TestHostManagerStopsAnIdleChildWhenKeepAliveSaysSo(t *testing.T) {
	var mu sync.Mutex
	var exits []Exit
	manager := CreateHostManager(HostManagerOptions{
		Driver:        managerDriver(t, nil, nil),
		KeepAlive:     func(liveness Liveness) bool { return !liveness.Booting && liveness.IdleFor < 150*time.Millisecond },
		CheckInterval: ms(50),
		OnExit: func(exit Exit) {
			mu.Lock()
			exits = append(exits, exit)
			mu.Unlock()
		},
	})
	if _, err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100 && manager.Status() != HostManagerStopped; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if manager.Status() != HostManagerStopped {
		t.Fatalf("status = %s", manager.Status())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(exits) != 1 {
		t.Fatalf("exits = %+v", exits)
	}
}

// manager.ts `current.exited.then` sets the status to stopped and calls onExit in one callback: onExit sees the manager
// stopped, and no other code sees it stopped before onExit has returned. Here the other goroutines wait for the report.
func TestHostManagerReadsStoppedInsideOnExitAndNowhereElseBeforeItReturns(t *testing.T) {
	driver := newFakeDriver()
	var manager *HostManager
	type inside struct {
		status   string
		instance Instance
	}
	inExit, release := make(chan inside), make(chan struct{})
	var returned atomic.Bool
	manager = CreateHostManager(HostManagerOptions{Driver: driver, OnExit: func(Exit) {
		inExit <- inside{manager.Status(), manager.Instance()}
		<-release
		returned.Store(true)
	}})
	startReady(t, manager, driver)
	driver.instances[0].end(Exit{Code: new(0)})
	if got := <-inExit; got.status != HostManagerStopped || got.instance != nil {
		t.Fatalf("inside onExit: status %s, instance %v; want stopped and none", got.status, got.instance)
	}
	// onExit has not returned. Another goroutine's reads wait for it: none may come back before it did.
	readers := make(chan string, 3)
	go func() { readers <- manager.Status() }()
	go func() { readers <- fmt.Sprint(manager.Instance()) }()
	go func() { readers <- fmt.Sprint(manager.Liveness()) }()
	select {
	case got := <-readers:
		t.Fatalf("a goroutine read %q while onExit was still running", got)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for range 3 {
		<-readers
	}
	if !returned.Load() {
		t.Fatal("a read came back before onExit returned")
	}
	if status := manager.Status(); status != HostManagerStopped {
		t.Fatalf("status after the report = %s, want stopped", status)
	}
}

// upstream's onExit may start the child again (manager.start() from the callback sees it stopped); the gate that keeps
// other goroutines out until onExit returns must not keep out the callback itself.
func TestHostManagerOnExitMayStartTheChildAgain(t *testing.T) {
	driver := newFakeDriver()
	var manager *HostManager
	restarted := make(chan error, 1)
	var once sync.Once
	manager = CreateHostManager(HostManagerOptions{Driver: driver, OnExit: func(Exit) {
		once.Do(func() {
			_, err := manager.Start()
			restarted <- err
		})
	}})
	startReady(t, manager, driver)
	driver.instances[0].end(Exit{Code: new(1)})
	second := <-driver.spawned
	second.waitListening(t)
	second.emit(ControlMessage{Type: ControlReady})
	if err := <-restarted; err != nil {
		t.Fatalf("start from onExit: %v", err)
	}
	if status := manager.Status(); status != HostManagerRunning {
		t.Fatalf("status after the restart = %s, want running", status)
	}
}

type fakeInstance struct {
	pid       int
	exited    chan struct{}
	exitOnce  sync.Once
	exit      Exit
	mu        sync.Mutex
	listeners []func(ControlMessage)
	sent      []ControlMessage
	// onSend reacts to a control message from the manager (a child's shutdown handling).
	onSend func(instance *fakeInstance, message ControlMessage)
	killed int
}

func newFakeInstance(pid int) *fakeInstance {
	return &fakeInstance{pid: pid, exited: make(chan struct{})}
}

func (instance *fakeInstance) PID() int                { return instance.pid }
func (instance *fakeInstance) Link() *IpcLink          { return nil }
func (instance *fakeInstance) Exited() <-chan struct{} { return instance.exited }
func (instance *fakeInstance) Exit() Exit {
	<-instance.exited
	return instance.exit
}

func (instance *fakeInstance) Send(message ControlMessage) {
	instance.mu.Lock()
	instance.sent = append(instance.sent, message)
	onSend := instance.onSend
	instance.mu.Unlock()
	if onSend != nil {
		onSend(instance, message)
	}
}

func (instance *fakeInstance) KillTree() {
	instance.mu.Lock()
	instance.killed++
	instance.mu.Unlock()
	instance.end(Exit{Signal: new("SIGKILL")})
}

func (instance *fakeInstance) OnControl(listener func(ControlMessage)) {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	instance.listeners = append(instance.listeners, listener)
}

func (instance *fakeInstance) emit(message ControlMessage) {
	instance.mu.Lock()
	listeners := append([]func(ControlMessage){}, instance.listeners...)
	instance.mu.Unlock()
	for _, listener := range listeners {
		listener(message)
	}
}

func (instance *fakeInstance) end(exit Exit) {
	instance.exitOnce.Do(func() {
		instance.exit = exit
		close(instance.exited)
	})
}

func (instance *fakeInstance) waitListening(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		instance.mu.Lock()
		n := len(instance.listeners)
		instance.mu.Unlock()
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("manager never listened for control messages")
		}
		time.Sleep(time.Millisecond)
	}
}

// gracefulOnShutdown exits with code 0 when told to shut down (the fixture worker's normal mode).
func gracefulOnShutdown(instance *fakeInstance, message ControlMessage) {
	if message.Type == ControlShutdown {
		go instance.end(Exit{Code: new(0)})
	}
}

type fakeDriver struct {
	mu        sync.Mutex
	instances []*fakeInstance
	spawned   chan *fakeInstance
	onSend    func(instance *fakeInstance, message ControlMessage)
	err       error
}

func newFakeDriver() *fakeDriver { return &fakeDriver{spawned: make(chan *fakeInstance, 16)} }

func (driver *fakeDriver) Spawn() (Instance, error) {
	if driver.err != nil {
		return nil, driver.err
	}
	driver.mu.Lock()
	instance := newFakeInstance(len(driver.instances) + 1)
	instance.onSend = driver.onSend
	driver.instances = append(driver.instances, instance)
	driver.mu.Unlock()
	driver.spawned <- instance
	return instance, nil
}

// startReady starts manager and answers ready from the spawned child.
func startReady(t *testing.T, manager *HostManager, driver *fakeDriver) Instance {
	t.Helper()
	type result struct {
		instance Instance
		err      error
	}
	done := make(chan result, 1)
	go func() {
		instance, err := manager.Start()
		done <- result{instance, err}
	}()
	child := <-driver.spawned
	child.waitListening(t)
	child.emit(ControlMessage{Type: ControlReady})
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	return got.instance
}

func ms(n int) *time.Duration { return new(time.Duration(n) * time.Millisecond) }

// manager.test.ts:80-105 "idleFor counts from the busy → idle transition" (manager.ts:316-322).
func TestHostManagerIdleForCountsFromTheBusyToIdleTransition(t *testing.T) {
	driver := newFakeDriver()
	manager := CreateHostManager(HostManagerOptions{Driver: driver})
	startReady(t, manager, driver)
	child := driver.instances[0]
	child.emit(ControlMessage{Type: ControlStatus, ActiveCalls: new(1.0)})
	time.Sleep(120 * time.Millisecond)
	if idle := manager.Liveness().IdleFor; idle != 0 {
		t.Fatalf("busy idleFor = %v", idle)
	}
	child.emit(ControlMessage{Type: ControlStatus, ActiveCalls: new(0.0)})
	if idle := manager.Liveness().IdleFor; idle >= 60*time.Millisecond {
		t.Fatalf("idleFor = %v, want it counted from the transition", idle)
	}
	if liveness := manager.Liveness(); liveness.Booting || liveness.ActiveCalls != 0 {
		t.Fatalf("liveness = %+v", liveness)
	}
}

// manager.ts:316-322 keeps a child's counts as the JS numbers it sent: a fractional count is work, so the child is
// busy (idleFor 0) and liveness reports the count as sent. Pi 1.2.0's manager, driven by node, gives
// {"activeCalls":0.5,"idleFor":0} for this report.
func TestHostManagerKeepsFractionalActivityCounts(t *testing.T) {
	driver := newFakeDriver()
	manager := CreateHostManager(HostManagerOptions{Driver: driver})
	startReady(t, manager, driver)
	var message ControlMessage
	if err := json.Unmarshal([]byte(`{"type":"status","activeCalls":0.5}`), &message); err != nil {
		t.Fatal(err)
	}
	driver.instances[0].emit(message)
	if liveness := manager.Liveness(); fmt.Sprint(liveness.ActiveCalls) != "0.5" || liveness.IdleFor != 0 {
		t.Fatalf("liveness = %+v, want activeCalls 0.5 and idleFor 0", liveness)
	}
}

// Unit (fake driver): start returns the running instance, stop is graceful (manager.ts:248-270, :279).
func TestHostManagerStartIsIdempotentAndStopIsGraceful(t *testing.T) {
	driver := newFakeDriver()
	driver.onSend = gracefulOnShutdown
	var exits []Exit
	var mu sync.Mutex
	manager := CreateHostManager(HostManagerOptions{Driver: driver, OnExit: func(exit Exit) {
		mu.Lock()
		exits = append(exits, exit)
		mu.Unlock()
	}})
	instance := startReady(t, manager, driver)
	if manager.Status() != HostManagerRunning || manager.Instance() != instance {
		t.Fatalf("status = %s", manager.Status())
	}
	again, err := manager.Start()
	if err != nil || again != instance || len(driver.instances) != 1 {
		t.Fatalf("second start = %v, %v (spawns %d)", again, err, len(driver.instances))
	}
	if err := manager.Stop(); err != nil {
		t.Fatal(err)
	}
	if manager.Status() != HostManagerStopped || manager.Instance() != nil {
		t.Fatalf("after stop: %s %v", manager.Status(), manager.Instance())
	}
	if exit := instance.Exit(); exit.Code == nil || *exit.Code != 0 || exit.Signal != nil {
		t.Fatalf("exit = %+v", exit)
	}
	if driver.instances[0].killed != 0 {
		t.Fatal("graceful stop killed the tree")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(exits) != 1 {
		t.Fatalf("onExit calls = %d", len(exits))
	}
}

// Unit (fake driver) for manager.test.ts:107-122: the tree is killed only past the grace period (manager.ts:254-266).
func TestHostManagerKillsTreeAfterGrace(t *testing.T) {
	driver := newFakeDriver()
	manager := CreateHostManager(HostManagerOptions{Driver: driver, Grace: ms(50)})
	instance := startReady(t, manager, driver)
	started := time.Now()
	if err := manager.Stop(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 50*time.Millisecond {
		t.Fatalf("stop returned after %v, before the grace period", elapsed)
	}
	if exit := instance.Exit(); exit.Signal == nil || *exit.Signal != "SIGKILL" {
		t.Fatalf("exit = %+v", exit)
	}
	if sent := driver.instances[0].sent; len(sent) != 1 || sent[0].Type != ControlShutdown {
		t.Fatalf("sent = %+v", sent)
	}
}

// Unit (fake driver) for manager.test.ts:124-128: a fatal child fails the start (manager.ts:304-308, :325-329).
func TestHostManagerFatalChildFailsStart(t *testing.T) {
	driver := newFakeDriver()
	manager := CreateHostManager(HostManagerOptions{Driver: driver})
	done := make(chan error, 1)
	go func() {
		_, err := manager.Start()
		done <- err
	}()
	child := <-driver.spawned
	child.waitListening(t)
	child.emit(ControlMessage{Type: ControlFatal, Message: "fixture refuses to start", Reason: "locked"})
	err := <-done
	var chordError *ChordError
	if !errors.As(err, &chordError) || chordError.Code != CodeUnavailable || chordError.Reason != "locked" ||
		!strings.Contains(err.Error(), "child failed: fixture refuses to start") {
		t.Fatalf("err = %v", err)
	}
	if manager.Status() != HostManagerStopped || child.killed != 1 {
		t.Fatalf("status = %s, killed = %d", manager.Status(), child.killed)
	}
}

// manager.ts:297-302, :292-295: a child that exits before ready, or never reports ready, fails the start.
func TestHostManagerExitBeforeReadyAndReadyTimeout(t *testing.T) {
	driver := newFakeDriver()
	manager := CreateHostManager(HostManagerOptions{Driver: driver, ReadyTimeout: ms(40)})
	done := make(chan error, 1)
	go func() {
		_, err := manager.Start()
		done <- err
	}()
	child := <-driver.spawned
	child.waitListening(t)
	child.end(Exit{Code: new(3)})
	if err := <-done; !IsChordError(err, CodeUnavailable) || !strings.HasSuffix(err.Error(), "child exited before ready (3)") {
		t.Fatalf("err = %v", err)
	}
	go func() {
		_, err := manager.Start()
		done <- err
	}()
	<-driver.spawned
	if err := <-done; !IsChordError(err, CodeUnavailable) || !strings.HasSuffix(err.Error(), "child not ready in time") {
		t.Fatalf("err = %v", err)
	}
	if manager.Status() != HostManagerStopped {
		t.Fatalf("status = %s", manager.Status())
	}
	driver.err = errors.New("spawn failed")
	if _, err := manager.Start(); err != driver.err || manager.Status() != HostManagerStopped {
		t.Fatalf("spawn error = %v, status = %s", err, manager.Status())
	}
}

// Unit (fake driver) for manager.test.ts:130-143: keepAlive stops an idle child (manager.ts:335-341).
func TestHostManagerKeepAliveStopsIdleChild(t *testing.T) {
	driver := newFakeDriver()
	driver.onSend = gracefulOnShutdown
	exits := make(chan Exit, 4)
	manager := CreateHostManager(HostManagerOptions{
		Driver:        driver,
		KeepAlive:     func(liveness Liveness) bool { return !liveness.Booting && liveness.IdleFor < 150*time.Millisecond },
		CheckInterval: ms(50),
		OnExit:        func(exit Exit) { exits <- exit },
	})
	startReady(t, manager, driver)
	deadline := time.Now().Add(2 * time.Second)
	for manager.Status() != HostManagerStopped && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if manager.Status() != HostManagerStopped {
		t.Fatalf("status = %s", manager.Status())
	}
	<-exits
	time.Sleep(120 * time.Millisecond)
	if len(exits) != 0 {
		t.Fatal("onExit called more than once")
	}
}

// manager.ts:233-237: a stop requested while a start waits for ready runs after that start settles.
func TestHostManagerStopWaitsForPendingStart(t *testing.T) {
	driver := newFakeDriver()
	driver.onSend = gracefulOnShutdown
	manager := CreateHostManager(HostManagerOptions{Driver: driver})
	started := make(chan error, 1)
	go func() {
		_, err := manager.Start()
		started <- err
	}()
	child := <-driver.spawned
	child.waitListening(t)
	stopped := make(chan error, 1)
	go func() { stopped <- manager.Stop() }()
	time.Sleep(50 * time.Millisecond)
	child.mu.Lock()
	early := len(child.sent)
	child.mu.Unlock()
	if early != 0 || manager.Status() != HostManagerStarting {
		t.Fatalf("stop ran during the start: sent %d, status %s", early, manager.Status())
	}
	child.emit(ControlMessage{Type: ControlReady})
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil || manager.Status() != HostManagerStopped {
		t.Fatalf("stop = %v, status %s", err, manager.Status())
	}
}

// Unit (fake driver) for manager.test.ts:42-65 and manager.ts:233-237, :375-379: serialized steps; replace waits for
// the exit before the next spawn.
func TestHostManagerReplaceWaitsForExit(t *testing.T) {
	driver := newFakeDriver()
	driver.onSend = gracefulOnShutdown
	manager := CreateHostManager(HostManagerOptions{Driver: driver})
	first := startReady(t, manager, driver)
	done := make(chan Instance, 1)
	go func() {
		next, err := manager.Replace()
		if err != nil {
			t.Error(err)
		}
		done <- next
	}()
	child := <-driver.spawned
	select {
	case <-first.Exited():
	default:
		t.Fatal("replace spawned before the previous child exited")
	}
	child.waitListening(t)
	child.emit(ControlMessage{Type: ControlReady})
	if next := <-done; next == first || next.PID() == first.PID() {
		t.Fatal("replace returned the old instance")
	}
}

// manager.ts:10-20 ControlMessage on the wire: { chord2control: message } with each type's members.
func TestControlMessageJSON(t *testing.T) {
	cases := []struct {
		message ControlMessage
		want    string
	}{
		{ControlMessage{Type: ControlInit, Value: nil}, `{"type":"init","value":null}`},
		{ControlMessage{Type: ControlReady}, `{"type":"ready"}`},
		{ControlMessage{Type: ControlFatal, Message: "boom"}, `{"type":"fatal","message":"boom"}`},
		{ControlMessage{Type: ControlFatal, Message: "boom", Reason: "locked"}, `{"type":"fatal","message":"boom","reason":"locked"}`},
		{ControlMessage{Type: ControlShutdown}, `{"type":"shutdown"}`},
		{ControlMessage{Type: ControlStatus, Viewers: new(2.0)}, `{"type":"status","viewers":2}`},
		{ControlMessage{Type: ControlStatus, ActiveCalls: new(1.0), Viewers: new(0.0), DurableTasks: new(3.0)}, `{"type":"status","activeCalls":1,"viewers":0,"durableTasks":3}`},
		{ControlMessage{Type: ControlStatus, ActiveCalls: new(0.5)}, `{"type":"status","activeCalls":0.5}`},
	}
	for _, tc := range cases {
		data, err := json.Marshal(tc.message)
		if err != nil || string(data) != tc.want {
			t.Fatalf("marshal %+v = %s, %v; want %s", tc.message, data, err, tc.want)
		}
		var back ControlMessage
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatal(err)
		}
		again, _ := json.Marshal(back)
		if string(again) != tc.want {
			t.Fatalf("round trip = %s, want %s", again, tc.want)
		}
	}
}

// manager.ts:381-416 childControl over a Node IPC channel: init resolves from the first init message, shutdown starts
// the handlers, and ready/fatal/report send control messages (a ChordError's reason included).
func TestChildControlOverNodeChannel(t *testing.T) {
	parentConn, childConn := net.Pipe()
	parent := newNodeChannel(parentConn)
	child := newNodeChannel(childConn)
	received := make(chan any, 8)
	listener := IpcListener(func(message any) { received <- message })
	parent.On("message", &listener)
	control := ChildControl(child)
	shutdown := make(chan struct{}, 2)
	control.OnShutdown(func() { shutdown <- struct{}{} })
	control.OnShutdown(func() { shutdown <- struct{}{} })

	parent.Send(map[string]any{"other": true}, nil)
	parent.Send(map[string]any{"chord2control": ControlMessage{Type: ControlInit, Value: map[string]any{"greeting": "hi"}}}, nil)
	parent.Send(map[string]any{"chord2control": ControlMessage{Type: ControlInit, Value: "second"}}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	init, err := control.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := json.Marshal(init); string(text) != `{"greeting":"hi"}` {
		t.Fatalf("init = %s", text)
	}
	parent.Send(map[string]any{"chord2control": ControlMessage{Type: ControlShutdown}}, nil)
	<-shutdown
	<-shutdown
	// The second init arrived before the shutdown; the first one still holds.
	if again, _ := control.Init(ctx); again == "second" {
		t.Fatal("a later init replaced the first")
	}

	// Node keeps its internal messages (cmd NODE_*) off the "message" event.
	child.Send(map[string]any{"cmd": "NODE_HANDLE_ACK"}, nil)
	control.Ready()
	control.Fatal(NewChordError(CodeUnavailable, "held", ChordErrorOptions{Reason: "locked"}))
	control.Fatal(errors.New("plain"))
	control.Report(ChildStatus{ActiveCalls: new(2.0)})
	want := []string{
		`{"chord2control":{"type":"ready"}}`,
		`{"chord2control":{"type":"fatal","message":"unavailable/locked: held","reason":"locked"}}`,
		`{"chord2control":{"type":"fatal","message":"plain"}}`,
		`{"chord2control":{"type":"status","activeCalls":2}}`,
	}
	for _, expected := range want {
		message := <-received
		if _, ok := message.(*delta.JsonObject); !ok {
			t.Fatalf("message %T is not a JSON object", message)
		}
		if text, _ := json.Marshal(message); string(text) != expected {
			t.Fatalf("sent %s, want %s", text, expected)
		}
	}
	_ = parentConn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for child.Connected() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if child.Connected() || child.Send(map[string]any{"x": 1}, nil) {
		t.Fatal("a closed channel still sends")
	}
}

// manager.test.ts (Pi cd6b2a55) processLock "admits one holder when the temporary directory is too deep for a socket
// path": a socket path over 104 bytes failed with EINVAL on macOS when TMPDIR was deep.
func TestProcessLockAdmitsOneHolderWhenTheTemporaryDirectoryIsTooDeepForASocketPath(t *testing.T) {
	deep := filepath.Join(managerScratch(t, "deep"), strings.Repeat("a", 60), strings.Repeat("b", 60))
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", deep)
	name := fmt.Sprintf("deep-%d", os.Getpid())
	release, err := ProcessLock(name)
	if err != nil {
		t.Fatal(err)
	}
	var chordError *ChordError
	if _, err := ProcessLock(name); !errors.As(err, &chordError) || chordError.Reason != "locked" {
		t.Fatalf("second lock err = %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := ProcessLock(name)
	if err != nil {
		t.Fatalf("relock after release: %v", err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
}

// manager.ts:93-138 processLock: one holder per name; a second fails with unavailable/locked; release frees it; a
// leftover socket file nobody accepts on is taken over (platforms with socket files).
func TestProcessLock(t *testing.T) {
	name := "pig-test-" + t.Name() + "-" + time.Now().Format("150405.000000000")
	release, err := ProcessLock(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessLock(name); !IsChordError(err, CodeUnavailable) || !strings.Contains(err.Error(), "lock "+name+" is held by another process") {
		t.Fatalf("second lock err = %v", err)
	}
	var chordError *ChordError
	if _, err := ProcessLock(name); !errors.As(err, &chordError) || chordError.Reason != "locked" {
		t.Fatalf("reason = %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := ProcessLock(name)
	if err != nil {
		t.Fatalf("relock after release: %v", err)
	}
	_ = again()

	stale := name + "-stale"
	address, file, err := lockAddress(lockKey(stale))
	if err != nil {
		t.Fatal(err)
	}
	if !file {
		return
	}
	// A holder that died without removing its socket file: the socket exists and nobody accepts on it.
	dead, err := net.ListenUnix("unix", &net.UnixAddr{Name: address, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	dead.SetUnlinkOnClose(false)
	_ = dead.Close()
	if _, err := os.Stat(address); err != nil {
		t.Fatalf("stale socket missing: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(address) })
	if filepath.Dir(address) != filepath.Clean(os.TempDir()) {
		t.Fatalf("address %s outside tmpdir", address)
	}
	takeover, err := ProcessLock(stale)
	if err != nil {
		t.Fatalf("stale takeover: %v", err)
	}
	_ = takeover()
}
