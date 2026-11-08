package envs

import (
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports the RemoteSession half of apps/p2/plugins/envs/remote-env.ts (the connection half is EnvConnection).

// RemoteSession is a remote environment at a directory that connects at its first operation, and its connection.
type RemoteSession struct {
	// Env is the environment at the directory RemoteSessionFor was given.
	Env        *env.RemoteExecutionEnv
	Connection *env.Connection
	// Remote is the detected remote system, nil until the connection has started.
	Remote func() *env.RemotePlatform
	// At is another environment on the same connection, at another directory (a subagent's own cwd).
	At func(cwd string) *env.RemoteExecutionEnv
}

// RemoteSessionFor is a remote environment at cwd that connects at its first operation: building it never needs the
// network, and a failed start (offline, untrusted host key) is the error of the operation that needed it.
func RemoteSessionFor(directory, name string, config *chordjson.Object, cwd string, options ConnectOptions) RemoteSession {
	options.Binary = ""
	connected := EnvConnection(directory, name, config, options)
	var shellPath string
	var shellEnv map[string]string
	if config.Value("type") == "ssh" {
		shellPath, _ = config.Value("shellPath").(string)
		if extra, ok := config.Value("shellEnv").(*chordjson.Object); ok {
			shellEnv = map[string]string{}
			for key, value := range extra.All() {
				if text, ok := value.(string); ok {
					shellEnv[key] = text
				}
			}
		}
	}
	at := func(directoryAt string) *env.RemoteExecutionEnv {
		return env.NewRemoteExecutionEnv(env.RemoteExecutionEnvOptions{
			Connection: connected.Connection, ID: EnvID(name, config), Cwd: directoryAt, ShellPath: shellPath, ShellEnv: shellEnv,
		})
	}
	return RemoteSession{Env: at(cwd), Connection: connected.Connection, Remote: connected.Remote, At: at}
}
