package host

// Node's IPC channel (child_process.fork with serialization "json"), the channel manager.ts's child and parent speak:
// one JSON text and a newline per message. Node keeps its own internal messages (a cmd starting with NODE_) off the
// "message" event; so does this channel.

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"syscall"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

type nodeChannel struct {
	conn      io.ReadWriteCloser
	mu        sync.Mutex
	write     sync.Mutex
	connected bool
	listeners map[string][]*IpcListener
	// pending holds the messages that arrived before the first "message" listener (and, while that listener catches up, the
	// ones behind them). Node starts a channel's messages in the tick that creates it, so its owner always registers
	// before one is emitted; here the reader is its own goroutine, and a message that beat the registration (the parent's
	// init, read from the pipe the moment the child opens it) would be lost and its owner would wait for it for good.
	pending   []any
	replaying bool
}

func newNodeChannel(conn io.ReadWriteCloser) *nodeChannel {
	channel := &nodeChannel{conn: conn, connected: true, listeners: map[string][]*IpcListener{}}
	//portlint:allow golifetime the reader is the channel's: it returns when conn reaches EOF or is closed, and then disconnects
	go channel.read()
	return channel
}

func (channel *nodeChannel) read() {
	reader := bufio.NewReader(channel.conn)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && err == nil {
			if message, decodeErr := delta.DecodeJson(line); decodeErr == nil && !internalNodeMessage(message) {
				channel.emit("message", message)
			}
		}
		if err != nil {
			channel.disconnect()
			return
		}
	}
}

func internalNodeMessage(message any) bool {
	object, ok := message.(*delta.JsonObject)
	if !ok || object == nil {
		return false
	}
	cmd, ok := object.Value("cmd").(string)
	return ok && strings.HasPrefix(cmd, "NODE_")
}

func (channel *nodeChannel) emit(event string, message any) {
	channel.mu.Lock()
	if event == "message" && (len(channel.listeners[event]) == 0 || channel.replaying) {
		channel.pending = append(channel.pending, message)
		channel.mu.Unlock()
		return
	}
	listeners := slices.Clone(channel.listeners[event])
	channel.mu.Unlock()
	for _, listener := range listeners {
		(*listener)(message)
	}
}

func (channel *nodeChannel) disconnect() {
	channel.mu.Lock()
	was := channel.connected
	channel.connected = false
	channel.mu.Unlock()
	if was {
		_ = channel.conn.Close()
		channel.emit("disconnect", nil)
	}
}

// Connected is Node's channel.connected.
func (channel *nodeChannel) Connected() bool {
	channel.mu.Lock()
	defer channel.mu.Unlock()
	return channel.connected
}

// Send writes message as one JSON line; it reports false when the channel is closed or the message is not JSON. A
// failed write (the peer exited: EPIPE) goes to callback when there is one, as Node's send(message, callback) reports
// it, and disconnects the channel.
func (channel *nodeChannel) Send(message any, callback func(err error)) bool {
	if !channel.Connected() {
		return false
	}
	data, err := json.Marshal(message)
	if err != nil {
		return false
	}
	channel.write.Lock()
	defer channel.write.Unlock()
	if _, err := channel.conn.Write(append(data, '\n')); err != nil {
		if callback != nil {
			//portlint:allow golifetime Node reports a failed write to the send callback asynchronously; the callback returns once the link ended
			go callback(ipcSendFailure{err})
		}
		//portlint:allow golifetime a failed write disconnects the channel, as Node emits "disconnect" asynchronously; disconnect closes conn and returns
		go channel.disconnect()
		return false
	}
	return true
}

// ipcSendFailure is a failed IPC write; like Node's, it has the code EPIPE for a write to a pipe whose peer is gone.
type ipcSendFailure struct{ error }

func (failure ipcSendFailure) Unwrap() error { return failure.error }

// Code is Node's error.code ("" when the write failed otherwise).
func (failure ipcSendFailure) Code() string {
	if errors.Is(failure.error, syscall.EPIPE) || errors.Is(failure.error, io.ErrClosedPipe) {
		return "EPIPE"
	}
	return ""
}

// On adds a "message" or "disconnect" listener.
//
// The first "message" listener is given the messages received before it, in order, ahead of any that arrive while it is
// being caught up: the replay runs here, off the lock, and the reader queues behind it. A listener added after the first
// hears only the messages that arrive after it: Node emits every message in a later tick, to every listener registered by
// then, but here the first listener is what ends the wait, so the channel's owner (ChildControl) has to register first.
func (channel *nodeChannel) On(event string, listener *IpcListener) {
	channel.mu.Lock()
	first := event == "message" && len(channel.listeners[event]) == 0
	channel.listeners[event] = append(channel.listeners[event], listener)
	if !first {
		channel.mu.Unlock()
		return
	}
	channel.replaying = true
	channel.mu.Unlock()
	for {
		channel.mu.Lock()
		if len(channel.pending) == 0 {
			channel.replaying = false
			channel.mu.Unlock()
			return
		}
		message := channel.pending[0]
		channel.pending = channel.pending[1:]
		listeners := slices.Clone(channel.listeners["message"])
		channel.mu.Unlock()
		for _, listener := range listeners {
			(*listener)(message)
		}
	}
}

// Off removes a listener.
func (channel *nodeChannel) Off(event string, listener *IpcListener) {
	channel.mu.Lock()
	defer channel.mu.Unlock()
	channel.listeners[event] = slices.DeleteFunc(channel.listeners[event], func(entry *IpcListener) bool { return entry == listener })
}
