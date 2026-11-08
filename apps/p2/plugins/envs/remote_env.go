package envs

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Connections to SSH and sandbox environments: the connection part of apps/p2/plugins/envs/remote-env.ts.

// KnownHostsPath holds the host keys this application trusts, separate from the user's own known_hosts.
func KnownHostsPath(directory string) string { return filepath.Join(directory, "ssh", "known_hosts") }

// HostKeyAlias is the name an env's host keys are stored under, independent of host aliases, ports and jump hosts.
func HostKeyAlias(name string) string { return "pi-env-" + name }

// EnvID is the env's FileSystem id: equal ids see the same files.
func EnvID(name string, config *chordjson.Object) string {
	return "pi-env:" + name + "@" + EnvRevision(config)
}

func optionalString(config *chordjson.Object, key string) *string {
	if value, ok := config.Value(key).(string); ok {
		return &value
	}
	return nil
}

// SSHOptions are the env.SshConnection options for SSH env name of envs.json in directory.
func SSHOptions(directory, name string, config *chordjson.Object) env.SshConnectOptions {
	host, _ := config.Value("host").(string)
	options := env.SshConnectOptions{SshTarget: env.SshTarget{
		Host:           host,
		User:           optionalString(config, "user"),
		IdentityFile:   optionalString(config, "identityFile"),
		KnownHostsFile: KnownHostsPath(directory),
		HostKeyAlias:   HostKeyAlias(name),
	}}
	if port, ok := config.Value("port").(float64); ok {
		number := int(port)
		options.Port = &number
	}
	options.LoginShell = config.Value("loginShell") == true
	return options
}

// ConnectOptions configure EnvConnection.
type ConnectOptions struct {
	// Launcher is the sandbox launcher to deploy (default: the one this package ships); sandboxes only.
	Launcher *Launcher
	// Binary is the daemon to run; "" is the one pi-env ships for the machine.
	Binary string
	OnLog  func(string)
}

// Connected is a connection and, once it started, the system it runs on (a sandbox's: Linux on its host's architecture).
type Connected struct {
	Connection *env.Connection
	// Remote is nil until the connection has started.
	Remote func() *env.RemotePlatform
}

func (options ConnectOptions) binary() *string {
	if options.Binary == "" {
		return nil
	}
	return &options.Binary
}

// EnvConnection is a pi-env connection to env name that starts at its first request: over SSH for an SSH env; for a
// sandbox, over SSH to its host, where p2's launcher starts the sandbox's VM if needed and runs the daemon in it. A
// sandbox's host configuration is read from directory's envs.json at each start.
func EnvConnection(directory, name string, config *chordjson.Object, options ConnectOptions) Connected {
	if config.Value("type") == "ssh" {
		connectOptions := SSHOptions(directory, name, config)
		connectOptions.Binary = options.binary()
		connectOptions.OnLog = options.OnLog
		connected := env.SshConnection(connectOptions)
		return Connected{Connection: connected.Connection, Remote: connected.Remote}
	}
	var (
		mu     sync.Mutex
		remote *env.RemotePlatform
	)
	hostName, _ := config.Value("host").(string)
	connection := env.NewConnection(env.ConnectionOptions{
		CommandFunc: func(ctx context.Context) ([]string, error) {
			launcher := options.Launcher
			if launcher == nil {
				var err error
				if launcher, err = DefaultLauncher(); err != nil {
					return nil, err
				}
			}
			host, err := SandboxHost(ctx, directory, name, hostName)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			detected := remote
			mu.Unlock()
			if detected == nil {
				found, err := env.DetectPlatform(ctx, host.SshTarget)
				if err != nil {
					return nil, err
				}
				detected = &found
			}
			if detected.Platform != "linux" {
				return nil, fmt.Errorf(`Sandboxes need a Linux host; "%s" runs %s`, hostName, detected.Platform)
			}
			mu.Lock()
			remote = detected
			mu.Unlock()
			// The VM is Linux on the host's architecture: the host's own daemon runs in it.
			daemon, err := env.DeployDaemon(ctx, host.SshTarget, *detected, options.binary())
			if err != nil {
				return nil, err
			}
			deployed, err := DeployLauncher(ctx, host.SshTarget, *detected, launcher)
			if err != nil {
				return nil, err
			}
			// Start the VM first, with the launcher's own allowance (a first start may install Gondolin and download the
			// guest image): the daemon connection must answer within pi-env's 30 s once it runs.
			key := SandboxKey(directory, name)
			// Secret values go on stdin, never on a command line; the argument names them.
			meta, values, err := SandboxSecrets(ctx, directory, name)
			if err != nil {
				return nil, err
			}
			argument := SandboxArgument(config, meta)
			input, err := chordjson.Marshal(values)
			if err != nil {
				return nil, err
			}
			if _, err := DeployedCommand(ctx, host.SshTarget, deployed, []string{"start", key, argument}, string(input)); err != nil {
				return nil, err
			}
			arguments, err := env.SshArguments(host.SshTarget)
			if err != nil {
				return nil, err
			}
			return append(append([]string{sshProgram(host.SshTarget)}, arguments...), RunLauncher(deployed, []string{"daemon", key, argument, daemon, "--"})), nil
		},
		OnLog: options.OnLog,
	})
	return Connected{Connection: connection, Remote: func() *env.RemotePlatform {
		mu.Lock()
		defer mu.Unlock()
		return remote
	}}
}

// SandboxHost is the SSH options of sandbox name's host, as configured now.
func SandboxHost(ctx context.Context, directory, name, host string) (env.SshConnectOptions, error) {
	configs, err := LoadEnvs(ctx, directory)
	if err != nil {
		return env.SshConnectOptions{}, err
	}
	config, _ := configs.Value(host).(*chordjson.Object)
	if config == nil || config.Value("type") != "ssh" {
		return env.SshConnectOptions{}, fmt.Errorf(`Sandbox %s runs on "%s", which is not an SSH environment`, name, host)
	}
	return SSHOptions(directory, host, config), nil
}
