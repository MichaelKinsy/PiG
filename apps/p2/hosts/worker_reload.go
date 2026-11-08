package hosts

import (
	"context"
	"errors"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// workerReloader is a session worker's side of Reload (reload.go): the worker's host, the facets it loaded and the digest
// of each plugin's set it applied last (a worker starts on every plugin's initial build, the empty revision).
type workerReloader struct {
	mu        sync.Mutex
	host      host.Host
	facets    []host.HostFacet
	selection []Plugin
	applied   map[string]string
}

// currentReloader is the running worker's (a worker process serves one session).
var currentReloader struct {
	mu sync.Mutex
	r  *workerReloader
}

func setReloader(r *workerReloader) {
	currentReloader.mu.Lock()
	currentReloader.r = r
	currentReloader.mu.Unlock()
}

// reloadWorker is `WorkerMainOptions.Reload`: the plugins whose script digest differs from the one this worker applied
// last are renewed, and the host reloads with the complete set of facets.
func reloadWorker(scripts []plugins.PluginScript, ctx context.Context) (host.ReloadReport, error) {
	currentReloader.mu.Lock()
	r := currentReloader.r
	currentReloader.mu.Unlock()
	if r == nil {
		return host.ReloadReport{}, errors.New("p2 worker: not loaded")
	}
	return r.apply(scripts, ctx)
}

func (r *workerReloader) apply(scripts []plugins.PluginScript, ctx context.Context) (host.ReloadReport, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := append([]host.HostFacet(nil), r.facets...)
	digests := make(map[string]string, len(scripts))
	changed := map[string]bool{}
	for _, script := range scripts {
		name := script.Plugin.Name
		digests[name] = script.Built.Digest
		previous, ok := r.applied[name]
		if !ok {
			previous = digestOf(name, "")
		}
		if previous != script.Built.Digest {
			changed[name] = true
		}
	}
	for index, entry := range next {
		if changed[entry.Name] {
			if plugin, ok := pluginNamed(r.selection, entry.Name); ok && plugin.Worker != nil {
				next[index].Facet = entry.Facet.Renew()
			}
		}
	}
	report, err := r.host.Reload(next, ctx, host.ReloadOptions{})
	if err != nil {
		return report, err
	}
	failed := map[string]bool{}
	for _, each := range report.Failed {
		failed[each.Plugin] = true
	}
	for index, entry := range next {
		if failed[entry.Name] {
			next[index] = r.facets[index]
		}
	}
	r.facets = next
	for name, digest := range digests {
		if !failed[name] {
			r.applied[name] = digest
		}
	}
	return report, nil
}
