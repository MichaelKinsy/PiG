package env

// Ports packages/env/src/list-files.ts

import (
	"bytes"
	"context"
	"math"
	"slices"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// listKinds are the kind bytes of `listFiles` records: `f`, `d`, `l`.
var listKinds = map[byte]durableenv.FileKind{
	'f': durableenv.FileKindFile, 'd': durableenv.FileKindDirectory, 'l': durableenv.FileKindSymlink,
}

// listBound is one numeric bound of ListFilesOptions under its wire name.
type listBound struct {
	key   string
	value *float64
}

// listBounds are the numeric bounds of options in the order list-files.ts validates them, which decides the error when
// several are invalid.
func listBounds(options *durableenv.ListFilesOptions) []listBound {
	if options == nil {
		return nil
	}
	return []listBound{{"maxEntries", options.MaxEntries}, {"maxBytes", options.MaxBytes}, {"timeoutMs", options.TimeoutMs}}
}

// listFilesOver is `listFilesOver` of list-files.ts: `listFiles` of an absolute path through the daemon. It fails with
// `not_supported` if the daemon does not list files (a release before `listFiles`), so callers fall back to their own walk.
func listFilesOver(ctx context.Context, connection *Connection, path string, options *durableenv.ListFilesOptions) (durableenv.FileListing, error) {
	bounds := listBounds(options)
	for _, bound := range bounds {
		if value := bound.value; value != nil && (math.Trunc(*value) != *value || math.Abs(*value) > maxSafeInteger || *value < 0) {
			return durableenv.FileListing{}, durableenv.NewFileError(durableenv.FileErrorInvalid, bound.key+" must be a non-negative safe integer", path, nil)
		}
	}
	if abort := abortError(ctx, path); abort != nil {
		return durableenv.FileListing{}, abort
	}
	info, err := connection.Info(ctx)
	if err != nil {
		return durableenv.FileListing{}, toFileError(err, path)
	}
	if !slices.Contains(info.Features, "listFiles") {
		return durableenv.FileListing{}, durableenv.NewFileError(durableenv.FileErrorNotSupported, "The pi-env daemon does not list files; update it", path, nil)
	}
	// Fields left undefined are not sent; the daemon applies the defaults.
	request := Json{"path": path}
	if options != nil {
		if options.Hidden != nil {
			request["hidden"] = *options.Hidden
		}
		if options.Gitignore != nil {
			request["gitignore"] = *options.Gitignore
		}
		if options.Exclude != nil {
			request["exclude"] = slices.Clone(options.Exclude)
		}
		for _, bound := range bounds {
			if bound.value != nil {
				request[bound.key] = *bound.value
			}
		}
	}
	reply, err := connection.Request(ctx, "listFiles", request, RequestOptions{})
	if err != nil {
		return durableenv.FileListing{}, toFileError(err, path)
	}
	if abort := abortError(ctx, path); abort != nil {
		return durableenv.FileListing{}, abort
	}
	// Records of a kind byte, the UTF-8 path, and a NUL.
	listing := durableenv.FileListing{Entries: []durableenv.FileListEntry{}, Truncated: reply.JSON["truncated"] == true}
	for start := 0; start < len(reply.Payload); {
		end := bytes.IndexByte(reply.Payload[start:], 0)
		if end == -1 {
			break
		}
		end += start
		if kind, ok := listKinds[reply.Payload[start]]; ok {
			listing.Entries = append(listing.Entries, durableenv.FileListEntry{Path: jsstring.FromUTF8(reply.Payload[start+1 : end]), Kind: kind})
		}
		start = end + 1
	}
	return listing, nil
}

// LocalExecutionEnvOptions are the options of a LocalExecutionEnv: those of a NodeExecutionEnv and a connection.
type LocalExecutionEnvOptions struct {
	node.NodeExecutionEnvOptions
	// Connection is a connection to this machine's daemon, `NewConnection(ConnectionOptions{Command: []string{daemon}})` with
	// LocalDaemon for `daemon`, for ListFiles.
	Connection *Connection
}

// LocalExecutionEnv is a NodeExecutionEnv with ListFiles through a pi-env daemon on the same machine, so local and
// remote environments list files with the same code. Everything else is NodeExecutionEnv's. The connection belongs to the
// caller, who closes it; several environments may share it.
type LocalExecutionEnv struct {
	*node.NodeExecutionEnv
	connection *Connection
}

var (
	_ durableenv.ExecutionEnv = (*LocalExecutionEnv)(nil)
)

// NewLocalExecutionEnv returns the environment of options.
func NewLocalExecutionEnv(options LocalExecutionEnvOptions) *LocalExecutionEnv {
	local := &LocalExecutionEnv{NodeExecutionEnv: node.NewNodeExecutionEnv(options.NodeExecutionEnvOptions), connection: options.Connection}
	local.Self = local
	return local
}

// ListFiles lists the files below path through the daemon.
func (e *LocalExecutionEnv) ListFiles(ctx context.Context, path string, options *durableenv.ListFilesOptions) (durableenv.FileListing, error) {
	resolved, err := e.AbsolutePath(ctx, path)
	if err != nil {
		return durableenv.FileListing{}, err
	}
	return listFilesOver(ctx, e.connection, resolved, options)
}
