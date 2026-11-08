package mcp

import (
	"encoding/json"
	"sync"

	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/mcp/environments.ts: the supervisor's pi-env connections to environments: one per environment and
// configuration revision, shared by every instance running there and closed with the last one. A connection lost meanwhile
// starts again at its next request (pi-env); the daemon killed the programs it ran, and their instances reconnect.

// ConnectEnv is a connection to environment name configured as config, which starts at its first request.
type ConnectEnv func(name string, config *chordjson.Object) *env.Connection

// EnvLease is one instance's use of an environment's connection; released once, when the instance closes.
type EnvLease struct {
	Connection *env.Connection
	release    func()
}

// Release lets go of the connection (the last release closes it).
func (l *EnvLease) Release() { l.release() }

// envEntry is an open connection and how many instances use it.
type envEntry struct {
	connection *env.Connection
	users      int
}

// EnvConnections are the open connections by environment and revision, with how many instances use each.
type EnvConnections struct {
	connect ConnectEnv
	mu      sync.Mutex
	open    map[string]*envEntry
}

// NewEnvConnections is a set of connections opened through connect.
func NewEnvConnections(connect ConnectEnv) *EnvConnections {
	return &EnvConnections{connect: connect, open: map[string]*envEntry{}}
}

// Acquire is the connection to name as configured now (revision: its connection revision; opened for its first user); release
// it once done.
func (e *EnvConnections) Acquire(name string, config *chordjson.Object, revision string) *EnvLease {
	key, _ := json.Marshal([]string{name, revision})
	e.mu.Lock()
	entry := e.open[string(key)]
	if entry == nil {
		entry = &envEntry{connection: e.connect(name, config)}
		e.open[string(key)] = entry
	}
	entry.users++
	e.mu.Unlock()
	var once sync.Once
	return &EnvLease{Connection: entry.connection, release: func() {
		once.Do(func() {
			e.mu.Lock()
			entry.users--
			last := entry.users <= 0
			if last {
				delete(e.open, string(key))
			}
			e.mu.Unlock()
			if last {
				entry.connection.Close()
			}
		})
	}}
}
