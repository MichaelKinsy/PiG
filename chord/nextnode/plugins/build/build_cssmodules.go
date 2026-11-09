package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports packages/chord/src/node/plugins/css-modules.ts. CSS modules in plugin builds (slice 11 §4b, decision "Looks and
// themes"; fix round 1, Opus L1): a plugin's `*.module.css` has its class names made unique to the file, the import giving
// the mapping, so twenty plugins' `.card` never collide. esbuild's `local-css` loader names classes uniquely within one
// build only (and a minified build names them `.a`, `.b`), while each facet is a build of its own; so each CSS module is
// scoped by a small build of its own whose names start with a prefix of the plugin and the file, and the facet's build gets
// the scoped stylesheet and the mapping. The mapping is esbuild's own (`composes` and escaped names as esbuild gives them);
// `url()` and `@import` stay as written and resolve in the facet's build, relative to the module (an `@import`ed file is
// plain CSS, its classes not scoped).
//
// Pi runs esbuild's mapping module in a `vm` context of its own process and spreads the object it exports. PiG's process has
// no JavaScript engine (AGENTS.md), so the mapping module runs in a Node subprocess of the Node that hosts the plugins
// (mappingEvaluator: the same `vm` call and the same spread, and `JSON.stringify` of the result, as Pi's facet module embeds
// it); the key order, the dropped `__proto__` and the text of the mapping are those of the JavaScript engine, never a reading
// of esbuild's output.

// ScopedCSS is a CSS module scoped: its stylesheet with unique class names, and the mapping its import gives.
type ScopedCSS struct {
	CSS string
	// Classes is the mapping as `JSON.stringify` writes the object Pi's `{ ...module.exports.default }` makes of esbuild's
	// mapping module: a JSON object of the class names (as the source writes them) to the names the build gives them (the
	// names of their `composes` classes too, space separated), in the order JavaScript lists the keys. The facet's module
	// embeds it as it is.
	Classes string
}

// scopedCSSNamespace is the namespace of a scoped stylesheet the facet's build imports.
const scopedCSSNamespace = "chord-css-module"

var nonWordName = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// ScopeCSSModule scopes the CSS module file of the plugin plugin (its directory dir) with contents: every class renamed
// `chord_<hash of plugin and path>_<file>_<class>`. evaluator reads the mapping (the builds of one component share one). It
// returns esbuild's failure (a syntax error) for the build to report.
func ScopeCSSModule(plugin, dir, file, contents string, evaluator model.MappingEvaluator, ctx context.Context) (ScopedCSS, error) {
	if evaluator == nil {
		return ScopedCSS{}, fmt.Errorf("css module %s: no evaluator reads its mapping (BuildOptions.Mappings)", file)
	}
	css, js, err := ScopeBuild(plugin, dir, file, contents)
	if err != nil {
		return ScopedCSS{}, err
	}
	classes, err := evaluator.Evaluate(ctx, js)
	if err != nil {
		return ScopedCSS{}, fmt.Errorf("css module %s: %w", file, err)
	}
	return ScopedCSS{CSS: css, Classes: classes}, nil
}

// ScopeBuild is the build that scopes a CSS module: its stylesheet, and esbuild's mapping module (CommonJS, as Pi's), or the
// build's failure.
func ScopeBuild(plugin, dir, file, contents string) (css, js string, err error) {
	rel, err := model.Relative(dir, file)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(plugin + "\x00" + rel))
	//portlint:allow utf16units the hex digits are ASCII: bytes are code units
	scope := hex.EncodeToString(sum[:])[:8]
	// esbuild names a local class `<file name>_<class>`: the file name carries the prefix.
	stem := filepath.Base(file)
	if stem != ".module.css" {
		stem = strings.TrimSuffix(stem, ".module.css")
	}
	// JavaScript's `\w` on UTF-16 code units: a character outside the BMP is two.
	stem = nonWordName.ReplaceAllStringFunc(stem, func(each string) string {
		if r, _ := utf8.DecodeRuneInString(each); r > 0xFFFF {
			return "__"
		}
		return "_"
	})
	name := "chord_" + scope + "_" + stem
	result := api.Build(api.BuildOptions{
		Stdin:            &api.StdinOptions{Contents: `export { default } from "scoped:` + name + `.module.css";`, Loader: api.LoaderJS},
		Bundle:           true,
		Write:            false,
		Outdir:           string(filepath.Separator),
		Format:           api.FormatCommonJS,
		MinifyWhitespace: true,
		LogLevel:         api.LogLevelSilent,
		Plugins: []api.Plugin{{
			Name: "chord-css-module",
			Setup: func(builder api.PluginBuild) {
				builder.OnResolve(api.OnResolveOptions{Filter: "^scoped:"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					return api.OnResolveResult{Path: "/" + strings.TrimPrefix(args.Path, "scoped:"), Namespace: "scoped"}, nil
				})
				// Kept as written: the facet's build resolves them relative to the module file.
				builder.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					if args.Kind == api.ResolveCSSURLToken || args.Kind == api.ResolveCSSImportRule {
						return api.OnResolveResult{Path: args.Path, External: true}, nil
					}
					return api.OnResolveResult{}, nil
				})
				builder.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "scoped"}, func(api.OnLoadArgs) (api.OnLoadResult, error) {
					return api.OnLoadResult{Contents: &contents, Loader: api.LoaderLocalCSS}, nil
				})
			},
		}},
	})
	if len(result.Errors) > 0 {
		return "", "", &cssModuleError{file: file, messages: result.Errors}
	}
	for _, output := range result.OutputFiles {
		switch {
		case strings.HasSuffix(output.Path, ".css") && css == "":
			css = string(output.Contents)
		case strings.HasSuffix(output.Path, ".js") && js == "":
			js = string(output.Contents)
		}
	}
	return css, js, nil
}

// cssModuleError is esbuild's failure to scope a CSS module: its messages.
type cssModuleError struct {
	file     string
	messages []api.Message
}

func (e *cssModuleError) Error() string {
	var text strings.Builder
	for i, message := range e.messages {
		if i > 0 {
			text.WriteByte('\n')
		}
		text.WriteString(message.Text)
	}
	return text.String()
}

// jsString is text as a JavaScript string literal (`JSON.stringify`).
func jsString(text string) string {
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text) // a string always encodes
	return strings.TrimSuffix(out.String(), "\n")
}

// DecodeUTF8 is what `new TextDecoder().decode(data)` gives (the WHATWG UTF-8 decoder): a byte sequence that is not UTF-8 is
// U+FFFD, one for each maximal subpart of a valid sequence (a truncated three-byte sequence is one, not three), and a byte
// order mark at the start is dropped.
func DecodeUTF8(data []byte) string {
	data = bytes.TrimPrefix(data, []byte("\uFEFF"))
	if utf8.Valid(data) {
		return string(data)
	}
	var out strings.Builder
	out.Grow(len(data))
	var codePoint rune
	needed, seen := 0, 0
	lower, upper := byte(0x80), byte(0xBF)
	for i := 0; i < len(data); {
		b := data[i]
		if needed == 0 {
			i++
			switch {
			case b <= 0x7F:
				out.WriteByte(b)
			case b >= 0xC2 && b <= 0xDF:
				needed, codePoint = 1, rune(b&0x1F)
			case b >= 0xE0 && b <= 0xEF:
				switch b {
				case 0xE0:
					lower = 0xA0
				case 0xED:
					upper = 0x9F
				}
				needed, codePoint = 2, rune(b&0x0F)
			case b >= 0xF0 && b <= 0xF4:
				switch b {
				case 0xF0:
					lower = 0x90
				case 0xF4:
					upper = 0x8F
				}
				needed, codePoint = 3, rune(b&0x07)
			default:
				out.WriteRune(utf8.RuneError)
			}
			continue
		}
		if b < lower || b > upper {
			// The sequence ends before this byte, which is read again as the start of another.
			needed, seen, codePoint = 0, 0, 0
			lower, upper = 0x80, 0xBF
			out.WriteRune(utf8.RuneError)
			continue
		}
		lower, upper = 0x80, 0xBF
		codePoint = codePoint<<6 | rune(b&0x3F)
		seen++
		i++
		if seen == needed {
			out.WriteRune(codePoint)
			needed, seen, codePoint = 0, 0, 0
		}
	}
	if needed != 0 {
		out.WriteRune(utf8.RuneError)
	}
	return out.String()
}
