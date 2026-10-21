package host

// Ports packages/chord/src/manager.ts, the host manager that packages/chord/src/node.ts exports (Node only in Pi): it
// runs a child host process and keeps at most one instance of it alive. node.ts also exports nodeLoader
// (packages/chord/src/loader.ts); its Go form is loader.go.
//
// Promise mapping: start/stop/replace block until their serialized step settles; Instance.exited is a channel closed
// at the exit (Exit then holds the result); keepAlive's interval and the exit watchers are owned goroutines that end
// with the child or the next stop.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// ControlMessageType is the discriminator of a ControlMessage: manager.ts ControlMessage's type literals.
type ControlMessageType string

// Control message types: manager.ts ControlMessage's type literals.
const (
	ControlInit     ControlMessageType = "init"
	ControlReady    ControlMessageType = "ready"
	ControlFatal    ControlMessageType = "fatal"
	ControlShutdown ControlMessageType = "shutdown"
	ControlStatus   ControlMessageType = "status"
)

// ControlMessage is manager.ts ControlMessage. Control messages share the IPC channel with chord frames:
// { chord2control: message }. Type selects the members a message carries: init carries Value, fatal carries Message
// and an optional Reason (empty = absent), status carries the optional counts (nil = absent), JS numbers as sent.
type ControlMessage struct {
	Type         ControlMessageType
	Value        any
	Message      string
	Reason       string
	ActiveCalls  *float64
	Viewers      *float64
	DurableTasks *float64
}

// MarshalJSON writes the members of the message's type, in manager.ts order.
func (message ControlMessage) MarshalJSON() ([]byte, error) {
	out := delta.JsonObjectOf("type", message.Type)
	switch message.Type {
	case ControlInit:
		out.Set("value", message.Value)
	case ControlFatal:
		out.Set("message", message.Message)
		if message.Reason != "" {
			out.Set("reason", message.Reason)
		}
	case ControlStatus:
		for _, member := range []struct {
			name  string
			value *float64
		}{{"activeCalls", message.ActiveCalls}, {"viewers", message.Viewers}, {"durableTasks", message.DurableTasks}} {
			if member.value != nil {
				out.Set(member.name, *member.value)
			}
		}
	}
	return json.Marshal(out) //perf:ok control messages are the manager's lifecycle and status messages (init, ready, fatal, shutdown, status), a handful per host process
}

// UnmarshalJSON reads a control message as manager.ts reads one: by its type, ignoring other members.
func (message *ControlMessage) UnmarshalJSON(data []byte) error {
	value, err := delta.DecodeJson(data)
	if err != nil {
		return err
	}
	decoded, ok := controlMessageOf(value)
	if !ok {
		return fmt.Errorf("control message is not an object")
	}
	*message = decoded
	return nil
}

// controlMessageOf reads a decoded control message.
func controlMessageOf(value any) (ControlMessage, bool) {
	object, ok := value.(*delta.JsonObject)
	if !ok || object == nil {
		return ControlMessage{}, false
	}
	message := ControlMessage{}
	typ, _ := object.Value("type").(string)
	message.Type = ControlMessageType(typ)
	message.Value = object.Value("value")
	message.Message, _ = object.Value("message").(string)
	message.Reason, _ = object.Value("reason").(string)
	count := func(name string) *float64 {
		number, ok := object.Value(name).(float64)
		if !ok {
			return nil
		}
		return &number
	}
	message.ActiveCalls, message.Viewers, message.DurableTasks = count("activeCalls"), count("viewers"), count("durableTasks")
	return message, true
}

// Liveness is manager.ts Liveness.
type Liveness struct {
	// Booting is true when the child started but is not ready yet.
	Booting     bool
	ActiveCalls float64
	// Viewers the child reports (it counts heartbeating subscribers).
	Viewers      float64
	DurableTasks float64
	// IdleFor is the time since the child last reported work (calls, viewers or tasks), or since it became ready.
	IdleFor time.Duration
}

// Exit is manager.ts Exit: the exit code, or the signal that ended the child (nil = null).
type Exit struct {
	Code   *int
	Signal *string
}

// Instance is manager.ts Instance: a running child, its link (for host.accept) and its exit.
type Instance interface {
	PID() int
	Link() *IpcLink
	// Exited is closed once the child exited; Exit then returns the exit.
	Exited() <-chan struct{}
	Exit() Exit
	Send(message ControlMessage)
	// KillTree kills the child and everything it spawned.
	KillTree()
	OnControl(listener func(message ControlMessage))
}

// Driver is manager.ts Driver.
type Driver interface {
	Spawn() (Instance, error)
}

// ForkDriverOptions is manager.ts ForkDriverOptions.
type ForkDriverOptions struct {
	// Entry is the child's entry module.
	Entry string
	Args  []string
	Env   map[string]string
	// ExecArgv are Node flags for the child, e.g. ["--import", sourceResolver] (default: none).
	ExecArgv []string
	// Init is sent to the child as its first control message (ChildControl's Init) when HasInit is set.
	Init    any
	HasInit bool
	// Lock is a lock name (nil = none): one live process per name, across managers and processes. The child acquires
	// it as a kernel lock (ChildControl's Lock, before opening its storage); a second child fails to start with
	// unavailable/locked.
	Lock *string
	// OnOutput is an optional stdout/stderr tap. Streams are still mirrored to the parent's standard output.
	OnOutput func(stream string, chunk []byte)
	// OnMessage is an optional application metadata tap; control messages and chord frames keep their normal handling.
	OnMessage func(message any)
}

const lockEnv = "CHORD2_LOCK"

// HostManagerOptions is manager.ts HostManagerOptions. A nil duration takes manager.ts's default.
type HostManagerOptions struct {
	Driver Driver
	// KeepAlive is evaluated every CheckInterval (default 1s) while running; false stops the child.
	KeepAlive     func(liveness Liveness) bool
	CheckInterval *time.Duration
	// Grace is how long a child may take to shut down before its process tree is killed (default 5s).
	Grace *time.Duration
	// ReadyTimeout is how long to wait for ready (default 30s).
	ReadyTimeout *time.Duration
	// OnExit is called when the child exited on its own (crash, fatal) or after a stop.
	OnExit func(exit Exit)
}

// Host manager statuses: manager.ts HostManager.status.
const (
	HostManagerStopped  = "stopped"
	HostManagerStarting = "starting"
	HostManagerRunning  = "running"
	HostManagerStopping = "stopping"
)

// HostManager is manager.ts HostManager, made by CreateHostManager.
type HostManager struct {
	options HostManagerOptions

	mu        sync.Mutex
	status    string
	instance  Instance
	handled   map[Instance]chan struct{}
	queue     chan struct{}
	report    [3]float64
	idleSince *time.Time
	booting   bool
	timer     chan struct{}
	// reporting is closed when the exit report in progress (onExit running after the manager read stopped) ends;
	// reporter is the goroutine that runs it.
	reporting chan struct{}
	reporter  uint64
}

func durationOr(value *time.Duration, fallback time.Duration) time.Duration {
	if value == nil {
		return fallback
	}
	return *value
}

// CreateHostManager is manager.ts createHostManager.
func CreateHostManager(options HostManagerOptions) *HostManager {
	queue := make(chan struct{})
	close(queue)
	return &HostManager{options: options, status: HostManagerStopped, queue: queue, handled: map[Instance]chan struct{}{}}
}

// Status is the manager's status: HostManagerStopped, HostManagerStarting, HostManagerRunning or HostManagerStopping.
func (manager *HostManager) Status() string {
	manager.lockSettled()
	defer manager.mu.Unlock()
	return manager.status
}

// lockSettled locks manager.mu once no exit report is running on another goroutine: while onExit runs, the manager reads
// as upstream's does inside the callback (stopped), to the goroutine that runs it, and to nobody else. The report's own
// goroutine, and anything onExit calls on it, passes: a callback that restarts the child is upstream's usual one.
func (manager *HostManager) lockSettled() {
	manager.mu.Lock()
	for manager.reporting != nil && manager.reporter != goroutineid.Current() {
		reported := manager.reporting
		manager.mu.Unlock()
		<-reported
		manager.mu.Lock()
	}
}

// Instance is the running child (nil = undefined).
func (manager *HostManager) Instance() Instance {
	manager.lockSettled()
	defer manager.mu.Unlock()
	return manager.instance
}

// Liveness is the child's last reported liveness.
func (manager *HostManager) Liveness() Liveness {
	manager.lockSettled()
	defer manager.mu.Unlock()
	return manager.livenessLocked()
}

func (manager *HostManager) livenessLocked() Liveness {
	liveness := Liveness{
		Booting:      manager.booting,
		ActiveCalls:  manager.report[0],
		Viewers:      manager.report[1],
		DurableTasks: manager.report[2],
	}
	if manager.idleSince != nil {
		liveness.IdleFor = time.Since(*manager.idleSince)
	}
	return liveness
}

// Start starts the child (or returns the running one) once it reported ready. Serialized with Stop and Replace.
func (manager *HostManager) Start() (Instance, error) {
	return manager.serialized(manager.doStart)
}

// Stop asks the child to shut down, then kills its process tree after the grace period.
func (manager *HostManager) Stop() error {
	_, err := manager.serialized(func() (Instance, error) { return nil, manager.doStop() })
	return err
}

// Replace stops, waits for the exit, and starts.
func (manager *HostManager) Replace() (Instance, error) {
	return manager.serialized(func() (Instance, error) {
		if err := manager.doStop(); err != nil {
			return nil, err
		}
		return manager.doStart()
	})
}

// serialized runs step after every step queued before it, whether those failed or not (queue.then(step, step)).
func (manager *HostManager) serialized(step func() (Instance, error)) (Instance, error) {
	done := make(chan struct{})
	manager.mu.Lock()
	previous := manager.queue
	manager.queue = done
	manager.mu.Unlock()
	<-previous
	defer close(done)
	return step()
}

func (manager *HostManager) stopTimerLocked() {
	if manager.timer != nil {
		//portlint:allow doubleclose every caller holds manager.mu (the Locked suffix), and the field is cleared with the close
		close(manager.timer)
		manager.timer = nil
	}
}

func (manager *HostManager) doStop() error {
	manager.lockSettled()
	current := manager.instance
	if current == nil {
		manager.status = HostManagerStopped
		manager.booting = false
		manager.mu.Unlock()
		return nil
	}
	manager.status = HostManagerStopping
	manager.stopTimerLocked()
	handled := manager.handled[current]
	manager.mu.Unlock()
	current.Send(ControlMessage{Type: ControlShutdown})
	grace := time.NewTimer(nodeDelay(durationOr(manager.options.Grace, 5*time.Second)))
	graceful := false
	select {
	case <-current.Exited():
		graceful = true
	case <-grace.C:
	}
	grace.Stop()
	if !graceful {
		current.KillTree()
	}
	<-current.Exited()
	// manager.ts's exited.then handler, registered at start, runs before this await resumes.
	<-handled
	manager.mu.Lock()
	if manager.instance == current {
		manager.instance = nil
	}
	manager.status = HostManagerStopped
	manager.mu.Unlock()
	return nil
}

func (manager *HostManager) doStart() (Instance, error) {
	manager.lockSettled()
	if manager.instance != nil && manager.status == HostManagerRunning {
		instance := manager.instance
		manager.mu.Unlock()
		return instance, nil
	}
	manager.status = HostManagerStarting
	manager.booting = true
	manager.report = [3]float64{}
	manager.mu.Unlock()
	current, err := manager.options.Driver.Spawn()
	if err != nil {
		manager.mu.Lock()
		manager.status = HostManagerStopped
		manager.booting = false
		manager.mu.Unlock()
		return nil, err
	}
	handled := make(chan struct{})
	manager.mu.Lock()
	manager.instance = current
	manager.handled[current] = handled
	manager.mu.Unlock()
	go func() {
		<-current.Exited()
		manager.mu.Lock()
		var reported chan struct{}
		if manager.instance == current {
			manager.instance = nil
			manager.status = HostManagerStopped
			manager.stopTimerLocked()
			if manager.options.OnExit != nil {
				// manager.ts sets the status and calls onExit in one callback, so nothing else sees the manager stopped
				// before onExit has run: other goroutines wait for it (lockSettled), onExit itself sees the manager stopped.
				reported = make(chan struct{})
				manager.reporting, manager.reporter = reported, goroutineid.Current()
			}
		}
		delete(manager.handled, current)
		manager.mu.Unlock()
		if manager.options.OnExit != nil {
			func() {
				defer func() {
					if reported != nil {
						manager.mu.Lock()
						manager.reporting = nil
						manager.mu.Unlock()
						close(reported)
					}
				}()
				manager.options.OnExit(current.Exit())
			}()
		}
		close(handled)
	}()

	settled := make(chan error, 1)
	settle := func(err error) {
		select {
		case settled <- err:
		default:
		}
	}
	current.OnControl(func(message ControlMessage) {
		switch message.Type {
		case ControlReady:
			settle(nil)
		case ControlFatal:
			settle(NewChordError(CodeUnavailable, "child failed: "+message.Message, ChordErrorOptions{Reason: message.Reason}))
		case ControlStatus:
			manager.mu.Lock()
			manager.report = [3]float64{valueOr(message.ActiveCalls), valueOr(message.Viewers), valueOr(message.DurableTasks)}
			// The idle clock starts at the busy → idle transition, not at the last busy report.
			if manager.report[0]+manager.report[1]+manager.report[2] > 0 {
				manager.idleSince = nil
			} else if manager.idleSince == nil {
				manager.idleSince = new(time.Now())
			}
			manager.mu.Unlock()
		}
	})
	timeout := time.NewTimer(nodeDelay(durationOr(manager.options.ReadyTimeout, 30*time.Second)))
	select {
	case err = <-settled:
	case <-current.Exited():
		// A ready or fatal that arrived before the exit settled first.
		select {
		case err = <-settled:
		default:
			err = NewChordError(CodeUnavailable, "child exited before ready ("+exitLabel(current.Exit())+")", ChordErrorOptions{})
		}
	case <-timeout.C:
		err = NewChordError(CodeUnavailable, "child not ready in time", ChordErrorOptions{})
	}
	timeout.Stop()
	if err != nil {
		current.KillTree()
		<-current.Exited()
		<-handled
		return nil, err
	}
	manager.mu.Lock()
	manager.booting = false
	if manager.report[0]+manager.report[1]+manager.report[2] == 0 && manager.idleSince == nil {
		manager.idleSince = new(time.Now())
	}
	manager.status = HostManagerRunning
	if manager.options.KeepAlive != nil {
		stop := make(chan struct{})
		manager.timer = stop
		//portlint:allow golifetime keepAliveLoop is manager.ts:339's setInterval; it returns when stopTimerLocked closes stop
		go manager.keepAliveLoop(stop, durationOr(manager.options.CheckInterval, time.Second))
	}
	manager.mu.Unlock()
	return current, nil
}

// keepAliveLoop is manager.ts's setInterval: a false keepAlive queues a stop (not awaited).
func (manager *HostManager) keepAliveLoop(stop chan struct{}, interval time.Duration) {
	ticker := time.NewTicker(nodeDelay(interval))
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		manager.mu.Lock()
		running := manager.status == HostManagerRunning && manager.timer == stop
		liveness := manager.livenessLocked()
		manager.mu.Unlock()
		if running && !manager.options.KeepAlive(liveness) {
			//portlint:allow golifetime manager.ts:340 `void serialized(doStop)`: the stop is queued and not awaited; Stop returns once the child exited
			go func() { _ = manager.Stop() }() //portlint:allow gorecover one stop per keepAlive tick, not a fan-out over input; Stop's own failures are returned, as the voided promise's are dropped
		}
	}
}

func valueOr(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

// exitLabel is manager.ts's `${exit.code ?? exit.signal}`.
func exitLabel(exit Exit) string {
	switch {
	case exit.Code != nil:
		return strconv.Itoa(*exit.Code)
	case exit.Signal != nil:
		return *exit.Signal
	}
	return "null"
}

// ignoreSendError drops a control message to a peer process that is gone: its exit or disconnect is reported
// separately.
func ignoreSendError(error) {}

// ChildControlChannel is the object manager.ts childControl returns: the child side of the control channel.
type ChildControlChannel struct {
	channel  IpcChannel
	init     chan struct{}
	initOnce sync.Once
	value    any
	mu       sync.Mutex
	shutdown []*func()
}

// ChildControl is manager.ts childControl(channel) (call in the child's entry). Pi's default channel is the process's
// own IPC channel: ProcessChannel.
func ChildControl(channel IpcChannel) *ChildControlChannel {
	control := &ChildControlChannel{channel: channel, init: make(chan struct{})}
	listener := IpcListener(func(message any) {
		object, ok := message.(*delta.JsonObject)
		if !ok || object == nil || !object.Has("chord2control") {
			return
		}
		decoded, ok := controlMessageOf(object.Value("chord2control"))
		if !ok {
			return
		}
		switch decoded.Type {
		case ControlInit:
			control.initOnce.Do(func() {
				control.value = decoded.Value
				close(control.init)
			})
		case ControlShutdown:
			control.mu.Lock()
			handlers := append([]*func(){}, control.shutdown...)
			control.mu.Unlock()
			// Unawaited, as manager.ts's `void handler()`.
			for _, handler := range handlers {
				//portlint:allow golifetime manager.ts:391 `void handler()`: shutdown handlers run unawaited and own their lifetime
				go (*handler)()
			}
		}
	})
	channel.On("message", &listener)
	return control
}

func (control *ChildControlChannel) send(message ControlMessage) {
	// A parent that is gone fails the write (EPIPE); the disconnect that follows is how a child notices.
	if control.channel.Connected() {
		control.channel.Send(map[string]any{"chord2control": message}, ignoreSendError)
	}
}

// Init waits for the manager's init value (manager.ts childControl().init); ctx ends the wait.
func (control *ChildControlChannel) Init(ctx context.Context) (any, error) {
	select {
	case <-control.init:
		return control.value, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Lock acquires the manager's lock (if one was configured) as a kernel lock held for this process's lifetime. Call it
// before opening storage. It fails with unavailable/locked, also reported to the manager as a fatal start error.
func (control *ChildControlChannel) Lock() error {
	name, ok := os.LookupEnv(lockEnv)
	if !ok {
		return nil
	}
	if _, err := ProcessLock(name); err != nil {
		control.Fatal(err)
		return err
	}
	return nil
}

// Ready reports the child ready.
func (control *ChildControlChannel) Ready() { control.send(ControlMessage{Type: ControlReady}) }

// Fatal reports a failed start: the error's message, and a ChordError's reason.
func (control *ChildControlChannel) Fatal(err error) {
	message := ControlMessage{Type: ControlFatal, Message: err.Error()}
	if chordError, ok := err.(*ChordError); ok { //nolint:errorlint // Pi's instanceof does not unwrap (manager.ts:397)
		message.Reason = chordError.Reason
	}
	control.send(message)
}

// ChildStatus is the status report of manager.ts childControl().report (nil = absent).
type ChildStatus struct {
	ActiveCalls  *float64
	Viewers      *float64
	DurableTasks *float64
}

// Report sends a status report.
func (control *ChildControlChannel) Report(status ChildStatus) {
	control.send(ControlMessage{Type: ControlStatus, ActiveCalls: status.ActiveCalls, Viewers: status.Viewers, DurableTasks: status.DurableTasks})
}

// OnShutdown adds a handler the manager's shutdown request starts.
func (control *ChildControlChannel) OnShutdown(handler func()) {
	control.mu.Lock()
	defer control.mu.Unlock()
	control.shutdown = append(control.shutdown, &handler)
}
