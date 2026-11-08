package surfaces

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	next "github.com/MichaelKinsy/PiG/chord/next/surfaces"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports packages/chord/src/node/surfaces/published.ts. A client kind's published scripts (slice 11 §1, decisions "Slice 11, P3"
// and "P5"): what the server serves of the kind's plugins, as files under the surface's prefix, and the set its scripts object
// shows. Each plugin's facet is a script that registers itself (`<prefix>plugin/<plugin>.<digest>.js`, its stylesheet beside
// it), the whole set one combined file named by its hash (`<prefix>plugins.<hash>.js`), assets under `<prefix>assets/`; all
// named by their content, served forever. A rollout shipping the kind publishes a new set (a plugin whose new build failed
// keeps its last script) without waiting for clients; the files of the last few sets stay served for clients that still load
// them.
//
// Forced Go difference. Requests read files from their own goroutines while a rollout publishes: the state is under a mutex
// (Pi's one JavaScript thread).

// keptSets is how many sets' files stay served: the current one and the few before it, for clients loading them meanwhile.
const keptSets = 4

// published is one plugin's published script: its digest, its registered text, its stylesheet and assets (kept with the script:
// a plugin whose next build fails keeps all three).
type published struct {
	digest string
	text   string
	css    string
	hasCSS bool
	assets []model.Asset
}

// sourceMap is a script's inline source map comment.
var sourceMap = regexp.MustCompile(`\n//# sourceMappingURL=data:[^\n]*`)

// PublishedScripts is a client kind's published scripts.
type PublishedScripts struct {
	surface SurfaceName
	kind    string
	prefix  string
	base    string
	plugins *plugins.Plugins
	host    host.Host

	mu sync.Mutex
	// current is the published script of each plugin now, names in order.
	current map[string]published
	names   []string
	set     next.ScriptSet
	// files are the files of the last keptSets sets, newest last, each by path.
	files []map[string]ServedFile
}

// NewPublishedScripts is the published scripts of kind (plugin facets of that kind) of surface, under prefix, for the base
// bundle base, from p, published on h.
func NewPublishedScripts(surface SurfaceName, kind, prefix, base string, p *plugins.Plugins, h host.Host) *PublishedScripts {
	return &PublishedScripts{surface: surface, kind: kind, prefix: prefix, base: base, plugins: p, host: h, current: map[string]published{}}
}

// Set is the set published now (after Start).
func (p *PublishedScripts) Set() next.ScriptSet {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.set
}

// Start takes the kind's current set as the first one (its plugins' facets of the kind built on first use), after the server's
// load (the surface's build publishes it). It returns the plugins whose build failed.
func (p *PublishedScripts) Start(ctx context.Context) ([]host.PluginFailure, error) {
	scripts, err := p.plugins.Scripts(p.kind, ctx)
	if err != nil {
		return nil, err
	}
	return p.publish(scripts)
}

// File is a published file by its path, from the last few sets (nil: none).
func (p *PublishedScripts) File(path string) *ServedFile {
	p.mu.Lock()
	defer p.mu.Unlock()
	for index := len(p.files) - 1; index >= 0; index-- {
		if found, ok := p.files[index][path]; ok {
			return &found
		}
	}
	return nil
}

// Step is the rollout step publishing the kind's current set as the rollout applied its build: one "host" named name, its report
// the plugins whose new build failed (they keep their last script, and stay dirty). Clients are not waited for.
func (p *PublishedScripts) Step(name string) plugins.RolloutStep {
	return plugins.RolloutStep{
		Name: name, Kinds: []string{p.kind},
		Reload: func(_ *plugins.Built, applied map[string][]plugins.PluginScript, ctx context.Context) (plugins.StepResult, error) {
			p.mu.Lock()
			before := map[string]string{}
			for plugin, each := range p.current {
				before[plugin] = each.digest
			}
			p.mu.Unlock()
			failed, err := p.publish(applied[p.kind])
			if err != nil {
				return plugins.StepResult{}, err
			}
			if err := PublishTo(p.host, p.surface, p.Set(), ctx); err != nil {
				return plugins.StepResult{}, err
			}
			p.mu.Lock()
			changes := host.PluginChanges{}
			for _, plugin := range p.names {
				digest := p.current[plugin].digest
				if was, had := before[plugin]; !had {
					changes.Added = append(changes.Added, plugin)
				} else if was != digest {
					changes.Changed = append(changes.Changed, plugin)
				}
			}
			for plugin := range before {
				if _, has := p.current[plugin]; !has {
					changes.Removed = append(changes.Removed, plugin)
				}
			}
			p.mu.Unlock()
			slices.Sort(changes.Removed)
			return plugins.StepResult{Hosts: []plugins.RolloutHost{{
				Kind: plugins.RolloutReported, Host: name, Report: host.ReloadReport{Plugins: changes, Failed: failed, Objects: nil},
			}}}, nil
		},
	}
}

// publish takes scripts (the kind's complete set) as the current one: a failed build keeps its plugin's last script; its files
// kept with the last few sets'. It returns the plugins whose build failed.
func (p *PublishedScripts) publish(scripts []plugins.PluginScript) ([]host.PluginFailure, error) {
	var failed []host.PluginFailure
	kept := map[string]published{}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, script := range scripts {
		built, name := script.Built, script.Plugin.Name
		if built.Kind == model.BuiltFacetScript {
			text := next.RegisteredSource(name, built.Digest, built.Source)
			entry := published{digest: built.Digest, text: text, assets: built.Assets}
			if built.CSS != nil {
				entry.css, entry.hasCSS = built.CSS.Text, true
			}
			kept[name] = entry
			continue
		}
		failed = append(failed, host.PluginFailure{Plugin: name, Error: &buildFailure{strings.Join(built.Errors, "\n")}})
		if previous, ok := p.current[name]; ok {
			kept[name] = previous
		}
	}
	p.current = kept
	p.names = p.names[:0]
	for name := range kept {
		p.names = append(p.names, name)
	}
	slices.SortFunc(p.names, compareUTF16)
	set, files := p.filesLocked()
	p.files = append(p.files, files)
	if len(p.files) > keptSets {
		p.files = p.files[1:]
	}
	p.set = set
	return failed, nil
}

// buildFailure is a plugin whose build failed: its errors.
type buildFailure struct{ text string }

func (e *buildFailure) Error() string { return e.text }

// filesLocked is the set of the current scripts and its files: each script (its source map inline), stylesheet and asset, and the
// combined file (without source maps: a browser reads only one per file, so they would be bytes for nothing).
func (p *PublishedScripts) filesLocked() (next.ScriptSet, map[string]ServedFile) {
	files := map[string]ServedFile{}
	entries := make([]next.ScriptEntry, 0, len(p.names))
	var combined strings.Builder
	for _, name := range p.names {
		each := p.current[name]
		// A plugin name (`@scope/name`) as one path segment, its slash a tilde: the set's URLs are looked up as they are, never
		// parsed back.
		stem := p.prefix + "plugin/" + strings.Replace(name, "/", "~", 1) + "." + each.digest
		files[stem+".js"] = ServedFile{Mime: JavaScript, Bytes: []byte(each.text)}
		entry := next.ScriptEntry{Name: name, Digest: each.digest, URL: stem + ".js"}
		if each.hasCSS {
			// Named by its own content too: a change of only the CSS keeps the script's digest.
			style := stem + "." + hashOf(each.css) + ".css"
			files[style] = ServedFile{Mime: CSS, Bytes: []byte(each.css)}
			entry.CSS = &style
		}
		entries = append(entries, entry)
		combined.WriteString(sourceMap.ReplaceAllString(each.text, ""))
		for _, asset := range each.assets {
			files[p.prefix+"assets/"+asset.Name] = ServedFile{Mime: MimeOf(asset.Name), Bytes: asset.Bytes}
		}
	}
	body := combined.String()
	url := p.prefix + "plugins." + hashOf(body) + ".js"
	files[url] = ServedFile{Mime: JavaScript, Bytes: []byte(body)}
	return next.ScriptSet{Base: p.base, Combined: url, Plugins: entries}, files
}

// hashOf is the first 16 hex digits of the SHA-256 of text.
func hashOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	//portlint:allow utf16units hex text is ASCII: bytes equal UTF-16 units, sliced at the same offset as Pi's .slice(0, 16)
	return hex.EncodeToString(sum[:])[:16]
}

// compareUTF16 orders strings as JavaScript's `<` does: by UTF-16 code unit.
func compareUTF16(a, b string) int {
	return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)))
}
