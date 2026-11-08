package app

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/node/app/info.test.ts (Pi main c23b9ec1): tests of the start-data objects (slice 13 §1): `AppInfo` on
// the server (data directory, app name, newest API version, owner) and `WorkerInfo` on a worker (its address and the start data
// made for it).

func TestAppInfoServesTheDataDirectoryNameVersionAndOwner(t *testing.T) {
	dir := t.TempDir()
	owner := delta.JsonObjectOf("kind", "owner")
	k := newKitHost(t, dir, host.HostOptions{}, host.HostFacet{Name: "chord.app.info", Facet: AppInfoFacet(AppInfoValues{
		DataDir: dir, Name: "chord-test", Version: "v2", Owner: owner,
	})})
	defer k.close()
	info, err := k.host.Get(background, AppInfo, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer info.Release()
	var got []any
	for _, member := range []string{"dataDir", "name", "version", "owner"} {
		value, err := info.Member(member).Value()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, value)
	}
	if want := []any{dir, "chord-test", "v2", owner}; !reflect.DeepEqual(got, want) {
		t.Fatalf("info = %v, want %v", got, want)
	}
}

func TestWorkerInfoServesItsAddressAndItsStartData(t *testing.T) {
	k := newKitHost(t, t.TempDir(), host.HostOptions{}, host.HostFacet{Name: "chord.app.worker-info", Facet: WorkerInfoFacet(WorkerInfoValues{
		Kind: "project", Key: "project|p7", Params: delta.JsonObjectOf("id", "p7"), Start: delta.JsonObjectOf("project", "project|p7"),
	})})
	defer k.close()
	info, err := k.host.Get(background, WorkerInfo, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer info.Release()
	address, err := info.Member("address").Value()
	if err != nil || !reflect.DeepEqual(address, delta.JsonObjectOf("kind", "project", "key", "project|p7", "params", delta.JsonObjectOf("id", "p7"))) {
		t.Fatalf("address = %v, %v", address, err)
	}
	start, err := info.Member("start").Value()
	if err != nil || !reflect.DeepEqual(start, delta.JsonObjectOf("project", "project|p7")) {
		t.Fatalf("start = %v, %v", start, err)
	}
}
