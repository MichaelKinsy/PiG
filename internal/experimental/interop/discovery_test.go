//go:build !windows

package interop

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/client"
)

// Ports packages/client/test/unix.test.ts discoverUnixServers against live servers: the Go and the pinned Node discovery find the
// same routes in one directory holding Node servers, Go servers, a server whose file name disagrees with its identity, a silent
// socket, a stale file, a plain file, a directory and a non-canonical name.
func TestDiscoverUnixServersAgainstLiveServers(t *testing.T) {
	t.Parallel()
	const (
		nodeID   = "10000000-0000-4000-8000-000000000001"
		goID     = "20000000-0000-4000-8000-000000000002"
		liarID   = "30000000-0000-4000-8000-000000000003"
		silentID = "40000000-0000-4000-8000-000000000004"
	)
	directory := socketDirectory(t)
	launchNodeServer(t, filepath.Join(directory, nodeID+".sock"), nodeID, nil)
	launchGoServer(t, filepath.Join(directory, goID+".sock"), goID, nil)
	// A server whose socket name claims another identity is not a route.
	launchNodeServer(t, filepath.Join(directory, liarID+".sock"), nodeID, nil)
	silent, err := net.Listen("unix", filepath.Join(directory, silentID+".sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = silent.Close() })
	for _, name := range []string{"50000000-0000-4000-8000-000000000005.sock", "notes.sock", "UPPER.txt"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(directory, "60000000-0000-4000-8000-000000000006.sock"), 0o700); err != nil {
		t.Fatal(err)
	}
	timeout := 400.0
	got, err := client.DiscoverUnixServers(t.Context(), client.DiscoverUnixServersOptions{Directory: directory, TimeoutMs: &timeout})
	if err != nil {
		t.Fatal(err)
	}
	cmd := nodeScript(t.Context(), t, "discover.mjs", directory, "400")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Node discovery: %v\n%s", err, stderr.String())
	}
	var want []client.UnixServerRoute
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("Node discovery output %q: %v", output, err)
	}
	sortRoutes := func(routes []client.UnixServerRoute) {
		slices.SortFunc(routes, func(a, b client.UnixServerRoute) int { return strings.Compare(a.ServerId, b.ServerId) })
	}
	sortRoutes(got)
	sortRoutes(want)
	if len(want) != 2 || want[0].ServerId != nodeID || want[1].ServerId != goID {
		t.Fatalf("the pinned Node discovery found %+v; the fixture must yield the Node and Go servers", want)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Go discovery found %+v; the pinned Node discovery found %+v", got, want)
	}
}
