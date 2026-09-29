package experimental

// Ports packages/coding-agent/src/experimental/server.ts

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// ActivatedServer owns a connected client and the stable logical server route it reached.
type ActivatedServer struct {
	Client *client.Client
	Route  client.UnixServerRoute
}

// ActivateServerOptions selects a logical server and startup-only worker model. Nil requested identity uses the directory's persisted default identity.
type ActivateServerOptions struct {
	Directory         string
	RequestedServerId *string
	SessionDir        string
	Provider          *string
	Model             *string
}

// ActivateServer serializes cold activation with other activators and foreground startup. It returns only after a real client handshake; failed activation terminates and joins its child.
func ActivateServer(ctx context.Context, options ActivateServerOptions) (activated *ActivatedServer, err error) {
	if options.Provider != nil && options.Model == nil {
		return nil, errors.New("Server model provider requires a model")
	}
	if err := EnsurePrivateServerDirectory(options.Directory); err != nil {
		return nil, err
	}
	profile, err := AcquireServerProfile(ctx, options.Directory, options.RequestedServerId)
	if err != nil {
		return nil, err
	}
	serverId := profile.ServerID
	if err := profile.Release(); err != nil {
		return nil, err
	}
	path, err := routing.GetUnixSocketPath(serverId, options.Directory)
	if err != nil {
		return nil, err
	}
	route := client.UnixServerRoute{ServerId: serverId, Path: path}
	release, err := AcquireServerActivation(ctx, options.Directory, serverId)
	if err != nil {
		return nil, err
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			err = releaseErr
			if activated != nil {
				err = errors.Join(err, disposeServerClient(ctx, activated.Client))
				activated = nil
			}
		}
	}()
	existing, err := connectServer(ctx, route)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if options.Model != nil {
			if err := disposeServerClient(ctx, existing); err != nil {
				return nil, err
			}
			return nil, errors.New("Model selection is only valid when automatically activating a new server")
		}
		return &ActivatedServer{Client: existing, Route: route}, nil
	}
	args := []string{options.Directory, serverId, options.SessionDir}
	if options.Model != nil {
		model, err := json.Marshal(SessionWorkerModel{Provider: options.Provider, Model: *options.Model})
		if err != nil {
			return nil, err
		}
		model, err = jsonstringify.Canonicalize(model)
		if err != nil {
			return nil, err
		}
		args = append(args, string(model))
	}
	child, err := SpawnInternalProcess("server", args, InternalProcessSpawnOptions{})
	if err != nil {
		return nil, &serverActivationStartError{cause: err}
	}
	defer func() {
		if err != nil {
			if terminateErr := TerminateInternalProcess(child); terminateErr != nil {
				err = terminateErr
			}
		}
	}()
	deadline := time.Now().Add(activationTimeout)
	for {
		connected, err := connectServer(ctx, route)
		if err != nil {
			return nil, err
		}
		if connected != nil {
			return &ActivatedServer{Client: connected, Route: route}, nil
		}
		select {
		case <-child.Done():
			return nil, errors.New("Automatically activated server exited during startup")
		default:
		}
		if !time.Now().Before(deadline) {
			return nil, errors.New("Timed out waiting for automatically activated server")
		}
		// upstream: packages/coding-agent/src/experimental/server.ts:ACTIVATION_RETRY_MS
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, context.Cause(ctx)
		case <-timer.C:
		}
	}
}

type serverActivationStartError struct{ cause error }

func (*serverActivationStartError) Error() string     { return "Failed to automatically activate server" }
func (err *serverActivationStartError) Unwrap() error { return err.cause }

func connectServer(ctx context.Context, route client.UnixServerRoute) (*client.Client, error) {
	factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: route.Path})
	if err != nil {
		return nil, err
	}
	peer, err := client.NewClient(client.ClientOptions{ServerId: route.ServerId, TransportFactory: factory})
	if err != nil {
		return nil, err
	}
	_, connectErr := peer.Connect(ctx)
	if connectErr == nil {
		return peer, nil
	}
	if err := disposeServerClient(ctx, peer); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	if isUnavailableServerError(connectErr) {
		return nil, nil
	}
	return nil, connectErr
}

func disposeServerClient(ctx context.Context, peer *client.Client) error {
	return errors.Join(peer.Dispose(), peer.WaitClosed(context.WithoutCancel(ctx)))
}

func isUnavailableServerError(err error) bool {
	//nolint:errorlint // Upstream instanceof checks only the outer error; wrapped server/disconnection errors do not make an endpoint unavailable.
	switch failure := err.(type) {
	case *client.DisconnectedError:
		return true
	case *client.ServerError:
		if failure.Code == "version" {
			return true
		}
	}
	seen := make(map[error]bool)
	for err != nil {
		if reflect.TypeOf(err).Comparable() {
			if seen[err] {
				return false
			}
			seen[err] = true
		}
		if slices.Contains([]error{syscall.ENOENT, syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.EPIPE, syscall.ETIMEDOUT}, err) {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}
