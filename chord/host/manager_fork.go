package host

import (
	"os"
	"slices"
	"strings"
	"sync"
)

// The parts of manager.ts forkDriver that do not depend on the platform's way of starting Node with an IPC channel:
// the child's environment, the output taps, and the instance that wires the channel to the manager's control messages.

var (
	processChannelOnce sync.Once
	processChannel     *nodeChannel
)

type forkInstance struct {
	pid      int
	link     *IpcLink
	channel  *nodeChannel
	killTree func()
	exited   chan struct{}
	exit     Exit

	mu        sync.Mutex
	listeners []func(message ControlMessage)
}

// newForkInstance is the end of manager.ts forkDriver's spawn: it routes the channel's messages to onMessage and the
// control listeners, links the channel for host.accept and sends the init message.
func newForkInstance(options ForkDriverOptions, pid int, channel *nodeChannel, killTree func()) *forkInstance {
	instance := &forkInstance{pid: pid, channel: channel, killTree: killTree, exited: make(chan struct{})}
	listener := IpcListener(func(message any) {
		if options.OnMessage != nil {
			options.OnMessage(message)
		}
		object, ok := message.(interface {
			Has(string) bool
			Value(string) any
		})
		if !ok || !object.Has("chord2control") {
			return
		}
		control, _ := controlMessageOf(object.Value("chord2control"))
		instance.mu.Lock()
		listeners := append([]func(ControlMessage){}, instance.listeners...)
		instance.mu.Unlock()
		for _, listener := range listeners {
			listener(control)
		}
	})
	channel.On("message", &listener)
	instance.link = NewIpcLink(channel)
	if options.HasInit {
		instance.Send(ControlMessage{Type: ControlInit, Value: options.Init})
	}
	return instance
}

// end records how the leader ended and releases Exit and Exited.
func (instance *forkInstance) end(exit Exit) {
	instance.exit = exit
	close(instance.exited) //portlint:allow doubleclose end runs once, from the one goroutine that waits for the leader
}

func (instance *forkInstance) PID() int                { return instance.pid }
func (instance *forkInstance) Link() *IpcLink          { return instance.link }
func (instance *forkInstance) Exited() <-chan struct{} { return instance.exited }

func (instance *forkInstance) Exit() Exit {
	<-instance.exited
	return instance.exit
}

func (instance *forkInstance) Send(message ControlMessage) {
	// A child that exited before the channel reported it fails the write (EPIPE): its exit is what counts.
	if instance.channel.Connected() {
		instance.channel.Send(map[string]any{"chord2control": message}, ignoreSendError)
	}
}

func (instance *forkInstance) KillTree() { instance.killTree() }

func (instance *forkInstance) OnControl(listener func(message ControlMessage)) {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	instance.listeners = append(instance.listeners, listener)
}

// forkEnvironment is manager.ts forkDriver's env: this process's, without a lock it inherited (e.g. as a child host
// itself; the child gets only its own), then options.Env and the lock, then Node's channel variables for the fd the
// child finds its IPC channel on.
func forkEnvironment(options ForkDriverOptions, channelFd string) []string {
	env := slices.DeleteFunc(os.Environ(), func(entry string) bool { return strings.HasPrefix(entry, lockEnv+"=") })
	for name, value := range options.Env {
		env = append(env, name+"="+value)
	}
	if options.Lock != nil {
		env = append(env, lockEnv+"="+*options.Lock)
	}
	return append(env, "NODE_CHANNEL_FD="+channelFd, "NODE_CHANNEL_SERIALIZATION_MODE=json")
}

// forkArguments is node's argument list: execArgv, the entry, then the entry's args.
func forkArguments(options ForkDriverOptions) []string {
	return append(append(append([]string{}, options.ExecArgv...), options.Entry), options.Args...)
}

// outputTap is one of the child's stdout and stderr when options.OnOutput taps them: the child writes to write, and
// the parent reads read, hands each chunk to onOutput and mirrors it to the parent's own stream.
type outputTap struct {
	stream string
	read   *os.File
	write  *os.File
	mirror *os.File
}

// openOutputTaps creates the stdout and stderr taps (none without options.OnOutput).
func openOutputTaps(options ForkDriverOptions) ([]outputTap, error) {
	if options.OnOutput == nil {
		return nil, nil
	}
	var taps []outputTap
	for _, stream := range []struct {
		name   string
		mirror *os.File
	}{{"stdout", os.Stdout}, {"stderr", os.Stderr}} {
		read, write, err := os.Pipe()
		if err != nil {
			closeOutputTaps(taps)
			return nil, err
		}
		taps = append(taps, outputTap{stream.name, read, write, stream.mirror})
	}
	return taps, nil
}

func closeOutputTaps(taps []outputTap) {
	for _, open := range taps {
		_, _ = open.read.Close(), open.write.Close()
	}
}

// pumpOutputTaps starts the readers once the child has the write ends: the parent's copies are closed.
func pumpOutputTaps(taps []outputTap, options ForkDriverOptions) {
	for _, open := range taps {
		_ = open.write.Close()
		//portlint:allow golifetime a tap reader returns at EOF, when the child's stdout or stderr closes
		go func() { //portlint:allow gorecover two readers (stdout, stderr), not a fan-out over input; a panicking onOutput listener ends the process as a throwing Node "data" listener does
			defer func() { _ = open.read.Close() }()
			buffer := make([]byte, 64*1024)
			for {
				n, err := open.read.Read(buffer)
				if n > 0 {
					chunk := append([]byte(nil), buffer[:n]...)
					options.OnOutput(open.stream, chunk)
					_, _ = open.mirror.Write(chunk)
				}
				if err != nil {
					return
				}
			}
		}()
	}
}
