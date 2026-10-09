package env

// Ports packages/env/src/errors.ts

import (
	"context"
	"errors"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// abortError is the FileError of an aborted context, or nil while ctx is live.
func abortError(ctx context.Context, path string) error {
	if ctx.Err() != nil {
		return durableenv.NewFileError(durableenv.FileErrorAborted, "aborted", path, nil)
	}
	return nil
}

// toFileError is NodeExecutionEnv's mapping of Node error codes to FileError codes.
func toFileError(err error, fallbackPath string) *durableenv.FileError {
	if fileError, ok := errors.AsType[*durableenv.FileError](err); ok {
		return fileError
	}
	remote, ok := errors.AsType[*RemoteError](err)
	if !ok {
		return durableenv.NewFileError(durableenv.FileErrorUnknown, err.Error(), fallbackPath, err)
	}
	path := remote.Path
	if path == "" {
		path = fallbackPath
	}
	code := durableenv.FileErrorUnknown
	switch remote.Code {
	case "aborted":
		code = durableenv.FileErrorAborted
	case "ENOENT":
		code = durableenv.FileErrorNotFound
	case "EACCES", "EPERM":
		code = durableenv.FileErrorPermissionDenied
	case "ENOTDIR":
		code = durableenv.FileErrorNotDirectory
	case "EISDIR":
		code = durableenv.FileErrorIsDirectory
	case "EINVAL", "SYMLINK", "NOT_REGULAR":
		code = durableenv.FileErrorInvalid
	}
	return durableenv.NewFileError(code, remote.Message, path, remote)
}
