// Package hostenv is a placeholder of the lane that ports daemon.ts and the envs slice; replaced by that lane's package.
package hostenv

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/env"
)

// Remote is a named environment's connection recipe.
type Remote struct {
	Revision string
	ShellEnv map[string]string
	Connect  func(ctx context.Context, onLog func(string)) (*env.Connection, error)
}

// Local is the connection to this machine's daemon.
func Local(ctx context.Context, onLog func(string)) (*env.Connection, error) {
	path, err := env.LocalDaemon()
	if err != nil {
		return nil, err
	}
	return env.NewConnection(env.ConnectionOptions{Command: []string{path}, OnLog: onLog}), nil
}

// Resolve is the named environment.
func Resolve(ctx context.Context, directory, name string) (*Remote, error) {
	return nil, errors.New("Unknown environment " + name)
}
