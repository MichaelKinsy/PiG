package surfaces

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/src/surfaces/loaded.ts. A host's loaded plugins (slice 10 D11, slice 11 §2): the facet loaded for each
// plugin, and loads and reloads with complete sets, the same on a server or worker (the plugins runtime) and in a page or a
// terminal (scripts that registered themselves). Unchanged plugins (the same digest) pass as the same facet values, removed
// ones are left out; a plugin whose script failed (its build, its evaluation) keeps its loaded facet (left out at the first
// load) and is named to the host, so its closure rule and its report see it.

// Prepared is a plugin's facet for a host, or why there is none: its script failed (Err).
type Prepared struct {
	Facet  host.Facet
	Digest string
	Err    error
}

// SetEntry is one plugin of a complete set: its name, its script's digest (nil when its build failed: nothing to compare)
// and how to get its facet (evaluated only when the digest is new).
type SetEntry struct {
	Name    string
	Digest  *string
	Prepare func() Prepared
}

// loadedFacet is a plugin's facet as the host loaded it, with its script's digest.
type loadedFacet struct {
	facet  host.Facet
	digest string
}

// loadedEntry is a loaded plugin with its name: the map of Pi's `Map<string, LoadedFacet>` keeps insertion order, which the
// host's facet list follows.
type loadedEntry struct {
	name string
	loadedFacet
}

// LoadedFacets is a host's loaded plugins: its core facets, each plugin's loaded facet, and its reloads, one after another.
type LoadedFacets struct {
	core      []host.HostFacet
	reloading sync.Mutex
	// loaded is held under reloading (Load made it before any reload).
	loaded []loadedEntry
}

func (l *LoadedFacets) find(name string) (loadedFacet, bool) {
	for _, each := range l.loaded {
		if each.name == name {
			return each.loadedFacet, true
		}
	}
	return loadedFacet{}, false
}

// LoadFacets prepares entries and loads them on h with core (the app's own facets): a plugin whose script failed is left out
// and reported in the report's Failed, with the host's own. It returns the host's loaded plugins, and the report.
func LoadFacets(h host.Host, core []host.HostFacet, entries []SetEntry, ctx context.Context) (*LoadedFacets, host.LoadReport, error) {
	prepared := make([]Prepared, len(entries))
	for i, entry := range entries {
		prepared[i] = entry.Prepare()
	}
	var failed []host.PluginFailure
	facets := append([]host.HostFacet(nil), core...)
	for i, each := range prepared {
		if each.Err != nil {
			failed = append(failed, host.PluginFailure{Plugin: entries[i].Name, Error: each.Err})
		} else {
			facets = append(facets, host.HostFacet{Name: entries[i].Name, Facet: each.Facet})
		}
	}
	report, err := h.Load(facets, ctx)
	if err != nil {
		return nil, host.LoadReport{}, err
	}
	left := map[string]bool{}
	for _, each := range report.Failed {
		left[each.Plugin] = true
	}
	kept := &LoadedFacets{core: append([]host.HostFacet(nil), core...)}
	for i, each := range prepared {
		if each.Err == nil && !left[entries[i].Name] {
			kept.loaded = append(kept.loaded, loadedEntry{entries[i].Name, loadedFacet{each.Facet, each.Digest}})
		}
	}
	return kept, host.LoadReport{Failed: append(failed, report.Failed...)}, nil
}

// Reload reloads h with entries, the complete set now, after its earlier reloads: core facets and unchanged plugins as the
// same values, new scripts prepared, a plugin whose script failed passed as its loaded facet (an added one left out; both named
// to the host's closure rule), plugins not in entries left out. What the host committed becomes the loaded set, also when the
// reload fails `aborted` after its commit.
func (l *LoadedFacets) Reload(h host.Host, entries []SetEntry, ctx context.Context) (host.ReloadReport, error) {
	// One after another: a reload computing its set from a loaded map an earlier one is about to change would pass that
	// plugin's old facet and roll it back.
	l.reloading.Lock()
	defer l.reloading.Unlock()
	var failed []host.PluginFailure
	var next []loadedEntry
	for _, entry := range entries {
		loaded, has := l.find(entry.Name)
		if entry.Digest != nil && has && *entry.Digest == loaded.digest {
			next = append(next, loadedEntry{entry.Name, loaded})
			continue
		}
		prepared := entry.Prepare()
		if prepared.Err == nil {
			next = append(next, loadedEntry{entry.Name, loadedFacet{prepared.Facet, prepared.Digest}})
			continue
		}
		failed = append(failed, host.PluginFailure{Plugin: entry.Name, Error: prepared.Err})
		if has {
			next = append(next, loadedEntry{entry.Name, loaded})
		}
	}
	facets := append([]host.HostFacet(nil), l.core...)
	for _, each := range next {
		facets = append(facets, host.HostFacet{Name: each.name, Facet: each.facet})
	}
	report, err := h.Reload(facets, ctx, host.ReloadOptions{Failed: failed})
	if err != nil {
		// Aborted after its commit: the commit stands, and is what the host runs now.
		if committed := committedOf(err); committed != nil {
			l.adopt(next, committed)
		}
		return host.ReloadReport{}, err
	}
	names := make([]string, len(report.Failed))
	for i, each := range report.Failed {
		names[i] = each.Plugin
	}
	l.adopt(next, names)
	return report, nil
}

// adopt adopts the set next a reload committed: a plugin the host kept old (failed) stays loaded as it was, an added one it
// left out is not loaded.
func (l *LoadedFacets) adopt(next []loadedEntry, failed []string) {
	for _, plugin := range failed {
		old, has := l.find(plugin)
		at := -1
		for i, each := range next {
			if each.name == plugin {
				at = i
				break
			}
		}
		switch {
		case !has && at >= 0:
			next = append(next[:at], next[at+1:]...)
		case has && at >= 0:
			next[at].loadedFacet = old
		case has:
			next = append(next, loadedEntry{plugin, old})
		}
	}
	l.loaded = next
}

// committedOf is what the commit of a reload that failed applied: an `aborted` error after its commit says
// (`details.committed`), by the plugins it kept old.
func committedOf(err error) []string {
	chordErr, ok := data.IsChordErrorCode(err, data.CodeAborted)
	if !ok {
		return nil
	}
	details, ok := chordErr.Details.(*delta.JsonObject)
	if !ok {
		return nil
	}
	committed, ok := details.Value("committed").(*delta.JsonObject)
	if !ok {
		return nil
	}
	names := []string{}
	if failed, ok := committed.Value("failed").([]any); ok {
		for _, each := range failed {
			if name, ok := each.(string); ok {
				names = append(names, name)
			}
		}
	}
	return names
}
