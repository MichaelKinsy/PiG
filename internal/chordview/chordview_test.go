package chordview

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	"github.com/MichaelKinsy/PiG/coding/uiview"
)

func abilities() uiview.ClientAbilities {
	return uiview.ClientAbilities{Slots: []uiview.Slot{uiview.SlotPanel}, Nodes: []string{"text"},
		Limits: uiview.Limits{Nodes: 8, Depth: 4, SnapshotBytes: 4096, TextBytes: 256, Mounts: 4, IntentArgBytes: 16}}
}

// TestSurfacesAreMountedUpdatedAndFinished: every surface of a chord app is one panel under the plugin name chord, keyed
// by the surface's name; the panel is replaced when the surface's base bundle changes, left alone when it did not, and
// finished when the app closes. Without a client the mounts fail unsupported, and a later Refresh makes them.
func TestSurfacesAreMountedUpdatedAndFinished(t *testing.T) {
	r := uiview.New(uiview.Options{})
	var mu sync.Mutex
	bases := map[string]string{}
	base := func(name string) func() string {
		return func() string {
			mu.Lock()
			defer mu.Unlock()
			return bases[name]
		}
	}
	v := Show(r, []Surface{{Name: "web", Base: base("web")}, {Name: "tui", Base: base("tui")}})
	if err := v.Refresh(); err == nil || err.Code != uiview.CodeUnsupported || len(r.Snapshots()) != 0 {
		t.Fatalf("without a client: %v, %d mounts", err, len(r.Snapshots()))
	}
	var sent []string
	att, _, aerr := r.Attach(abilities(), func(d uiview.Delivery) bool {
		sent = append(sent, d.Snapshot.Plugin+"/"+d.Snapshot.Mount.Key+" "+string(d.Snapshot.State)+" "+string(d.Snapshot.View))
		return true
	})
	if aerr != nil {
		t.Fatal(aerr)
	}
	defer att.Detach()
	if err := v.Refresh(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	bases["web"] = "3f9a"
	mu.Unlock()
	for range 2 {
		if err := v.Refresh(); err != nil {
			t.Fatal(err)
		}
	}
	// A panel the host ended is mounted again.
	r.Break(Plugin, uiview.ReasonSessionReplaced)
	if err := v.Refresh(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	bases["tui"] = "77c1"
	mu.Unlock()
	if err := v.Refresh(); err != nil {
		t.Fatal(err)
	}
	v.Close()
	v.Close()
	if err := v.Refresh(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(sent[len(sent)-2:])
	want := []string{
		`chord/web live {"root":{"kind":"text","text":"chord web: base not built"}}`,
		`chord/tui live {"root":{"kind":"text","text":"chord tui: base not built"}}`,
		`chord/web live {"root":{"kind":"text","text":"chord web: base 3f9a"}}`,
		`chord/web broken {"root":{"kind":"text","text":"chord web: base 3f9a"}}`,
		`chord/tui broken {"root":{"kind":"text","text":"chord tui: base not built"}}`,
		`chord/web live {"root":{"kind":"text","text":"chord web: base 3f9a"}}`,
		`chord/tui live {"root":{"kind":"text","text":"chord tui: base not built"}}`,
		`chord/tui live {"root":{"kind":"text","text":"chord tui: base 77c1"}}`,
		`chord/tui finished {"root":{"kind":"text","text":"chord tui: base 77c1"}}`,
		`chord/web finished {"root":{"kind":"text","text":"chord web: base 3f9a"}}`,
	}
	slices.Sort(sent[3:5])
	slices.Sort(want[3:5])
	if !slices.Equal(sent, want) {
		t.Fatalf("the client was sent\n  %s\nwant\n  %s", strings.Join(sent, "\n  "), strings.Join(want, "\n  "))
	}
	for _, s := range r.Snapshots() {
		if s.Plugin != Plugin || len(s.Intents) != 0 {
			t.Errorf("mount %s/%s with %d intents", s.Plugin, s.Mount.Key, len(s.Intents))
		}
	}
}

// TestSurfacesOfAnApp: an app without client surfaces has no panel.
func TestSurfacesOfAnApp(t *testing.T) {
	if got := Surfaces(&app.App{}); len(got) != 0 {
		t.Fatalf("an app without surfaces has %d", len(got))
	}
	if got := Surfaces(&app.App{Web: &app.AppSurface{}}); len(got) != 1 || got[0].Name != "web" {
		t.Fatalf("an app with a web surface has %+v", got)
	}
}
