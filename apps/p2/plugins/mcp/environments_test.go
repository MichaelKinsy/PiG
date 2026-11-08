package mcp

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports test/p2/mcp-environments.test.ts: the supervisor's pi-env connections: one per environment and configuration
// revision, shared by its users, closed with the last one; an edited environment is another connection. Connections here
// never start (a command that does not exist): a closed one refuses at once with "Connection closed", an open one tries to
// start and fails to spawn.

var (
	gpuConfig    = chordjson.ObjectOf("type", "ssh", "host", "gpu.example")
	editedConfig = chordjson.ObjectOf("type", "ssh", "host", "gpu.example", "port", float64(2222))
)

// isClosed is whether connection was closed (it refuses at once) rather than open (it tries to start the daemon).
func isClosed(connection *env.Connection) bool {
	_, err := connection.Info(context.Background())
	return err != nil && err.Error() == "Connection closed"
}

func unstarted() *env.Connection {
	return env.NewConnection(env.ConnectionOptions{Command: []string{"/nonexistent/pi-env"}})
}

func TestUsersOfOneEnvironmentShareItsConnectionTheLastReleaseClosesIt(t *testing.T) {
	var opened []string
	envs := NewEnvConnections(func(name string, _ *chordjson.Object) *env.Connection {
		opened = append(opened, name)
		return unstarted()
	})
	first := envs.Acquire("gpu", gpuConfig, "r1")
	second := envs.Acquire("gpu", gpuConfig, "r1")
	if second.Connection != first.Connection {
		t.Fatal("users of one environment must share its connection")
	}
	other := envs.Acquire("box", gpuConfig, "r1")
	if other.Connection == first.Connection {
		t.Fatal("another environment is another connection")
	}
	if want := []string{"gpu", "box"}; !reflect.DeepEqual(opened, want) {
		t.Fatalf("opened %v", opened)
	}
	first.Release()
	if isClosed(second.Connection) {
		t.Fatal("closed with a user left")
	}
	second.Release()
	if !isClosed(second.Connection) {
		t.Fatal("the last release must close it")
	}
	if isClosed(other.Connection) {
		t.Fatal("another environment's connection stays")
	}
	other.Release()
	// A new user after the last one opens a new connection.
	again := envs.Acquire("gpu", gpuConfig, "r1")
	if again.Connection == first.Connection {
		t.Fatal("a new user must get a new connection")
	}
	if want := []string{"gpu", "box", "gpu"}; !reflect.DeepEqual(opened, want) {
		t.Fatalf("opened %v", opened)
	}
	again.Release()
}

func TestAnEditedEnvironmentIsAnotherConnectionTheOldOneStaysForItsUsersUntilTheyReleaseIt(t *testing.T) {
	envs := NewEnvConnections(func(string, *chordjson.Object) *env.Connection { return unstarted() })
	before := envs.Acquire("gpu", gpuConfig, "r1")
	after := envs.Acquire("gpu", editedConfig, "r2")
	if after.Connection == before.Connection {
		t.Fatal("an edited environment is another connection")
	}
	before.Release()
	if !isClosed(before.Connection) {
		t.Fatal("the old connection must close with its last user")
	}
	if isClosed(after.Connection) {
		t.Fatal("the new connection stays")
	}
	after.Release()
}
