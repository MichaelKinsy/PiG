package surfaces

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	next "github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// A facet for a surface that is neither the web nor the terminal fails its load naming the surface (PublishTo refuses it too).
func TestScriptsFacetRefusesAnUnknownSurface(t *testing.T) {
	kind, err := contracts.NewHostKind("server")
	if err != nil {
		t.Fatal(err)
	}
	address := kind.Address(nil)
	server, err := host.CreateHost(host.HostOptions{Address: &address, Principal: delta.JsonObjectOf("kind", "server"), OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	facet := ScriptsFacet("desktop", func() next.ScriptSet { return next.ScriptSet{} })
	report, err := server.Load([]host.HostFacet{{Name: "chord.desktop", Facet: facet}}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Failed) != 1 || !strings.Contains(report.Failed[0].Error.Error(), `unknown surface "desktop"`) {
		t.Fatalf("failed = %+v", report.Failed)
	}
	if err := PublishTo(server, "desktop", next.ScriptSet{}, context.Background()); err == nil || !strings.Contains(err.Error(), `unknown surface "desktop"`) {
		t.Fatalf("publish = %v", err)
	}
}
