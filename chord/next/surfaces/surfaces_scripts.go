package surfaces

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/src/surfaces/scripts.ts. The current plugin scripts of a client kind (slice 11 §1, decision "Slice 11,
// P5"): what the server's surface serves as a remote object every page and terminal holds. Its state is the set: the base
// bundle's hash, the combined file of every plugin script, and each plugin's script (and stylesheet); a rollout shipping the
// kind publishes a new set, and each client fetches what changed.

// ScriptEntry is one plugin's script of a set: its name, its script's digest and URL, its stylesheet's URL when it has one
// (CSS empty: none).
type ScriptEntry struct {
	Name   string
	Digest string
	URL    string
	CSS    string
}

// ScriptSet is a client kind's current scripts: Base (the base bundle's hash: a change reloads the client), Combined (the URL
// of one file holding every plugin's script, what a fresh client loads), Plugins (each one's script, in name order).
type ScriptSet struct {
	Base     string
	Combined string
	Plugins  []ScriptEntry
}

// scriptEntrySchema is Pi's `ScriptEntry` schema.
var scriptEntrySchema = contracts.Obj(
	contracts.Prop("name", contracts.Str()), contracts.Prop("digest", contracts.Str()), contracts.Prop("url", contracts.Str()),
	contracts.Prop("css", contracts.Opt(contracts.Str())),
)

// ScriptSetSchema is the schema of a ScriptSet (Pi's `ScriptSet` value).
var ScriptSetSchema = contracts.Obj(
	contracts.Prop("base", contracts.Str()), contracts.Prop("combined", contracts.Str()),
	contracts.Prop("plugins", contracts.Arr(scriptEntrySchema)),
)

// JSON is the set as the value of ScriptSetSchema.
func (set ScriptSet) JSON() *delta.JsonObject {
	plugins := make([]any, 0, len(set.Plugins))
	for _, entry := range set.Plugins {
		object := delta.JsonObjectOf("name", entry.Name, "digest", entry.Digest, "url", entry.URL)
		if entry.CSS != "" {
			object.Set("css", entry.CSS)
		}
		plugins = append(plugins, object)
	}
	return delta.JsonObjectOf("base", set.Base, "combined", set.Combined, "plugins", plugins)
}

// ScriptSetOf is the set a value of ScriptSetSchema holds.
func ScriptSetOf(value delta.JsonValue) ScriptSet {
	object := value.(*delta.JsonObject)
	set := ScriptSet{Base: object.Value("base").(string), Combined: object.Value("combined").(string)}
	for _, item := range object.Value("plugins").([]any) {
		entry := item.(*delta.JsonObject)
		css, _ := entry.Value("css").(string)
		set.Plugins = append(set.Plugins, ScriptEntry{
			Name: entry.Value("name").(string), Digest: entry.Value("digest").(string), URL: entry.Value("url").(string), CSS: css,
		})
	}
	return set
}

// WebScripts is the current web scripts (ScriptSet), served by the server's web surface to any principal that may load the app.
var WebScripts = scriptsContract("chord.web.scripts")

// TuiScripts is the current terminal scripts (ScriptSet), served by the server's terminal surface as WebScripts is.
var TuiScripts = scriptsContract("chord.tui.scripts")

// scriptsContract is the contract of a client kind's current scripts, named name: a remote object on the server, its state the
// set.
func scriptsContract(name string) *contracts.Contract {
	contract, err := contracts.Remote(name, contracts.RemoteDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) {
			return serverKind.Address(nil), nil
		},
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
			State: []contracts.Property{contracts.Prop("set", ScriptSetSchema)},
		})},
	})
	if err != nil {
		panic(err)
	}
	return contract
}
