//go:build !unix

package main

import (
	"context"
	"errors"
)

func entrypoint(context.Context, []string) (int, error) {
	return 0, errors.New("the eval container entry point runs only on POSIX systems")
}
