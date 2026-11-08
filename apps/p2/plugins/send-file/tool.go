// Package sendfile is p2's send-file plugin (apps/p2/plugins/send-file): the `send_file` tool, which offers a file of the
// session's environment for download, and the text preview of a sent file for viewers without a cookie.
package sendfile

import (
	"context"
	"errors"
	"strconv"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/send-file/tool.ts.

// readChunk is how much of the source one read takes.
const readChunk = 1024 * 1024

// SaveSnapshot saves a snapshot of name (size bytes, read in order by next until it gives none) into the session's files,
// as offered by conversation conversation (what reads a turn's offered files, e.g. Discord's reply, finds it by that).
type SaveSnapshot func(name string, size, conversation int, next func() ([]byte, error), ctx context.Context) (p2runtime.DownloadInfo, error)

func sourceError(err error) error {
	var fileErr *durableenv.FileError
	if errors.As(err, &fileErr) && (fileErr.Code == durableenv.FileErrorIsDirectory || fileErr.Code == durableenv.FileErrorInvalid) {
		return &p2runtime.DownloadError{Status: 400, Message: "Source must be a regular file"}
	}
	return &p2runtime.DownloadError{Status: 400, Message: "Could not open source file"}
}

func sendFileTool(save SaveSnapshot) (*durable.ToolRegistration, error) {
	tool, err := p2runtime.ToolSchemaOf("send_file", toolDescription, schema.Obj(schema.Properties{
		schema.Prop("path", schema.Str(schema.MinLength(1))),
		schema.Opt("name", schema.Str(schema.MinLength(1))),
	}, schema.Open()))
	if err != nil {
		return nil, err
	}
	return harness.DefineTool(durable.ToolRegistration{
		ToolSchema: tool,
		Replay:     durable.ReplayUnsafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			fields, _ := args.(map[string]any)
			path, _ := fields["path"].(string)
			// Through the session's environment, so a remote session sends a file of its host.
			environment := api.Env()
			if environment == nil {
				return durable.ToolExecutionResult{}, errors.New("No execution environment")
			}
			reader, err := environment.OpenBinaryReader(ctx, path, nil)
			if err != nil {
				return durable.ToolExecutionResult{}, sourceError(err)
			}
			defer func() { _ = reader.Close(ctx) }()
			info, err := reader.Info(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, &p2runtime.DownloadError{Status: 400, Message: "Could not open source file"}
			}
			name := info.Name
			if given, ok := fields["name"].(string); ok {
				name = given
			}
			var offset int64
			saved, err := save(name, int(info.Size), int(api.ConversationId()), func() ([]byte, error) {
				bytes, err := reader.Read(ctx, offset, readChunk)
				if err != nil {
					return nil, &p2runtime.DownloadError{Status: 400, Message: err.Error()}
				}
				offset += int64(len(bytes))
				return bytes, nil
			}, ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return durable.ToolExecutionResult{
				Output: []ai.ToolResultMessageContent{ai.TextContent{
					Text: "Download ready: " + saved.Name + " (" + strconv.FormatFloat(saved.Size, 'f', -1, 64) + " bytes).",
				}},
				Details:    delta.JsonObjectOf("id", saved.ID, "name", saved.Name, "size", saved.Size),
				HasDetails: true,
			}, nil
		},
	}), nil
}

// extensionName is the extension that carries the tool.
const extensionName = "p2-downloads"

// CreateSendFileExtension is the extension with the `send_file` tool, saving through save.
func CreateSendFileExtension(save SaveSnapshot) (*p2runtime.P2Extension, error) {
	tool, err := sendFileTool(save)
	if err != nil {
		return nil, err
	}
	return &p2runtime.P2Extension{
		Extension: &durable.Extension{Name: extensionName},
		Tools:     []p2runtime.P2Tool{{ToolRegistration: tool}},
	}, nil
}
