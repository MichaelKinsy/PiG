package files

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/google/uuid"
)

// Ports apps/p2/plugins/files/worker.ts: a session's uploads and download snapshots on its worker, in the session's
// `Files`: uploads stream in from the server's `POST /upload` (`SessionFiles`), a message's attachments are materialized
// where its conversation works (`Attachments`), snapshots are found and read for `GET /download`, and each change of a
// space is pushed to the server's mirror of the session's file summaries (`FilesMirror`).

func serveFilesMirror(facet host.FacetHost) error {
	return facet.Serve(contract.FilesMirror, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		files := self.Get(app.Files, delta.NewJsonObject(0))
		return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"push": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				space, _ := args.(string)
				summary, found, err := files.Keyed("summary").Get(ctx, space)
				if err != nil {
					return nil, err
				}
				if !found || summary == nil {
					summary = delta.JsonObjectOf("files", float64(0), "versions", float64(0), "bytes", float64(0), "newest", []any{})
				}
				mirror, err := hostCtx(ctx).Get(contract.FileSummaries, delta.NewJsonObject(0), host.GetOptions{})
				if err != nil {
					return nil, err
				}
				_, err = mirror.Call("push", delta.JsonObjectOf("space", space, "summary", summary), ctx)
				return nil, err
			},
		}}}, nil
	}), host.ServeOptions{})
}

// joinOn joins a directory and a name with the separator of the machine's path rules.
func joinOn(windows bool, directory, name string) string {
	if windows {
		return strings.TrimRight(directory, `\`) + `\` + name
	}
	return path.Join(directory, name)
}

func serveAttachments(facet host.FacetHost) error {
	return facet.Serve(contract.Attachments, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		worker := p2runtime.WorkerOf(self)
		harness := p2runtime.HarnessOf(self)
		files := self.Get(app.Files, delta.NewJsonObject(0))
		// Only uploads of this session are accepted. Copies complete before input admission; a failure never falls back.
		return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"resolve": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := objectArgs(args)
				ids, _ := in.Value("ids").([]any)
				if len(ids) == 0 {
					return []any{}, nil
				}
				parts, err := worker()
				if err != nil {
					return nil, err
				}
				session, err := parts.Init.Session()
				if err != nil {
					return nil, err
				}
				type found struct {
					id   string
					file *delta.JsonObject
				}
				resolved := make([]found, len(ids))
				for index, each := range ids {
					id, _ := each.(string)
					file, err := files.Call("byKey", delta.JsonObjectOf("space", p2runtime.UploadsSpace, "key", id), ctx)
					if err != nil {
						return nil, err
					}
					if objectOf(file) == nil {
						return nil, errors.New("Unknown attachment")
					}
					resolved[index] = found{id, objectOf(file)}
				}
				// This machine's copy first: the model reads it here, and inline images come from it on any machine.
				local := filepath.Join(parts.Init.DataDir, "uploads", session.ID)
				attachments := make([]*delta.JsonObject, len(resolved))
				for index, each := range resolved {
					path, err := MaterializeLocally(files, each.file, local, ctx)
					if err != nil {
						return nil, err
					}
					name := p2runtime.DisplayName(each.file)
					attachment := delta.JsonObjectOf("id", each.id, "name", name, "path", path, "size", each.file.Value("size"))
					if mime := ImageType(name); mime != "" {
						attachment.Set("mimeType", mime)
					}
					attachments[index] = attachment
				}
				// Another machine (the conversation's: a subagent's own location may be elsewhere than the session's) gets
				// its own copy. Decided from the environment, never from a location's name.
				harnessParts, err := harness()
				if err != nil {
					return nil, err
				}
				environments, err := harnessParts.Environments()
				if err != nil {
					return nil, err
				}
				durableHarness, err := harnessParts.Durable()
				if err != nil {
					return nil, err
				}
				conversation, _ := in.Value("conversation").(float64)
				_, executionEnv, err := p2runtime.ConversationEnvironment(ctx, environments, durable.ConversationId(conversation), durableHarness)
				if err != nil {
					return nil, err
				}
				if p2runtime.IsLocalEnvironment(executionEnv) {
					return objects(attachments), nil
				}
				windows := false
				if remote, ok := executionEnv.(*env.RemoteExecutionEnv); ok {
					info, err := remote.Connection.Info(ctx)
					if err != nil {
						return nil, err
					}
					windows = env.NodePlatform(info).Platform == "win32"
				}
				directory, err := executionEnv.AbsolutePath(ctx, "~/.pi/p2/uploads")
				if err != nil {
					return nil, err
				}
				directory = joinOn(windows, directory, session.ID)
				if err := executionEnv.CreateDir(ctx, directory, &durableenv.CreateDirOptions{}); err != nil {
					return nil, err
				}
				copies := make([]any, len(attachments))
				for index, attachment := range attachments {
					remotePath := joinOn(windows, directory, resolved[index].file.Value("path").(string))
					content, err := p2runtime.ReadWhole(files, resolved[index].file, ctx)
					if err != nil {
						return nil, err
					}
					if err := executionEnv.WriteFile(ctx, remotePath, content); err != nil {
						return nil, err
					}
					copy := delta.NewJsonObject(0)
					for _, key := range attachment.Keys() {
						copy.Set(key, attachment.Value(key))
					}
					copy.Set("localPath", attachment.Value("path"))
					copy.Set("path", remotePath)
					copies[index] = copy
				}
				return copies, nil
			},
		}}}, nil
	}), host.ServeOptions{})
}

func objects(list []*delta.JsonObject) []any {
	out := make([]any, len(list))
	for index, each := range list {
		out[index] = each
	}
	return out
}

func serveSessionFiles(facet host.FacetHost) error {
	return facet.Serve(contract.SessionFiles, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		if who, err := p2runtime.FromChord(principal); err != nil || who != (p2runtime.ServerPrincipal{}) {
			return contracts.Implementation{}, p2runtime.Forbidden("server only")
		}
		files := self.Get(app.Files, delta.NewJsonObject(0))
		mirror := self.Get(contract.FilesMirror, delta.NewJsonObject(0))
		return contracts.Implementation{Variant: "server", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"begin": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := objectArgs(args)
				id := uuid.NewString()
				path := UploadPath(id, stringOf(in.Value("name")))
				mime := ImageType(path)
				if mime == "" {
					mime = "application/octet-stream"
				}
				write := delta.JsonObjectOf("space", in.Value("space"), "path", path, "mime", mime, "source", in.Value("source"), "key", id,
					"meta", delta.JsonObjectOf("name", path[len(id)+1:]))
				begun, err := files.Call("begin", write, ctx)
				if err != nil {
					return nil, err
				}
				// A fresh key: always a new upload.
				if objectOf(begun).Value("kind") != "upload" {
					return nil, errors.New("File exists already")
				}
				return delta.JsonObjectOf("id", id, "upload", objectOf(begun).Value("upload")), nil
			},
			"append": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := objectArgs(args)
				bytes, _ := in.Value("bytes").([]byte)
				if offset, _ := in.Value("offset").(float64); int(offset)+len(bytes) > MaxUploadBytes {
					return nil, errors.New("File too large")
				}
				_, err := files.Call("append", in, ctx)
				return nil, err
			},
			"finish": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				finished, err := files.Call("finish", args, ctx)
				if err != nil {
					return nil, err
				}
				file := objectOf(finished)
				if _, err := mirror.Call("push", file.Value("space"), ctx); err != nil {
					return nil, err
				}
				// Begun as `<id>-<name>` with its ID as the key.
				name := p2runtime.DisplayName(file)
				uploaded := delta.JsonObjectOf("id", file.Value("key"), "name", name, "size", file.Value("size"))
				if mime := ImageType(name); mime != "" {
					uploaded.Set("mimeType", mime)
				}
				return uploaded, nil
			},
			"cancel": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				_, err := files.Call("cancel", args, ctx)
				return nil, err
			},
			"download": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				found, err := p2runtime.FindDownload(files, stringOf(args), ctx)
				if err != nil || found == nil {
					return nil, err
				}
				return delta.JsonObjectOf("file", found.File.Value("id"), "name", found.Name, "size", found.File.Value("size")), nil
			},
			"read": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := objectArgs(args)
				info, err := files.Call("file", in.Value("file"), ctx)
				if err != nil {
					return nil, err
				}
				// Only what the routes serve: download snapshots.
				if objectOf(info) == nil || objectOf(info).Value("space") != p2runtime.DownloadsSpace {
					return nil, errors.New("File not found")
				}
				return files.Call("read", delta.JsonObjectOf("id", in.Value("file"), "offset", in.Value("offset"), "length", in.Value("length")), ctx)
			},
		}}}, nil
	}), host.ServeOptions{})
}

// Facet is the plugin's worker facet.
func Facet() host.Facet {
	return host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		for _, serve := range []func(host.FacetHost) error{serveFilesMirror, serveAttachments, serveSessionFiles} {
			if err := serve(facet); err != nil {
				return err
			}
		}
		return nil
	})
}
