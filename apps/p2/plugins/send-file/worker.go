package sendfile

import (
	"bytes"
	"context"
	"unicode/utf8"

	filescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/send-file/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
)

// Ports apps/p2/plugins/send-file/worker.ts: the tool saving snapshots into the session's files, and `SentFiles`.

// PreviewText is bytes as text when they are UTF-8 without NUL (a leading byte order mark is dropped, as a TextDecoder does);
// a code point cut at the end of a partial read is dropped. ok is false for anything else.
func PreviewText(content []byte, partial bool) (string, bool) {
	if bytes.IndexByte(content, 0) >= 0 {
		return "", false
	}
	maxCut := 0
	if partial {
		maxCut = 3
	}
	for cut := 0; cut <= min(maxCut, len(content)); cut++ {
		if text := content[:len(content)-cut]; utf8.Valid(text) {
			return string(bytes.TrimPrefix(text, []byte("\xef\xbb\xbf"))), true
		}
	}
	return "", false
}

// toolsName is the worker object that contributes the tool (`p2.send-file-tools`).
const toolsName = "p2.send-file-tools"

func serveTools(facet host.FacetHost) error {
	return p2runtime.Contribute(facet, toolsName, func(self host.StoreSelf) error {
		worker := p2runtime.WorkerOf(self)
		// Snapshots go into the session's files (space `downloads`, written by the agent), in a root of the worker's own; the
		// server's mirror hears of the change before the tool answers.
		save := func(name string, size, conversation int, next func() ([]byte, error), ctx context.Context) (p2runtime.DownloadInfo, error) {
			parts, err := worker()
			if err != nil {
				return p2runtime.DownloadInfo{}, err
			}
			saved, err := parts.Root(func(root host.Ctx) (any, error) {
				files, err := root.Get(app.Files, delta.NewJsonObject(0), host.GetOptions{})
				if err != nil {
					return nil, err
				}
				info, err := p2runtime.SaveDownload(files, p2runtime.Snapshot{
					Name: name, Size: size, Source: "agent", Ref: contract.OfferedBy(conversation),
				}, next, root)
				if err != nil {
					return nil, err
				}
				mirror, err := root.Get(filescontract.FilesMirror, delta.NewJsonObject(0), host.GetOptions{})
				if err != nil {
					return nil, err
				}
				_, err = mirror.Call("push", p2runtime.DownloadsSpace, root)
				return info, err
			}, ctx)
			if err != nil {
				return p2runtime.DownloadInfo{}, err
			}
			return saved.(p2runtime.DownloadInfo), nil
		}
		extension, err := CreateSendFileExtension(save)
		if err != nil {
			return err
		}
		p2runtime.ContributeKeyed(self, p2runtime.Extensions, p2runtime.Entry(extensionName, extension))
		return nil
	})
}

func serveSentFiles(facet host.FacetHost) error {
	return facet.Serve(contract.SentFiles, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		session, _ := params.Value("session").(string)
		if who, err := p2runtime.FromChord(principal); err != nil || !p2runtime.CanView(who, session) {
			return contracts.Implementation{}, p2runtime.Forbidden("viewers only")
		}
		files := self.Get(app.Files, delta.NewJsonObject(0))
		return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"preview": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				id, _ := args.(*delta.JsonObject).Value("id").(string)
				found, err := p2runtime.FindDownload(files, id, ctx)
				if err != nil {
					return nil, err
				}
				if found == nil {
					return nil, &p2runtime.DownloadError{Status: 404, Message: "File not found"}
				}
				size := int(found.File.Value("size").(float64))
				length := min(size, contract.MaxSentPreview)
				var content []byte
				if length > 0 {
					value, err := files.Call("read", delta.JsonObjectOf("id", found.File.Value("id"), "offset", float64(0), "length", float64(length)), ctx)
					if err != nil {
						return nil, err
					}
					content, _ = value.([]byte)
				}
				truncated := size > len(content)
				var text delta.JsonValue
				if shown, ok := PreviewText(content, truncated); ok {
					text = shown
				}
				return delta.JsonObjectOf("name", found.Name, "size", float64(size), "text", text, "truncated", truncated), nil
			},
		}}}, nil
	}), host.ServeOptions{})
}

// Facet is the plugin's worker facet.
func Facet() host.Facet {
	return host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		if err := serveTools(facet); err != nil {
			return err
		}
		return serveSentFiles(facet)
	})
}
