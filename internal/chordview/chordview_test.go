package chordview

import (
	"encoding/json"
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

// TestARefusedRecordReachesNoRegistry: mount and update build their records in Go; a record the decoder refuses is
// refused with the decoder's code and text, and nothing is mounted or replaced.
func TestARefusedRecordReachesNoRegistry(t *testing.T) {
	r := uiview.New(uiview.Options{})
	att, _, aerr := r.Attach(abilities(), func(uiview.Delivery) bool { return true })
	if aerr != nil {
		t.Fatal(aerr)
	}
	defer att.Detach()
	refused := func(name string, err *uiview.Error, raw, text string, decode func([]byte) *uiview.Error) {
		t.Helper()
		want := decode([]byte(raw))
		if want == nil || want.Code != uiview.CodeInvalid || want.Message != text {
			t.Fatalf("%s: the decoder answers %v, want invalid %q", name, want, text)
		}
		if err == nil || err.Code != want.Code || err.Message != want.Message {
			t.Errorf("%s: %v, want %v", name, err, want)
		}
	}
	decodeMount := func(raw []byte) *uiview.Error { _, err := uiview.DecodeMount(raw); return err }
	decodeUpdate := func(raw []byte) *uiview.Error { _, err := uiview.DecodeUpdate(raw); return err }
	long := strings.Repeat("s", 129)

	// A surface whose name is no mount key: through Refresh, as the app's surfaces arrive.
	for name, raw := range map[string]string{"": `{"slot":"panel","view":{"root":{"kind":"text","text":"chord : base not built"}}}`, long: `{"key":"` + long + `","slot":"panel","view":{"root":{"kind":"text","text":"chord ` + long + `: base not built"}}}`} {
		v := Show(r, []Surface{{Name: name, Base: func() string { return "" }}})
		refused("mount of a surface named "+name, v.Refresh(), raw, "key must be 1..128 bytes", decodeMount)
	}
	v := Show(r, nil)
	_, err := v.mount("", uiview.Content{View: textView("x")})
	refused("mount", err, `{"slot":"panel","view":{"root":{"kind":"text","text":"x"}}}`, "key must be 1..128 bytes", decodeMount)
	_, err = v.mount("web", uiview.Content{View: json.RawMessage(`{}`)})
	refused("mount without a root", err, `{"key":"web","slot":"panel","view":{}}`, "view must be an object with a root", decodeMount)
	if n := len(r.Snapshots()); n != 0 {
		t.Fatalf("%d mounts after refused records", n)
	}

	st, err := v.mount("web", uiview.Content{View: textView("x")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.update(uiview.Ref{Epoch: st.Mount.Epoch, Rev: st.Mount.Rev}, uiview.Content{View: textView("y")})
	refused("update", err, `{"mount":{"key":"","epoch":1,"rev":1},"view":{"root":{"kind":"text","text":"y"}}}`, "mount.key is required", decodeUpdate)
	_, err = v.update(st.Mount, uiview.Content{View: json.RawMessage(`{}`)})
	refused("update without a root", err, `{"mount":{"key":"web","epoch":1,"rev":1},"view":{}}`, "view must be an object with a root", decodeUpdate)
	if snaps := r.Snapshots(); len(snaps) != 1 || snaps[0].Mount != st.Mount || string(snaps[0].View) != `{"root":{"kind":"text","text":"x"}}` {
		t.Fatalf("after refused updates: %+v", snaps)
	}
}
