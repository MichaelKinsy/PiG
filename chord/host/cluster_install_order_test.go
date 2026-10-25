package host

// cluster.test.ts calls createHost({ upstream }) and worker.install(facet) in one synchronous turn (:449-460 in the
// test cluster_upstream_test.go ports for "a reconciliation running when a reload starts", :352-360 and :408-416 in
// its siblings), and Pi's memory link delivers a frame in a later promise job (link.ts:259), so the install always
// precedes the delivery of the worker's hello. A parent ahead of the worker reconciles it on that hello (host.ts:987
// peerHello, cluster.ts:123 reconcileChild): the child's prepare frame takes its reload slot (reload.ts:143
// beginReload), it loads its modules, and host.ts:381 refuses an install until the commit.
//
// In Go the hello is delivered on another goroutine, so an Install after the connect races that reconciliation:
// portmap-3 (run 40044829243) failed at the Install of
// TestCrossHostReloadAReconciliationRunningWhenAReloadStartsSyncsTheChildOnlyAfterItToTheCommittedCodeVersion, and the
// worker.Close of its cleanup then waited for the loader, which waited for a latch that a cleanup registered earlier
// opens only after it. The cluster tests install before they connect (prepareWorker); these two cases hold the
// worker's reconciliation inside its loader, so the order does not depend on scheduling: the loader has entered once
// the reload slot is taken (reload.go beginReload sets it before resolve).

import (
	"testing"
)

// clusterAheadOfAWorker is a server at code version 2 whose cluster reload gave session hosts the greeter module, and
// the modules a worker's reconciliation loads.
func clusterAheadOfAWorker(t *testing.T) (*HostImpl, *clusterModules) {
	t.Helper()
	modules := &clusterModules{modules: map[string]Facet{"greeter": greeterFacet("v1", false)}}
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	plugin := clusterPlugin("server-plugin")
	if err := server.Install(plugin, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Reload([]PluginRef{&plugin}, ReloadOptions{Remote: sessionRemote("greeter")}); err != nil {
		t.Fatal(err)
	}
	if got := server.CodeVersion(); got != 2 {
		t.Fatalf("server code version = %d, want 2", got)
	}
	return server, modules
}

// holdingLoader is a worker loader that opens entered when the reconciliation reaches it and returns once release opens.
func holdingLoader(modules *clusterModules, entered, release *clusterLatch) FacetLoader {
	return func(names []string) ([]Facet, error) {
		entered.open()
		release.wait()
		return modules.loader(names)
	}
}

// A worker that installs after it connected behind its parent is refused while the parent's reconciliation holds its
// reload slot (host.ts:381): Pi's code cannot reach that state because its install precedes the hello's delivery.
func TestCrossHostAnInstallAfterAChildConnectedBehindItsParentIsRefusedWhileItIsReconciled(t *testing.T) {
	server, modules := clusterAheadOfAWorker(t)
	entered, release := newClusterLatch(), newClusterLatch()
	worker, _ := joinWorker(t, server, nil, HostOptions{Loader: holdingLoader(modules, entered, release)})
	// Cleanups run last first: the latch opens before worker.Close waits for the reconciliation that waits on it.
	t.Cleanup(release.open)
	entered.waitT(t, "the worker's reconciliation to load its modules")
	if err := worker.Install(modules.modules["greeter"], LoadOptions{}); messageOf(err) != "host.install during a reload" {
		t.Fatalf("Install during the reconciliation = %s, want host.install during a reload", messageOf(err))
	}
	release.open()
	waitFor(t, "the worker to be reconciled", func() bool { return worker.CodeVersion() == server.CodeVersion() })
}

// A worker that installs before it connects has the facet installed when its reconciliation loads, and the
// reconciliation then brings it to the parent's code version: the order the cluster tests keep (prepareWorker).
func TestCrossHostAnInstallBeforeAChildConnectsBehindItsParentIsInPlaceWhenItIsReconciled(t *testing.T) {
	server, modules := clusterAheadOfAWorker(t)
	entered, release := newClusterLatch(), newClusterLatch()
	worker, _, connect := prepareWorker(t, server, nil, HostOptions{Loader: holdingLoader(modules, entered, release)})
	if err := worker.Install(modules.modules["greeter"], LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	connect()
	t.Cleanup(release.open)
	entered.waitT(t, "the worker's reconciliation to load its modules")
	var installed bool
	worker.run(func() { installed = worker.facets.Has("greeter") })
	if !installed {
		t.Fatal("the facet was not installed when the reconciliation reached the loader")
	}
	release.open()
	waitFor(t, "the worker to be reconciled", func() bool { return worker.CodeVersion() == server.CodeVersion() })
	greeter := getGreeter(t, server, clusterGreeter, "S1")
	if got := greet(t, greeter, "a", nil); got != "v1 a" {
		t.Fatalf("greet a = %q", got)
	}
}
