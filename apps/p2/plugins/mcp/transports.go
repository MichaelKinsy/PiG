package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	pimcp "github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports apps/p2/plugins/mcp/transports.ts: how an instance opens its MCP transport, by where it runs: a program on the p2
// host (`StdioTransport`), a program on an environment through its pi-env daemon (`PipeTransport` over `OpenProcess`, through
// `cmd.exe` on Windows), or a Streamable HTTP server (with the stored OAuth tokens when it signs in).

// Opened is a transport, not started yet, and the `file://` roots its client offers.
type Opened struct {
	Transport pimcp.Transport
	Roots     []string
}

// Transports is how one instance opens transports, holding what they need meanwhile (an environment's connection) until
// Release. ctx ends the open: an instance that closed or restarted meanwhile.
type Transports struct {
	Open    func(ctx context.Context) (Opened, error)
	Release func()
}

// DaemonTooOldError is the environment's daemon predating the `process` operation: retrying does not help, updating it does.
type DaemonTooOldError struct{ message string }

func (e *DaemonTooOldError) Error() string { return e.message }

// AuthProviders gives the auth provider of an HTTP server that signs in with OAuth.
type AuthProviders func(server OAuthServer) (oauth.McpAuthProvider, error)

// TransportsFor is the transports of an instance connecting as connect; an environment's connection is taken from envs, an
// OAuth server's tokens from auth.
func TransportsFor(connect Connect, envs *EnvConnections, auth AuthProviders) Transports {
	switch connect.Kind {
	case ConnectRemote:
		lease := envs.Acquire(connect.Environment, connect.Config, connect.Revision)
		return Transports{
			Open:    func(ctx context.Context) (Opened, error) { return remoteTransport(ctx, connect, lease.Connection) },
			Release: lease.Release,
		}
	case ConnectHost:
		return Transports{
			Open: func(context.Context) (Opened, error) {
				values := cloneStrings(connect.Env)
				if connect.HostVariables {
					expanded, err := expandValues(connect.Env)
					if err != nil {
						return Opened{}, err
					}
					values = expanded
				}
				return Opened{
					Transport: pimcp.NewStdioTransport(pimcp.StdioTransportOptions{
						Command: connect.Command, Args: connect.Args, Cwd: connect.Cwd, Env: values, Stderr: "pipe",
					}),
					Roots: connect.Roots,
				}, nil
			},
			Release: func() {},
		}
	case ConnectHTTP:
		var provider oauth.McpAuthProvider
		var providerFailure error
		if connect.OAuth != nil {
			provider, providerFailure = auth(*connect.OAuth)
		}
		return Transports{
			Open: func(context.Context) (Opened, error) {
				if providerFailure != nil {
					return Opened{}, providerFailure
				}
				headers, err := expandValues(connect.Headers)
				if err != nil {
					return Opened{}, err
				}
				options := pimcp.StreamableHTTPTransportOptions{URL: connect.URL, Headers: headers}
				if provider != nil {
					options.AuthProvider = provider
				}
				transport, err := pimcp.NewStreamableHTTPTransport(options)
				if err != nil {
					return Opened{}, err
				}
				return Opened{Transport: transport, Roots: nil}, nil
			},
			Release: func() {},
		}
	}
	return Transports{
		Open:    func(context.Context) (Opened, error) { return Opened{}, errors.New(connect.Reason) },
		Release: func() {},
	}
}

// remoteTransport is a program on an environment: `~` is the remote home, relative paths are relative to the location's cwd
// there (else the home), in the remote system's path rules; on Windows, through `cmd.exe` unless it is an `.exe` or `.com`.
func remoteTransport(ctx context.Context, connect Connect, connection *env.Connection) (Opened, error) {
	info, err := connection.Info(ctx)
	if err != nil {
		return Opened{}, err
	}
	windows := env.NodePlatform(info).Platform == "win32"
	expandHome := func(value string) string {
		join := nodepath.PosixJoin
		if windows {
			join = nodepath.Win32Join
		}
		if value == "~" {
			return info.Home
		}
		if rest, ok := strings.CutPrefix(value, "~/"); ok {
			return join(info.Home, rest)
		}
		return value
	}
	base := info.Home
	if connect.Base != nil {
		base = *connect.Base
	}
	resolve := nodepath.PosixResolve
	if windows {
		resolve = nodepath.Win32Resolve
	}
	cwd, err := resolve(nodepath.Process(), base, expandHome(connect.Cwd))
	if err != nil {
		return Opened{}, err
	}
	command := expandHome(connect.Command)
	args := mapStrings(connect.Args, expandHome)
	launch := WindowsCommand{Argv: append([]string{command}, args...), Env: map[string]string{}}
	if windows {
		launch = WindowsCommandOf(command, args)
	}
	values := cloneStrings(connect.Env)
	if connect.HostVariables {
		if values, err = expandValues(connect.Env); err != nil {
			return Opened{}, err
		}
	}
	for key, value := range launch.Env {
		values[key] = value
	}
	transport := pimcp.NewPipeTransport(pimcp.PipeTransportOptions{
		Open: func(handlers pimcp.McpPipeHandlers) (pimcp.McpPipe, error) {
			process, err := env.OpenProcess(ctx, connection, env.ProcessOptions{
				Cwd: cwd, Argv: launch.Argv, Env: values, OnStdout: handlers.OnStdout, OnStderr: handlers.OnStderr,
			})
			if err != nil {
				var remote *env.RemoteError
				if errors.As(err, &remote) && remote.Code == "not_supported" {
					return nil, &DaemonTooOldError{"the pi-env daemon on " + connect.Environment + " is too old; update it"}
				}
				return nil, err
			}
			return process, nil
		},
	})
	roots := []string{}
	if connect.Base != nil {
		roots = []string{nodeurl.PathToFileURL(*connect.Base, windows)}
	}
	return Opened{Transport: transport, Roots: roots}, nil
}

// expandValues is values with `${NAME}` from the p2 host's environment (the owner's configuration only).
func expandValues(values map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(values))
	for key, value := range values {
		expanded, err := ExpandHostVariables(value)
		if err != nil {
			return nil, err
		}
		out[key] = expanded
	}
	return out, nil
}

func cloneStrings(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
