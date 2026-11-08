package hostenv

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Local is a connection to a daemon on this machine, which starts at its first request. onLog receives the daemon's
// diagnostics, each prefixed `pi-env: `.
func Local(ctx context.Context, onLog func(string)) (*env.Connection, error) {
	platform, arch := LocalSystem()
	binary, err := TerminalDaemon(ctx, platform, arch, true)
	if err != nil {
		return nil, err
	}
	return env.NewConnection(env.ConnectionOptions{Command: []string{binary}, OnLog: prefixed("pi-env: ", onLog)}), nil
}

func prefixed(prefix string, onLog func(string)) func(string) {
	if onLog == nil {
		return nil
	}
	return func(text string) { onLog(prefix + text) }
}

// Remote is what the terminals host knows of environment name of an envs.json before connecting: the revision a connection to
// it is opened for (see ConnectionRevision: a caller keeps a connection while the revision stays), the environment variables
// its terminals start with (an SSH env's shellEnv, and SHELL from its shellPath), and how to connect.
type Remote struct {
	Revision string
	ShellEnv map[string]string
	// Connect opens a connection that can open terminals; a sandbox runs its host's daemon in its VM. onLog receives the
	// daemon's diagnostics, each prefixed `pi-env <name>: `. The connection is closed when it cannot open terminals.
	Connect func(ctx context.Context, onLog func(string)) (*env.Connection, error)
}

// Resolve reads directory's envs.json for environment name.
func Resolve(ctx context.Context, directory, name string) (*Remote, error) {
	configs, err := envs.LoadEnvs(ctx, directory)
	if err != nil {
		return nil, err
	}
	config, _ := configs.Value(name).(*chordjson.Object)
	if config == nil {
		return nil, fmt.Errorf("Unknown environment %s", name)
	}
	remote := &Remote{Revision: envs.ConnectionRevision(configs, name), ShellEnv: map[string]string{}}
	isSSH := config.Value("type") == "ssh"
	if isSSH {
		if extra, ok := config.Value("shellEnv").(*chordjson.Object); ok {
			for key, value := range extra.All() {
				if text, ok := value.(string); ok {
					remote.ShellEnv[key] = text
				}
			}
		}
		if shell, _ := config.Value("shellPath").(string); shell != "" {
			remote.ShellEnv["SHELL"] = shell
		}
	}
	remote.Connect = func(ctx context.Context, onLog func(string)) (*env.Connection, error) {
		var target env.SshConnectOptions
		if isSSH {
			target = envs.SSHOptions(directory, name, config)
		} else {
			hostName, _ := config.Value("host").(string)
			var err error
			if target, err = envs.SandboxHost(ctx, directory, name, hostName); err != nil {
				return nil, err
			}
		}
		platform, err := env.DetectPlatform(ctx, target.SshTarget)
		if err != nil {
			return nil, err
		}
		binary, err := TerminalDaemon(ctx, platform.Platform, platform.Arch, false)
		if err != nil {
			return nil, err
		}
		conn := envs.EnvConnection(directory, name, config, envs.ConnectOptions{Binary: binary, OnLog: prefixed("pi-env "+name+": ", onLog)}).Connection
		if err := RequireTerminalSupport(ctx, conn); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}
	return remote, nil
}
