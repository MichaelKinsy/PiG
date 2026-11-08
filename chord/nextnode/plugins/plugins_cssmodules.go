package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/evanw/esbuild/pkg/api"
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
// pig divergence: Pi runs esbuild's mapping module in a `vm` context to read the object it exports. PiG has no JavaScript
// engine in this process, and esbuild writes that module as one object literal of string keys and values: parseClassMap
// reads exactly that literal (and fails on anything else), which is a parser of the same output, never an evaluation of it.

// ClassName is one class of a CSS module: its name in the source, and the name the build gives it (the names of its
// `composes` classes too, space separated).
type ClassName struct {
	Name   string
	Scoped string
}

// ScopedCSS is a CSS module scoped: its stylesheet with unique class names, and the mapping its import gives, in the order
// esbuild lists it.
type ScopedCSS struct {
	CSS     string
	Classes []ClassName
}

// scopedCSSNamespace is the namespace of a scoped stylesheet the facet's build imports.
const scopedCSSNamespace = "chord-css-module"

var nonWordName = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// ScopeCSSModule scopes the CSS module file of the plugin plugin (its directory dir) with contents: every class renamed
// `chord_<hash of plugin and path>_<file>_<class>`. It returns esbuild's failure (a syntax error) for the build to report.
func ScopeCSSModule(plugin, dir, file, contents string) (ScopedCSS, error) {
	rel, err := relative(dir, file)
	if err != nil {
		return ScopedCSS{}, err
	}
	sum := sha256.Sum256([]byte(plugin + "\x00" + rel))
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
		Format:           api.FormatESModule,
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
		return ScopedCSS{}, &cssModuleError{file: file, messages: result.Errors}
	}
	var css, js string
	for _, output := range result.OutputFiles {
		switch {
		case strings.HasSuffix(output.Path, ".css") && css == "":
			css = string(output.Contents)
		case strings.HasSuffix(output.Path, ".js") && js == "":
			js = string(output.Contents)
		}
	}
	classes, err := parseClassMap(js)
	if err != nil {
		return ScopedCSS{}, fmt.Errorf("css module %s: %w", file, err)
	}
	return ScopedCSS{CSS: css, Classes: classes}, nil
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

var defaultExport = regexp.MustCompile(`export\s*\{[^}]*?([A-Za-z_$][\w$]*)\s+as\s+default`)

// parseClassMap reads the mapping out of esbuild's JavaScript for a CSS module, an ES module whose default export is
// `var name = { class: "scoped", … };`: the object literal's keys (identifiers, possibly with `\u` escapes, or strings) and
// string values, in order (a repeated key keeps its first place and takes the last value, as an object literal does).
func parseClassMap(js string) ([]ClassName, error) {
	exported := defaultExport.FindStringSubmatch(js)
	if exported == nil {
		return nil, errors.New("no default export in the scoped module")
	}
	binding := regexp.MustCompile(`(?:^|[^\w$.])` + regexp.QuoteMeta(exported[1]) + `\s*=\s*\{`).FindStringIndex(js)
	if binding == nil {
		return nil, errors.New("the default export is not an object literal")
	}
	p := classMapParser{text: js, at: binding[1]}
	var classes []ClassName
	index := map[string]int{}
	for {
		p.space()
		if p.eat('}') {
			return classes, nil
		}
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		p.space()
		if !p.eat(':') {
			return nil, p.fail("expected `:`")
		}
		p.space()
		value, err := p.str()
		if err != nil {
			return nil, err
		}
		if at, seen := index[key]; seen {
			classes[at].Scoped = value
		} else {
			index[key] = len(classes)
			classes = append(classes, ClassName{Name: key, Scoped: value})
		}
		p.space()
		if p.eat(',') {
			continue
		}
		p.space()
		if !p.eat('}') {
			return nil, p.fail("expected `,` or `}`")
		}
		return classes, nil
	}
}

// classMapParser is a cursor over the JavaScript of a mapping.
type classMapParser struct {
	text string
	at   int
}

func (p *classMapParser) fail(what string) error {
	return fmt.Errorf("class map: %s at %d", what, p.at)
}

func (p *classMapParser) space() {
	for p.at < len(p.text) && strings.IndexByte(" \t\r\n", p.text[p.at]) >= 0 {
		p.at++
	}
}

func (p *classMapParser) eat(c byte) bool {
	if p.at < len(p.text) && p.text[p.at] == c {
		p.at++
		return true
	}
	return false
}

// units is the UTF-16 code units of a name or a value being read: an escape may write half of a surrogate pair, which only
// the other half completes.
type units []uint16

func (u *units) rune(r rune)   { *u = utf16.AppendRune(*u, r) }
func (u *units) unit(c uint16) { *u = append(*u, c) }
func (u units) String() string { return string(utf16.Decode(u)) }

// key reads a property key: a string literal or an identifier (its `\u` escapes read).
func (p *classMapParser) key() (string, error) {
	if p.at < len(p.text) && (p.text[p.at] == '"' || p.text[p.at] == '\'') {
		return p.str()
	}
	var name units
	for p.at < len(p.text) {
		c := p.text[p.at]
		switch {
		case c == '\\':
			p.at++
			if !p.eat('u') {
				return "", p.fail("bad escape in a key")
			}
			if err := p.unicodeEscape(&name); err != nil {
				return "", err
			}
		case c >= 0x80:
			r, size := utf8.DecodeRuneInString(p.text[p.at:])
			name.rune(r)
			p.at += size
		case c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			name.unit(uint16(c))
			p.at++
		default:
			if len(name) == 0 {
				return "", p.fail("expected a key")
			}
			return name.String(), nil
		}
	}
	return "", p.fail("unterminated object")
}

// str reads a string literal, its escapes read.
func (p *classMapParser) str() (string, error) {
	if p.at >= len(p.text) || (p.text[p.at] != '"' && p.text[p.at] != '\'') {
		return "", p.fail("expected a string")
	}
	quote := p.text[p.at]
	p.at++
	var out units
	for p.at < len(p.text) {
		c := p.text[p.at]
		switch {
		case c == quote:
			p.at++
			return out.String(), nil
		case c == '\n':
			return "", p.fail("newline in a string")
		case c >= 0x80:
			r, size := utf8.DecodeRuneInString(p.text[p.at:])
			out.rune(r)
			p.at += size
		case c != '\\':
			out.unit(uint16(c))
			p.at++
		default:
			p.at++
			if p.at >= len(p.text) {
				return "", p.fail("unterminated string")
			}
			e := p.text[p.at]
			p.at++
			switch e {
			case 'n':
				out.unit('\n')
			case 'r':
				out.unit('\r')
			case 't':
				out.unit('\t')
			case 'b':
				out.unit('\b')
			case 'f':
				out.unit('\f')
			case 'v':
				out.unit('\v')
			case '0':
				out.unit(0)
			case '\n':
			case 'x':
				if p.at+2 > len(p.text) {
					return "", p.fail("bad \\x escape")
				}
				n, err := strconv.ParseUint(p.text[p.at:p.at+2], 16, 8)
				if err != nil {
					return "", p.fail("bad \\x escape")
				}
				p.at += 2
				out.unit(uint16(n))
			case 'u':
				if err := p.unicodeEscape(&out); err != nil {
					return "", err
				}
			default:
				out.unit(uint16(e))
			}
		}
	}
	return "", p.fail("unterminated string")
}

// unicodeEscape reads what follows `\u`: four hex digits or `{…}`.
func (p *classMapParser) unicodeEscape(out *units) error {
	var digits string
	if p.eat('{') {
		end := strings.IndexByte(p.text[p.at:], '}')
		if end < 0 {
			return p.fail("bad \\u escape")
		}
		digits = p.text[p.at : p.at+end]
		p.at += end + 1
	} else {
		if p.at+4 > len(p.text) {
			return p.fail("bad \\u escape")
		}
		digits = p.text[p.at : p.at+4]
		p.at += 4
	}
	n, err := strconv.ParseUint(digits, 16, 32)
	if err != nil || n > utf8.MaxRune {
		return p.fail("bad \\u escape")
	}
	if n < 0x10000 {
		out.unit(uint16(n))
	} else {
		out.rune(rune(n))
	}
	return nil
}

// jsString is text as a JavaScript string literal (`JSON.stringify`).
func jsString(text string) string {
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text) // a string always encodes
	return strings.TrimSuffix(out.String(), "\n")
}

// classMapObject is classes as an object literal, in their order: the default export of a CSS module's import.
func classMapObject(classes []ClassName) string {
	var out strings.Builder
	out.WriteByte('{')
	for i, class := range classes {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(jsString(class.Name))
		out.WriteByte(':')
		out.WriteString(jsString(class.Scoped))
	}
	out.WriteByte('}')
	return out.String()
}
