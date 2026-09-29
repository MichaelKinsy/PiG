package subprocess

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func BenchmarkNodeAutocompleteRoundTrip(b *testing.B) {
	root, err := os.MkdirTemp("", "autocomplete-bench-")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(root) })
	source := filepath.Join(root, "provider.mjs")
	if err := os.WriteFile(source, []byte(`export default function(pi){pi.on("session_start",(_,ctx)=>ctx.ui.addAutocompleteProvider(current=>({async getSuggestions(lines,line,col,options){await current.getSuggestions(lines,line,col,options);return {items:Array.from({length:32},(_,i)=>({value:"item-"+i,label:"Item "+i})),prefix:"/"}},applyCompletion:(...a)=>current.applyCompletion(...a)})))}`), 0o600); err != nil {
		b.Fatal(err)
	}
	host := NewHost(root)
	defer host.Shutdown("benchmark done")
	ui := newTestUIContext()
	bridge := NewUIBridge(nil)
	bridge.SetUIContext(ui)
	host.SetUIBridge(bridge)
	loaded, err := host.Load(b.Context(), ExtConfig{Name: "provider", Source: source, Enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := loaded.Handlers["session_start"][0](map[string]any{"type": "session_start"}, b.Context()); err != nil {
		b.Fatal(err)
	}
	provider := ui.autocompleteProvider()
	b.ReportAllocs()
	for b.Loop() {
		result, err := provider.GetSuggestions(b.Context(), []string{"/"}, 0, 1, false)
		if err != nil || result == nil {
			b.Fatalf("query=%v %v", result, err)
		}
	}
}

func BenchmarkAutocompleteReferenceLifecycle(b *testing.B) {
	bridge := NewUIBridge(nil)
	owner := &Conn{}
	provider := &extension.AutocompleteProvider{}
	b.ReportAllocs()
	for b.Loop() {
		ref := bridge.autocompleteReference("owner", owner, provider)
		bridge.mu.Lock()
		delete(bridge.autocompleteReferences, ref.ID)
		bridge.mu.Unlock()
	}
}
