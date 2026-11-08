// Package artifacts is p2's artifacts plugin (apps/p2/plugins/artifacts): the `artifact` tool, a session's artifact versions
// in its files, their index per agent, and the server's `/artifact` route.
package artifacts

import (
	"context"
	"fmt"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts/contract"
	filescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/plugins/artifacts/worker.ts: what a session's worker contributes (the tool saving into the session's
// files, and the contracts that read the artifacts back).

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

// toolsName is the worker object that contributes the tool (`p2.artifact-tools`).
const toolsName = "p2.artifact-tools"

func serveTools(facet host.FacetHost) error {
	return p2runtime.Contribute(facet, toolsName, func(self host.StoreSelf) error {
		worker := p2runtime.WorkerOf(self)
		// Versions go into the session's files (space `artifacts`), in a root of the worker's own; the server's mirror hears
		// of the change before the tool answers.
		save := func(input ArtifactInput, holder int, ctx context.Context) (p2runtime.ArtifactInfo, error) {
			parts, err := worker()
			if err != nil {
				return p2runtime.ArtifactInfo{}, err
			}
			saved, err := parts.Root(func(root host.Ctx) (any, error) {
				files, err := root.Get(app.Files, delta.NewJsonObject(0), host.GetOptions{})
				if err != nil {
					return nil, err
				}
				info, err := SaveArtifact(files, input, fmt.Sprint(holder), root)
				if err != nil {
					return nil, err
				}
				mirror, err := root.Get(filescontract.FilesMirror, delta.NewJsonObject(0), host.GetOptions{})
				if err != nil {
					return nil, err
				}
				_, err = mirror.Call("push", p2runtime.ArtifactsSpace, root)
				return info, err
			}, ctx)
			if err != nil {
				return p2runtime.ArtifactInfo{}, err
			}
			return saved.(p2runtime.ArtifactInfo), nil
		}
		extension, err := CreateArtifactExtension(save)
		if err != nil {
			return err
		}
		p2runtime.ContributeKeyed(self, p2runtime.Extensions, p2runtime.Entry(extensionName, extension))
		return nil
	})
}

func readArgs(args delta.JsonValue) (string, int) {
	in := objectOf(args)
	name, _ := in.Value("name").(string)
	version, _ := in.Value("version").(float64)
	return name, int(version)
}

func serveSource(facet host.FacetHost) error {
	return facet.Serve(contract.ArtifactSource, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		if who, err := p2runtime.FromChord(principal); err != nil || who != (p2runtime.ServerPrincipal{}) {
			return contracts.Implementation{}, p2runtime.Forbidden("server only")
		}
		files := self.Get(app.Files, delta.NewJsonObject(0))
		return contracts.Implementation{Variant: "server", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"read": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				name, version := readArgs(args)
				info, content, err := ReadArtifact(files, name, version, ctx)
				if err != nil {
					return nil, err
				}
				return delta.JsonObjectOf("info", infoJSON(info), "content", content), nil
			},
		}}}, nil
	}), host.ServeOptions{})
}

// preview is content cut to at most MaxArtifactPreview UTF-16 units, never inside a surrogate pair, and whether it was cut.
func preview(content string) (string, bool) {
	// A UTF-16 unit takes at least one byte: what is no longer than the limit in bytes is not cut.
	if len(content) <= contract.MaxArtifactPreview {
		return content, false
	}
	units := utf16.Encode([]rune(content))
	if len(units) <= contract.MaxArtifactPreview {
		return content, false
	}
	end := contract.MaxArtifactPreview
	if units[end-1] >= 0xD800 && units[end-1] <= 0xDBFF {
		end--
	}
	return string(utf16.Decode(units[:end])), true
}

func serveContent(facet host.FacetHost) error {
	return facet.Serve(contract.ArtifactContent, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		session, _ := params.Value("session").(string)
		if who, err := p2runtime.FromChord(principal); err != nil || !p2runtime.CanView(who, session) {
			return contracts.Implementation{}, p2runtime.Forbidden("viewers only")
		}
		files := self.Get(app.Files, delta.NewJsonObject(0))
		return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"read": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				name, version := readArgs(args)
				info, content, err := ReadArtifact(files, name, version, ctx)
				if err != nil {
					return nil, err
				}
				shown, truncated := preview(content)
				return delta.JsonObjectOf("info", infoJSON(info), "content", shown, "truncated", truncated), nil
			},
		}}}, nil
	}), host.ServeOptions{})
}

func serveList(facet host.FacetHost) error {
	return facet.Serve(contract.ArtifactList, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		session, _ := params.Value("session").(string)
		conversation, _ := params.Value("conversation").(float64)
		if who, err := p2runtime.FromChord(principal); err != nil || !p2runtime.CanView(who, session) {
			return contracts.Implementation{}, p2runtime.Forbidden("viewers only")
		}
		harnessOf := p2runtime.HarnessOf(self)
		id := durable.ConversationId(conversation)
		// The viewed agent's list (the main agent's on conversation 1), followed.
		listed := self.Follow(p2runtime.FollowDurable(func(ctx context.Context, observer durable.Observer[ArtifactState]) (durable.Observation[ArtifactState], error) {
			parts, err := harnessOf()
			if err != nil {
				return nil, err
			}
			opened, err := parts.Durable()
			if err != nil {
				return nil, err
			}
			found, err := opened.Conversation(ctx, id)
			if err != nil {
				return nil, err
			}
			if found == nil {
				return nil, fmt.Errorf("Unknown conversation %d", id)
			}
			holder, err := p2runtime.AgentConversation(ctx, opened, id)
			if err != nil {
				return nil, err
			}
			observed, err := durable.ObserveDoc(ctx, opened, ArtifactDoc, observer, holder)
			if err != nil || observed != nil {
				return observed, err
			}
			// No artifact saved yet: the document is absent, and an absent address cannot be followed. Create it empty.
			if _, err := opened.Commit(ctx, func(tx durable.Tx) (any, error) {
				_, err := durable.TxDoc[ArtifactState](tx, ArtifactDoc, holder)
				return nil, err
			}); err != nil {
				return nil, err
			}
			return durable.ObserveDoc(ctx, opened, ArtifactDoc, observer, holder)
		}))
		artifacts := self.Derive([]any{listed}, func(values []any) (delta.JsonValue, error) {
			return objectOf(values[0]).Value("artifacts"), nil
		})
		return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"artifacts": artifacts},
		}}, nil
	}), host.ServeOptions{})
}

// Facet is the plugin's worker facet.
func Facet() host.Facet {
	return host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		for _, serve := range []func(host.FacetHost) error{serveTools, serveSource, serveContent, serveList} {
			if err := serve(facet); err != nil {
				return err
			}
		}
		return nil
	})
}
