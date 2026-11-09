package app

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// goPackageServer is a plugin whose server facet serves GoPackaged: its text is a GoValue the plugin reads and its shout method
// calls a GoFunc of the app-offered GoPackage, so neither answer is the plugin's own or the fallback of an app that offers nothing.
const goPackageServer = `import { constant, defineFacet, local, method, str } from "@test/app/v1";
import { shout, tag } from "@acme/go";
const GoPackaged = local("test.gopackaged", { version: 1, params: {}, members: { text: str(), shout: method(str(), str()) } });
export default defineFacet((host) => host.serve(GoPackaged, () => ({ text: constant(tag), shout: async (word) => shout(word) })));`

var goPackaged = mustLocal("test.gopackaged", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
	contracts.Mem("text", contracts.Str()), contracts.Mem("shout", contracts.NewMethod(contracts.Str(), contracts.Str())),
}})

// AppOptions.GoPackages reaches the server host's plugins: a plugin of the app's root imports the package, reads its value and
// calls its Go function.
func TestAppOffersGoPackagesToItsPlugins(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"gopkg": {
		"package.json": `{"name":"@test/gopkg","private":true,"type":"module","chord":{"external":["@acme/go"]}}`,
		"server.ts":    goPackageServer,
	}}, func(o *AppOptions) {
		o.GoPackages = []plugins.GoPackage{{Specifier: "@acme/go", Exports: map[string]plugins.GoExport{
			"shout": plugins.GoFunc(func(_ context.Context, args delta.JsonValue) (delta.JsonValue, error) {
				return "go-" + args.(string), nil
			}),
			"tag": plugins.GoValue("offered"),
		}}}
	})
	served, err := s.app.Node.Get(background, goPackaged, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer served.Release()
	untilText(t, served, "offered")
	if got, err := served.Call("shout", "pi", background); err != nil || got != "go-pi" {
		t.Fatalf("shout(pi) = %v, %v; want go-pi", got, err)
	}
}
