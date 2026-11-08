package surfaces

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/src/node/surfaces/bundle.ts. Base bundles (slice 11 §1, decision "Slice 11, P3"): a client kind's base
// code built on the server at start by esbuild from the app's entry (its start code, its API with the kind's module, Chord,
// React: inlined once), named by the hash of everything it is made of, served forever. A page's is a browser script with its
// stylesheet and assets; a terminal's one CommonJS script the sandbox evaluates with its own `require` (its allowed builtins
// only). Packages resolve with the server process's conditions (`--conditions`), so a base reads the same package files as
// the server runs; every file it read is recorded with its bytes' hash (BaseInputs).
//
// Forced Go differences. esbuild's Go API (pin 0.28.2, as the plugins component's builds) replaces its JavaScript API; a
// failing build is an error naming its messages. A Go process has no `execArgv`: the server process's conditions are
// those of NODE_OPTIONS (see D601).

// BaseBundle is a built base bundle: its hash, its script's and stylesheet's paths (under the surface's prefix), every file by
// path, and the files esbuild read for it with their bytes' hashes (the app's base code: a file that differs from them
// outdates the bundle).
type BaseBundle struct {
	Hash   string
	Script string
	// Style is the stylesheet's path; empty when the bundle imports no CSS.
	Style  string
	Files  map[string]ServedFile
	Inputs plugins.BaseInputs
}

// BasePlatform is where a base bundle runs.
type BasePlatform string

const (
	// PlatformBrowser is a page (a script, its CSS and assets).
	PlatformBrowser BasePlatform = "browser"
	// PlatformSandbox is a terminal's sandbox (one CommonJS script).
	PlatformSandbox BasePlatform = "sandbox"
)

// BuildBase builds the base bundle of entry for platform, its files under prefix (`/_chord/web/`): the script
// `base.<hash>.js`, the stylesheet `base.<hash>.css` when it imports CSS, assets under `assets/` by their own hashes.
// Packages resolve with conditions, by default (nil) the server process's (ProcessConditions); an empty non-nil slice resolves
// with none. It fails with esbuild's messages: the server does not start with a broken base.
func BuildBase(entry string, platform BasePlatform, prefix string, conditions []string) (*BaseBundle, error) {
	browser := platform == PlatformBrowser
	recorder := plugins.NewRecorder()
	resolving := conditions
	if resolving == nil {
		resolving = ProcessConditions(nil, os.Getenv("NODE_OPTIONS"))
	}
	options := api.BuildOptions{
		EntryPoints: []string{entry}, Outdir: "/", EntryNames: "base", AssetNames: "assets/[name]-[hash]", PublicPath: prefix,
		Write: false, Bundle: true,
		MinifyWhitespace: true, MinifyIdentifiers: true, MinifySyntax: true, LegalComments: api.LegalCommentsNone,
		// Ignore any tsconfig.json: TypeScript is stripped, JSX compiled with the automatic React runtime.
		TsconfigRaw: "{}", JSX: api.JSXAutomatic,
		Define:   map[string]string{"process.env.NODE_ENV": `"production"`},
		Plugins:  []api.Plugin{recorder.Plugin()},
		LogLevel: api.LogLevelSilent,
	}
	if browser {
		options.Format, options.Platform, options.Target = api.FormatIIFE, api.PlatformBrowser, api.ES2022
		// Custom conditions drop esbuild's default `module`, which a browser build keeps; the platform stays its own.
		options.Conditions = append(slices.Clone(resolving), "module")
	} else {
		options.Format, options.Platform = api.FormatCommonJS, api.PlatformNode
		options.Engines = []api.Engine{{Name: api.EngineNode, Version: "22"}}
		options.Conditions = slices.Clone(resolving)
	}
	result := api.Build(options)
	if len(result.Errors) > 0 {
		return nil, errors.New(strings.Join(api.FormatMessages(result.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage}), "\n"))
	}
	outputs := slices.Clone(result.OutputFiles)
	slices.SortFunc(outputs, func(a, b api.OutputFile) int { return strings.Compare(a.Path, b.Path) })
	sum := sha256.New()
	for _, output := range outputs {
		sum.Write([]byte(output.Path))
		sum.Write(output.Contents)
	}
	digest := hex.EncodeToString(sum.Sum(nil))[:16]
	bundle := &BaseBundle{Hash: digest, Files: map[string]ServedFile{}, Inputs: recorder.Inputs()}
	for _, output := range result.OutputFiles {
		extension := filepath.Ext(output.Path)
		if filepath.Base(output.Path) == "base"+extension {
			target := prefix + "base." + digest + extension
			mime := CSS
			if extension == ".js" {
				bundle.Script, mime = target, JavaScript
			} else {
				bundle.Style = target
			}
			bundle.Files[target] = ServedFile{Mime: mime, Bytes: output.Contents}
		} else {
			bundle.Files[prefix+"assets/"+path.Base(filepath.ToSlash(output.Path))] = ServedFile{Mime: MimeOf(output.Path), Bytes: output.Contents}
		}
	}
	return bundle, nil
}

// ProcessConditions is the resolution conditions of a Node process started with execArgv and the environment's nodeOptions
// (`NODE_OPTIONS`): the `--conditions` and `-C` flags of both.
func ProcessConditions(execArgv []string, nodeOptions string) []string {
	return conditionsOf(append(splitNodeOptions(nodeOptions), execArgv...))
}

// inlineCondition is `--conditions=<name>` and `-C=<name>`.
var inlineCondition = regexp.MustCompile(`^(?:--conditions|-C)=(.*)$`)

// conditionsOf is the conditions of `--conditions=<name>`, `--conditions <name>`, `-C <name>` and `-C=<name>` in args.
func conditionsOf(args []string) []string {
	conditions := []string{}
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if inline := inlineCondition.FindStringSubmatch(flag); inline != nil {
			conditions = append(conditions, inline[1])
		} else if (flag == "--conditions" || flag == "-C") && i+1 < len(args) {
			i++
			conditions = append(conditions, args[i])
		}
	}
	return conditions
}

// splitNodeOptions is `NODE_OPTIONS` split as Node splits it: at spaces outside double quotes; inside them a backslash takes the
// next character as it is.
func splitNodeOptions(text string) []string {
	var args []string
	var current strings.Builder
	quoted, started := false, false
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		char := runes[i]
		switch {
		case quoted && char == '\\' && i+1 < len(runes):
			i++
			current.WriteRune(runes[i])
		case char == '"':
			quoted = !quoted
		case char == ' ' && !quoted:
			if started {
				args = append(args, current.String())
			}
			current.Reset()
			started = false
			continue
		default:
			current.WriteRune(char)
		}
		started = true
	}
	if started {
		args = append(args, current.String())
	}
	return args
}
