// Ports test/p2/tui-radius.test.ts (Pi main c23b9ec1): the TUI launcher through Radius. Its own file, as in Pi.

package e2e_test

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/radius/radiustest"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/vterm"
)

// Pi: "the TUI through Radius loads the server's bundle and opens its chord link". PiG has no bundle to load (D611): the compiled-in sessions surface comes up
// over the chord link that the launcher's Radius transport opens (one Radius client connection for the link, one per request).
func TestTheTUIThroughRadiusLoadsTheServersBundleAndOpensItsChordLink(t *testing.T) {
	f := radiusFixture(t, nil, radiustest.Options{Channels: true})
	transport := host.NewRadiusTransport(&host.RadiusClient{
		Gateway: f.radius.Gateway,
		Access:  func(context.Context) (string, error) { return "fixture-token", nil },
	}, radiustest.NewID())
	selection, err := hosts.SelectPlugins(nil)
	if err != nil {
		t.Fatal(err)
	}
	terminal := vterm.New(100, 30)
	var mu sync.Mutex
	var failures []string
	address, err := url.Parse("https://radius.invalid")
	if err != nil {
		t.Fatal(err)
	}
	client, err := host.StartClient(host.ClientOptions{
		Terminal: terminal, URL: address, Transport: transport, Role: "owner", Facets: hosts.TUIFacets(selection),
		ColorQuery: 10 * time.Millisecond, OnFailure: func(message string) { mu.Lock(); failures = append(failures, message); mu.Unlock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(strings.Join(terminal.Viewport(), "\n"), "New session") {
		if time.Now().After(deadline) {
			t.Fatalf("sessions over the chord link never showed:\n%s", strings.Join(terminal.Viewport(), "\n"))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !client.Connected() {
		t.Fatal("the launcher is not connected")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(failures) != 0 {
		t.Fatalf("failures: %v", failures)
	}
}
