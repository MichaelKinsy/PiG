package e2e_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts"
	filescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	"github.com/MichaelKinsy/PiG/schema"
)

// A plugin of this test binary only, for the artifacts test's long artifact: Pi's test saves it with a script of the `codemode`
// tool, and the fake provider streams a tool call's arguments at 800 characters a second (half a MiB would take eleven
// minutes). This plugin's worker object saves an artifact of the asked size into the session's files as the tool does (the
// same SaveArtifact, then the mirror's push), for the server only. The worker is this binary too, so it registers when the
// environment names it (`saveArtifactEnv`, set by the test before the fixture starts), in the server as well.

const saveArtifactEnv = "P2_E2E_SAVE_ARTIFACT"

var saveArtifactOnce sync.Once

func init() {
	if os.Getenv(saveArtifactEnv) != "" {
		registerSaveArtifact()
	}
}

// saveArtifact is the test plugin's contract: `save` saves a text artifact of size characters.
var saveArtifact = func() *contracts.Contract {
	contract, err := contracts.Remote("e2e.save-artifact", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
		HostAddress: p2runtime.SessionHost,
		Variants: []contracts.VariantEntry{
			contracts.Var("server", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
				contracts.Meth("save", contracts.NewMethod(p2runtime.Contract(schema.Obj(schema.Properties{
					schema.Prop("name", schema.Str()), schema.Prop("size", schema.Int(schema.Minimum(0))),
				})))),
			}}),
		},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()

func registerSaveArtifact() {
	saveArtifactOnce.Do(func() {
		worker := host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
			return facet.Serve(saveArtifact, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
				if who, err := p2runtime.FromChord(principal); err != nil || who != (p2runtime.ServerPrincipal{}) {
					return contracts.Implementation{}, p2runtime.Forbidden("server only")
				}
				files := self.Get(app.Files, delta.NewJsonObject(0))
				mirror := self.Get(filescontract.FilesMirror, delta.NewJsonObject(0))
				return contracts.Implementation{Variant: "server", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
					"save": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						in, _ := args.(*delta.JsonObject)
						name, _ := in.Value("name").(string)
						size, _ := in.Value("size").(float64)
						if _, err := artifacts.SaveArtifact(files, artifacts.ArtifactInput{
							Name: name, Title: "Long", Type: p2runtime.ArtifactText, Content: strings.Repeat("x", int(size)),
						}, "1", ctx); err != nil {
							return nil, errors.Join(errors.New("saving the artifact"), err)
						}
						_, err := mirror.Call("push", p2runtime.ArtifactsSpace, ctx)
						return nil, err
					},
				}}}, nil
			}), host.ServeOptions{})
		})
		hosts.RegisterPlugin(hosts.Plugin{Name: "e2e-save-artifact", Worker: &worker})
	})
}
